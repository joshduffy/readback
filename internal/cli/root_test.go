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

func TestSchemaUnknownIsUsageError(t *testing.T) {
	code, _, errb := run(t, "schema", "nope")
	if code != output.ExitUsage || !strings.Contains(errb, "unknown module") {
		t.Fatalf("exit %d, stderr %q", code, errb)
	}
}

func TestDefaultRegistryWiresGithub(t *testing.T) {
	registry := defaultRegistry(t.TempDir())
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
			registryFactory = func(cwd string) verify.Registry {
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
	reg := defaultRegistry(t.TempDir())
	if _, ok := reg["url_serving"].(*httpprovider.Checker); !ok {
		t.Fatalf("url_serving = %T", reg["url_serving"])
	}
	if _, ok := reg["file_exists"].(*local.Checker); !ok {
		t.Fatalf("file_exists = %T", reg["file_exists"])
	}
}
