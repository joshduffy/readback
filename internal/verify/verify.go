// Package verify : Read side-effect claims in an agent report back from GitHub, deploy platforms, and URLs
package verify

import (
	"io"

	"github.com/joshduffy/readback/internal/output"
	"github.com/joshduffy/readback/internal/registry"
	"github.com/spf13/cobra"
)

const name = "verify"

func init() {
	registry.Register(registry.Module{
		Name:      name,
		Summary:   "Verify typed side-effect claims against GitHub, Cloudflare Workers Builds, and HTTP; per-claim verified, contradicted, or indeterminate",
		Status:    registry.StatusStub,
		Milestone: "v0.1",
		Keywords:  []string{"claim", "handoff", "hallucination", "merged", "deployed", "sent"},
		Schema: map[string]interface{}{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"title":   "readback verify result",
			"type":    "object",
		},
	})
}

// Command returns the cobra command for this module. Until implemented it exits
// with output.ExitCouldNotCheck and a structured error so agents never mistake a
// stub for a verified result.
func Command(w func() *output.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "verify <claims.json|handoff.md>",
		Short: "Read side-effect claims in an agent report back from GitHub, deploy platforms, and URLs",
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
