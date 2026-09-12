package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshduffy/readback/internal/verify"
)

const fixtureSHA = "0123456789abcdef0123456789abcdef01234567"
const testToken = "test-credential-never-output"

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type checkFunc func(context.Context, verify.Claim) verify.Outcome

func (f checkFunc) Check(ctx context.Context, c verify.Claim) verify.Outcome { return f(ctx, c) }

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../../../testdata/providers/cloudflare", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func apiClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	endpoint, _ := url.Parse(server.URL)
	return &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		if r.URL.Host == "api.cloudflare.com" {
			if r.Header.Get("Authorization") != "Bearer "+testToken {
				t.Error("missing expected authorization")
			}
			r.URL.Scheme, r.URL.Host = endpoint.Scheme, endpoint.Host
		} else if r.Header.Get("Authorization") != "" {
			t.Error("credential sent to health URL")
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
}

func versionChecker(t *testing.T, version string) *Checker {
	deployments := fixture(t, "deployments.json")
	client := apiClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/deployments") {
			fmt.Fprint(w, deployments)
		} else {
			fmt.Fprint(w, version)
		}
	})
	return New(Options{Client: client, Token: testToken, Account: "test-account", HTTP: checkFunc(func(context.Context, verify.Claim) verify.Outcome {
		return verify.Outcome{Status: verify.StatusVerified}
	})})
}

func deployment() verify.Claim {
	return verify.Claim{Type: "deployment_serving", Provider: "cloudflare-workers", SHA: fixtureSHA, Worker: "test-worker", URL: "https://example.test"}
}
func wantOutcome(t *testing.T, got verify.Outcome, status, reason string) {
	t.Helper()
	if got.Status != status || got.Reason != reason {
		t.Fatalf("got %+v, want %s %s", got, status, reason)
	}
}

func TestDeploymentVersionShaMatch(t *testing.T) {
	c := versionChecker(t, fixture(t, "version-with-sha.json"))
	got := c.Check(context.Background(), deployment())
	wantOutcome(t, got, verify.StatusVerified, "")
	if len(got.Evidence) != 1 || got.Evidence[0].Source != "cloudflare" || got.Evidence[0].Observed["rung"] != "version_sha" {
		t.Fatalf("evidence: %+v", got.Evidence)
	}
}
func TestDeploymentVersionShaPrefixMatch(t *testing.T) {
	// Annotations are free text: a shorter hex run never verifies, and a full id embedded
	// in a message does. The 12-char prefix rule belongs to the health commit_sha only.
	for _, n := range []int{12, 20, 39} {
		c := versionChecker(t, strings.ReplaceAll(fixture(t, "version-with-sha.json"), fixtureSHA, "release "+fixtureSHA[:n]))
		claim := deployment()
		claim.Marker = "live"
		wantOutcome(t, c.Check(context.Background(), claim), verify.StatusIndeterminate, verify.ReasonVersionSHAUnavailable)
	}
	c := versionChecker(t, strings.ReplaceAll(fixture(t, "version-with-sha.json"), fixtureSHA, "deploy "+fixtureSHA+" via ci"))
	wantOutcome(t, c.Check(context.Background(), deployment()), verify.StatusVerified, "")
}

func TestDeploymentAnnotationHexRunNeverMatches(t *testing.T) {
	// A message like "build 123456789012" must not prefix-match a claim sha starting with those digits.
	claim := deployment()
	claim.SHA = "1234567890120000000000000000000000000000"
	claim.Marker = "live"
	c := versionChecker(t, strings.ReplaceAll(fixture(t, "version-with-sha.json"), fixtureSHA, "build 123456789012"))
	wantOutcome(t, c.Check(context.Background(), claim), verify.StatusIndeterminate, verify.ReasonVersionSHAUnavailable)
}
func TestDeploymentVersionShaMismatch(t *testing.T) {
	c := versionChecker(t, strings.ReplaceAll(fixture(t, "version-with-sha.json"), fixtureSHA, strings.Repeat("a", 40)))
	wantOutcome(t, c.Check(context.Background(), deployment()), verify.StatusContradicted, verify.ReasonVersionSHAMismatch)
}
func TestDeploymentVersionShaUnavailable(t *testing.T) {
	c := versionChecker(t, fixture(t, "version-without-sha.json"))
	claim := deployment()
	claim.Marker = "live"
	wantOutcome(t, c.Check(context.Background(), claim), verify.StatusIndeterminate, verify.ReasonVersionSHAUnavailable)
}
func TestDeploymentNotFoundIsContradicted(t *testing.T) {
	for _, code := range []int{200, 404} {
		body := fixture(t, "version-not-found.json")
		c := New(Options{Token: testToken, Account: "test-account", Client: apiClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code); fmt.Fprint(w, body) })})
		wantOutcome(t, c.Check(context.Background(), deployment()), verify.StatusContradicted, verify.ReasonDeploymentNotFound)
	}
}
func TestDeploymentAuthMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	wantOutcome(t, New(Options{}).Check(context.Background(), deployment()), verify.StatusIndeterminate, verify.ReasonAuthMissing)
	for _, code := range []int{401, 403} {
		c := New(Options{Token: testToken, Account: "test-account", Client: apiClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) })})
		wantOutcome(t, c.Check(context.Background(), deployment()), verify.StatusIndeterminate, verify.ReasonAuthMissing)
	}
}
func healthCheck(t *testing.T, body string) verify.Outcome {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("readback_bust") == "" || r.Header.Get("Cache-Control") != "no-cache" || !strings.HasPrefix(r.Header.Get("User-Agent"), "readback/") {
			t.Error("missing HTTP request rules")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("credential leaked")
		}
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	claim := deployment()
	claim.Worker = ""
	claim.Health = server.URL
	return New(Options{Token: testToken, Account: "test-account"}).Check(context.Background(), claim)
}
func TestDeploymentHealthShaPrefix(t *testing.T) {
	for _, n := range []int{12, 40} {
		wantOutcome(t, healthCheck(t, `{"commit_sha":"`+fixtureSHA[:n]+`"}`), verify.StatusVerified, "")
	}
}
func TestDeploymentHealthShaMismatch(t *testing.T) {
	for _, sha := range []string{fixtureSHA[:11], strings.Repeat("b", 40), ""} {
		wantOutcome(t, healthCheck(t, `{"commit_sha":"`+sha+`"}`), verify.StatusContradicted, verify.ReasonHealthSHAMismatch)
	}
	wantOutcome(t, healthCheck(t, "not json"), verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
}
func TestDeploymentNoObservableRungIsIndeterminate(t *testing.T) {
	c := versionChecker(t, fixture(t, "version-without-sha.json"))
	for _, worker := range []string{"", "test-worker"} {
		claim := deployment()
		claim.Worker = worker
		got := c.Check(context.Background(), claim)
		wantOutcome(t, got, verify.StatusIndeterminate, verify.ReasonMarkerMissing)
		if got.Evidence[0].Observed["message"] != "at least one observable rung is required" {
			t.Fatal(got)
		}
	}
}
func TestDeploymentMarkerRungDelegatesToHTTP(t *testing.T) {
	calls := 0
	c := New(Options{Token: testToken, Account: "test-account", HTTP: checkFunc(func(ctx context.Context, claim verify.Claim) verify.Outcome {
		calls++
		if claim.Type != "url_serving" || claim.URL != "https://example.test" || claim.Marker != "live" || claim.ExpectStatus != 200 {
			t.Fatalf("claim %+v", claim)
		}
		return verify.Outcome{Status: verify.StatusVerified}
	})})
	claim := deployment()
	claim.Worker = ""
	claim.Marker = "live"
	got := c.Check(context.Background(), claim)
	wantOutcome(t, got, verify.StatusVerified, "")
	if calls != 1 || got.Evidence[0].Source != "http" {
		t.Fatal(got)
	}
}
func TestDeploymentSmokeAllMustVerify(t *testing.T) {
	for _, status := range []string{verify.StatusVerified, verify.StatusIndeterminate, verify.StatusContradicted} {
		calls := 0
		reason := ""
		if status != verify.StatusVerified {
			reason = verify.ReasonMarkerMissing
		}
		c := New(Options{Token: testToken, HTTP: checkFunc(func(context.Context, verify.Claim) verify.Outcome {
			calls++
			if calls == 2 {
				return verify.Outcome{Status: status, Reason: reason}
			}
			return verify.Outcome{Status: verify.StatusVerified}
		})})
		claim := deployment()
		claim.Worker = ""
		claim.Smoke = []verify.Claim{{Type: "url_serving", URL: "https://example.test/a"}, {Type: "url_serving", URL: "https://example.test/b"}}
		got := c.Check(context.Background(), claim)
		wantOutcome(t, got, status, reason)
		if calls != 2 || len(got.Evidence) != 2 {
			t.Fatal(got)
		}
	}
}
func TestDeploymentTokenNeverInEvidence(t *testing.T) {
	c := versionChecker(t, `{"success":false,"errors":[{"code":123,"message":"`+testToken+`"}]}`)
	claim := deployment()
	claim.Marker = "live"
	c.http = checkFunc(func(context.Context, verify.Claim) verify.Outcome {
		return verify.Outcome{Status: verify.StatusVerified, Evidence: []verify.Evidence{{Call: testToken, Observed: map[string]any{"nested": []string{testToken}}}}}
	})
	got := c.Check(context.Background(), claim)
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), testToken) {
		t.Fatal("credential leaked in outcome")
	}
	c.client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("transport echoed %s", testToken) })}
	data, _ = json.Marshal(c.Check(context.Background(), deployment()))
	if strings.Contains(string(data), testToken) {
		t.Fatal("credential leaked in transport failure")
	}
}
func TestDefaultRegistryWiresCloudflare(t *testing.T) {
	registry := verify.StubRegistry()
	registry["deployment_serving"] = New(Options{Token: testToken, Account: "test-account"})
	claim := deployment()
	claim.Worker = ""
	wantOutcome(t, registry["deployment_serving"].Check(context.Background(), claim), verify.StatusIndeterminate, verify.ReasonMarkerMissing)
}
func TestDeploymentActiveVersionsAndPrecedence(t *testing.T) {
	matching := fixture(t, "version-with-sha.json")
	for _, active := range []bool{true, false} {
		client := apiClient(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/deployments"):
				percentage := 0
				if active {
					percentage = 50
				}
				fmt.Fprintf(w, `{"success":true,"result":{"deployments":[{"versions":[{"version_id":"wrong","percentage":50},{"version_id":"match","percentage":%d}]},{"versions":[{"version_id":"match","percentage":100}]}]}}`, percentage)
			case strings.HasSuffix(r.URL.Path, "/wrong"):
				fmt.Fprint(w, strings.ReplaceAll(matching, fixtureSHA, strings.Repeat("a", 40)))
			default:
				fmt.Fprint(w, matching)
			}
		})
		c := New(Options{Token: testToken, Account: "test-account", Client: client})
		status, reason := verify.StatusContradicted, verify.ReasonVersionSHAMismatch
		if active {
			status, reason = verify.StatusVerified, ""
		}
		wantOutcome(t, c.Check(context.Background(), deployment()), status, reason)
	}
	c := versionChecker(t, fixture(t, "version-without-sha.json"))
	claim := deployment()
	claim.Marker = "live"
	c.http = checkFunc(func(context.Context, verify.Claim) verify.Outcome {
		return verify.Outcome{Status: verify.StatusContradicted, Reason: verify.ReasonMarkerMissing}
	})
	wantOutcome(t, c.Check(context.Background(), claim), verify.StatusContradicted, verify.ReasonMarkerMissing)
}
func TestDeploymentContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := versionChecker(t, fixture(t, "version-with-sha.json"))
	wantOutcome(t, c.Check(ctx, deployment()), verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
	claim := deployment()
	claim.Worker = ""
	claim.Health = "https://example.test/health"
	wantOutcome(t, c.Check(ctx, claim), verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
}
func TestDeploymentAccountAndTokenResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "")
	if err := os.Mkdir(filepath.Join(home, ".cloudflare"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cloudflare", "api-token"), []byte("  "+testToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if New(Options{}).token != testToken {
		t.Fatal("file token not resolved")
	}
	t.Setenv("CLOUDFLARE_API_TOKEN", "env-credential")
	if New(Options{}).token != "env-credential" || New(Options{Token: testToken}).token != testToken {
		t.Fatal("token precedence")
	}
	matching := fixture(t, "version-with-sha.json")
	deployments := fixture(t, "deployments.json")
	for _, source := range []string{"claim", "options", "environment", "discovery"} {
		t.Run(source, func(t *testing.T) {
			t.Setenv("CLOUDFLARE_ACCOUNT_ID", "")
			calls := 0
			client := apiClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/client/v4/accounts" {
					calls++
					fmt.Fprint(w, `{"success":true,"result":[{"id":"discovery"},{"id":"wrong"}]}`)
					return
				}
				if !strings.Contains(r.URL.Path, "/accounts/"+source+"/") {
					t.Errorf("account path %s", r.URL.Path)
				}
				if strings.HasSuffix(r.URL.Path, "/deployments") {
					fmt.Fprint(w, deployments)
				} else {
					fmt.Fprint(w, matching)
				}
			})
			opts := Options{Token: testToken, Client: client}
			claim := deployment()
			switch source {
			case "claim":
				claim.Account = source
				opts.Account = "wrong"
			case "options":
				opts.Account = source
				t.Setenv("CLOUDFLARE_ACCOUNT_ID", "wrong")
			case "environment":
				t.Setenv("CLOUDFLARE_ACCOUNT_ID", source)
			}
			wantOutcome(t, New(opts).Check(context.Background(), claim), verify.StatusVerified, "")
			if (source == "discovery") != (calls == 1) {
				t.Fatalf("account discovery calls %d", calls)
			}
		})
	}
}
