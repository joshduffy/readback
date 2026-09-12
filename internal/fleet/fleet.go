// Package fleet : Crawl a root for every repo and surface stashes, no-upstream branches, missing remotes, stale worktrees, dirty trees
package fleet

import (
	"io"

	"github.com/joshduffy/readback/internal/output"
	"github.com/joshduffy/readback/internal/registry"
	"github.com/spf13/cobra"
)

const name = "fleet"

func init() {
	registry.Register(registry.Module{
		Name:      name,
		Summary:   "Crawl a root for every repo and surface stashes, no-upstream branches, missing remotes, stale worktrees, dirty trees",
		Status:    registry.StatusPlanned,
		Milestone: "v0.3",
		Keywords:  []string{"fleet", "orphan", "stash", "worktree", "dirty", "upstream"},
		Schema: map[string]interface{}{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"title":   "readback fleet result",
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
		Short: "Crawl a root for every repo and surface stashes, no-upstream branches, missing remotes, stale worktrees, dirty trees",
		RunE: func(cmd *cobra.Command, args []string) error {
			code := w().Emit(output.Result{
				Command: name,
				OK:      false,
				Exit:    output.ExitCouldNotCheck,
				Error:   name + ": not implemented (milestone v0.3)",
			}, func(o io.Writer) {})
			cmd.SilenceUsage = true
			return exitError(code)
		},
	}
}

type exitError int

func (e exitError) Error() string { return "" }
func (e exitError) ExitCode() int { return int(e) }
