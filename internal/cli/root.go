// Package cli wires the cobra root. Global flags: --json. Discovery commands
// (capabilities, schema, search, install-skills) live here because they read the
// registry rather than doing work.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/joshduffy/readback/internal/deploy"
	"github.com/joshduffy/readback/internal/doctor"
	"github.com/joshduffy/readback/internal/fleet"
	"github.com/joshduffy/readback/internal/hook"
	"github.com/joshduffy/readback/internal/memory"
	"github.com/joshduffy/readback/internal/output"
	"github.com/joshduffy/readback/internal/policy"
	"github.com/joshduffy/readback/internal/registry"
	"github.com/joshduffy/readback/internal/verify"
	"github.com/spf13/cobra"
)

// Version is set by goreleaser via -ldflags.
var Version = "dev"

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
		verify.Command(getW),
		deploy.Command(getW),
		doctor.Command(getW),
		policy.Command(getW),
		hook.Command(getW),
		fleet.Command(getW),
		memory.Command(getW),
		capabilitiesCmd(getW),
		schemaCmd(getW),
		searchCmd(getW),
	)

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
			mods := registry.All()
			w().Emit(output.Result{Command: "capabilities", OK: true, Data: map[string]interface{}{
				"version": Version, "modules": mods,
			}}, func(o io.Writer) {
				rows := make([][]string, 0, len(mods))
				for _, m := range mods {
					rows = append(rows, []string{m.Name, string(m.Status), m.Milestone, m.Summary})
				}
				output.Table(o, []string{"MODULE", "STATUS", "MILESTONE", "SUMMARY"}, rows)
			})
			return nil
		},
	}
}

func schemaCmd(w func() *output.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "schema <module>",
		Short: "Print the JSON schema of a module's result payload",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, ok := registry.Get(args[0])
			if !ok {
				cmd.SilenceUsage = true
				return fmt.Errorf("unknown module %q (try: readback search %s)", args[0], args[0])
			}
			w().Emit(output.Result{Command: "schema", OK: true, Data: m.Schema}, func(o io.Writer) {
				fmt.Fprintf(o, "%s: %s\n", m.Name, m.Summary)
			})
			return nil
		},
	}
}

func searchCmd(w func() *output.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "search <term>",
		Short: "Find a module by name, summary, or keyword",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hits := registry.Search(args[0])
			w().Emit(output.Result{Command: "search", OK: true, Data: hits}, func(o io.Writer) {
				rows := make([][]string, 0, len(hits))
				for _, m := range hits {
					rows = append(rows, []string{m.Name, m.Summary})
				}
				output.Table(o, []string{"MODULE", "SUMMARY"}, rows)
			})
			return nil
		},
	}
}
