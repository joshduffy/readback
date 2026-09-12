// Package local checks file_exists claims against the filesystem.
package local

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/joshduffy/readback/internal/verify"
)

const maxContainsRead = 2 << 20 // 2 MiB

const readChunk = 32 << 10 // 32 KiB

type Checker struct {
	cwd string
}

func New(cwd string) *Checker {
	return &Checker{cwd: cwd}
}

func (c *Checker) Check(ctx context.Context, claim verify.Claim) verify.Outcome {
	path := claim.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.cwd, path)
	}
	evidence := func(observed map[string]any) []verify.Evidence {
		return []verify.Evidence{{Source: "local", Call: "stat " + path, Observed: observed}}
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return verify.Outcome{
				Status:   verify.StatusContradicted,
				Reason:   verify.ReasonFileMissing,
				Evidence: evidence(map[string]any{"error": err.Error()}),
			}
		}
		return verify.Outcome{
			Status:   verify.StatusIndeterminate,
			Reason:   verify.ReasonProviderUnreachable,
			Evidence: evidence(map[string]any{"error": err.Error()}),
		}
	}
	observed := map[string]any{
		"size":           info.Size(),
		"mode":           info.Mode().String(),
		"contains_found": false,
	}
	if info.IsDir() {
		observed["is_dir"] = true
		return verify.Outcome{
			Status:   verify.StatusContradicted,
			Reason:   verify.ReasonFileMissing,
			Evidence: evidence(observed),
		}
	}
	if claim.Contains != "" {
		found, truncated, err := c.contains(ctx, path, claim.Contains)
		if err != nil {
			observed["error"] = err.Error()
			return verify.Outcome{
				Status:   verify.StatusIndeterminate,
				Reason:   verify.ReasonProviderUnreachable,
				Evidence: evidence(observed),
			}
		}
		if !found {
			if truncated {
				observed["truncated"] = true
			}
			return verify.Outcome{
				Status:   verify.StatusContradicted,
				Reason:   verify.ReasonContentMissing,
				Evidence: evidence(observed),
			}
		}
		observed["contains_found"] = true
	}
	return verify.Outcome{Status: verify.StatusVerified, Evidence: evidence(observed)}
}

// contains streams up to maxContainsRead bytes of path, checking ctx between
// chunks. truncated reports that the file continues beyond the cap, so a
// needle appearing only past the cap is reported as missing with truncation
// noted in evidence.
func (c *Checker) contains(ctx context.Context, path, needle string) (found, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return false, false, err
	}
	defer f.Close()

	want := []byte(needle)
	buf := make([]byte, readChunk)
	var tail []byte
	read := 0
	for read < maxContainsRead {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, false, ctxErr
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			read += n
			chunk := make([]byte, 0, len(tail)+n)
			chunk = append(chunk, tail...)
			chunk = append(chunk, buf[:n]...)
			if bytes.Contains(chunk, want) {
				return true, false, nil
			}
			if keep := len(want) - 1; keep > 0 {
				if keep > len(chunk) {
					keep = len(chunk)
				}
				tail = append(tail[:0], chunk[len(chunk)-keep:]...)
			}
		}
		if readErr == io.EOF {
			return false, false, nil
		}
		if readErr != nil {
			return false, false, readErr
		}
	}
	return false, true, nil
}
