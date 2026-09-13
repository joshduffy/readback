package cli

import "github.com/joshduffy/readback/internal/output"

// exitCoder is implemented by module errors that carry a process exit code.
type exitCoder interface{ ExitCode() int }

type commandExit int

func (e commandExit) Error() string { return "" }
func (e commandExit) ExitCode() int { return int(e) }

func emitted(code int) error {
	if code == output.ExitVerified {
		return nil
	}
	return commandExit(code)
}
