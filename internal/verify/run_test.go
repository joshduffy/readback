package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshduffy/readback/internal/output"
)

type testChecker func(context.Context, Claim) Outcome

func (f testChecker) Check(ctx context.Context, c Claim) Outcome { return f(ctx, c) }

func TestExitPrecedenceContradictedBeatsIndeterminate(t *testing.T) {
	r := Run(context.Background(), RunInput{Doc: Document{Claims: []Claim{{Type: "file_exists"}, {Type: "future"}}}, Registry: Registry{"file_exists": testChecker(func(context.Context, Claim) Outcome {
		return Outcome{Status: StatusContradicted, Reason: ReasonFileMissing}
	})}})
	if r.ExitCode() != 1 || r.Summary.Contradicted != 1 || r.Summary.Indeterminate != 1 {
		t.Fatalf("result: %+v", r)
	}
}
func TestEmptyClaimsIsExit2(t *testing.T) {
	if r := Run(context.Background(), RunInput{}); r.ExitCode() != 2 {
		t.Fatalf("result: %+v", r)
	}
}
func TestResultJSONMatchesSchema(t *testing.T) {
	schema, err := os.ReadFile("../../schemas/result.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct{ Required []string }
	if err = json.Unmarshal(schema, &spec); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 12, 18, 2, 11, 0, time.UTC)
	r := Run(context.Background(), RunInput{Doc: Document{Claims: []Claim{{Type: "file_exists", ID: "c1"}}}, Now: func() time.Time { return now }, Registry: Registry{"file_exists": testChecker(func(context.Context, Claim) Outcome { return Outcome{Status: StatusVerified} })}})
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	for _, key := range spec.Required {
		if _, ok := record[key]; !ok {
			t.Errorf("missing %s", key)
		}
	}
	var claims []map[string]json.RawMessage
	if err = json.Unmarshal(record["claims"], &claims); err != nil {
		t.Fatal(err)
	}
	if string(claims[0]["reason"]) != "null" || string(claims[0]["evidence"]) != "[]" || string(claims[0]["checked_at"]) != `"2026-09-12T18:02:11Z"` || string(claims[0]["claim"]) != `{"type":"file_exists"}` {
		t.Fatalf("JSON: %s", data)
	}
	if _, ok := claims[0]["Claim"]; ok {
		t.Fatal("internal Claim leaked")
	}
	if r.ExitCode() != 0 {
		t.Fatal(r)
	}
}

func TestCheckedConditionRedactsCredentials(t *testing.T) {
	const repeated = "repeated-sensitive-value"
	condition := checkedCondition(Claim{
		Type: "url_serving",
		URL:  "https://user:password@example.com/path?keep=1&token=secret#private",
		Header: map[string]string{
			"Authorization": "authorization-sensitive-value",
			"Bearer":        "bearer-sensitive-value",
			"PRIVATE-TOKEN": repeated,
			"X-Auth-Token":  repeated,
			"X-CSRF-Token":  "csrf-sensitive-value",
			"X-Version":     "one",
		},
	})
	data, err := json.Marshal(condition)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"user", "password", "secret", "private", repeated,
		"authorization-sensitive-value", "bearer-sensitive-value", "csrf-sensitive-value",
	} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("condition leaked %q in %s", secret, data)
		}
	}
	for _, safe := range []string{"example.com", "keep=1", "X-Version", "one", "rb_"} {
		if !strings.Contains(string(data), safe) {
			t.Fatalf("condition lost %q in %s", safe, data)
		}
	}
	if condition.Header["PRIVATE-TOKEN"] != condition.Header["X-Auth-Token"] ||
		condition.Header["PRIVATE-TOKEN"] == condition.Header["X-CSRF-Token"] {
		t.Fatalf("repeated and distinct header values lost their relationship: %#v", condition.Header)
	}
}

func TestCheckedConditionOmitsUnusedClaimDefaults(t *testing.T) {
	condition := checkedCondition(Claim{
		Type: "file_exists", Path: "go.mod", Contains: "module ", ExpectStatus: 200,
		URL: "https://user:password@example.com/?token=secret",
	})
	data, err := json.Marshal(condition)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"type":"file_exists","path":"go.mod","contains":"module "}`; got != want {
		t.Fatalf("condition = %s, want %s", got, want)
	}
}

func TestTerminalResultEscapesControlCharacters(t *testing.T) {
	result := RunResult{
		Summary: Summary{RequiredUnmet: []Requirement{}},
		Claims: []ClaimResult{{
			Type: "file_exists", ID: "line\n\x1b[31mred", Status: StatusVerified,
			Evidence: []Evidence{{Source: "local\nsource", Call: "stat\x1b[2J", Observed: map[string]any{"value": "line\nvalue"}}},
		}},
	}
	var out bytes.Buffer
	renderRunResult(&out, result)
	if strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), "line\nsource") {
		t.Fatalf("raw control character in %q", out.String())
	}
	for _, escaped := range []string{`local\nsource`, `\x1b[31m`, `stat\x1b[2J`, `line\nvalue`} {
		if !strings.Contains(out.String(), escaped) {
			t.Fatalf("missing %q in %q", escaped, out.String())
		}
	}
}
func TestUnsupportedTypeNeverReachesChecker(t *testing.T) {
	r := Run(context.Background(), RunInput{Doc: Document{Claims: []Claim{{Type: "future"}}}, Registry: Registry{"future": testChecker(func(context.Context, Claim) Outcome { t.Fatal("checker called"); return Outcome{} })}})
	if r.ExitCode() != 2 || r.Claims[0].Reason != ReasonUnsupportedClaimType {
		t.Fatal(r)
	}
}
func TestHostNotAllowedSkipsChecker(t *testing.T) {
	for _, c := range []Claim{
		{Type: "url_serving", URL: "https://foreign.test"},
		{Type: "deployment_serving", URL: "https://allowed.test", Health: "https://foreign.test"},
		{Type: "deployment_serving", URL: "https://allowed.test", Smoke: []Claim{{URL: "https://foreign.test"}}},
	} {
		r := Run(context.Background(), RunInput{Doc: Document{Claims: []Claim{c}}, Assertions: &Assertions{AllowHosts: []string{"allowed.test"}}, Registry: Registry{c.Type: testChecker(func(context.Context, Claim) Outcome { t.Fatal("checker called"); return Outcome{} })}})
		if r.ExitCode() != 1 || r.Claims[0].Reason != ReasonHostNotAllowed {
			t.Fatal(r)
		}
	}
}
func TestRequiredUnmetForcesExit1(t *testing.T) {
	r := Run(context.Background(), RunInput{Assertions: &Assertions{Require: []Requirement{{"type": "pr_merged"}}}})
	if r.ExitCode() != 1 || len(r.Summary.RequiredUnmet) != 1 {
		t.Fatal(r)
	}
}
func TestStubRegistryIndeterminate(t *testing.T) {
	registry := StubRegistry()
	if len(registry) != 6 {
		t.Fatal(registry)
	}
	for kind := range registry {
		r := Run(context.Background(), RunInput{Doc: Document{Claims: []Claim{{Type: kind}}}, Registry: registry})
		if r.ExitCode() != 2 || r.Claims[0].Status != StatusIndeterminate || r.Claims[0].Reason != ReasonProviderUnreachable || r.Claims[0].Evidence[0].Source != "stub" {
			t.Fatal(r)
		}
	}
}
func TestVerifyCommandExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"stub", `{"version":1,"claims":[{"type":"file_exists","path":"README.md"}]}`, 2},
		{"schema", `{"version":1,"claims":[{"type":"checks_passed","repo":"o/r","sha":"short"}]}`, 64},
		{"missing", "", 2},
		{"prose", "everything deployed", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "claims.json")
			if tc.body != "" {
				if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			cmd := Command(func() *output.Writer { return output.New(&out, &out, true) }, nil)
			cmd.SetArgs([]string{path, "--cwd", dir})
			err := cmd.Execute()
			var ec interface{ ExitCode() int }
			if !errors.As(err, &ec) || ec.ExitCode() != tc.code {
				t.Fatalf("error: %v", err)
			}
			var result output.Result
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Exit != tc.code || result.OK {
				t.Fatalf("output: %s", out.String())
			}
			if tc.name != "stub" && result.Error == "" {
				t.Fatal("missing error")
			}
		})
	}
}
func TestRunDeadlineAndOrder(t *testing.T) {
	var ids []string
	checker := testChecker(func(ctx context.Context, c Claim) Outcome {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 20*time.Second {
			t.Fatal("missing claim deadline")
		}
		ids = append(ids, c.ID)
		return Outcome{Status: StatusVerified}
	})
	r := Run(context.Background(), RunInput{Doc: Document{Claims: []Claim{{Type: "file_exists", ID: "one"}, {Type: "file_exists", ID: "two"}}}, Registry: Registry{"file_exists": checker}})
	if r.ExitCode() != 0 || strings.Join(ids, ",") != "one,two" {
		t.Fatal(r, ids)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = Run(ctx, RunInput{Doc: Document{Claims: []Claim{{Type: "file_exists"}}}, Registry: Registry{"file_exists": checker}})
	if r.ExitCode() != 2 || len(ids) != 2 {
		t.Fatal(r, ids)
	}
}
func TestVerifyTableRequiredUnmet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claims.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"claims":[{"type":"file_exists","id":"c1","path":"README.md"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readback.assertions.yaml"), []byte("version: 1\nrequire:\n  - type: file_exists\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writer := output.New(&out, &out, false)
	writer.JSON = false
	cmd := Command(func() *output.Writer { return writer }, nil)
	cmd.SetArgs([]string{path, "--cwd", dir})
	err := cmd.Execute()
	var ec interface{ ExitCode() int }
	if !errors.As(err, &ec) || ec.ExitCode() != 1 {
		t.Fatal(err)
	}
	for _, want := range []string{"indeterminate  file_exists  c1  provider_unreachable", "summary: verified=0 contradicted=0 indeterminate=1 required_unmet=1", `required_assertion_unmet  {"type":"file_exists"}`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
}

func TestCheckerIgnoringCtxIsBoundedByDeadline(t *testing.T) {
	finished := make(chan struct{})
	checker := testChecker(func(_ context.Context, c Claim) Outcome {
		if c.ID == "slow" {
			time.Sleep(3 * time.Second)
			defer close(finished)
		}
		return Outcome{Status: StatusVerified, Evidence: []Evidence{{Source: "checker"}}}
	})
	start := time.Now()
	r := Run(context.Background(), RunInput{
		Doc:      Document{Claims: []Claim{{Type: "file_exists", ID: "slow"}, {Type: "file_exists", ID: "next"}}},
		Registry: Registry{"file_exists": checker}, ClaimTimeout: 200 * time.Millisecond,
	})
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("Run took %s", elapsed)
	}
	first := r.Claims[0]
	if first.Status != StatusIndeterminate || first.Reason != ReasonProviderUnreachable || r.Claims[1].Status != StatusVerified {
		t.Fatal(r)
	}
	if len(first.Evidence) != 1 || first.Evidence[0].Source != "runner" || first.Evidence[0].Call != "deadline" || first.Evidence[0].Observed["timeout_ms"] != int64(200) {
		t.Fatal(first.Evidence)
	}
	before, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	<-finished
	after, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("late checker mutated result")
	}
}

func TestCheckerPanicIsIsolated(t *testing.T) {
	r := Run(context.Background(), RunInput{
		Doc: Document{Claims: []Claim{{Type: "file_exists", ID: "panic"}, {Type: "file_exists", ID: "next"}}},
		Registry: Registry{"file_exists": testChecker(func(_ context.Context, c Claim) Outcome {
			if c.ID == "panic" {
				panic("provider crashed")
			}
			return Outcome{Status: StatusVerified}
		})},
	})
	first := r.Claims[0]
	if first.Status != StatusIndeterminate || first.Reason != ReasonProviderUnreachable || r.Claims[1].Status != StatusVerified {
		t.Fatal(r)
	}
	if len(first.Evidence) != 1 || first.Evidence[0].Source != "runner" || first.Evidence[0].Call != "panic" || first.Evidence[0].Observed["message"] != "provider crashed" {
		t.Fatal(first.Evidence)
	}
}

func TestUnknownReasonIsRejected(t *testing.T) {
	for _, status := range []string{StatusContradicted, StatusIndeterminate} {
		for _, reason := range []string{"invented_reason", ""} {
			t.Run(status+"/"+reason, func(t *testing.T) {
				r := Run(context.Background(), RunInput{Doc: Document{Claims: []Claim{{Type: "file_exists"}}},
					Registry: Registry{"file_exists": testChecker(func(context.Context, Claim) Outcome {
						return Outcome{Status: status, Reason: reason, Evidence: []Evidence{{Source: "checker"}}}
					})}})
				c := r.Claims[0]
				if KnownReason(reason) || !KnownReason(c.Reason) || c.Reason != ReasonProviderUnreachable || c.Status != status {
					t.Fatal(c)
				}
				if len(c.Evidence) != 2 || c.Evidence[0].Source != "checker" || c.Evidence[1].Source != "runner" || c.Evidence[1].Call != "reason_rejected" || c.Evidence[1].Observed["reason"] != reason {
					t.Fatal(c.Evidence)
				}
			})
		}
	}
}

func TestCoercedStatusDropsEvidence(t *testing.T) {
	r := Run(context.Background(), RunInput{Doc: Document{Claims: []Claim{{Type: "file_exists"}}},
		Registry: Registry{"file_exists": testChecker(func(context.Context, Claim) Outcome {
			return Outcome{Status: "invented_status", Reason: "invented_reason", Evidence: []Evidence{{Source: "untrusted"}}}
		})}})
	c := r.Claims[0]
	if c.Status != StatusIndeterminate || c.Reason != ReasonProviderUnreachable || len(c.Evidence) != 0 {
		t.Fatal(c)
	}
}

func TestHostNotAllowedBeatsUnsupportedType(t *testing.T) {
	for _, claim := range []Claim{
		{Type: "future", URL: "https://foreign.test"},
		{Type: "future", Health: "https://foreign.test"},
		{Type: "future", Smoke: []Claim{{URL: "https://foreign.test"}}},
	} {
		r := Run(context.Background(), RunInput{Doc: Document{Claims: []Claim{claim}},
			Assertions: &Assertions{AllowHosts: []string{"allowed.test"}},
			Registry: Registry{"future": testChecker(func(context.Context, Claim) Outcome {
				return Outcome{Status: StatusVerified}
			})}})
		if r.ExitCode() != 1 || r.Claims[0].Status != StatusContradicted || r.Claims[0].Reason != ReasonHostNotAllowed {
			t.Fatal(r)
		}
	}
}

func TestAssertionsDiscoveryErrorIsExit2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claims.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"claims":[{"type":"file_exists","path":"README.md"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := Command(func() *output.Writer { return output.New(&out, &out, true) }, nil)
	cmd.SetArgs([]string{path, "--cwd", path})
	err := cmd.Execute()
	var ec interface{ ExitCode() int }
	if !errors.As(err, &ec) || ec.ExitCode() != 2 {
		t.Fatalf("error: %v", err)
	}
	var result output.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Exit != 2 || result.Error == "" || result.OK {
		t.Fatalf("output: %s", out.String())
	}
}

func TestCommandNilFactoryUsesStubs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claims.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"claims":[{"type":"pr_merged","repo":"joshduffy/readback","pr":1,"into":"main"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := Command(func() *output.Writer { return output.New(&out, &out, true) }, nil)
	cmd.SetArgs([]string{path, "--cwd", dir})
	var ec interface{ ExitCode() int }
	if err := cmd.Execute(); !errors.As(err, &ec) || ec.ExitCode() != output.ExitCouldNotCheck {
		t.Fatalf("error = %v", err)
	}
	var result struct{ Data RunResult }
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data.Claims) != 1 {
		t.Fatalf("result = %+v", result)
	}
	claim := result.Data.Claims[0]
	if claim.Status != StatusIndeterminate || claim.Reason != ReasonProviderUnreachable || len(claim.Evidence) != 1 || claim.Evidence[0].Source != "stub" {
		t.Fatalf("claim = %+v", claim)
	}
}
