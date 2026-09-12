package cli

// exitCoder is implemented by module errors that carry a process exit code.
type exitCoder interface{ ExitCode() int }
