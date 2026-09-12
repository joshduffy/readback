package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshduffy/readback/internal/output"
	"github.com/joshduffy/readback/internal/verify"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

type captureChecker struct{ claim verify.Claim }

func (c *captureChecker) Check(_ context.Context, claim verify.Claim) verify.Outcome {
	c.claim = claim
	return verify.Outcome{Status: verify.StatusVerified}
}

func run(t *testing.T, factory verify.RegistryFactory, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	cmd := Command(func() *output.Writer { return output.New(&out, &out, true) }, factory)
	cmd.SetArgs(args)
	err := cmd.Execute()
	var ec interface{ ExitCode() int }
	if !errors.As(err, &ec) {
		t.Fatalf("error: %v", err)
	}
	return ec.ExitCode(), out.String()
}

func TestVerifyDeployBuildsSingleClaim(t *testing.T) {
	checker := &captureChecker{}
	factoryCalls := 0
	factory := func(string) verify.Registry {
		factoryCalls++
		return verify.Registry{"deployment_serving": checker}
	}
	dir := t.TempDir()
	code, stdout := run(t, factory, testSHA,
		"--url", "https://example.com",
		"--marker", "sha-0123",
		"--health", "https://example.com/api/health",
		"--worker", "example-worker",
		"--account", "acct123",
		"--cwd", dir)
	if code != output.ExitVerified || factoryCalls != 1 {
		t.Fatalf("exit = %d, factory calls = %d, stdout = %s", code, factoryCalls, stdout)
	}
	claim := checker.claim
	if claim.Type != "deployment_serving" || claim.SHA != testSHA ||
		claim.URL != "https://example.com" || claim.Marker != "sha-0123" ||
		claim.Health != "https://example.com/api/health" ||
		claim.Worker != "example-worker" || claim.Account != "acct123" ||
		claim.Provider != "cloudflare-workers" {
		t.Fatalf("claim = %+v", claim)
	}
	var envelope struct {
		Command string           `json:"command"`
		Data    verify.RunResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Command != name {
		t.Fatalf("command = %q", envelope.Command)
	}
	if len(envelope.Data.Claims) != 1 || envelope.Data.Claims[0].Type != "deployment_serving" {
		t.Fatalf("claims = %+v", envelope.Data.Claims)
	}
	if envelope.Data.Claims[0].Status != verify.StatusVerified {
		t.Fatalf("status = %q", envelope.Data.Claims[0].Status)
	}
}

func TestVerifyDeployRejectsShortSha(t *testing.T) {
	factory := func(string) verify.Registry {
		t.Fatal("factory must not be called for invalid input")
		return nil
	}
	code, stdout := run(t, factory, "abc123", "--url", "https://example.com", "--cwd", t.TempDir())
	if code != output.ExitUsage {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if !strings.Contains(stdout, "sha must be the full 40-character commit hash") {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestVerifyDeployRejectsHttpUrl(t *testing.T) {
	factory := func(string) verify.Registry {
		t.Fatal("factory must not be called for invalid input")
		return nil
	}
	code, stdout := run(t, factory, testSHA, "--url", "http://example.com", "--cwd", t.TempDir())
	if code != output.ExitUsage {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if !strings.Contains(stdout, "https") {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestVerifyDeployNoObservableRungIsExit2(t *testing.T) {
	code, stdout := run(t, nil, testSHA, "--url", "https://example.com", "--cwd", t.TempDir())
	if code != output.ExitCouldNotCheck {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	var envelope struct{ Data verify.RunResult }
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Claims) != 1 {
		t.Fatalf("claims = %+v", envelope.Data.Claims)
	}
	claim := envelope.Data.Claims[0]
	if claim.Status != verify.StatusIndeterminate || claim.Reason != verify.ReasonProviderUnreachable {
		t.Fatalf("claim = %+v", claim)
	}
}

func TestVerifyDeployAssertionsDiscoveryErrorIsExit2(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	code, _ := run(t, nil, "0123456789abcdef0123456789abcdef01234567", "--url", "https://example.invalid/", "--cwd", file)
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestVerifyDeployNoArgsIsUsage(t *testing.T) {
	code, _ := run(t, nil)
	if code != 64 {
		t.Fatalf("exit %d, want 64", code)
	}
}
