// Package deploy : Prove a commit SHA is live: poll the platform build, then fetch a content marker from the edge
package deploy

import (
	"io"

	"github.com/joshduffy/readback/internal/output"
	"github.com/joshduffy/readback/internal/registry"
	"github.com/spf13/cobra"
)

const name = "verify-deploy"

func init() {
	registry.Register(registry.Module{
		Name:      name,
		Summary:   "Prove a commit SHA is serving: build success, deployment activation, marker observation, optional smoke check",
		Status:    registry.StatusStub,
		Milestone: "v0.1",
		Keywords:  []string{"deploy", "sha", "cloudflare", "vercel", "railway", "netlify", "fly", "github actions"},
		Schema: map[string]interface{}{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"title":   "readback deploy result",
			"type":    "object",
		},
	})
}

// Command returns the cobra command for this module. Until implemented it exits
// with output.ExitCouldNotCheck and a structured error so agents never mistake a
// stub for a verified result.
func Command(w func() *output.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "verify-deploy <sha>",
		Short: "Prove a commit SHA is serving: build, activation, marker observed at the edge",
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
