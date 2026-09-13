// Package http checks url_serving claims against live URLs.
package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	nethttp "net/http"
	neturl "net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/joshduffy/readback/internal/verify"
)

const defaultMaxBody = 2 << 20 // 2 MiB

var (
	errRedirectLimit = errors.New("readback: redirect limit exceeded")
	errDowngrade     = errors.New("readback: https to http redirect refused")
	errURLMalformed  = errors.New("readback: redirect URL query is malformed")
	errURLNotAllowed = errors.New("readback: redirect URL is not allowed")
)

var bustCounter atomic.Int64

type Options struct {
	UserAgent        string
	DisableCacheBust bool
	MaxRedirects     int
	MaxBody          int64
	Client           *nethttp.Client
	Assertions       *verify.Assertions
}

type Checker struct {
	userAgent    string
	cacheBust    bool
	maxRedirects int
	maxBody      int64
	client       *nethttp.Client
	assertions   *verify.Assertions
}

type RedirectGuard struct {
	MalformedURL     string
	RefusedURL       string
	DowngradeRefused bool
	Redirects        int

	assertions *verify.Assertions
	max        int
	caller     func(*nethttp.Request, []*nethttp.Request) error
}

func NewRedirectGuard(assertions *verify.Assertions, max int, caller func(*nethttp.Request, []*nethttp.Request) error) *RedirectGuard {
	return &RedirectGuard{assertions: assertions, max: max, caller: caller}
}

func (g *RedirectGuard) CheckRedirect(next *nethttp.Request, via []*nethttp.Request) error {
	g.Redirects = len(via)
	if err := g.checkURL(next.URL.String()); err != nil {
		return err
	}
	if len(via) > g.max {
		return errRedirectLimit
	}
	if last := via[len(via)-1]; last.URL.Scheme == "https" && next.URL.Scheme == "http" {
		g.DowngradeRefused = true
		return errDowngrade
	}
	if g.caller != nil {
		if err := g.caller(next, via); err != nil {
			return err
		}
		if err := g.checkURL(next.URL.String()); err != nil {
			return err
		}
		if last := via[len(via)-1]; last.URL.Scheme == "https" && next.URL.Scheme == "http" {
			g.DowngradeRefused = true
			return errDowngrade
		}
	}
	return nil
}

func (g *RedirectGuard) checkURL(raw string) error {
	allowed, err := verify.URLAllowed(g.assertions, raw)
	if err != nil {
		g.MalformedURL = raw
		return errURLMalformed
	}
	if !allowed {
		g.RefusedURL = raw
		return errURLNotAllowed
	}
	return nil
}

func New(opts Options) *Checker {
	userAgent := opts.UserAgent
	if userAgent == "" {
		userAgent = "readback/dev"
	}
	maxRedirects := opts.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = 5
	}
	maxBody := opts.MaxBody
	if maxBody <= 0 {
		maxBody = defaultMaxBody
	}
	base := opts.Client
	if base == nil {
		base = &nethttp.Client{}
	}
	return &Checker{
		userAgent:    userAgent,
		cacheBust:    !opts.DisableCacheBust,
		maxRedirects: maxRedirects,
		maxBody:      maxBody,
		client:       base,
		assertions:   opts.Assertions,
	}
}

func (c *Checker) Check(ctx context.Context, claim verify.Claim) verify.Outcome {
	target := claim.URL
	allowed, urlErr := verify.URLAllowed(c.assertions, target)
	if urlErr != nil {
		return verify.Outcome{Status: verify.StatusIndeterminate, Reason: verify.ReasonProviderUnreachable,
			Evidence: c.evidence(target, map[string]any{"url_refused": verify.SanitizeURL(target), "malformed_url": true})}
	}
	if !allowed {
		return verify.Outcome{Status: verify.StatusContradicted, Reason: verify.ReasonHostNotAllowed,
			Evidence: c.evidence(target, map[string]any{"url_refused": verify.SanitizeURL(target)})}
	}
	if c.cacheBust {
		if parsed, err := neturl.Parse(target); err == nil {
			q := parsed.Query()
			bust := bustCounter.Add(1)
			// Add, never Set: an existing readback_bust value stays first and is what the server reads.
			q.Add("readback_bust", strconv.FormatInt(time.Now().UnixNano(), 10)+"-"+strconv.FormatInt(bust, 10))
			parsed.RawQuery = q.Encode()
			target = parsed.String()
		}
	}
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, target, nil)
	if err != nil {
		return verify.Outcome{Status: verify.StatusIndeterminate, Reason: verify.ReasonProviderUnreachable,
			Evidence: c.evidence(target, map[string]any{"error": verify.SanitizeURLs(err.Error())})}
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Cache-Control", "no-cache")

	client := *c.client
	guard := NewRedirectGuard(c.assertions, c.maxRedirects, c.client.CheckRedirect)
	client.CheckRedirect = guard.CheckRedirect

	resp, err := client.Do(req)
	if err != nil {
		observed := map[string]any{"redirects": guard.Redirects}
		if guard.MalformedURL != "" {
			observed["url_refused"] = verify.SanitizeURL(guard.MalformedURL)
			observed["malformed_url"] = true
		} else if guard.RefusedURL != "" {
			observed["url_refused"] = verify.SanitizeURL(guard.RefusedURL)
		} else {
			observed["error"] = verify.SanitizeURLs(err.Error())
		}
		if guard.DowngradeRefused {
			observed["downgrade_refused"] = true
		}
		reason := verify.ReasonProviderUnreachable
		status := verify.StatusIndeterminate
		if guard.RefusedURL != "" {
			status, reason = verify.StatusContradicted, verify.ReasonHostNotAllowed
		}
		return verify.Outcome{Status: status, Reason: reason,
			Evidence: c.evidence(target, observed)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return verify.Outcome{Status: verify.StatusIndeterminate, Reason: verify.ReasonProviderUnreachable,
			Evidence: c.evidence(resp.Request.URL.String(), map[string]any{
				"error":     verify.SanitizeURLs(err.Error()),
				"redirects": guard.Redirects,
				"status":    resp.StatusCode,
				"final_url": verify.SanitizeURL(resp.Request.URL.String()),
			})}
	}
	truncated := int64(len(body)) > c.maxBody
	if truncated {
		body = body[:c.maxBody]
	}

	markerOffset := -1
	if claim.Marker != "" {
		markerOffset = bytes.Index(body, []byte(claim.Marker))
	}
	observed := map[string]any{
		"status":        resp.StatusCode,
		"final_url":     verify.SanitizeURL(resp.Request.URL.String()),
		"marker_offset": markerOffset,
		"redirects":     guard.Redirects,
		"bytes_read":    len(body),
		"truncated":     truncated,
	}
	evidence := c.evidence(resp.Request.URL.String(), observed)

	expectStatus := claim.ExpectStatus
	if expectStatus == 0 {
		expectStatus = nethttp.StatusOK
	}
	if resp.StatusCode != expectStatus {
		if resp.StatusCode == nethttp.StatusTooManyRequests || resp.StatusCode >= 500 {
			return verify.Outcome{Status: verify.StatusIndeterminate, Reason: verify.ReasonProviderUnreachable, Evidence: evidence}
		}
		return verify.Outcome{Status: verify.StatusContradicted, Reason: verify.ReasonStatusMismatch, Evidence: evidence}
	}
	for name, want := range claim.Header {
		values := resp.Header.Values(name)
		got := ""
		if len(values) > 0 {
			got = values[0]
		}
		if len(values) == 0 || got != want {
			observed["header_mismatch"] = name
			return verify.Outcome{Status: verify.StatusContradicted, Reason: verify.ReasonHeaderMismatch, Evidence: evidence}
		}
	}
	if claim.Marker != "" && markerOffset < 0 {
		if truncated {
			return verify.Outcome{Status: verify.StatusIndeterminate, Reason: verify.ReasonResponseTruncated, Evidence: evidence}
		}
		return verify.Outcome{Status: verify.StatusContradicted, Reason: verify.ReasonMarkerMissing, Evidence: evidence}
	}
	return verify.Outcome{Status: verify.StatusVerified, Evidence: evidence}
}

func (c *Checker) evidence(call string, observed map[string]any) []verify.Evidence {
	if observed == nil {
		observed = map[string]any{}
	}
	return []verify.Evidence{{Source: "http", Call: "GET " + verify.SanitizeURL(call), Observed: observed}}
}
