package verify

import (
	"context"
	"fmt"
	"time"
)

type Checker interface {
	Check(context.Context, Claim) Outcome
}

type Outcome struct {
	Status   string
	Reason   string
	Evidence []Evidence
}

type Registry map[string]Checker

type RunInput struct {
	Doc          Document
	Assertions   *Assertions
	Registry     Registry
	Now          func() time.Time
	ClaimTimeout time.Duration
}

func Run(ctx context.Context, input RunInput) RunResult {
	now := input.Now
	if now == nil {
		now = time.Now
	}
	claimTimeout := input.ClaimTimeout
	if claimTimeout == 0 {
		claimTimeout = 20 * time.Second
	}
	result := RunResult{
		Input:   InputInfo{Claims: len(input.Doc.Claims)},
		Summary: Summary{RequiredUnmet: []Requirement{}},
		Claims:  make([]ClaimResult, 0, len(input.Doc.Claims)),
	}
	for _, claim := range input.Doc.Claims {
		outcome := Outcome{Status: StatusIndeterminate, Reason: ReasonProviderUnreachable}
		switch {
		case input.Assertions != nil && !claimHostsAllowed(*input.Assertions, claim):
			outcome.Status, outcome.Reason = StatusContradicted, ReasonHostNotAllowed
		case !claim.Supported():
			outcome.Reason = ReasonUnsupportedClaimType
		case ctx.Err() == nil && input.Registry[claim.Type] != nil:
			claimCtx, cancel := context.WithTimeout(ctx, claimTimeout)
			completed := make(chan Outcome, 1)
			checker := input.Registry[claim.Type]
			go func() {
				var checked Outcome
				defer func() {
					if message := recover(); message != nil {
						checked = Outcome{Status: StatusIndeterminate, Reason: ReasonProviderUnreachable,
							Evidence: []Evidence{{Source: "runner", Call: "panic", Observed: map[string]any{"message": fmt.Sprint(message)}}}}
					}
					completed <- checked
				}()
				checked = checker.Check(claimCtx, claim)
			}()
			select {
			case outcome = <-completed:
			case <-claimCtx.Done():
				outcome = Outcome{Status: StatusIndeterminate, Reason: ReasonProviderUnreachable,
					Evidence: []Evidence{{Source: "runner", Call: "deadline", Observed: map[string]any{"timeout_ms": claimTimeout.Milliseconds()}}}}
			}
			cancel()
		}
		switch outcome.Status {
		case StatusVerified:
			outcome.Reason = ""
			result.Summary.Verified++
		case StatusContradicted:
			result.Summary.Contradicted++
		default:
			if outcome.Status != StatusIndeterminate {
				outcome.Reason = ReasonProviderUnreachable
				outcome.Evidence = nil
			}
			outcome.Status = StatusIndeterminate
			result.Summary.Indeterminate++
		}
		if outcome.Status != StatusVerified && !KnownReason(outcome.Reason) {
			outcome.Evidence = append(outcome.Evidence, Evidence{Source: "runner", Call: "reason_rejected", Observed: map[string]any{"reason": outcome.Reason}})
			outcome.Reason = ReasonProviderUnreachable
		}
		result.Claims = append(result.Claims, ClaimResult{
			Claim: claim, Condition: checkedCondition(claim), ID: claim.ID, Type: claim.Type, Status: outcome.Status,
			CheckedAt: now().UTC(), Evidence: outcome.Evidence, Reason: outcome.Reason,
		})
	}
	if input.Assertions != nil {
		result.Summary.RequiredUnmet = append(result.Summary.RequiredUnmet, input.Assertions.Unmet(result.Claims)...)
	}
	return result
}

func claimHostsAllowed(a Assertions, c Claim) bool {
	for _, address := range []string{c.URL, c.Health} {
		if address != "" && !a.HostAllowed(address) {
			return false
		}
	}
	for _, smoke := range c.Smoke {
		if !claimHostsAllowed(a, smoke) {
			return false
		}
	}
	return true
}

type stubChecker struct{}

func (stubChecker) Check(context.Context, Claim) Outcome {
	return Outcome{Status: StatusIndeterminate, Reason: ReasonProviderUnreachable, Evidence: []Evidence{{Source: "stub"}}}
}

func StubRegistry() Registry {
	registry := Registry{}
	for _, kind := range []string{"pr_merged", "checks_passed", "commit_on_branch", "file_exists", "url_serving", "deployment_serving"} {
		registry[kind] = stubChecker{}
	}
	return registry
}
