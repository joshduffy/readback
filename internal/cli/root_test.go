package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/joshduffy/readback/internal/output"
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
	for _, m := range []string{"verify", "verify-deploy", "doctor", "policy", "hook", "fleet", "memory"} {
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
