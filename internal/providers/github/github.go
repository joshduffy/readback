// Package github reads claim evidence using gh's authentication.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/joshduffy/readback/internal/verify"
)

type Runner func(ctx context.Context, args ...string) (stdout []byte, exitCode int, err error)

type Checker struct{ run Runner }

var _ verify.Checker = (*Checker)(nil)

func New(run Runner) *Checker {
	if run == nil {
		run = runGH
	}
	return &Checker{run: run}
}

func runGH(ctx context.Context, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	output, err := cmd.Output()
	if err == nil {
		return output, 0, nil
	}
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return output, exited.ExitCode(), fmt.Errorf("%w: %s", err, exited.Stderr)
	}
	return output, -1, err
}

func outcome(status, reason string, evidence ...verify.Evidence) verify.Outcome {
	return verify.Outcome{Status: status, Reason: reason, Evidence: evidence}
}

func (checker *Checker) api(ctx context.Context, endpoint, missing string, paginate bool) ([]byte, *verify.Outcome) {
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"api", endpoint}
	if paginate {
		args = append(args, "--paginate")
	}
	data, code, err := checker.run(callCtx, args...)
	if err == nil && code == 0 && callCtx.Err() == nil {
		return data, nil
	}
	status, reason := verify.StatusIndeterminate, verify.ReasonProviderUnreachable
	message := ""
	if err != nil {
		message = strings.ToLower(err.Error())
	}
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		message += strings.ToLower(string(exited.Stderr))
	}
	if callCtx.Err() == nil {
		switch {
		case code == 4 || errors.Is(err, exec.ErrNotFound) || strings.Contains(message, "auth"):
			reason = verify.ReasonAuthMissing
		case code == 1 && strings.Contains(message, "http 404"):
			status, reason = verify.StatusContradicted, missing
		case code == 1 && strings.Contains(message, "http 422"):
			status, reason = verify.StatusContradicted, missing
		}
	}
	failed := outcome(status, reason, verify.Evidence{Source: "github", Call: "GET " + endpoint, Observed: map[string]any{"exit_code": code, "http_404": strings.Contains(message, "http 404"), "http_422": strings.Contains(message, "http 422")}})
	return nil, &failed
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func (checker *Checker) Check(ctx context.Context, claim verify.Claim) verify.Outcome {
	// The repo string becomes part of the gh api path. Anything outside owner/name
	// characters (query strings, extra segments, traversal) is refused before any call.
	if !repoPattern.MatchString(claim.Repo) || strings.Contains(claim.Repo, "..") {
		return outcome(verify.StatusContradicted, verify.ReasonProviderUnreachable, verify.Evidence{Source: "github", Call: "validate repo", Observed: map[string]any{"repo_rejected": true}})
	}
	owner, name, _ := strings.Cut(claim.Repo, "/")
	root := "repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	switch claim.Type {
	case "pr_merged":
		endpoint := fmt.Sprintf("%s/pulls/%d", root, claim.PR)
		data, failed := checker.api(ctx, endpoint, verify.ReasonNotMerged, false)
		if failed != nil {
			return *failed
		}
		var pr struct {
			Merged         *bool   `json:"merged"`
			MergedAt       *string `json:"merged_at"`
			MergeCommitSHA string  `json:"merge_commit_sha"`
			Base           struct {
				Ref string `json:"ref"`
			} `json:"base"`
			State string `json:"state"`
		}
		if json.Unmarshal(data, &pr) != nil || pr.Merged == nil {
			return outcome(verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
		}
		evidence := verify.Evidence{Source: "github", Call: "GET " + endpoint, Observed: map[string]any{
			"merged": *pr.Merged, "merged_at": pr.MergedAt, "merge_commit_sha": pr.MergeCommitSHA, "base_ref": pr.Base.Ref, "state": pr.State,
		}}
		switch {
		case !*pr.Merged:
			return outcome(verify.StatusContradicted, verify.ReasonNotMerged, evidence)
		case claim.Into != "" && claim.Into != pr.Base.Ref:
			return outcome(verify.StatusContradicted, verify.ReasonMergedIntoOtherBranch, evidence)
		case claim.SHA != "" && claim.SHA != pr.MergeCommitSHA:
			return outcome(verify.StatusContradicted, verify.ReasonNotMerged, evidence)
		default:
			return outcome(verify.StatusVerified, "", evidence)
		}
	case "commit_on_branch":
		endpoint := root + "/compare/" + url.PathEscape(claim.Branch) + "..." + url.PathEscape(claim.SHA)
		data, failed := checker.api(ctx, endpoint, verify.ReasonSHANotOnBranch, false)
		if failed != nil {
			return *failed
		}
		var comparison struct {
			Status   string `json:"status"`
			AheadBy  int    `json:"ahead_by"`
			BehindBy int    `json:"behind_by"`
		}
		if json.Unmarshal(data, &comparison) != nil {
			return outcome(verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
		}
		evidence := verify.Evidence{Source: "github", Call: "GET " + endpoint, Observed: map[string]any{
			"status": comparison.Status, "ahead_by": comparison.AheadBy, "behind_by": comparison.BehindBy,
		}}
		switch comparison.Status {
		case "identical", "behind":
			return outcome(verify.StatusVerified, "", evidence)
		case "ahead", "diverged":
			return outcome(verify.StatusContradicted, verify.ReasonSHANotOnBranch, evidence)
		default:
			return outcome(verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
		}
	case "checks_passed":
		return checker.checks(ctx, claim, root+"/commits/"+url.PathEscape(claim.SHA))
	default:
		return outcome(verify.StatusIndeterminate, verify.ReasonUnsupportedClaimType)
	}
}

type checkRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

func (checker *Checker) checks(ctx context.Context, claim verify.Claim, endpoint string) verify.Outcome {
	data, failed := checker.api(ctx, endpoint+"/check-runs", verify.ReasonCheckMissing, true)
	if failed != nil {
		return *failed
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var runs []checkRun
	pages := 0
	for {
		var page struct {
			CheckRuns *[]checkRun `json:"check_runs"`
		}
		err := decoder.Decode(&page)
		if err == io.EOF {
			break
		}
		if err != nil || page.CheckRuns == nil {
			return outcome(verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
		}
		runs = append(runs, (*page.CheckRuns)...)
		pages++
	}
	if pages == 0 {
		return outcome(verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
	}
	data, failed = checker.api(ctx, endpoint+"/status", verify.ReasonCheckMissing, false)
	if failed != nil {
		return *failed
	}
	var combined struct {
		State    string `json:"state"`
		Statuses *[]struct {
			Context string `json:"context"`
		} `json:"statuses"`
	}
	if json.Unmarshal(data, &combined) != nil || combined.Statuses == nil {
		return outcome(verify.StatusIndeterminate, verify.ReasonProviderUnreachable)
	}
	names := make(map[string]bool)
	counts := make(map[string]int)
	pending, failing, unknown := false, false, false
	for _, run := range runs {
		names[run.Name] = true
		counts[run.Conclusion]++
		switch run.Status {
		case "queued", "in_progress":
			pending = true
		case "completed":
			switch run.Conclusion {
			case "success", "neutral", "skipped":
			case "action_required", "stale":
				pending = true
			case "":
				unknown = true
			default:
				failing = true
			}
		default:
			unknown = true
		}
	}
	for _, status := range *combined.Statuses {
		names[status.Context] = true
	}
	missing := []string{}
	for _, name := range claim.Require {
		if !names[name] {
			missing = append(missing, name)
		}
	}
	evidence := []verify.Evidence{
		{Source: "github", Call: "GET " + endpoint + "/check-runs", Observed: map[string]any{"counts_per_conclusion": counts, "check_run_count": len(runs)}},
		{Source: "github", Call: "GET " + endpoint + "/status", Observed: map[string]any{"combined_state": combined.State, "status_count": len(*combined.Statuses), "missing_names": missing}},
	}
	switch {
	case len(runs) == 0 && len(*combined.Statuses) == 0:
		return outcome(verify.StatusIndeterminate, verify.ReasonCheckMissing, evidence...)
	case len(missing) > 0:
		return outcome(verify.StatusContradicted, verify.ReasonCheckMissing, evidence...)
	case failing || combined.State == "failure" || combined.State == "error":
		return outcome(verify.StatusContradicted, verify.ReasonChecksFailing, evidence...)
	case pending || combined.State == "pending":
		return outcome(verify.StatusIndeterminate, verify.ReasonChecksPending, evidence...)
	case unknown || combined.State != "success":
		return outcome(verify.StatusIndeterminate, verify.ReasonProviderUnreachable, evidence...)
	default:
		return outcome(verify.StatusVerified, "", evidence...)
	}
}
