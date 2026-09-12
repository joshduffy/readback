package github

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshduffy/readback/internal/verify"
)

const testSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../../../testdata/providers/github", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func recorded(t *testing.T, claim verify.Claim, names ...string) verify.Outcome {
	t.Helper()
	calls := 0
	checker := New(func(ctx context.Context, args ...string) ([]byte, int, error) {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		var want []string
		switch claim.Type {
		case "pr_merged":
			want = []string{"api", "repos/o/r/pulls/42"}
		case "commit_on_branch":
			want = []string{"api", "repos/o/r/compare/main..." + testSHA}
		case "checks_passed":
			if calls == 0 {
				want = []string{"api", "repos/o/r/commits/" + testSHA + "/check-runs", "--paginate"}
			} else {
				want = []string{"api", "repos/o/r/commits/" + testSHA + "/status"}
			}
		}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("args = %q, want %q", args, want)
		}
		if calls >= len(names) {
			t.Fatal("unexpected call")
		}
		data := fixture(t, names[calls])
		calls++
		return data, 0, nil
	})
	result := checker.Check(context.Background(), claim)
	if calls != len(names) {
		t.Fatalf("calls = %d, want %d", calls, len(names))
	}
	return result
}

func assertOutcome(t *testing.T, got verify.Outcome, status, reason string) {
	t.Helper()
	if got.Status != status || got.Reason != reason {
		t.Fatalf("got %#v, want %s %s", got, status, reason)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "PRIVATE") {
		t.Fatal("response body leaked")
	}
}

func prClaim() verify.Claim {
	return verify.Claim{Type: "pr_merged", Repo: "o/r", PR: 42, Into: "main", SHA: testSHA}
}
func checksClaim() verify.Claim {
	return verify.Claim{Type: "checks_passed", Repo: "o/r", SHA: testSHA}
}
func branchClaim() verify.Claim {
	return verify.Claim{Type: "commit_on_branch", Repo: "o/r", SHA: testSHA, Branch: "main"}
}

func TestPrMergedVerified(t *testing.T) {
	got := recorded(t, prClaim(), "pr-merged")
	assertOutcome(t, got, verify.StatusVerified, "")
	observed := got.Evidence[0].Observed
	if len(observed) != 5 || observed["merged"] != true || observed["base_ref"] != "main" || observed["merge_commit_sha"] != testSHA {
		t.Fatalf("observed = %#v", observed)
	}
}
func TestPrMergedIntoOtherBranch(t *testing.T) {
	claim := prClaim()
	claim.Into = "release"
	assertOutcome(t, recorded(t, claim, "pr-merged"), verify.StatusContradicted, verify.ReasonMergedIntoOtherBranch)
}
func TestPrNotFoundIsContradicted(t *testing.T) {
	checker := New(func(context.Context, ...string) ([]byte, int, error) {
		return nil, 1, errors.New("gh: Not Found (HTTP 404)")
	})
	for _, tc := range []struct {
		claim  verify.Claim
		reason string
	}{{prClaim(), verify.ReasonNotMerged}, {checksClaim(), verify.ReasonCheckMissing}, {branchClaim(), verify.ReasonSHANotOnBranch}} {
		assertOutcome(t, checker.Check(context.Background(), tc.claim), verify.StatusContradicted, tc.reason)
	}
}
func TestPrMergedShaMismatch(t *testing.T) {
	claim := prClaim()
	claim.SHA = strings.Repeat("b", 40)
	assertOutcome(t, recorded(t, claim, "pr-merged"), verify.StatusContradicted, verify.ReasonNotMerged)
}
func TestPrUnmerged(t *testing.T) {
	assertOutcome(t, recorded(t, prClaim(), "pr-open"), verify.StatusContradicted, verify.ReasonNotMerged)
}
func TestChecksPassedVerified(t *testing.T) {
	claim := checksClaim()
	claim.Require = []string{"build", "lint", "optional", "legacy-ci"}
	got := recorded(t, claim, "checks-success", "status-success")
	assertOutcome(t, got, verify.StatusVerified, "")
	if !reflect.DeepEqual(got.Evidence[0].Observed["counts_per_conclusion"], map[string]int{"success": 1, "neutral": 1, "skipped": 1}) {
		t.Fatalf("evidence = %#v", got.Evidence)
	}
}
func TestChecksPendingIsIndeterminate(t *testing.T) {
	for _, names := range [][]string{{"checks-pending", "status-success"}, {"checks-success", "status-pending"}, {"checks-success", "status-empty"}} {
		assertOutcome(t, recorded(t, checksClaim(), names...), verify.StatusIndeterminate, verify.ReasonChecksPending)
	}
}
func TestChecksFailingIsContradicted(t *testing.T) {
	for _, names := range [][]string{{"checks-failing", "status-success"}, {"checks-pending", "status-failing"}, {"checks-failing", "status-pending"}} {
		assertOutcome(t, recorded(t, checksClaim(), names...), verify.StatusContradicted, verify.ReasonChecksFailing)
	}
}
func TestChecksMissingRequiredName(t *testing.T) {
	claim := checksClaim()
	claim.Require = []string{"absent"}
	got := recorded(t, claim, "checks-pending", "status-success")
	assertOutcome(t, got, verify.StatusContradicted, verify.ReasonCheckMissing)
	if !reflect.DeepEqual(got.Evidence[1].Observed["missing_names"], []string{"absent"}) {
		t.Fatalf("evidence = %#v", got.Evidence)
	}
}
func TestZeroChecksNeverVerified(t *testing.T) {
	for _, name := range []string{"status-empty", "status-empty-failure", "status-empty-success"} {
		assertOutcome(t, recorded(t, checksClaim(), "checks-empty", name), verify.StatusIndeterminate, verify.ReasonCheckMissing)
		// A require list against zero checks is still indeterminate, never contradicted.
		claim := checksClaim()
		claim.Require = []string{"test"}
		assertOutcome(t, recorded(t, claim, "checks-empty", name), verify.StatusIndeterminate, verify.ReasonCheckMissing)
	}
}
func TestCommitOnBranchBehindIsVerified(t *testing.T) {
	got := recorded(t, branchClaim(), "compare-behind")
	assertOutcome(t, got, verify.StatusVerified, "")
	if got.Evidence[0].Observed["behind_by"] != 2 {
		t.Fatalf("evidence = %#v", got.Evidence)
	}
}
func TestCommitOnBranchDivergedIsContradicted(t *testing.T) {
	assertOutcome(t, recorded(t, branchClaim(), "compare-diverged"), verify.StatusContradicted, verify.ReasonSHANotOnBranch)
}
func TestGhAuthMissing(t *testing.T) {
	for _, tc := range []struct {
		code int
		err  error
	}{{4, nil}, {1, errors.New("authentication required PRIVATE")}, {-1, &exec.Error{Name: "gh", Err: exec.ErrNotFound}}, {1, &exec.ExitError{Stderr: []byte("auth required PRIVATE")}}} {
		checker := New(func(context.Context, ...string) ([]byte, int, error) { return nil, tc.code, tc.err })
		assertOutcome(t, checker.Check(context.Background(), prClaim()), verify.StatusIndeterminate, verify.ReasonAuthMissing)
	}
}
func TestGhTimeoutIsIndeterminate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	checker := New(func(ctx context.Context, _ ...string) ([]byte, int, error) { <-ctx.Done(); return nil, -1, ctx.Err() })
	assertOutcome(t, checker.Check(ctx, prClaim()), verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
}
func TestChecksPagination(t *testing.T) {
	calls := 0
	checker := New(func(context.Context, ...string) ([]byte, int, error) {
		calls++
		if calls == 1 {
			return append(fixture(t, "checks-success"), fixture(t, "checks-failing")...), 0, nil
		}
		return fixture(t, "status-success"), 0, nil
	})
	assertOutcome(t, checker.Check(context.Background(), checksClaim()), verify.StatusContradicted, verify.ReasonChecksFailing)
}
func TestMalformedResponseIsIndeterminate(t *testing.T) {
	for _, claim := range []verify.Claim{prClaim(), checksClaim(), branchClaim()} {
		for _, body := range []string{"PRIVATE HTML", "null", "{}", "[]", "{} trailing"} {
			checker := New(func(context.Context, ...string) ([]byte, int, error) { return []byte(body), 0, nil })
			assertOutcome(t, checker.Check(context.Background(), claim), verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
		}
	}
}
func TestExecFailureIsIndeterminate(t *testing.T) {
	checker := New(func(context.Context, ...string) ([]byte, int, error) {
		return []byte("PRIVATE"), 1, errors.New("HTTP 503 PRIVATE")
	})
	assertOutcome(t, checker.Check(context.Background(), prClaim()), verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
}

func TestRepoWithQueryOrTraversalIsRefused(t *testing.T) {
	calls := 0
	checker := New(func(ctx context.Context, args ...string) ([]byte, int, error) {
		calls++
		return []byte(`{"merged":true,"base":{"ref":"main"}}`), 0, nil
	})
	for _, repo := range []string{"o/r?x=1", "o/r/../x", "o/r/pulls", "o r/x", "o/r#frag", "../o/r"} {
		got := checker.Check(context.Background(), verify.Claim{Type: "pr_merged", Repo: repo, PR: 1})
		if got.Status == verify.StatusVerified {
			t.Fatalf("repo %q must never verify", repo)
		}
	}
	if calls != 0 {
		t.Fatalf("gh was called %d times for invalid repos", calls)
	}
}

func TestActionRequiredIsPendingNotFailing(t *testing.T) {
	checker := New(func(ctx context.Context, args ...string) ([]byte, int, error) {
		if strings.Contains(args[len(args)-1], "check-runs") || strings.Contains(strings.Join(args, " "), "check-runs") {
			return []byte(`{"check_runs":[{"name":"ci","status":"completed","conclusion":"action_required"}]}`), 0, nil
		}
		return []byte(`{"state":"pending","statuses":[]}`), 0, nil
	})
	got := checker.Check(context.Background(), verify.Claim{Type: "checks_passed", Repo: "o/r", SHA: strings.Repeat("a", 40)})
	if got.Status != verify.StatusIndeterminate || got.Reason != verify.ReasonChecksPending {
		t.Fatalf("got %s %s", got.Status, got.Reason)
	}
}

func TestNotFoundCarriesCallEvidence(t *testing.T) {
	checker := New(func(ctx context.Context, args ...string) ([]byte, int, error) {
		return nil, 1, &exec.ExitError{Stderr: []byte("gh: Not Found (HTTP 404)")}
	})
	got := checker.Check(context.Background(), verify.Claim{Type: "pr_merged", Repo: "o/r", PR: 999})
	if got.Status != verify.StatusContradicted || len(got.Evidence) == 0 || got.Evidence[0].Call == "" {
		t.Fatalf("got %s with evidence %+v", got.Status, got.Evidence)
	}
}
