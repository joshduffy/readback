package verify

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestSanitizeURLPseudonymizesCredentialsLosslessly(t *testing.T) {
	raw := "https://same:same@example.com/a/b?keep=1&access_token=secret&access_token=second&access_token=secret#fragment"
	first := SanitizeURL(raw)
	second := SanitizeURL(raw)
	if first != second {
		t.Fatalf("same URL produced different pseudonyms: %q and %q", first, second)
	}
	for _, secret := range []string{"same", "secret", "second", "fragment"} {
		if strings.Contains(first, secret) {
			t.Fatalf("SanitizeURL leaked %q in %q", secret, first)
		}
	}

	parsed, err := url.Parse(first)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "example.com" || parsed.Path != "/a/b" || parsed.Fragment == "" {
		t.Fatalf("safe URL structure was lost: %q", first)
	}
	password, present := parsed.User.Password()
	if !present || parsed.User.Username() == password {
		t.Fatalf("userinfo structure or component separation was lost: %q", first)
	}
	query := parsed.Query()
	values := query["access_token"]
	if query.Get("keep") != "1" || len(values) != 3 || values[0] != values[2] || values[0] == values[1] {
		t.Fatalf("query multiplicity or equality was lost: %#v", query)
	}
}

func TestSanitizeURLsPseudonymizesMalformedRelativeLocations(t *testing.T) {
	const secret = "dummy-relative-location-secret"
	location := "/%zz?token=" + secret
	errorText := `failed to parse Location header "` + location + `": parse "` + location + `": invalid URL escape "%zz"`
	got := SanitizeURLs(errorText)
	if strings.Contains(got, secret) {
		t.Fatalf("SanitizeURLs leaked the credential in %q", got)
	}
	sanitized := SanitizeURL(location)
	if strings.Count(got, sanitized) != 2 || !strings.Contains(sanitized, "/rb_") || !strings.Contains(sanitized, "token=rb_") {
		t.Fatalf("malformed Location relationship was lost: location=%q error=%q", sanitized, got)
	}
	if other := SanitizeURL("/%xy?token=another-dummy-secret"); other == sanitized || strings.Contains(other, "another-dummy-secret") {
		t.Fatalf("distinct malformed Locations were not safely distinguishable: %q and %q", sanitized, other)
	}
	if !strings.Contains(got, `failed to parse Location header`) || !strings.Contains(got, `invalid URL escape "%zz"`) {
		t.Fatalf("useful error class was lost: %q", got)
	}
}

func TestURLAllowedRejectsCredentialsMalformedQueriesAndHosts(t *testing.T) {
	assertions := &Assertions{AllowHosts: []string{"example.com"}}
	for _, address := range []string{
		"https://user@example.com/path",
		"https://example.com/path?api-key=secret",
		"https://example.com/path?token=secret;bad=value",
		"https://foreign.test/path",
	} {
		if allowed, _ := URLAllowed(assertions, address); allowed {
			t.Fatalf("URLAllowed(%q) = true", address)
		}
	}
	if allowed, err := URLAllowed(assertions, "https://example.com/path?build=123"); err != nil || !allowed {
		t.Fatal("safe URL was rejected")
	}
	if _, err := URLAllowed(assertions, "https://example.com/path?a=1;b=2"); err == nil {
		t.Fatal("malformed query was not distinguished from a policy refusal")
	}
}

func TestURLContainsCredentialsRecognizesKnownKeys(t *testing.T) {
	keys := []string{
		"refresh_token", "id_token", "security_token", "session_token",
		"X-Amz-Credential", "X-Amz-Signature", "X-Amz-Security-Token",
		"X-Goog-Credential", "X-Goog-Signature", "X-Goog-Security-Token",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			hasCredentials, err := URLContainsCredentials("https://example.com/?" + url.QueryEscape(key) + "=value")
			if err != nil || !hasCredentials {
				t.Fatalf("hasCredentials = %v, err = %v", hasCredentials, err)
			}
		})
	}
}

func TestSanitizeURLsHandlesEscapedLocationQuotes(t *testing.T) {
	const secret = "dummy-escaped-location-token"
	for _, location := range []string{"/%zz\"?token=" + secret, "https://example.test/%zz\"?token=" + secret} {
		message := fmt.Sprintf("failed to parse Location header %q: parse %q: invalid URL escape", location, location)
		sanitized := SanitizeURLs(message)
		if strings.Contains(sanitized, secret) || !strings.Contains(sanitized, "invalid URL escape") {
			t.Fatalf("unsafe or uninformative error: %s", sanitized)
		}
	}
}
