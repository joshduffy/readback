package http_test

import (
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	provider "github.com/joshduffy/readback/internal/providers/http"
	"github.com/joshduffy/readback/internal/verify"
)

func check(t *testing.T, c *provider.Checker, ctx context.Context, claim verify.Claim) verify.Outcome {
	t.Helper()
	outcome := c.Check(ctx, claim)
	if outcome.Status != verify.StatusVerified && !verify.KnownReason(outcome.Reason) {
		t.Fatalf("unknown reason %q", outcome.Reason)
	}
	if len(outcome.Evidence) == 0 {
		t.Fatalf("no evidence recorded for status %q", outcome.Status)
	}
	return outcome
}

func TestUrlServingMarkerFound(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = w.Write([]byte("hello deploy-marker-123 world"))
	}))
	defer srv.Close()

	c := provider.New(provider.Options{})
	outcome := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL, ExpectStatus: 200, Marker: "deploy-marker-123",
	})
	if outcome.Status != verify.StatusVerified {
		t.Fatalf("want verified, got %q (%s)", outcome.Status, outcome.Reason)
	}
	observed := outcome.Evidence[0].Observed
	if observed["marker_offset"] != 6 {
		t.Fatalf("want marker_offset 6, got %v", observed["marker_offset"])
	}
}

func TestUrlServingStatusMismatch(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusNotFound)
	}))
	defer srv.Close()

	c := provider.New(provider.Options{})
	outcome := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL, ExpectStatus: 200,
	})
	if outcome.Status != verify.StatusContradicted || outcome.Reason != verify.ReasonStatusMismatch {
		t.Fatalf("want contradicted/status_mismatch, got %q/%q", outcome.Status, outcome.Reason)
	}
}

func TestUrlServingCacheBustQueryPresent(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		q := r.URL.Query()
		if q.Get("readback_bust") == "" {
			w.WriteHeader(nethttp.StatusBadRequest)
			_, _ = w.Write([]byte("missing readback_bust param"))
			return
		}
		if q.Get("keep") != "1" {
			w.WriteHeader(nethttp.StatusBadRequest)
			_, _ = w.Write([]byte("existing query param lost"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := provider.New(provider.Options{})
	outcome := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL + "?keep=1", ExpectStatus: 200,
	})
	if outcome.Status != verify.StatusVerified {
		t.Fatalf("want verified, got %q (%s)", outcome.Status, outcome.Reason)
	}
}

func TestUrlServingRedirectLimit(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		nethttp.Redirect(w, r, r.URL.Path+"x", nethttp.StatusFound)
	}))
	defer srv.Close()

	c := provider.New(provider.Options{MaxRedirects: 2})
	outcome := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL + "/", ExpectStatus: 200,
	})
	if outcome.Status != verify.StatusIndeterminate || outcome.Reason != verify.ReasonProviderUnreachable {
		t.Fatalf("want indeterminate/provider_unreachable, got %q/%q", outcome.Status, outcome.Reason)
	}
}

func TestUrlServingHeaderMismatch(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Header().Set("X-Deploy-Sha", "abc123")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := provider.New(provider.Options{})

	mismatch := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL, ExpectStatus: 200,
		Header: map[string]string{"X-Deploy-Sha": "def456"},
	})
	if mismatch.Status != verify.StatusContradicted || mismatch.Reason != verify.ReasonHeaderMismatch {
		t.Fatalf("want contradicted/header_mismatch, got %q/%q", mismatch.Status, mismatch.Reason)
	}

	// Header names match case-insensitively, values exactly.
	match := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL, ExpectStatus: 200,
		Header: map[string]string{"x-deploy-sha": "abc123"},
	})
	if match.Status != verify.StatusVerified {
		t.Fatalf("want verified for case-insensitive name, got %q (%s)", match.Status, match.Reason)
	}
}

func TestUrlServingTimeoutIsIndeterminate(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	c := provider.New(provider.Options{})
	outcome := check(t, c, ctx, verify.Claim{
		Type: "url_serving", URL: srv.URL, ExpectStatus: 200,
	})
	if outcome.Status != verify.StatusIndeterminate || outcome.Reason != verify.ReasonProviderUnreachable {
		t.Fatalf("want indeterminate/provider_unreachable, got %q/%q", outcome.Status, outcome.Reason)
	}
}

func TestUrlServingBodyCap(t *testing.T) {
	body := strings.Repeat("a", 256) + "tail-marker"
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := provider.New(provider.Options{MaxBody: 128})
	outcome := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL, ExpectStatus: 200, Marker: "tail-marker",
	})
	if outcome.Status != verify.StatusContradicted || outcome.Reason != verify.ReasonMarkerMissing {
		t.Fatalf("want contradicted/marker_missing, got %q/%q", outcome.Status, outcome.Reason)
	}
	if got := outcome.Evidence[0].Observed["bytes_read"]; got != 128 {
		t.Fatalf("want bytes_read 128, got %v", got)
	}
}

func redirectChainServer() *httptest.Server {
	return httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/":
			nethttp.Redirect(w, r, "/a", nethttp.StatusFound)
		case "/a":
			nethttp.Redirect(w, r, "/b", nethttp.StatusFound)
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
}

func TestUrlServingRedirectLimitEdges(t *testing.T) {
	srv := redirectChainServer()
	defer srv.Close()

	c := provider.New(provider.Options{MaxRedirects: 1})

	// /a -> /b is exactly one redirect and must be allowed.
	one := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL + "/a", ExpectStatus: 200,
	})
	if one.Status != verify.StatusVerified {
		t.Fatalf("want verified with exactly MaxRedirects redirects, got %q (%s)", one.Status, one.Reason)
	}
	if got := one.Evidence[0].Observed["redirects"]; got != 1 {
		t.Fatalf("want redirects 1, got %v", got)
	}

	// / -> /a -> /b needs two redirects; the second must be refused.
	two := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL + "/", ExpectStatus: 200,
	})
	if two.Status != verify.StatusIndeterminate || two.Reason != verify.ReasonProviderUnreachable {
		t.Fatalf("want indeterminate/provider_unreachable past the limit, got %q/%q", two.Status, two.Reason)
	}
}

func TestUrlServingChainsCallerCheckRedirect(t *testing.T) {
	srv := redirectChainServer()
	defer srv.Close()

	calls := 0
	base := &nethttp.Client{CheckRedirect: func(_ *nethttp.Request, via []*nethttp.Request) error {
		calls++
		if len(via) != 1 {
			t.Errorf("caller callback saw via of length %d, want 1", len(via))
		}
		return nil
	}}
	c := provider.New(provider.Options{Client: base})
	outcome := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL + "/a", ExpectStatus: 200,
	})
	if outcome.Status != verify.StatusVerified {
		t.Fatalf("want verified, got %q (%s)", outcome.Status, outcome.Reason)
	}
	if calls != 1 {
		t.Fatalf("caller CheckRedirect invoked %d times, want 1", calls)
	}

	// A refusing caller policy must be honored, not discarded.
	refusing := &nethttp.Client{CheckRedirect: func(_ *nethttp.Request, _ []*nethttp.Request) error {
		return errors.New("caller policy refused redirect")
	}}
	c = provider.New(provider.Options{Client: refusing})
	refused := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL + "/a", ExpectStatus: 200,
	})
	if refused.Status != verify.StatusIndeterminate || refused.Reason != verify.ReasonProviderUnreachable {
		t.Fatalf("want indeterminate/provider_unreachable when caller refuses, got %q/%q", refused.Status, refused.Reason)
	}
	if got := refused.Evidence[0].Observed["error"]; !strings.Contains(got.(string), "caller policy refused redirect") {
		t.Fatalf("want caller error in evidence, got %v", got)
	}
}

func TestUrlServingRefusesDowngrade(t *testing.T) {
	plain := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		nethttp.Redirect(w, r, plain.URL, nethttp.StatusFound)
	}))
	defer secure.Close()

	c := provider.New(provider.Options{Client: secure.Client()})
	outcome := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: secure.URL, ExpectStatus: 200,
	})
	if outcome.Status != verify.StatusIndeterminate || outcome.Reason != verify.ReasonProviderUnreachable {
		t.Fatalf("want indeterminate/provider_unreachable on downgrade, got %q/%q", outcome.Status, outcome.Reason)
	}
	if got := outcome.Evidence[0].Observed["downgrade_refused"]; got != true {
		t.Fatalf("want observed.downgrade_refused true, got %v", got)
	}
}

func TestUrlServingCacheBustDoesNotOverwrite(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		q := r.URL.Query()
		if q.Get("readback") != "original" {
			w.WriteHeader(nethttp.StatusBadRequest)
			_, _ = w.Write([]byte("existing readback param overwritten"))
			return
		}
		if q.Get("readback_bust") == "" {
			w.WriteHeader(nethttp.StatusBadRequest)
			_, _ = w.Write([]byte("missing readback_bust param"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := provider.New(provider.Options{})
	outcome := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL + "?readback=original", ExpectStatus: 200,
	})
	if outcome.Status != verify.StatusVerified {
		t.Fatalf("want verified, got %q (%s)", outcome.Status, outcome.Reason)
	}
}

func TestUrlServingAbsentHeaderIsMismatch(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Header()["X-Present-Empty"] = []string{""}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := provider.New(provider.Options{})

	absent := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL, ExpectStatus: 200,
		Header: map[string]string{"X-Absent": ""},
	})
	if absent.Status != verify.StatusContradicted || absent.Reason != verify.ReasonHeaderMismatch {
		t.Fatalf("want contradicted/header_mismatch for absent header, got %q/%q", absent.Status, absent.Reason)
	}
	if got := absent.Evidence[0].Observed["header_mismatch"]; got != "X-Absent" {
		t.Fatalf("want header_mismatch X-Absent, got %v", got)
	}

	// An empty expected value matches a header that is present with an empty value.
	present := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL, ExpectStatus: 200,
		Header: map[string]string{"X-Present-Empty": ""},
	})
	if present.Status != verify.StatusVerified {
		t.Fatalf("want verified for present empty header, got %q (%s)", present.Status, present.Reason)
	}
}

func TestUrlServing5xxIsIndeterminate(t *testing.T) {
	for _, code := range []int{nethttp.StatusServiceUnavailable, nethttp.StatusInternalServerError, nethttp.StatusTooManyRequests} {
		srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.WriteHeader(code)
		}))

		c := provider.New(provider.Options{})
		outcome := check(t, c, context.Background(), verify.Claim{
			Type: "url_serving", URL: srv.URL, ExpectStatus: 200,
		})
		if outcome.Status != verify.StatusIndeterminate || outcome.Reason != verify.ReasonProviderUnreachable {
			t.Fatalf("status %d: want indeterminate/provider_unreachable, got %q/%q", code, outcome.Status, outcome.Reason)
		}
		if got := outcome.Evidence[0].Observed["status"]; got != code {
			t.Fatalf("status %d: want observed.status %d, got %v", code, code, got)
		}
		srv.Close()
	}
}

func TestUrlServingPartialOptionsKeepCacheBust(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.URL.Query().Get("readback_bust") == "" {
			w.WriteHeader(nethttp.StatusBadRequest)
			_, _ = w.Write([]byte("missing readback_bust param"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := provider.New(provider.Options{UserAgent: "readback-test", MaxRedirects: 3})
	outcome := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL, ExpectStatus: 200,
	})
	if outcome.Status != verify.StatusVerified {
		t.Fatalf("want verified with partial options, got %q (%s)", outcome.Status, outcome.Reason)
	}
}

func TestUrlServingBodyErrorKeepsStatusEvidence(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		conn, rw, err := w.(nethttp.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		// Declare a 1024-byte body, then close after 5 bytes.
		_, _ = rw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 1024\r\nContent-Type: text/plain\r\n\r\nshort")
		_ = rw.Flush()
	}))
	defer srv.Close()

	c := provider.New(provider.Options{})
	outcome := check(t, c, context.Background(), verify.Claim{
		Type: "url_serving", URL: srv.URL, ExpectStatus: 200,
	})
	if outcome.Status != verify.StatusIndeterminate || outcome.Reason != verify.ReasonProviderUnreachable {
		t.Fatalf("want indeterminate/provider_unreachable on body error, got %q/%q", outcome.Status, outcome.Reason)
	}
	observed := outcome.Evidence[0].Observed
	if got := observed["status"]; got != 200 {
		t.Fatalf("want observed.status 200, got %v", got)
	}
	if got := observed["final_url"]; got == nil || got == "" {
		t.Fatalf("want observed.final_url recorded, got %v", got)
	}
}

func TestUrlServingNoCacheBustLeavesQueryAlone(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.URL.RawQuery != "keep=1" {
			w.WriteHeader(nethttp.StatusBadRequest)
			_, _ = w.Write([]byte("query was modified: " + r.URL.RawQuery))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	outcome := provider.New(provider.Options{DisableCacheBust: true}).Check(context.Background(), verify.Claim{Type: "url_serving", URL: srv.URL + "?keep=1", ExpectStatus: 200})
	if outcome.Status != verify.StatusVerified {
		t.Fatalf("want verified, got %+v", outcome)
	}
}

func TestUrlServingExistingBustParamStaysFirst(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.URL.Query().Get("readback_bust") != "original" || len(r.URL.Query()["readback_bust"]) != 2 {
			w.WriteHeader(nethttp.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	outcome := provider.New(provider.Options{}).Check(context.Background(), verify.Claim{Type: "url_serving", URL: srv.URL + "/?readback_bust=original", ExpectStatus: 200})
	if outcome.Status != verify.StatusVerified {
		t.Fatalf("existing readback_bust was clobbered: %+v", outcome)
	}
}
