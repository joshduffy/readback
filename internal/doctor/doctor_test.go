package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/joshduffy/readback/internal/output"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type envelope struct {
	OK    bool   `json:"ok"`
	Exit  int    `json:"exit"`
	Data  report `json:"data"`
	Error string `json:"error"`
}

func runDoctor(t *testing.T, probes Probes) (envelope, string, int) {
	t.Helper()
	var buf bytes.Buffer
	w := output.New(&buf, io.Discard, true)
	code := execute(w, probes)
	var env envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("doctor output is not valid JSON: %v\n%s", err, buf.String())
	}
	return env, buf.String(), code
}

// baseProbes succeeds everywhere; tests override the probes they exercise.
func baseProbes(t *testing.T) Probes {
	t.Helper()
	return Probes{
		LookPath: func(name string) (string, error) { return "/fake/bin/" + name, nil },
		RunVersion: func(ctx context.Context, path string) (string, error) {
			return "fake 1.0.0", nil
		},
		GhAuth: func(ctx context.Context) (string, string, error) {
			return "", "Logged in to github.com", nil
		},
		CFClient: func() *http.Client { return &http.Client{} },
		Home:     func() (string, error) { return t.TempDir(), nil },
		Cwd:      func() (string, error) { return t.TempDir(), nil },
		Env:      func(key string) string { return "" },
	}
}

func TestDoctorReportsMissingGh(t *testing.T) {
	probes := baseProbes(t)
	probes.LookPath = func(name string) (string, error) {
		return "", errors.New("executable file not found in $PATH")
	}
	env, _, code := runDoctor(t, probes)
	if code != output.ExitUnproven {
		t.Fatalf("exit = %d, want %d", code, output.ExitUnproven)
	}
	if env.Data.Gh.Found {
		t.Fatal("gh.found = true, want false when LookPath fails")
	}
	if env.Data.Gh.AuthOK {
		t.Fatal("gh.auth_ok = true, want false when gh is missing")
	}
	if env.OK {
		t.Fatal("ok = true, want false when gh is missing")
	}
}

func TestDoctorNeverPrintsToken(t *testing.T) {
	const token = "cfat_SECRETVALUE123"
	var authHeader string
	probes := baseProbes(t)
	probes.Env = func(key string) string {
		if key == "CLOUDFLARE_API_TOKEN" {
			return token
		}
		return ""
	}
	probes.CFClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			authHeader = r.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"success":true,"result":[]}`)),
				Header:     make(http.Header),
			}, nil
		})}
	}
	env, raw, _ := runDoctor(t, probes)
	if authHeader != "Bearer "+token {
		t.Fatalf("Authorization header = %q, want the token sent to the API", authHeader)
	}
	if !env.Data.Cloudflare.AccountReachable {
		t.Fatal("cloudflare.account_reachable = false, want true on success:true")
	}
	if env.Data.Cloudflare.TokenSource != "env" {
		t.Fatalf("cloudflare.token_source = %q, want env", env.Data.Cloudflare.TokenSource)
	}
	for _, leak := range []string{"SECRET", "cfat_", token} {
		if strings.Contains(raw, leak) {
			t.Fatalf("output contains token material %q:\n%s", leak, raw)
		}
	}
	if strings.Contains(raw, "token_length") || strings.Contains(raw, "length") {
		t.Fatalf("output exposes token length:\n%s", raw)
	}
}

func TestDoctorAuthOkExit0(t *testing.T) {
	env, _, code := runDoctor(t, baseProbes(t))
	if code != output.ExitVerified {
		t.Fatalf("exit = %d, want %d", code, output.ExitVerified)
	}
	if !env.Data.Gh.Found || !env.Data.Gh.AuthOK {
		t.Fatalf("gh = %+v, want found and auth_ok", env.Data.Gh)
	}
	if !env.OK {
		t.Fatal("ok = false, want true when gh auth works")
	}
	if env.Data.Cloudflare.TokenSource != "none" {
		t.Fatalf("token_source = %q, want none with no env token and no file", env.Data.Cloudflare.TokenSource)
	}
}

func TestDoctorRedactsGhStderr(t *testing.T) {
	probes := baseProbes(t)
	probes.GhAuth = func(ctx context.Context) (string, string, error) {
		return "", "error validating token ghp_ABCDEF123456 for github.com; also saw github_pat_XYZ789abc in config", errors.New("exit status 1")
	}
	env, raw, code := runDoctor(t, probes)
	if code != output.ExitUnproven {
		t.Fatalf("exit = %d, want %d", code, output.ExitUnproven)
	}
	if env.Data.Gh.AuthOK {
		t.Fatal("gh.auth_ok = true, want false when gh auth status fails")
	}
	for _, leak := range []string{"ABCDEF123456", "XYZ789abc"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("output contains unredacted gh token material %q:\n%s", leak, raw)
		}
	}
	if !strings.Contains(env.Data.Gh.AuthDetail, "[redacted]") {
		t.Fatalf("auth_detail = %q, want [redacted] markers", env.Data.Gh.AuthDetail)
	}
}

func TestDoctorAgentsProbeTimeoutIsNotError(t *testing.T) {
	probes := baseProbes(t)
	probes.RunVersion = func(ctx context.Context, path string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	env, _, code := runDoctor(t, probes)
	if code != output.ExitVerified {
		t.Fatalf("exit = %d, want %d; agent version timeouts must not fail doctor", code, output.ExitVerified)
	}
	for _, bin := range agentBins {
		a := env.Data.Agents[bin]
		if !a.Found {
			t.Fatalf("agents.%s.found = false, want true", bin)
		}
		if a.Version != "" {
			t.Fatalf("agents.%s.version = %q, want empty on timeout", bin, a.Version)
		}
	}
}

func TestDoctorRedactsMaskedGhToken(t *testing.T) {
	got := redactGhTokens("Token: gho_**** and ghs_abc and ghp_ and github_pat_")
	want := "Token: [redacted] and [redacted] and [redacted] and [redacted]"
	if got != want {
		t.Fatalf("redactGhTokens = %q, want %q", got, want)
	}
	for _, leak := range []string{"gho_", "ghs_", "ghp_", "github_pat_"} {
		if strings.Contains(got, leak) {
			t.Fatalf("redacted output still contains %q: %q", leak, got)
		}
	}
}

func TestDoctorAuthDetailIsStderrOnly(t *testing.T) {
	probes := baseProbes(t)
	probes.GhAuth = func(ctx context.Context) (string, string, error) {
		return "PUBLIC", "err", errors.New("exit status 1")
	}
	env, raw, _ := runDoctor(t, probes)
	if !strings.Contains(env.Data.Gh.AuthDetail, "err") {
		t.Fatalf("auth_detail = %q, want it to contain stderr %q", env.Data.Gh.AuthDetail, "err")
	}
	if strings.Contains(env.Data.Gh.AuthDetail, "PUBLIC") {
		t.Fatalf("auth_detail = %q, must not contain stdout", env.Data.Gh.AuthDetail)
	}
	if strings.Contains(raw, "PUBLIC") {
		t.Fatalf("output contains stdout material:\n%s", raw)
	}
}

func TestDoctorSlowVersionDoesNotFailAuth(t *testing.T) {
	probes := baseProbes(t)
	// Only gh resolves, so the slow version probe runs once (3s deadline).
	probes.LookPath = func(name string) (string, error) {
		if name == "gh" {
			return "/fake/bin/gh", nil
		}
		return "", errors.New("executable file not found in $PATH")
	}
	probes.RunVersion = func(ctx context.Context, path string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	env, _, code := runDoctor(t, probes)
	if code != output.ExitVerified {
		t.Fatalf("exit = %d, want %d; a slow gh --version must not starve the auth check", code, output.ExitVerified)
	}
	if !env.Data.Gh.AuthOK {
		t.Fatal("gh.auth_ok = false, want true when only the version probe is slow")
	}
	if env.Data.Gh.Version != "" {
		t.Fatalf("gh.version = %q, want empty on version timeout", env.Data.Gh.Version)
	}
}

func TestDoctorCloudflareRequestHasDeadline(t *testing.T) {
	probes := baseProbes(t)
	probes.Env = func(key string) string {
		if key == "CLOUDFLARE_API_TOKEN" {
			return "cfat_SECRETVALUE123"
		}
		return ""
	}
	probes.CFClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if _, ok := r.Context().Deadline(); !ok {
				t.Error("cloudflare request context has no deadline; want a 5s deadline on the request itself")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"success":true,"result":[]}`)),
				Header:     make(http.Header),
			}, nil
		})}
	}
	env, _, _ := runDoctor(t, probes)
	if !env.Data.Cloudflare.AccountReachable {
		t.Fatal("cloudflare.account_reachable = false, want true on success:true")
	}
}
