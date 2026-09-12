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
)

var bustCounter atomic.Int64

type Options struct {
	UserAgent        string
	DisableCacheBust bool
	MaxRedirects     int
	MaxBody          int64
	Client           *nethttp.Client
}

type Checker struct {
	userAgent    string
	cacheBust    bool
	maxRedirects int
	maxBody      int64
	client       *nethttp.Client
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
	}
}

func (c *Checker) Check(ctx context.Context, claim verify.Claim) verify.Outcome {
	target := claim.URL
	if c.cacheBust {
		if parsed, err := neturl.Parse(target); err == nil {
			q := parsed.Query()
			bust := bustCounter.Add(1)
			q.Set("readback_bust", strconv.FormatInt(time.Now().UnixNano(), 10)+"-"+strconv.FormatInt(bust, 10))
			parsed.RawQuery = q.Encode()
			target = parsed.String()
		}
	}
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, target, nil)
	if err != nil {
		return verify.Outcome{Status: verify.StatusIndeterminate, Reason: verify.ReasonProviderUnreachable,
			Evidence: c.evidence(target, map[string]any{"error": err.Error()})}
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Cache-Control", "no-cache")

	redirects := 0
	client := *c.client
	callerRedirect := c.client.CheckRedirect
	client.CheckRedirect = func(next *nethttp.Request, via []*nethttp.Request) error {
		redirects = len(via)
		if len(via) > c.maxRedirects {
			return errRedirectLimit
		}
		if last := via[len(via)-1]; last.URL.Scheme == "https" && next.URL.Scheme == "http" {
			return errDowngrade
		}
		if callerRedirect != nil {
			return callerRedirect(next, via)
		}
		return nil
	}

	resp, err := client.Do(req)
	if err != nil {
		observed := map[string]any{"error": err.Error(), "redirects": redirects}
		if errors.Is(err, errDowngrade) {
			observed["downgrade_refused"] = true
		}
		return verify.Outcome{Status: verify.StatusIndeterminate, Reason: verify.ReasonProviderUnreachable,
			Evidence: c.evidence(target, observed)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody))
	if err != nil {
		return verify.Outcome{Status: verify.StatusIndeterminate, Reason: verify.ReasonProviderUnreachable,
			Evidence: c.evidence(resp.Request.URL.String(), map[string]any{
				"error":     err.Error(),
				"redirects": redirects,
				"status":    resp.StatusCode,
				"final_url": resp.Request.URL.String(),
			})}
	}

	markerOffset := -1
	if claim.Marker != "" {
		markerOffset = bytes.Index(body, []byte(claim.Marker))
	}
	observed := map[string]any{
		"status":        resp.StatusCode,
		"final_url":     resp.Request.URL.String(),
		"marker_offset": markerOffset,
		"redirects":     redirects,
		"bytes_read":    len(body),
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
	if claim.Marker != "" && markerOffset < 0 {
		return verify.Outcome{Status: verify.StatusContradicted, Reason: verify.ReasonMarkerMissing, Evidence: evidence}
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
	return verify.Outcome{Status: verify.StatusVerified, Evidence: evidence}
}

func (c *Checker) evidence(call string, observed map[string]any) []verify.Evidence {
	if observed == nil {
		observed = map[string]any{}
	}
	return []verify.Evidence{{Source: "http", Call: "GET " + call, Observed: observed}}
}
