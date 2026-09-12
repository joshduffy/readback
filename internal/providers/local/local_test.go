package local_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshduffy/readback/internal/providers/local"
	"github.com/joshduffy/readback/internal/verify"
)

func TestFileExistsContains(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("alpha beta gamma"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := local.New(dir)
	outcome := c.Check(context.Background(), verify.Claim{
		Type: "file_exists", Path: path, Contains: "beta",
	})
	if outcome.Status != verify.StatusVerified {
		t.Fatalf("want verified, got %q (%s)", outcome.Status, outcome.Reason)
	}
	observed := outcome.Evidence[0].Observed
	if observed["contains_found"] != true {
		t.Fatalf("want contains_found true, got %v", observed["contains_found"])
	}

	missing := c.Check(context.Background(), verify.Claim{
		Type: "file_exists", Path: path, Contains: "delta",
	})
	if missing.Status != verify.StatusContradicted || missing.Reason != verify.ReasonContentMissing {
		t.Fatalf("want contradicted/content_missing, got %q/%q", missing.Status, missing.Reason)
	}
}

func TestFileMissingIsContradicted(t *testing.T) {
	c := local.New(t.TempDir())
	outcome := c.Check(context.Background(), verify.Claim{
		Type: "file_exists", Path: filepath.Join(t.TempDir(), "nope.txt"),
	})
	if outcome.Status != verify.StatusContradicted || outcome.Reason != verify.ReasonFileMissing {
		t.Fatalf("want contradicted/file_missing, got %q/%q", outcome.Status, outcome.Reason)
	}
}

func TestFileRelativeResolvesAgainstCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := local.New(dir)
	outcome := c.Check(context.Background(), verify.Claim{
		Type: "file_exists", Path: filepath.Join("sub", "file.txt"),
	})
	if outcome.Status != verify.StatusVerified {
		t.Fatalf("want verified, got %q (%s)", outcome.Status, outcome.Reason)
	}

	fromElsewhere := local.New(t.TempDir())
	missing := fromElsewhere.Check(context.Background(), verify.Claim{
		Type: "file_exists", Path: filepath.Join("sub", "file.txt"),
	})
	if missing.Status != verify.StatusContradicted || missing.Reason != verify.ReasonFileMissing {
		t.Fatalf("want contradicted/file_missing from other cwd, got %q/%q", missing.Status, missing.Reason)
	}
}

func TestFileContainsBoundedRead(t *testing.T) {
	dir := t.TempDir()

	big := filepath.Join(dir, "big.txt")
	data := make([]byte, 0, (2<<20)+len("needle-beyond-cap"))
	data = append(data, []byte(strings.Repeat("a", 2<<20))...)
	data = append(data, []byte("needle-beyond-cap")...)
	if err := os.WriteFile(big, data, 0o644); err != nil {
		t.Fatal(err)
	}

	c := local.New(dir)
	outcome := c.Check(context.Background(), verify.Claim{
		Type: "file_exists", Path: big, Contains: "needle-beyond-cap",
	})
	if outcome.Status != verify.StatusContradicted || outcome.Reason != verify.ReasonContentMissing {
		t.Fatalf("want contradicted/content_missing past the cap, got %q/%q", outcome.Status, outcome.Reason)
	}
	if got := outcome.Evidence[0].Observed["truncated"]; got != true {
		t.Fatalf("want observed.truncated true, got %v", got)
	}

	// A needle inside the cap still matches, including one straddling a chunk
	// boundary.
	small := filepath.Join(dir, "small.txt")
	straddler := strings.Repeat("b", (32<<10)-4) + "chunk-straddling-needle" + strings.Repeat("c", 100)
	if err := os.WriteFile(small, []byte(straddler), 0o644); err != nil {
		t.Fatal(err)
	}
	found := c.Check(context.Background(), verify.Claim{
		Type: "file_exists", Path: small, Contains: "chunk-straddling-needle",
	})
	if found.Status != verify.StatusVerified {
		t.Fatalf("want verified for needle inside the cap, got %q (%s)", found.Status, found.Reason)
	}
}

func TestFileDirectoryIsMissing(t *testing.T) {
	dir := t.TempDir()
	c := local.New(dir)
	outcome := c.Check(context.Background(), verify.Claim{
		Type: "file_exists", Path: dir,
	})
	if outcome.Status != verify.StatusContradicted || outcome.Reason != verify.ReasonFileMissing {
		t.Fatalf("want contradicted/file_missing for a directory, got %q/%q", outcome.Status, outcome.Reason)
	}
	if got := outcome.Evidence[0].Observed["is_dir"]; got != true {
		t.Fatalf("want observed.is_dir true, got %v", got)
	}
}
