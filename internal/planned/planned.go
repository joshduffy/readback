package planned

import (
	"github.com/joshduffy/readback/internal/output"
	"github.com/joshduffy/readback/internal/registry"
	"github.com/spf13/cobra"
)

type module struct {
	name      string
	summary   string
	milestone string
	keywords  []string
}

var modules = []module{
	{
		name: "fleet", milestone: "v0.3",
		summary:  "Crawl a root for every repo and surface stashes, no-upstream branches, missing remotes, stale worktrees, dirty trees",
		keywords: []string{"fleet", "orphan", "stash", "worktree", "dirty", "upstream"},
	},
	{
		name: "hook", milestone: "v0.2",
		summary:  "Runtime entrypoint the compiled hooks call; reads the CLI hook event on stdin",
		keywords: []string{"hook", "pretooluse", "stdin", "deny", "allow"},
	},
	{
		name: "memory", milestone: "v0.3",
		summary:  "Lint agent memory files: stale references, duplicates, verdicts-vs-facts, PII, credentials, missing index",
		keywords: []string{"memory", "lint", "MEMORY.md", "verdict", "stale", "pii"},
	},
	{
		name: "policy", milestone: "v0.2",
		summary:  "Compile one readback.policy.yaml into every agent CLI hook format",
		keywords: []string{"hook", "policy", "claude code", "codex", "cursor", "gemini", "agent-hooks"},
	},
}

func init() {
	for _, planned := range modules {
		registry.Register(registry.Module{
			Name: planned.name, Summary: planned.summary, Status: registry.StatusPlanned,
			Milestone: planned.milestone, Keywords: planned.keywords,
			Schemas: map[string]map[string]interface{}{"result": {
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"title":   "readback " + planned.name + " result",
				"type":    "object",
			}},
		})
	}
}

func Commands(writer func() *output.Writer) []*cobra.Command {
	commands := make([]*cobra.Command, 0, len(modules))
	for _, planned := range modules {
		commands = append(commands, &cobra.Command{
			Use: planned.name, Short: "Planned: " + planned.summary,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cmd.SilenceUsage = true
				code := writer().Emit(output.Result{
					Command: planned.name, Exit: output.ExitCouldNotCheck,
					Error: planned.name + ": not implemented (milestone " + planned.milestone + ")",
				}, nil)
				return exitError(code)
			},
		})
	}
	return commands
}

type exitError int

func (e exitError) Error() string { return "" }
func (e exitError) ExitCode() int { return int(e) }
