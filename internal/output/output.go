// Package output owns the two output contracts every readback command honors:
// JSON when --json is set or stdout is not a TTY, a human table otherwise.
// Exit codes are part of the contract and agents branch on them.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// Exit codes. Stable; documented in README and `readback schema exit-codes`.
const (
	ExitVerified      = 0 // claim proven, policy satisfied, nothing at risk
	ExitUnproven      = 1 // a claim failed readback, a rule fired, or work is at risk
	ExitCouldNotCheck = 2 // the system of record was unreachable or the input was unusable
	ExitUsage         = 64
)

// Writer renders results in the mode chosen at startup.
type Writer struct {
	out  io.Writer
	err  io.Writer
	JSON bool
}

func New(out, err io.Writer, forceJSON bool) *Writer {
	return &Writer{out: out, err: err, JSON: forceJSON || !isTTY(out)}
}

// Result is the envelope every JSON response uses.
type Result struct {
	Command string      `json:"command"`
	OK      bool        `json:"ok"`
	Exit    int         `json:"exit"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// Emit writes r as JSON or delegates to human for table output. Returns r.Exit.
func (w *Writer) Emit(r Result, human func(io.Writer)) int {
	if w.JSON {
		out := &checkedWriter{Writer: w.out}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r); err != nil || out.err != nil {
			return writeFailureExit(r.Exit)
		}
		return r.Exit
	}
	out := &checkedWriter{Writer: w.out}
	errOut := &checkedWriter{Writer: w.err}
	if human != nil {
		human(out)
	}
	if r.Error != "" {
		fmt.Fprintln(errOut, "error:", r.Error)
	}
	if out.err != nil || errOut.err != nil {
		return writeFailureExit(r.Exit)
	}
	return r.Exit
}

type checkedWriter struct {
	io.Writer
	err error
}

func (w *checkedWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}

func writeFailureExit(current int) int {
	if current != ExitVerified {
		return current
	}
	return ExitCouldNotCheck
}

// Table is a small helper for aligned human output.
func Table(out io.Writer, header []string, rows [][]string) {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
