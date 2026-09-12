package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/joshduffy/readback/internal/providers/cloudflare"
	httpprovider "github.com/joshduffy/readback/internal/providers/http"
	"github.com/joshduffy/readback/internal/providers/local"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/joshduffy/readback/internal/output"
	"github.com/joshduffy/readback/internal/providers/github"
	"github.com/joshduffy/readback/internal/verify"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestCapabilitiesJSON(t *testing.T) {
	code, out, _ := run(t, "capabilities", "--json")
	if code != output.ExitVerified {
		t.Fatalf("exit %d", code)
	}
	var r output.Result
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	data := r.Data.(map[string]interface{})
	mods := data["modules"].([]interface{})
	if len(mods) != 7 {
		t.Fatalf("expected 7 registered modules, got %d", len(mods))
	}
}

func TestStubExitsCouldNotCheck(t *testing.T) {
	for _, m := range []string{"policy", "hook", "fleet", "memory"} {
		code, out, _ := run(t, m, "--json")
		if code != output.ExitCouldNotCheck {
			t.Errorf("%s: stub must exit %d, got %d", m, output.ExitCouldNotCheck, code)
		}
		if !strings.Contains(out, "not implemented") {
			t.Errorf("%s: stub must say not implemented:\n%s", m, out)
		}
	}
}

func TestSchemaVerifyPrintsClaimsSchema(t *testing.T) {
	code, out, errb := run(t, "schema", "verify")
	if code != 0 || !strings.Contains(out, `"readback-claims"`) && !strings.Contains(out, `"claims"`) || !strings.Contains(out, "pr_merged") {
		t.Fatalf("exit %d stdout %s stderr %s", code, out, errb)
	}
	var printed struct {
		Data map[string]any `json:"data"`
	}
	var embedded map[string]any
	if json.Unmarshal([]byte(out), &printed) != nil || json.Unmarshal(verify.ClaimsSchemaJSON(), &embedded) != nil || !reflect.DeepEqual(printed.Data, embedded) {
		t.Fatalf("schema verify must print the embedded claims schema exactly")
	}
}

func TestNoCacheBustFlagReachesTheRegistry(t *testing.T) {
	original := registryFactory
	t.Cleanup(func() { registryFactory = original })
	claims := filepath.Join("..", "..", "testdata", "verify", "claims.json")
	sha := strings.Repeat("a", 40)
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"verify", claims}, false},
		{[]string{"verify", "--no-cache-bust", claims}, true},
		{[]string{"verify-deploy", sha, "--url", "https://example.test"}, false},
		{[]string{"verify-deploy", sha, "--url", "https://example.test", "--no-cache-bust"}, true},
	} {
		var got *verify.RegistryOptions
		registryFactory = func(_ string, opts verify.RegistryOptions) verify.Registry {
			got = &opts
			return verify.StubRegistry()
		}
		code, out, errb := run(t, tc.args...)
		if got == nil || got.NoCacheBust != tc.want {
			t.Fatalf("%v: factory options %+v (exit %d)\n%s%s", tc.args, got, code, out, errb)
		}
	}
}

func TestJSONWithoutVersionIsSchemaError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claims.json")
	if err := os.WriteFile(path, []byte(`{"claims":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	code, out, errb := run(t, "verify", path)
	if code != output.ExitUsage {
		t.Fatalf("exit %d, want 64\n%s%s", code, out, errb)
	}
}

func TestSchemaUnknownIsUsageError(t *testing.T) {
	code, _, errb := run(t, "schema", "nope")
	if code != output.ExitUsage || !strings.Contains(errb, "unknown module") {
		t.Fatalf("exit %d, stderr %q", code, errb)
	}
}

func TestDefaultRegistryWiresGithub(t *testing.T) {
	registry := defaultRegistry(t.TempDir(), verify.RegistryOptions{})
	if len(registry) != 6 {
		t.Fatalf("registry size = %d", len(registry))
	}
	for _, kind := range []string{"pr_merged", "checks_passed", "commit_on_branch"} {
		if _, ok := registry[kind].(*github.Checker); !ok {
			t.Fatalf("%s checker = %T", kind, registry[kind])
		}
	}
	if _, ok := registry["deployment_serving"].(*cloudflare.Checker); !ok {
		t.Fatalf("deployment_serving checker = %T", registry["deployment_serving"])
	}
	for _, checker := range registry {
		if _, stub := checker.(interface{ IsStub() bool }); stub {
			t.Fatalf("a stub remains in the default registry: %T", checker)
		}
	}
}

func TestVerifyUsesFactory(t *testing.T) {
	original := registryFactory
	t.Cleanup(func() { registryFactory = original })
	for _, explicitCWD := range []bool{false, true} {
		t.Run(fmt.Sprint(explicitCWD), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "claims.json")
			if err := os.WriteFile(path, []byte(`{"version":1,"claims":[{"type":"pr_merged","repo":"joshduffy/readback","pr":1,"into":"main"}]}`), 0600); err != nil {
				t.Fatal(err)
			}
			wantCWD, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"verify", path, "--json"}
			if explicitCWD {
				wantCWD = dir
				args = append(args, "--cwd", dir)
			}
			calls := 0
			registryFactory = func(cwd string, _ verify.RegistryOptions) verify.Registry {
				calls++
				if cwd != wantCWD {
					t.Errorf("cwd = %q, want %q", cwd, wantCWD)
				}
				return verify.Registry{"pr_merged": github.New(func(context.Context, ...string) ([]byte, int, error) {
					return []byte(`{"merged":true,"base":{"ref":"main"}}`), 0, nil
				})}
			}
			code, out, stderr := run(t, args...)
			if code != output.ExitVerified || calls != 1 {
				t.Fatalf("exit = %d, factory calls = %d, stdout = %s, stderr = %s", code, calls, out, stderr)
			}
			var result struct{ Data verify.RunResult }
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Data.Claims) != 1 {
				t.Fatalf("result = %+v", result)
			}
			claim := result.Data.Claims[0]
			if claim.Status != verify.StatusVerified || len(claim.Evidence) != 1 || claim.Evidence[0].Source != "github" {
				t.Fatalf("claim = %+v", claim)
			}
		})
	}
}

func TestDefaultRegistryWiresHTTPAndLocal(t *testing.T) {
	reg := defaultRegistry(t.TempDir(), verify.RegistryOptions{})
	if _, ok := reg["url_serving"].(*httpprovider.Checker); !ok {
		t.Fatalf("url_serving = %T", reg["url_serving"])
	}
	if _, ok := reg["file_exists"].(*local.Checker); !ok {
		t.Fatalf("file_exists = %T", reg["file_exists"])
	}
}

func TestInstallSkillsWritesPerAgent(t *testing.T) {
	dir := t.TempDir()
	code, out, stderr := run(t, "install-skills", "--dir", dir, "--json")
	path := filepath.Join(dir, "SKILL.md")
	got, err := os.ReadFile(path)
	if code != 0 || err != nil || !bytes.Equal(got, embeddedSkill) || !strings.Contains(out, path) {
		t.Fatalf("exit=%d read=%v stdout=%s stderr=%s", code, err, out, stderr)
	}
	if code, _, _ := run(t, "install-skills", "--agent", "codex", "--dir", dir); code != 64 {
		t.Fatalf("--dir with --agent should be usage error 64, got %d", code)
	}
	t.Setenv("HOME", t.TempDir())
	code, out, stderr = run(t, "install-skills")
	if code != 0 {
		t.Fatalf("exit=%d %s %s", code, out, stderr)
	}
	for _, agent := range []string{"claude", "codex", "cursor"} {
		got, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "."+agent, "skills", "readback-cli-usage", "SKILL.md"))
		if err != nil || !bytes.Equal(got, embeddedSkill) {
			t.Fatalf("%s: %v", agent, err)
		}
	}
}

func TestInstallSkillsRefusesForeignFileWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	foreign := []byte("# Foreign skill\nkeep me\n")
	if err := os.WriteFile(path, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, "install-skills", "--dir", dir)
	got, err := os.ReadFile(path)
	if code != 2 || err != nil || !bytes.Equal(got, foreign) || !strings.Contains(out, "--force") {
		t.Fatalf("exit=%d read=%v output=%s", code, err, out)
	}
	code, out, _ = run(t, "install-skills", "--dir", dir, "--force")
	got, err = os.ReadFile(path)
	if code != 0 || err != nil || !bytes.Equal(got, embeddedSkill) {
		t.Fatalf("exit=%d read=%v output=%s", code, err, out)
	}
	code, out, _ = run(t, "install-skills", "--dir", dir)
	if code != 0 {
		t.Fatalf("own skill overwrite: %d %s", code, out)
	}
}

func TestInstallSkillsProtectsLocalEditsAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	if code, out, _ := run(t, "install-skills", "--dir", dir); code != 0 {
		t.Fatalf("first install: %d %s", code, out)
	}
	before, _ := os.Stat(path)
	// Identical content: no rewrite, so the mtime is untouched.
	if code, out, _ := run(t, "install-skills", "--dir", dir); code != 0 {
		t.Fatalf("second install: %d %s", code, out)
	}
	after, _ := os.Stat(path)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("identical skill was rewritten")
	}
	// CRLF copy of our own skill counts as identical.
	crlf := bytes.ReplaceAll(embeddedSkill, []byte("\n"), []byte("\r\n"))
	if err := os.WriteFile(path, crlf, 0644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := run(t, "install-skills", "--dir", dir); code != 0 {
		t.Fatalf("crlf copy refused: %d %s", code, out)
	}
	// A local edit below our first line is refused without --force.
	edited := append(append([]byte{}, embeddedSkill...), []byte("\nlocal note\n")...)
	if err := os.WriteFile(path, edited, 0644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, "install-skills", "--dir", dir)
	got, _ := os.ReadFile(path)
	if code != 2 || !bytes.Equal(got, edited) || !strings.Contains(out, "edited locally") {
		t.Fatalf("local edit not protected: exit=%d out=%s", code, out)
	}
	// An empty file has nothing to protect.
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = run(t, "install-skills", "--dir", dir)
	got, _ = os.ReadFile(path)
	if code != 0 || !bytes.Equal(got, embeddedSkill) {
		t.Fatalf("empty file refused: exit=%d out=%s", code, out)
	}
}

func TestSkillEmbeddedMatchesRepoFile(t *testing.T) {
	got, err := os.ReadFile(filepath.Join("..", "..", "skills", "readback-cli-usage", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, embeddedSkill) {
		t.Fatal("embedded skill differs from shipped skill")
	}
}
