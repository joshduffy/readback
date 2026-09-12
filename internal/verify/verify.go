// Package verify : Read side-effect claims in an agent report back from GitHub, deploy platforms, and URLs
package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

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

type RegistryFactory func(cwd string) Registry

func Command(w func() *output.Writer, factory RegistryFactory) *cobra.Command {
	if factory == nil {
		factory = func(string) Registry { return StubRegistry() }
	}
	var assertionsPath, cwd string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "verify <path>",
		Short: "Verify typed claims from JSON or a readback-claims fence",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			fail := func(code int, err error) error {
				return exitError(w().Emit(output.Result{Command: name, Exit: code, Error: err.Error()}, nil))
			}
			if len(args) == 0 {
				return fail(2, fmt.Errorf("verify: no usable input; providers not implemented (milestone v0.1); supply a claims path"))
			}
			if timeout <= 0 {
				return fail(64, fmt.Errorf("timeout must be positive"))
			}
			workingDir := cwd
			if workingDir == "" {
				var err error
				workingDir, err = os.Getwd()
				if err != nil {
					return fail(2, err)
				}
			}
			src, err := os.ReadFile(args[0])
			if err != nil {
				return fail(2, err)
			}
			doc, err := ExtractClaims(src)
			if err != nil {
				var validation *ValidationError
				if errors.As(err, &validation) {
					return fail(64, validation)
				}
				return fail(2, err)
			}
			path := assertionsPath
			if path == "" {
				path = filepath.Join(workingDir, "readback.assertions.yaml")
				info, err := os.Stat(path)
				switch {
				case errors.Is(err, os.ErrNotExist):
					path = ""
				case err != nil:
					return fail(2, err)
				case !info.Mode().IsRegular():
					path = ""
				}
			}
			var assertions *Assertions
			if path != "" {
				loaded, err := LoadAssertions(path)
				if err != nil {
					var readError *os.PathError
					if errors.As(err, &readError) {
						return fail(2, err)
					}
					return fail(64, err)
				}
				assertions = &loaded
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			result := Run(ctx, RunInput{Doc: doc, Assertions: assertions, Registry: factory(workingDir)})
			result.Input.Path, result.Input.Assertions = args[0], path
			code := result.ExitCode()
			return exitError(w().Emit(output.Result{Command: name, OK: code == 0, Exit: code, Data: result}, func(o io.Writer) {
				for _, claim := range result.Claims {
					fmt.Fprintf(o, "%s  %s  %s  %s\n", claim.Status, claim.Type, claim.ID, claim.Reason)
				}
				fmt.Fprintf(o, "summary: verified=%d contradicted=%d indeterminate=%d required_unmet=%d\n", result.Summary.Verified, result.Summary.Contradicted, result.Summary.Indeterminate, len(result.Summary.RequiredUnmet))
				for _, requirement := range result.Summary.RequiredUnmet {
					fields, _ := json.Marshal(requirement)
					fmt.Fprintf(o, "%s  %s\n", ReasonRequiredAssertionUnmet, fields)
				}
			}))
		},
	}
	cmd.Flags().StringVar(&assertionsPath, "assertions", "", "operator-owned assertions file")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory for assertion discovery and local providers")
	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "overall verification timeout")
	return cmd
}

type exitError int

func (e exitError) Error() string { return "" }
func (e exitError) ExitCode() int { return int(e) }
