// Package cloudflare verifies Workers deployments against active versions and live URLs.
package cloudflare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	httpprovider "github.com/joshduffy/readback/internal/providers/http"
	"github.com/joshduffy/readback/internal/verify"
)

type Options struct {
	Client           *http.Client
	Token            string
	Account          string
	URLChecker       verify.Checker
	UserAgent        string
	DisableCacheBust bool
	Assertions       *verify.Assertions
}

type Checker struct {
	client      *http.Client
	token       string
	account     string
	urlChecker  verify.Checker
	userAgent   string
	noCacheBust bool
	assertions  *verify.Assertions

	mu         sync.Mutex
	discovered string // account id resolved from /accounts, cached for the checker's lifetime
}

func New(opts Options) *Checker {
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN"))
	}
	if token == "" {
		if home, err := os.UserHomeDir(); err == nil {
			data, _ := os.ReadFile(filepath.Join(home, ".cloudflare", "api-token"))
			token = strings.TrimSpace(string(data))
		}
	}
	account := opts.Account
	if account == "" {
		account = os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	userAgent := opts.UserAgent
	if userAgent == "" {
		userAgent = "readback/dev"
	}
	checker := opts.URLChecker
	if checker == nil {
		checker = httpprovider.New(httpprovider.Options{
			Client: client, UserAgent: userAgent, DisableCacheBust: opts.DisableCacheBust, Assertions: opts.Assertions,
		})
	}
	return &Checker{
		client: client, token: token, account: account, urlChecker: checker,
		userAgent: userAgent, noCacheBust: opts.DisableCacheBust, assertions: opts.Assertions,
	}
}

func rung(source, name, status, reason string, observed map[string]any) verify.Outcome {
	if observed == nil {
		observed = make(map[string]any)
	}
	observed["rung"], observed["status"] = name, status
	if reason != "" {
		observed["reason"] = reason
	}
	return verify.Outcome{Status: status, Reason: reason, Evidence: []verify.Evidence{{Source: source, Call: name, Observed: observed}}}
}

func (c *Checker) Check(ctx context.Context, claim verify.Claim) verify.Outcome {
	if c.token == "" && claim.Worker != "" {
		return rung("cloudflare", "auth", verify.StatusIndeterminate, verify.ReasonAuthMissing, nil)
	}
	var outcomes []verify.Outcome
	observed := 0 // rungs that actually fetched something at the edge
	shaVerified := false
	if claim.Worker != "" {
		outcome := c.version(ctx, claim)
		outcomes = append(outcomes, outcome)
		shaVerified = outcome.Status == verify.StatusVerified
	}
	if claim.Health != "" {
		outcome := c.health(ctx, claim)
		outcomes = append(outcomes, outcome)
		shaVerified = shaVerified || outcome.Status == verify.StatusVerified
		observed++
	}
	if claim.Marker != "" && claim.URL != "" {
		outcomes = append(outcomes, c.delegate(ctx, "marker", verify.Claim{Type: "url_serving", URL: claim.URL, Marker: claim.Marker, ExpectStatus: 200}))
		observed++
	}
	for _, smoke := range claim.Smoke {
		if smoke.URL == "" {
			continue
		}
		outcomes = append(outcomes, c.delegate(ctx, "smoke", smoke))
		observed++
	}
	result := verify.Outcome{Status: verify.StatusVerified}
	for _, outcome := range outcomes {
		result.Evidence = append(result.Evidence, outcome.Evidence...)
		if shaVerified && outcome.Status == verify.StatusIndeterminate && outcome.Reason == verify.ReasonVersionSHAUnavailable {
			continue
		}
		if outcome.Status == verify.StatusContradicted && result.Status != verify.StatusContradicted || outcome.Status == verify.StatusIndeterminate && result.Status == verify.StatusVerified {
			result.Status, result.Reason = outcome.Status, outcome.Reason
		}
	}
	// API-side evidence alone never verifies a deployment: without a health, marker, or
	// smoke rung the claim is indeterminate (spec section 4). A contradicted rung still wins.
	observable := observed > 0
	if result.Status == verify.StatusVerified && !shaVerified {
		result.Status, result.Reason = verify.StatusIndeterminate, verify.ReasonVersionSHAUnavailable
	}
	if result.Status == verify.StatusVerified && !observable {
		result.Status, result.Reason = verify.StatusIndeterminate, verify.ReasonMarkerMissing
	}
	if len(outcomes) == 0 {
		result.Status, result.Reason = verify.StatusIndeterminate, verify.ReasonVersionSHAUnavailable
		if len(result.Evidence) == 0 {
			result.Evidence = rung("cloudflare", "observable", result.Status, result.Reason, nil).Evidence
		}
	}
	if !shaVerified && result.Status != verify.StatusContradicted {
		result.Evidence[len(result.Evidence)-1].Observed["message"] = "a provider or health response must report the claimed commit sha"
	} else if !observable && result.Status != verify.StatusContradicted {
		result.Evidence[len(result.Evidence)-1].Observed["message"] = "at least one observable rung is required"
	}
	if c.token == "" {
		return result
	}
	// Even injected transports and checkers must not echo the credential into evidence.
	tokenPseudonym := verify.Pseudonymize("cloudflare-api-token", c.token)
	for i := range result.Evidence {
		e := &result.Evidence[i]
		result.Reason = strings.ReplaceAll(result.Reason, c.token, tokenPseudonym)
		e.Source = strings.ReplaceAll(e.Source, c.token, tokenPseudonym)
		e.Call = strings.ReplaceAll(e.Call, c.token, tokenPseudonym)
		if data, err := json.Marshal(e.Observed); err == nil {
			var clean map[string]any
			encodedToken, _ := json.Marshal(c.token)
			if json.Unmarshal([]byte(strings.ReplaceAll(string(data), string(encodedToken[1:len(encodedToken)-1]), tokenPseudonym)), &clean) == nil {
				e.Observed = clean
			} else {
				e.Observed = map[string]any{"message": "evidence unavailable"}
			}
		} else {
			e.Observed = map[string]any{"message": "evidence unavailable"}
		}
	}
	return result
}

func (c *Checker) delegate(ctx context.Context, name string, claim verify.Claim) verify.Outcome {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	outcome := c.urlChecker.Check(ctx, claim)
	if outcome.Status != verify.StatusVerified && outcome.Status != verify.StatusContradicted && outcome.Status != verify.StatusIndeterminate {
		outcome.Status, outcome.Reason = verify.StatusIndeterminate, verify.ReasonProviderUnreachable
	}
	return rung("http", name, outcome.Status, outcome.Reason, map[string]any{"checks": outcome.Evidence})
}

type apiResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code int `json:"code"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

func (c *Checker) api(ctx context.Context, path string, target any) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.cloudflare.com/client/v4"+path, nil)
	if err != nil {
		return verify.StatusIndeterminate, verify.ReasonProviderUnreachable
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	client := *c.client
	// API redirects must never forward credentials outside the system of record.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return verify.StatusIndeterminate, verify.ReasonProviderUnreachable
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 401, 403:
		return verify.StatusIndeterminate, verify.ReasonAuthMissing
	case 404:
		return verify.StatusContradicted, verify.ReasonDeploymentNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return verify.StatusIndeterminate, verify.ReasonProviderUnreachable
	}
	var envelope apiResponse
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 || json.Unmarshal(body, &envelope) != nil {
		return verify.StatusIndeterminate, verify.ReasonProviderUnreachable
	}
	if !envelope.Success {
		for _, e := range envelope.Errors {
			if e.Code == 10007 {
				return verify.StatusContradicted, verify.ReasonDeploymentNotFound
			}
		}
		return verify.StatusIndeterminate, verify.ReasonProviderUnreachable
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" || json.Unmarshal(envelope.Result, target) != nil {
		return verify.StatusIndeterminate, verify.ReasonProviderUnreachable
	}
	return verify.StatusVerified, ""
}

// Annotations are free text (a commit message can contain any hex run), so only a maximal
// hex run of exactly 40 characters counts as a commit id. Matching maximal runs rather
// than delimited tokens keeps every adjacent token visible: a delimiter-consuming pattern
// would skip a token that shares its delimiter with the previous match. The 12-char
// prefix rule applies to the structured health commit_sha field only.
var hexRun = regexp.MustCompile(`[0-9a-fA-F]+`)

func matchesSHA(value, sha string) bool {
	return len(value) >= 12 && len(value) <= 40 && strings.HasPrefix(strings.ToLower(sha), strings.ToLower(value))
}

func (c *Checker) version(ctx context.Context, claim verify.Claim) verify.Outcome {
	observed := map[string]any{}
	finish := func(status, reason string) verify.Outcome {
		return rung("cloudflare", "version_sha", status, reason, observed)
	}
	account := claim.Account
	if account == "" {
		account = c.account
	}
	if account == "" {
		discovered, status, reason := c.discoverAccount(ctx)
		if status != verify.StatusVerified {
			return finish(status, reason)
		}
		account = discovered
	}
	base := "/accounts/" + url.PathEscape(account) + "/workers/scripts/" + url.PathEscape(claim.Worker)
	var deployments struct {
		Deployments []struct {
			Versions []struct {
				ID         string  `json:"version_id"`
				Percentage float64 `json:"percentage"`
			} `json:"versions"`
		} `json:"deployments"`
	}
	status, reason := c.api(ctx, base+"/deployments", &deployments)
	if status != verify.StatusVerified {
		return finish(status, reason)
	}
	if len(deployments.Deployments) == 0 {
		observed["deployments"] = 0
		return finish(verify.StatusIndeterminate, verify.ReasonVersionSHAUnavailable)
	}
	matched, mismatch, unannotated := false, false, false
	authFailed, unreachable := false, false
	var shas []string
	for _, version := range deployments.Deployments[0].Versions {
		if version.Percentage <= 0 {
			continue
		}
		if version.ID == "" {
			unreachable = true
			continue
		}
		var detail struct {
			Annotations map[string]any `json:"annotations"`
		}
		status, reason := c.api(ctx, base+"/versions/"+url.PathEscape(version.ID), &detail)
		if status != verify.StatusVerified {
			// A referenced version that 404s or returns API10007 is inconsistent version
			// evidence, not proof the deployment is absent; only the deployments-list
			// call may contradict with deployment_not_found, so any non-auth failure
			// here only makes the version unreachable.
			if reason == verify.ReasonAuthMissing {
				authFailed = true
			} else {
				unreachable = true
			}
			continue
		}
		usable := false
		for _, key := range []string{"workers/message", "workers/git-commit"} {
			text, _ := detail.Annotations[key].(string)
			for _, sha := range hexRun.FindAllString(text, -1) {
				if len(sha) != 40 {
					continue
				}
				shas = append(shas, sha)
				usable = true
				if strings.EqualFold(sha, claim.SHA) {
					matched = true
				} else {
					mismatch = true
				}
			}
		}
		if !usable {
			unannotated = true
		}
	}
	observed["annotation_shas"] = shas
	if matched {
		return finish(verify.StatusVerified, "")
	}
	// Without a match, uncertainty blocks contradiction with a stable priority that does
	// not depend on version order; only a completely observed active set contradicts.
	switch {
	case authFailed:
		return finish(verify.StatusIndeterminate, verify.ReasonAuthMissing)
	case unreachable:
		return finish(verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
	case unannotated:
		return finish(verify.StatusIndeterminate, verify.ReasonVersionSHAUnavailable)
	case mismatch:
		return finish(verify.StatusContradicted, verify.ReasonVersionSHAMismatch)
	}
	return finish(verify.StatusIndeterminate, verify.ReasonVersionSHAUnavailable)
}

func (c *Checker) health(ctx context.Context, claim verify.Claim) verify.Outcome {
	finish := func(status, reason string, observed map[string]any) verify.Outcome {
		return rung("cloudflare", "health_sha", status, reason, observed)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	allowed, urlErr := verify.URLAllowed(c.assertions, claim.Health)
	if urlErr != nil {
		return finish(verify.StatusIndeterminate, verify.ReasonProviderUnreachable, map[string]any{
			"url_refused": verify.SanitizeURL(claim.Health), "malformed_url": true,
		})
	}
	if !allowed {
		return finish(verify.StatusContradicted, verify.ReasonHostNotAllowed, map[string]any{
			"url_refused": verify.SanitizeURL(claim.Health),
		})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claim.Health, nil)
	if err != nil {
		return finish(verify.StatusIndeterminate, verify.ReasonProviderUnreachable, nil)
	}
	if !c.noCacheBust {
		query := req.URL.Query()
		query.Add("readback_bust", strconv.FormatInt(time.Now().UnixNano(), 10))
		req.URL.RawQuery = query.Encode()
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Cache-Control", "no-cache")
	client := *c.client
	guard := httpprovider.NewRedirectGuard(c.assertions, 5, c.client.CheckRedirect)
	client.CheckRedirect = guard.CheckRedirect
	resp, err := client.Do(req)
	if err != nil {
		if guard.MalformedURL != "" {
			return finish(verify.StatusIndeterminate, verify.ReasonProviderUnreachable, map[string]any{
				"url_refused": verify.SanitizeURL(guard.MalformedURL), "malformed_url": true,
			})
		}
		if guard.RefusedURL != "" {
			return finish(verify.StatusContradicted, verify.ReasonHostNotAllowed, map[string]any{
				"url_refused": verify.SanitizeURL(guard.RefusedURL),
			})
		}
		observed := map[string]any{"error": verify.SanitizeURLs(err.Error())}
		if guard.DowngradeRefused {
			observed["downgrade_refused"] = true
		}
		return finish(verify.StatusIndeterminate, verify.ReasonProviderUnreachable, observed)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return finish(verify.StatusIndeterminate, verify.ReasonProviderUnreachable, nil)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	var health struct {
		SHA json.RawMessage `json:"commit_sha"`
	}
	if err != nil || len(body) > 2<<20 || json.Unmarshal(body, &health) != nil {
		return finish(verify.StatusIndeterminate, verify.ReasonProviderUnreachable, nil)
	}
	var shaValue string
	if json.Unmarshal(health.SHA, &shaValue) != nil {
		// JSON arrived but commit_sha is absent or not a string: the endpoint answered and it does not match.
		return finish(verify.StatusContradicted, verify.ReasonHealthSHAMismatch, map[string]any{"commit_sha_type": "non-string"})
	}
	observed := map[string]any{"commit_sha": shaValue}
	if matchesSHA(shaValue, claim.SHA) {
		return finish(verify.StatusVerified, "", observed)
	}
	return finish(verify.StatusContradicted, verify.ReasonHealthSHAMismatch, observed)
}

// discoverAccount resolves the token's first account once per checker so a run with
// several deployment claims makes one /accounts call, not one per claim.
func (c *Checker) discoverAccount(ctx context.Context) (string, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.discovered != "" {
		return c.discovered, verify.StatusVerified, ""
	}
	var accounts []struct {
		ID string `json:"id"`
	}
	status, reason := c.api(ctx, "/accounts", &accounts)
	if status != verify.StatusVerified {
		return "", status, reason
	}
	if len(accounts) == 0 || accounts[0].ID == "" {
		return "", verify.StatusIndeterminate, verify.ReasonProviderUnreachable
	}
	c.discovered = accounts[0].ID
	return c.discovered, verify.StatusVerified, ""
}
