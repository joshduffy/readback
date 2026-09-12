package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRequiredAssertionUnmetIsExit1(t *testing.T) {
	a := Assertions{Require: []Requirement{{"type": "pr_merged"}, {"type": "deployment_serving"}}}
	results := []ClaimResult{{Claim: Claim{Type: "pr_merged"}, Status: "indeterminate"}}
	if got := a.Unmet(results); len(got) == 0 || !reflect.DeepEqual(got, a.Require) {
		t.Fatalf("required_assertion_unmet must map to exit 1, got %v", got)
	}
}

func TestAssertionMatchesOnAllFields(t *testing.T) {
	a := Assertions{Require: []Requirement{{"type": "pr_merged", "into": "main"}}}
	for _, tc := range []struct {
		claim  Claim
		status string
		met    bool
	}{
		{Claim{Type: "pr_merged", Into: "main"}, "verified", true},
		{Claim{Type: "pr_merged", Into: "develop"}, "verified", false},
		{Claim{Type: "file_exists", Into: "main"}, "verified", false},
		{Claim{Type: "pr_merged", Into: "main"}, "contradicted", false},
		{Claim{Type: "pr_merged", Into: "main"}, "indeterminate", false},
	} {
		if got := len(a.Unmet([]ClaimResult{{Claim: tc.claim, Status: tc.status}})) == 0; got != tc.met {
			t.Errorf("claim %v status %s: met = %t, want %t", tc.claim, tc.status, got, tc.met)
		}
	}
	if got := a.Unmet([]ClaimResult{
		{Claim: Claim{Type: "pr_merged", Into: "develop"}, Status: "verified"},
		{Claim: Claim{Type: "pr_merged", Into: "main"}, Status: "verified"},
	}); len(got) != 0 {
		t.Fatalf("later matching result did not satisfy requirement: %v", got)
	}
}

func TestAssertionScalarFields(t *testing.T) {
	c := Claim{Type: "url_serving", ID: "one", PR: 42, ExpectStatus: 200, Marker: "ready"}
	for _, requirement := range []Requirement{
		{"pr": "42", "expect_status": "200", "id": "one", "marker": "ready"},
		{"pr": "0042"}, {"unknown": ""}, {"require": "[]"}, {"Raw": ""},
	} {
		a := Assertions{Require: []Requirement{requirement}}
		want := requirement["id"] == "one"
		if met := len(a.Unmet([]ClaimResult{{Claim: c, Status: "verified"}})) == 0; met != want {
			t.Errorf("requirement %v: met = %t, want %t", requirement, met, want)
		}
	}
}

func TestAllowHostsRejectsForeignHost(t *testing.T) {
	a := Assertions{AllowHosts: []string{"example.com"}}
	for _, raw := range []string{"https://foreign.com", "https://example.com.foreign.com", "https://example.com@foreign.com", "https://sub.example.com", "/example.com", "://"} {
		if a.HostAllowed(raw) {
			t.Errorf("allowed foreign or invalid URL %q", raw)
		}
	}
	if !a.HostAllowed("https://EXAMPLE.COM:443/path") {
		t.Fatal("rejected exact host with case difference and port")
	}
}

func TestAllowHostsWildcard(t *testing.T) {
	a := Assertions{AllowHosts: []string{"*.EXAMPLE.com"}}
	for raw, want := range map[string]bool{
		"https://a.example.com": true, "https://a.b.Example.COM:8443": true,
		"https://example.com": false, "https://evil-example.com": false,
		"https://a.example.com.evil.com": false, "https://.example.com": false,
		"https://a..example.com": false,
	} {
		if got := a.HostAllowed(raw); got != want {
			t.Errorf("HostAllowed(%q) = %t, want %t", raw, got, want)
		}
	}
}

func TestAllowHostsEmptyAllowsAll(t *testing.T) {
	for _, raw := range []string{"https://example.com", "https://foreign.com", "not a URL"} {
		if !(Assertions{}).HostAllowed(raw) {
			t.Errorf("empty allowlist rejected %q", raw)
		}
	}
}

func TestParseAssertionsRejectsUnknownKey(t *testing.T) {
	if _, err := ParseAssertions([]byte("version: 1\nunknown: value\n")); err == nil || !strings.Contains(err.Error(), "unknown top-level key") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}

func TestParseAssertionsRejectsVersion(t *testing.T) {
	for _, input := range []string{"", "require:\n", "version: 2", "version: 0", "version: nope", "version: 1.0"} {
		if _, err := ParseAssertions([]byte(input)); err == nil || !strings.Contains(err.Error(), "version") {
			t.Errorf("input %q: expected version error, got %v", input, err)
		}
	}
}

func TestFindAssertionsNoParentWalk(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "readback.assertions.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindAssertions(child); ok || got != "" {
		t.Fatalf("walked to parent: %q, %t", got, ok)
	}
	if got, ok := FindAssertions(parent); !ok || got != path {
		t.Fatalf("did not find cwd file: %q, %t", got, ok)
	}
	if a, err := LoadAssertions(path); err != nil || a.Version != 1 {
		t.Fatalf("LoadAssertions: %v, %v", a, err)
	}
	if _, err := LoadAssertions(filepath.Join(child, "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestParseAssertionsSubset(t *testing.T) {
	input := "# operator-owned\nversion: 1 # schema\nrequire:\n  - type: pr_merged\n    into: 'main'\n    pr: 42\n    marker: \"ready # yes\" # comment\n    contains: 'it''s ready'\n  - type: deployment_serving\n    provider: cloudflare-workers\nallow_hosts: # optional\n  - example.com\n  - \"*.example.com\"\n"
	want := Assertions{Version: 1, Require: []Requirement{
		{"type": "pr_merged", "into": "main", "pr": "42", "marker": "ready # yes", "contains": "it's ready"},
		{"type": "deployment_serving", "provider": "cloudflare-workers"},
	}, AllowHosts: []string{"example.com", "*.example.com"}}
	got, err := ParseAssertions([]byte(input))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseAssertions = %#v, %v; want %#v", got, err, want)
	}
}

func TestParseAssertionsRejectsUnsupportedSyntax(t *testing.T) {
	for _, input := range []string{
		"version: 1\nversion: 1", "version: 1\nrequire: []", "version: 1\nallow_hosts: {}",
		"version: 1\nrequire:\n - type: pr_merged", "version: 1\nrequire:\n\t- type: pr_merged",
		"version: 1\nrequire:\n    type: pr_merged", "version: 1\nrequire:\n  - type: pr_merged\n    type: file_exists",
		"version: 1\nrequire:\n  - type: [pr_merged]", "version: 1\nrequire:\n  - type: &anchor pr_merged",
		"version: 1\nrequire:\n  - type: *alias", "version: 1\nrequire:\n  - type: |",
		"version: 1\nrequire:\n  - type: \"unclosed", "version: 1\nrequire:\n  - type: 'ok' junk",
		"version: 1\nallow_hosts:\n  -", "version: 1\nallow_hosts:\n    - example.com",
		"version: 1\nrequire:\n  - type:\n      nested: value", "version: 1\n---",
	} {
		if _, err := ParseAssertions([]byte(input)); err == nil {
			t.Errorf("accepted unsupported syntax %q", input)
		}
	}
}

func TestParseAssertionsRejectsHostWithPortOrScheme(t *testing.T) {
	for _, host := range []string{"example.com:443", "https://example.com", "example.com/path"} {
		for _, entry := range []string{host, "\"" + host + "\""} {
			_, err := ParseAssertions([]byte("version: 1\nallow_hosts:\n  - " + entry + "\n"))
			if err == nil || !strings.Contains(err.Error(), host) || !strings.Contains(err.Error(), "hosts are bare hostnames") {
				t.Errorf("entry %q: expected bare-hostname error naming entry, got %v", entry, err)
			}
		}
	}
}

func TestParseAssertionsRejectsKeyWithoutSpace(t *testing.T) {
	for _, input := range []string{
		"version:1", "version:\"1\"", "version: 1\nrequire:# comment",
		"version: 1\nallow_hosts:# comment",
		"version: 1\nrequire:\n  - type:pr_merged",
		"version: 1\nrequire:\n  - type: pr_merged\n    into:main",
	} {
		if _, err := ParseAssertions([]byte(input)); err == nil {
			t.Errorf("accepted key without space in %q", input)
		}
	}
}

func TestParseAssertionsRejectsBareWildcard(t *testing.T) {
	for _, entry := range []string{"*", "*.", "\"*\"", "\"*.\"", "'*'", "'*.'"} {
		if _, err := ParseAssertions([]byte("version: 1\nallow_hosts:\n  - " + entry + "\n")); err == nil {
			t.Errorf("accepted bare wildcard %q", entry)
		}
	}
}

func TestAllowHostsTrailingDotNormalized(t *testing.T) {
	for _, tc := range []struct {
		allowed string
		url     string
		want    bool
	}{
		{"example.com", "https://EXAMPLE.COM.:443/path", true},
		{"example.com", "https://example.com..", false},
		{"example.com", "https://sub.example.com.", false},
		{"*.example.com", "https://A.B.EXAMPLE.COM.:8443/path", true},
		{"*.example.com", "https://a.example.com..", false},
		{"*.example.com", "https://example.com.", false},
		{"*.example.com", "https://a.example.com.evil.com.", false},
	} {
		a := Assertions{AllowHosts: []string{tc.allowed}}
		if got := a.HostAllowed(tc.url); got != tc.want {
			t.Errorf("allow %q, URL %q: got %t, want %t", tc.allowed, tc.url, got, tc.want)
		}
	}
}

func TestZeroKeyRequirementIsUnmet(t *testing.T) {
	a := Assertions{Require: []Requirement{{}, nil}}
	for _, results := range [][]ClaimResult{
		nil,
		{{Claim: Claim{Type: "pr_merged"}, Status: "verified"}},
		{{Claim: Claim{}, Status: "verified"}, {Claim: Claim{Type: "file_exists"}, Status: "verified"}},
	} {
		if got := a.Unmet(results); !reflect.DeepEqual(got, a.Require) {
			t.Errorf("results %v: unmet = %v, want %v", results, got, a.Require)
		}
	}
}

func TestParseAssertionsRejectsEmptyRequirement(t *testing.T) {
	for _, entry := range []string{"-", "- ", "- # empty", "- {}"} {
		for _, tail := range []string{"", "allow_hosts:\n  - example.com\n", "  - type: pr_merged\n"} {
			input := "version: 1\nrequire:\n  " + entry + "\n" + tail
			if _, err := ParseAssertions([]byte(input)); err == nil {
				t.Errorf("accepted empty requirement in %q", input)
			}
		}
	}
}
