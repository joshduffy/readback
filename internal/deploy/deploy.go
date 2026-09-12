// Package deploy : Prove a commit SHA is live: poll the platform build, then fetch a content marker from the edge
package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/joshduffy/readback/internal/output"
	"github.com/joshduffy/readback/internal/registry"
	"github.com/joshduffy/readback/internal/verify"
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
			"title":   "readback verify-deploy result",
			"type":    "object",
		},
	})
}

var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// Command builds exactly one deployment_serving claim from flags and runs it
// through the same runner as verify, with the same envelope and exit codes.
func Command(w func() *output.Writer, factory verify.RegistryFactory) *cobra.Command {
	if factory == nil {
		factory = func(string, verify.RegistryOptions) verify.Registry { return verify.StubRegistry() }
	}
	var url, marker, health, worker, account, provider, assertionsPath, cwd string
	var timeout time.Duration
	var noCacheBust bool
	cmd := &cobra.Command{
		Use:   "verify-deploy <sha>",
		Short: "Prove a commit SHA is serving: build, activation, marker observed at the edge",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			fail := func(code int, err error) error {
				return exitError(w().Emit(output.Result{Command: name, Exit: code, Error: err.Error()}, nil))
			}
			if len(args) == 0 {
				return fail(64, fmt.Errorf("verify-deploy: a full 40-character commit sha and --url are required"))
			}
			if timeout <= 0 {
				return fail(64, fmt.Errorf("timeout must be positive"))
			}
			if !shaPattern.MatchString(args[0]) {
				return fail(64, fmt.Errorf("sha must be the full 40-character commit hash"))
			}
			workingDir := cwd
			if workingDir == "" {
				var err error
				workingDir, err = os.Getwd()
				if err != nil {
					return fail(2, err)
				}
			}
			doc := verify.Document{Version: verify.ClaimsSchemaVersion, Claims: []verify.Claim{{
				Type:         "deployment_serving",
				SHA:          args[0],
				URL:          url,
				ExpectStatus: 200,
				Marker:       marker,
				Provider:     provider,
				Account:      account,
				Worker:       worker,
				Health:       health,
			}}}
			if err := verify.Validate(doc); err != nil {
				return fail(64, err)
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
			var assertions *verify.Assertions
			if path != "" {
				loaded, err := verify.LoadAssertions(path)
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
			result := verify.Run(ctx, verify.RunInput{Doc: doc, Assertions: assertions, Registry: factory(workingDir, verify.RegistryOptions{NoCacheBust: noCacheBust})})
			result.Input.Assertions = path
			code := result.ExitCode()
			return exitError(w().Emit(output.Result{Command: name, OK: code == 0, Exit: code, Data: result}, func(o io.Writer) {
				for _, claim := range result.Claims {
					fmt.Fprintf(o, "%s  %s  %s  %s\n", claim.Status, claim.Type, claim.ID, claim.Reason)
				}
				fmt.Fprintf(o, "summary: verified=%d contradicted=%d indeterminate=%d required_unmet=%d\n", result.Summary.Verified, result.Summary.Contradicted, result.Summary.Indeterminate, len(result.Summary.RequiredUnmet))
				for _, requirement := range result.Summary.RequiredUnmet {
					fields, _ := json.Marshal(requirement)
					fmt.Fprintf(o, "%s  %s\n", verify.ReasonRequiredAssertionUnmet, fields)
				}
			}))
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "https URL the deployment must serve")
	cmd.Flags().StringVar(&marker, "marker", "", "content marker expected at --url")
	cmd.Flags().StringVar(&health, "health", "", "https health URL exposing the deployed commit sha")
	cmd.Flags().StringVar(&worker, "worker", "", "Cloudflare Worker name")
	cmd.Flags().StringVar(&account, "account", "", "Cloudflare account id")
	cmd.Flags().StringVar(&provider, "provider", "cloudflare-workers", "deployment provider")
	cmd.Flags().StringVar(&assertionsPath, "assertions", "", "operator-owned assertions file")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory for assertion discovery and local providers")
	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "overall verification timeout")
	cmd.Flags().BoolVar(&noCacheBust, "no-cache-bust", false, "do not append the readback_bust query parameter to probed URLs")
	return cmd
}

type exitError int

func (e exitError) Error() string { return "" }
func (e exitError) ExitCode() int { return int(e) }
