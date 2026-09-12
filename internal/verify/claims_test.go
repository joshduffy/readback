package verify

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func claimProblems(t *testing.T, input string) []Problem {
	t.Helper()
	_, err := ParseDocument([]byte(input))
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	return validation.Problems
}

func TestClaimsSchemaRejectsShortSha(t *testing.T) {
	p := claimProblems(t, `{"version":1,"claims":[{"type":"checks_passed","repo":"owner/name","sha":"abc123"}]}`)
	if len(p) != 1 || p[0].Field != "sha" {
		t.Fatalf("problems: %+v", p)
	}
}

func TestClaimsSchemaRejectsCommandField(t *testing.T) {
	for _, field := range forbiddenFields {
		t.Run(field, func(t *testing.T) {
			p := claimProblems(t, `{"version":1,"claims":[{"type":"future","`+field+`":null}]}`)
			if len(p) != 1 || p[0].Field != field {
				t.Fatalf("problems: %+v", p)
			}
		})
	}
}

func TestClaimsSchemaRejectsDotDotPath(t *testing.T) {
	for _, path := range []string{"..", "../file", "dir/../file", "/dir/..", `dir\..\file`} {
		encoded, err := json.Marshal(path)
		if err != nil {
			t.Fatal(err)
		}
		p := claimProblems(t, `{"version":1,"claims":[{"type":"file_exists","path":`+string(encoded)+`}]}`)
		if len(p) != 1 || p[0].Field != "path" {
			t.Fatalf("problems: %+v", p)
		}
	}
}

func TestClaimsUnknownTypeIsIndeterminate(t *testing.T) {
	doc, err := ParseDocument([]byte(`{"version":1,"claims":[{"type":"future","id":"x","extra":{"value":true,"number":1e999}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Claims) != 1 || doc.Claims[0].Supported() || doc.Claims[0].ID != "x" {
		t.Fatalf("claim: %+v", doc.Claims)
	}
	if _, ok := doc.Claims[0].Raw["extra"]; !ok {
		t.Fatal("unknown field was lost")
	}
}

func TestClaimsParsesFixture(t *testing.T) {
	data, err := os.ReadFile("../../testdata/verify/claims.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Claims) != 4 {
		t.Fatalf("got %d claims", len(doc.Claims))
	}
	for _, c := range doc.Claims {
		if !c.Supported() {
			t.Fatalf("unsupported fixture claim: %s", c.Type)
		}
	}
}

func TestClaimsSchemaFileMatchesEmbedded(t *testing.T) {
	data, err := os.ReadFile("../../schemas/claims.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fileSchema, embeddedSchema any
	if err := json.Unmarshal(data, &fileSchema); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(ClaimsSchemaJSON(), &embeddedSchema); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fileSchema, embeddedSchema) || string(data) != string(ClaimsSchemaJSON()) {
		t.Fatal("embedded schema differs from file")
	}
}

func TestClaimsRejectsHttpUrl(t *testing.T) {
	for _, field := range []string{"url", "health"} {
		p := claimProblems(t, `{"version":1,"claims":[{"type":"future","`+field+`":"http://example.com"}]}`)
		if len(p) != 1 || p[0].Field != field {
			t.Fatalf("problems: %+v", p)
		}
	}
}

func TestClaimsRejectsBadRepo(t *testing.T) {
	for _, repo := range []string{"owner", "owner/", "/name", "a/b/c", "a /b", "a/b\t", "a/\u00a0b"} {
		raw, err := json.Marshal(repo)
		if err != nil {
			t.Fatal(err)
		}
		p := claimProblems(t, `{"version":1,"claims":[{"type":"pr_merged","pr":1,"repo":`+string(raw)+`}]}`)
		if len(p) != 1 || p[0].Field != "repo" {
			t.Fatalf("problems: %+v", p)
		}
	}
}

func TestClaimsCollectsAllProblems(t *testing.T) {
	input := `{"version":1,"claims":[{"type":"checks_passed","repo":"owner/name","sha":"short"},{"type":"file_exists","path":"../x"},{"type":"url_serving","url":"http://example.com"}]}`
	p := claimProblems(t, input)
	if len(p) != 3 {
		t.Fatalf("problems: %+v", p)
	}
	for i, field := range []string{"sha", "path", "url"} {
		if p[i].Index != i || p[i].Field != field {
			t.Fatalf("problem %d: %+v", i, p[i])
		}
	}
	_, err := ParseDocument([]byte(input))
	for _, field := range []string{"sha", "path", "url"} {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("error omits %s: %v", field, err)
		}
	}
}

func TestClaimsRequiredFieldsAndTypes(t *testing.T) {
	for _, typ := range []string{"pr_merged", "checks_passed", "commit_on_branch", "file_exists", "url_serving", "deployment_serving"} {
		t.Run(typ, func(t *testing.T) { claimProblems(t, `{"version":1,"claims":[{"type":"`+typ+`"}]}`) })
	}
	for _, input := range []string{
		`null`, `[]`, `{}`, `{"version":2,"claims":[]}`, `{"version":1,"claims":null}`,
		`{"version":1,"claims":[null,42,[]]}`,
		`{"version":1,"claims":[{"type":"future","sha":null,"repo":9,"require":[null],"header":{"x":null},"expect_status":1.5}]}`,
		`{"version":1,"claims":[{"type":"pr_merged","repo":"o/r","pr":0}]}`,
		`{"version":1,"claims":[{"type":"future","url":"https:relative"}]}`,
		`{"version":1,"claims":[{"type":"future","url":"https://"}]}`,
		`{"version":1,"claims":[{"type":"future","path":""}]}`,
		`{"version":1,"claims":[{"type":"future","branch":""}]}`,
	} {
		claimProblems(t, input)
	}
	p := claimProblems(t, `{"version":1,"claims":[{"type":"pr_merged","repo":9,"pr":1.5},{"type":"file_exists","path":null}]}`)
	if len(p) != 3 {
		t.Fatalf("type errors were not collected: %+v", p)
	}
}

func TestClaimsDeploymentSmokeAndDefaults(t *testing.T) {
	input := `{"version":1,"claims":[{"type":"deployment_serving","provider":"cloudflare-workers","sha":"ABCDEF0123456789ABCDEF0123456789ABCDEF01","url":"https://example.com","smoke":[{"type":"url_serving","url":"https://example.com/a"},{"type":"url_serving","url":"https://example.com/b","expect_status":201}]}]}`
	doc, err := ParseDocument([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	smoke := doc.Claims[0].Smoke
	if smoke[0].ExpectStatus != 200 || smoke[1].ExpectStatus != 201 {
		t.Fatalf("smoke defaults: %+v", smoke)
	}
	bad := strings.Replace(input, `"expect_status":201`, `"command":"oops"`, 1)
	p := claimProblems(t, bad)
	if len(p) != 1 || p[0].Field != "smoke[1].command" || p[0].Index != 0 {
		t.Fatalf("problems: %+v", p)
	}
	claimProblems(t, strings.Replace(input, `"cloudflare-workers"`, `"other"`, 1))
	claimProblems(t, strings.Replace(input, `"type":"url_serving"`, `"type":"future"`, 1))
}

func TestClaimsValidateConstructedDocument(t *testing.T) {
	doc := Document{Version: 1, Claims: []Claim{{Type: "commit_on_branch", Repo: "o/r", SHA: strings.Repeat("a", 40), Branch: "main"}, {Type: "file_exists", Path: "dir/file..txt"}}}
	if err := Validate(doc); err != nil {
		t.Fatal(err)
	}
	doc.Claims[0].Raw = map[string]any{"token": "forbidden"}
	doc.Claims[1].Path = "dir/../file"
	var validation *ValidationError
	if err := Validate(doc); !errors.As(err, &validation) || len(validation.Problems) != 2 {
		t.Fatalf("validation: %v", err)
	}
}

func TestClaimsRejectsNestedSmoke(t *testing.T) {
	for _, nested := range []string{`[]`, `null`, `{}`, `[{"type":"url_serving","url":"https://example.com"}]`} {
		t.Run(nested, func(t *testing.T) {
			p := claimProblems(t, `{"version":1,"claims":[{"type":"deployment_serving","provider":"cloudflare-workers","sha":"`+strings.Repeat("a", 40)+`","url":"https://example.com","smoke":[{"type":"url_serving","url":"http://example.com","smoke":`+nested+`}]},{"type":"file_exists","path":"../bad"}]}`)
			want := []Problem{
				{0, "smoke[0].smoke", "nested smoke not allowed"},
				{0, "smoke[0].url", "must be an absolute https URL with a host"},
				{1, "path", "must not contain a .. segment"},
			}
			if !reflect.DeepEqual(p, want) {
				t.Fatalf("problems: %+v, want %+v", p, want)
			}
		})
	}
	doc := Document{Version: 1, Claims: []Claim{{Type: "url_serving", URL: "https://example.com", Smoke: []Claim{{Type: "url_serving", URL: "https://example.com", Smoke: []Claim{}}}}}}
	var validation *ValidationError
	if err := Validate(doc); !errors.As(err, &validation) || !reflect.DeepEqual(validation.Problems, []Problem{{0, "smoke[0].smoke", "nested smoke not allowed"}}) {
		t.Fatalf("constructed document validation: %v", err)
	}
}

func TestClaimsRejectsDeepSmokeChain(t *testing.T) {
	claim := `{"type":"url_serving","url":"https://example.com"}`
	for i := 0; i < 50; i++ {
		claim = `{"type":"url_serving","url":"https://example.com","smoke":[` + claim + `]}`
	}
	p := claimProblems(t, `{"version":1,"claims":[`+claim+`]}`)
	if !reflect.DeepEqual(p, []Problem{{0, "smoke[0].smoke", "nested smoke not allowed"}}) {
		t.Fatalf("problems: %+v", p)
	}
}

func TestClaimsForbiddenFieldsCaseInsensitive(t *testing.T) {
	for _, field := range []string{"Command", "COMMAND", "Token", "EnV", "ARGS"} {
		for _, nested := range []bool{false, true} {
			location := "top"
			claim := `{"type":"url_serving","url":"https://example.com","` + field + `":null}`
			path := field
			if nested {
				location = "smoke"
				claim = `{"type":"url_serving","url":"https://example.com","smoke":[` + claim + `]}`
				path = "smoke[0]." + field
			}
			t.Run(location+"/"+field, func(t *testing.T) {
				p := claimProblems(t, `{"version":1,"claims":[`+claim+`]}`)
				if !reflect.DeepEqual(p, []Problem{{0, path, "forbidden authority field"}}) {
					t.Fatalf("problems: %+v", p)
				}
			})
		}
	}
}

func TestClaimsSchemaForbidsUppercaseCommand(t *testing.T) {
	var schema struct {
		Defs map[string]struct {
			PropertyNames struct {
				Not struct {
					Pattern string `json:"pattern"`
				} `json:"not"`
			} `json:"propertyNames"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(ClaimsSchemaJSON(), &schema); err != nil {
		t.Fatal(err)
	}
	pattern := schema.Defs["claim"].PropertyNames.Not.Pattern
	if !strings.HasPrefix(pattern, "(?i)") {
		t.Fatalf("missing case-insensitive propertyNames restriction: %q", pattern)
	}
	forbidden, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range append([]string{"Command", "COMMAND", "Token", "EnV", "ARGS"}, forbiddenFields...) {
		if !forbidden.MatchString(field) {
			t.Errorf("propertyNames allows %q", field)
		}
	}
	for _, field := range []string{"url", "smoke", "command_extra", "extra_command"} {
		if forbidden.MatchString(field) {
			t.Errorf("propertyNames forbids %q", field)
		}
	}
}
