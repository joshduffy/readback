// Package verify : Read side-effect claims in an agent report back from GitHub, deploy platforms, and URLs
package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
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
		Status:    registry.StatusBeta,
		Milestone: "v0.1",
		Keywords:  []string{"claim", "handoff", "hallucination", "merged", "deployed", "sent"},
		Schemas: map[string]map[string]interface{}{
			"input": claimsSchema(), "result": ResultSchema(),
		},
	})
}

// claimsSchema is the embedded claims.v1.json, printed by `readback schema verify`.
func claimsSchema() map[string]interface{} {
	var schema map[string]interface{}
	if err := json.Unmarshal(ClaimsSchemaJSON(), &schema); err != nil {
		panic("embedded claims schema is not valid JSON: " + err.Error())
	}
	return schema
}

func ResultSchema() map[string]interface{} {
	var schema map[string]interface{}
	if err := json.Unmarshal(ResultSchemaJSON(), &schema); err != nil {
		panic("embedded result schema is not valid JSON: " + err.Error())
	}
	return schema
}

// RegistryOptions carries CLI flags that shape how providers are built.
type RegistryOptions struct {
	NoCacheBust bool
	Assertions  *Assertions
}

type RegistryFactory func(cwd string, opts RegistryOptions) Registry

func Command(w func() *output.Writer, factory RegistryFactory) *cobra.Command {
	if factory == nil {
		factory = func(string, RegistryOptions) Registry { return StubRegistry() }
	}
	var assertionsPath, cwd string
	var timeout time.Duration
	var noCacheBust bool
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
				return fail(2, fmt.Errorf("verify: no usable input; supply a claims JSON path or a Markdown file with a readback-claims fence"))
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
			return exitError(ExecuteDocument(cmd.Context(), w(), factory, CommandRun{
				Command: name, Document: doc, InputPath: args[0], AssertionsPath: assertionsPath,
				CWD: cwd, Timeout: timeout, NoCacheBust: noCacheBust,
			}))
		},
	}
	cmd.Flags().StringVar(&assertionsPath, "assertions", "", "operator-owned assertions file")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory for assertion discovery and local providers")
	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "overall verification timeout")
	cmd.Flags().BoolVar(&noCacheBust, "no-cache-bust", false, "do not append the readback_bust query parameter to probed URLs")
	return cmd
}

type CommandRun struct {
	Command        string
	Document       Document
	InputPath      string
	AssertionsPath string
	CWD            string
	Timeout        time.Duration
	NoCacheBust    bool
}

func ExecuteDocument(ctx context.Context, writer *output.Writer, factory RegistryFactory, command CommandRun) int {
	fail := func(code int, err error) int {
		return writer.Emit(output.Result{Command: command.Command, Exit: code, Error: err.Error()}, nil)
	}
	if command.Timeout <= 0 {
		return fail(output.ExitUsage, fmt.Errorf("timeout must be positive"))
	}
	workingDir := command.CWD
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return fail(output.ExitCouldNotCheck, err)
		}
	}
	assertions, assertionsPath, err := ResolveAssertions(workingDir, command.AssertionsPath)
	if err != nil {
		var readError *os.PathError
		if errors.As(err, &readError) {
			return fail(output.ExitCouldNotCheck, err)
		}
		return fail(output.ExitUsage, err)
	}
	if factory == nil {
		factory = func(string, RegistryOptions) Registry { return StubRegistry() }
	}
	runCtx, cancel := context.WithTimeout(ctx, command.Timeout)
	defer cancel()
	result := Run(runCtx, RunInput{
		Doc: command.Document, Assertions: assertions,
		Registry: factory(workingDir, RegistryOptions{NoCacheBust: command.NoCacheBust, Assertions: assertions}),
	})
	result.Input.Path, result.Input.Assertions = command.InputPath, assertionsPath
	code := result.ExitCode()
	if _, err := json.Marshal(result); err != nil {
		if code == output.ExitVerified {
			code = output.ExitCouldNotCheck
		}
		return fail(code, fmt.Errorf("encode result: %w", err))
	}
	return writer.Emit(output.Result{Command: command.Command, OK: code == 0, Exit: code, Data: result}, func(out io.Writer) {
		renderRunResult(out, result)
	})
}

func renderRunResult(out io.Writer, result RunResult) {
	for _, claim := range result.Claims {
		fmt.Fprintf(out, "%s  %s  %s  %s\n", claim.Status, terminalText(claim.Type), terminalText(claim.ID), terminalText(claim.Reason))
		if len(claim.Evidence) > 0 {
			evidence := claim.Evidence[0]
			observed, err := json.Marshal(evidence.Observed)
			if err != nil {
				observed = []byte("{\"message\":\"evidence unavailable\"}")
			}
			if len(observed) > 240 {
				observed = append(observed[:237], '.', '.', '.')
			}
			fmt.Fprintf(out, "  evidence: %s  %s  %s\n", terminalText(evidence.Source), terminalText(evidence.Call), observed)
		}
	}
	fmt.Fprintf(out, "summary: verified=%d contradicted=%d indeterminate=%d required_unmet=%d\n", result.Summary.Verified, result.Summary.Contradicted, result.Summary.Indeterminate, len(result.Summary.RequiredUnmet))
	for _, requirement := range result.Summary.RequiredUnmet {
		fields, _ := json.Marshal(requirement)
		fmt.Fprintf(out, "%s  %s\n", ReasonRequiredAssertionUnmet, fields)
	}
}

func terminalText(value string) string {
	quoted := strconv.QuoteToASCII(value)
	return quoted[1 : len(quoted)-1]
}

type exitError int

func (e exitError) Error() string { return "" }
func (e exitError) ExitCode() int { return int(e) }
