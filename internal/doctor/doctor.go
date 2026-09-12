// Package doctor : report detected agent CLIs, provider auth, and hook installation state.
package doctor

import (
	"io"

	"github.com/joshduffy/readback/internal/output"
	"github.com/joshduffy/readback/internal/registry"
	"github.com/spf13/cobra"
)

const name = "doctor"

func init() {
	registry.Register(registry.Module{
		Name:      name,
		Summary:   "Report detected agent CLIs, provider auth, and whether compiled hooks are installed and firing",
		Status:    registry.StatusStub,
		Milestone: "v0.1",
		Keywords:  []string{"doctor", "install", "auth", "gh", "wrangler", "hooks"},
		Schema: map[string]interface{}{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"title":   "readback doctor result",
			"type":    "object",
		},
	})
}

// Command returns the cobra command for this module. Until implemented it exits
// with output.ExitCouldNotCheck and a structured error so agents never mistake a
// stub for a verified result.
func Command(w func() *output.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report detected agent CLIs, provider auth, and hook installation state",
		RunE: func(cmd *cobra.Command, args []string) error {
			code := w().Emit(output.Result{
				Command: name,
				OK:      false,
				Exit:    output.ExitCouldNotCheck,
				Error:   name + ": not implemented (milestone v0.1)",
			}, func(o io.Writer) {})
			cmd.SilenceUsage = true
			return exitError(code)
		},
	}
}

type exitError int

func (e exitError) Error() string { return "" }
func (e exitError) ExitCode() int { return int(e) }
