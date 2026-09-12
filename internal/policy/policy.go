// Package policy : Compile one readback.policy.yaml into every agent CLI hook format
package policy

import (
	"io"

	"github.com/joshduffy/readback/internal/output"
	"github.com/joshduffy/readback/internal/registry"
	"github.com/spf13/cobra"
)

const name = "policy"

func init() {
	registry.Register(registry.Module{
		Name:      name,
		Summary:   "Compile one readback.policy.yaml into every agent CLI hook format",
		Status:    registry.StatusPlanned,
		Milestone: "v0.2",
		Keywords:  []string{"hook", "policy", "claude code", "codex", "cursor", "gemini", "agent-hooks"},
		Schema: map[string]interface{}{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"title":   "readback policy result",
			"type":    "object",
		},
	})
}

// Command returns the cobra command for this module. Until implemented it exits
// with output.ExitCouldNotCheck and a structured error so agents never mistake a
// stub for a verified result.
func Command(w func() *output.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: "Compile one readback.policy.yaml into every agent CLI hook format",
		RunE: func(cmd *cobra.Command, args []string) error {
			code := w().Emit(output.Result{
				Command: name,
				OK:      false,
				Exit:    output.ExitCouldNotCheck,
				Error:   name + ": not implemented (milestone v0.2)",
			}, func(o io.Writer) {})
			cmd.SilenceUsage = true
			return exitError(code)
		},
	}
}

type exitError int

func (e exitError) Error() string { return "" }
func (e exitError) ExitCode() int { return int(e) }
