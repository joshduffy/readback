// Package cli wires the cobra root. Global flags: --json. Discovery commands
// (capabilities, schema, search, install-skills) live here because they read the
// registry rather than doing work.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/joshduffy/readback/internal/deploy"
	"github.com/joshduffy/readback/internal/doctor"
	"github.com/joshduffy/readback/internal/output"
	"github.com/joshduffy/readback/internal/planned"
	"github.com/joshduffy/readback/internal/providers/cloudflare"
	"github.com/joshduffy/readback/internal/providers/github"
	httpprovider "github.com/joshduffy/readback/internal/providers/http"
	"github.com/joshduffy/readback/internal/providers/local"
	"github.com/joshduffy/readback/internal/registry"
	"github.com/joshduffy/readback/internal/verify"
	"github.com/spf13/cobra"
)

// Version is set by goreleaser via -ldflags.
var Version = "dev"

var registryFactory verify.RegistryFactory = defaultRegistry

func defaultRegistry(cwd string, opts verify.RegistryOptions) verify.Registry {
	registry := verify.StubRegistry()
	gh := github.New(nil)
	for _, kind := range []string{"pr_merged", "checks_passed", "commit_on_branch"} {
		registry[kind] = gh
	}
	userAgent := "readback/" + Version
	registry["url_serving"] = httpprovider.New(httpprovider.Options{
		UserAgent: userAgent, DisableCacheBust: opts.NoCacheBust, Assertions: opts.Assertions,
	})
	registry["deployment_serving"] = cloudflare.New(cloudflare.Options{
		URLChecker: registry["url_serving"], UserAgent: userAgent,
		DisableCacheBust: opts.NoCacheBust, Assertions: opts.Assertions,
	})
	registry["file_exists"] = local.New(cwd)
	return registry
}

func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var forceJSON bool
	var w *output.Writer
	getW := func() *output.Writer { return w }

	root := &cobra.Command{
		Use:           "readback",
		Short:         "Agent ops for your own machine. Agents claim; readback verifies.",
		Version:       Version,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			w = output.New(stdout, stderr, forceJSON)
			doctor.Version = Version
		},
	}
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().BoolVar(&forceJSON, "json", false, "emit JSON regardless of TTY")

	root.AddCommand(
		verify.Command(getW, registryFactory),
		deploy.Command(getW, registryFactory),
		doctor.Command(getW),
		capabilitiesCmd(getW),
		schemaCmd(getW),
		searchCmd(getW),
		installSkillsCmd(getW),
	)
	root.AddCommand(planned.Commands(getW)...)

	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		var ec exitCoder
		if errors.As(err, &ec) {
			return ec.ExitCode()
		}
		fmt.Fprintln(stderr, "error:", err)
		return output.ExitUsage
	}
	return output.ExitVerified
}

func capabilitiesCmd(w func() *output.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities",
		Short: "List modules, their status, and milestone",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mods := moduleSummaries(registry.All())
			return emitted(w().Emit(output.Result{Command: "capabilities", OK: true, Data: map[string]interface{}{
				"version": Version, "modules": mods,
			}}, func(o io.Writer) {
				rows := make([][]string, 0, len(mods))
				for _, m := range mods {
					rows = append(rows, []string{m.Name, string(m.Status), m.Milestone, m.Summary})
				}
				output.Table(o, []string{"MODULE", "STATUS", "MILESTONE", "SUMMARY"}, rows)
			}))
		},
	}
}

func schemaCmd(w func() *output.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "schema <module>",
		Short: "Print a module's named input and result JSON schemas",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, ok := registry.Get(args[0])
			if !ok {
				cmd.SilenceUsage = true
				return fmt.Errorf("unknown module %q (try: readback search %s)", args[0], args[0])
			}
			encoded, err := json.MarshalIndent(m.Schemas, "", "  ")
			if err != nil {
				return emitted(w().Emit(output.Result{Command: "schema", Exit: output.ExitCouldNotCheck, Error: err.Error()}, nil))
			}
			return emitted(w().Emit(output.Result{Command: "schema", OK: true, Data: m.Schemas}, func(o io.Writer) {
				fmt.Fprintln(o, string(encoded))
			}))
		},
	}
}

func searchCmd(w func() *output.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "search <term>",
		Short: "Find a module by name, summary, or keyword",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			matches := moduleSummaries(registry.Search(args[0]))
			return emitted(w().Emit(output.Result{Command: "search", OK: true, Data: matches}, func(o io.Writer) {
				rows := make([][]string, 0, len(matches))
				for _, m := range matches {
					rows = append(rows, []string{m.Name, m.Summary})
				}
				output.Table(o, []string{"MODULE", "SUMMARY"}, rows)
			}))
		},
	}
}

type moduleSummary struct {
	Name      string          `json:"name"`
	Summary   string          `json:"summary"`
	Status    registry.Status `json:"status"`
	Milestone string          `json:"milestone"`
}

func moduleSummaries(modules []registry.Module) []moduleSummary {
	summaries := make([]moduleSummary, 0, len(modules))
	for _, module := range modules {
		summaries = append(summaries, moduleSummary{Name: module.Name, Summary: module.Summary, Status: module.Status, Milestone: module.Milestone})
	}
	return summaries
}
