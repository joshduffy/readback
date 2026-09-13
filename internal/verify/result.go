package verify

import (
	"encoding/json"
	"time"
)

const (
	StatusVerified               = "verified"
	StatusContradicted           = "contradicted"
	StatusIndeterminate          = "indeterminate"
	ReasonUnsupportedClaimType   = "unsupported_claim_type"
	ReasonNotMerged              = "not_merged"
	ReasonMergedIntoOtherBranch  = "merged_into_other_branch"
	ReasonChecksFailing          = "checks_failing"
	ReasonChecksPending          = "checks_pending"
	ReasonCheckMissing           = "check_missing"
	ReasonSHANotOnBranch         = "sha_not_on_branch"
	ReasonFileMissing            = "file_missing"
	ReasonContentMissing         = "content_missing"
	ReasonStatusMismatch         = "status_mismatch"
	ReasonMarkerMissing          = "marker_missing"
	ReasonHeaderMismatch         = "header_mismatch"
	ReasonVersionSHAMismatch     = "version_sha_mismatch"
	ReasonVersionSHAUnavailable  = "version_sha_unavailable"
	ReasonHealthSHAMismatch      = "health_sha_mismatch"
	ReasonDeploymentNotFound     = "deployment_not_found"
	ReasonAuthMissing            = "auth_missing"
	ReasonProviderUnreachable    = "provider_unreachable"
	ReasonResponseTruncated      = "response_truncated"
	ReasonHostNotAllowed         = "host_not_allowed"
	ReasonNoClaimsBlock          = "no_claims_block"
	ReasonRequiredAssertionUnmet = "required_assertion_unmet"
)

func KnownReason(reason string) bool {
	switch reason {
	case ReasonUnsupportedClaimType, ReasonNotMerged, ReasonMergedIntoOtherBranch,
		ReasonChecksFailing, ReasonChecksPending, ReasonCheckMissing, ReasonSHANotOnBranch,
		ReasonFileMissing, ReasonContentMissing, ReasonStatusMismatch, ReasonMarkerMissing,
		ReasonHeaderMismatch, ReasonVersionSHAMismatch, ReasonVersionSHAUnavailable,
		ReasonHealthSHAMismatch, ReasonDeploymentNotFound, ReasonAuthMissing,
		ReasonProviderUnreachable, ReasonResponseTruncated, ReasonHostNotAllowed,
		ReasonNoClaimsBlock, ReasonRequiredAssertionUnmet:
		return true
	default:
		return false
	}
}

type Evidence struct {
	Source   string         `json:"source"`
	Call     string         `json:"call"`
	Observed map[string]any `json:"observed"`
}

type ClaimResult struct {
	Claim     Claim            `json:"-"`
	Condition CheckedCondition `json:"claim"`
	ID        string           `json:"id"`
	Type      string           `json:"type"`
	Status    string           `json:"status"`
	CheckedAt time.Time        `json:"checked_at"`
	Evidence  []Evidence       `json:"evidence"`
	Reason    string           `json:"reason"`
}

type CheckedCondition struct {
	Type         string             `json:"type"`
	Repo         string             `json:"repo,omitempty"`
	PR           int                `json:"pr,omitempty"`
	Into         string             `json:"into,omitempty"`
	SHA          string             `json:"sha,omitempty"`
	Require      []string           `json:"require,omitempty"`
	Branch       string             `json:"branch,omitempty"`
	Path         string             `json:"path,omitempty"`
	Contains     string             `json:"contains,omitempty"`
	URL          string             `json:"url,omitempty"`
	ExpectStatus int                `json:"expect_status,omitempty"`
	Marker       string             `json:"marker,omitempty"`
	Header       map[string]string  `json:"header,omitempty"`
	Provider     string             `json:"provider,omitempty"`
	Account      string             `json:"account,omitempty"`
	Worker       string             `json:"worker,omitempty"`
	Health       string             `json:"health,omitempty"`
	Smoke        []CheckedCondition `json:"smoke,omitempty"`
}

func checkedCondition(claim Claim) CheckedCondition {
	condition := CheckedCondition{Type: claim.Type}
	switch claim.Type {
	case "pr_merged":
		condition.Repo, condition.PR, condition.Into, condition.SHA = claim.Repo, claim.PR, claim.Into, claim.SHA
	case "checks_passed":
		condition.Repo, condition.SHA, condition.Require = claim.Repo, claim.SHA, claim.Require
	case "commit_on_branch":
		condition.Repo, condition.SHA, condition.Branch = claim.Repo, claim.SHA, claim.Branch
	case "file_exists":
		condition.Path, condition.Contains = claim.Path, claim.Contains
	case "url_serving":
		condition.URL, condition.ExpectStatus, condition.Marker = SanitizeURL(claim.URL), claim.ExpectStatus, claim.Marker
		if condition.ExpectStatus == 0 {
			condition.ExpectStatus = 200
		}
		if len(claim.Header) > 0 {
			condition.Header = make(map[string]string, len(claim.Header))
			for name, value := range claim.Header {
				if sensitiveHeaderName(name) {
					value = Pseudonymize("header-value", value)
				}
				condition.Header[name] = value
			}
		}
	case "deployment_serving":
		condition.SHA, condition.URL, condition.Marker = claim.SHA, SanitizeURL(claim.URL), claim.Marker
		condition.Provider, condition.Account, condition.Worker = claim.Provider, claim.Account, claim.Worker
		if claim.Health != "" {
			condition.Health = SanitizeURL(claim.Health)
		}
		for _, smoke := range claim.Smoke {
			condition.Smoke = append(condition.Smoke, checkedCondition(smoke))
		}
	}
	return condition
}

func sensitiveHeaderName(name string) bool {
	switch normalizeCredentialName(name) {
	case "authorization", "bearer", "cookie", "privatetoken", "proxyauthorization",
		"setcookie", "token", "xapikey", "xauthtoken", "xcsrftoken",
		"secret", "password", "credential", "signature":
		return true
	default:
		return false
	}
}

func (r ClaimResult) MarshalJSON() ([]byte, error) {
	type record ClaimResult
	var reason *string
	if r.Reason != "" {
		reason = &r.Reason
	}
	if r.Evidence == nil {
		r.Evidence = []Evidence{}
	}
	return json.Marshal(struct {
		record
		Reason *string `json:"reason"`
	}{record(r), reason})
}

type InputInfo struct {
	Path       string `json:"path"`
	Claims     int    `json:"claims"`
	Assertions string `json:"assertions"`
}

type Summary struct {
	Verified      int           `json:"verified"`
	Contradicted  int           `json:"contradicted"`
	Indeterminate int           `json:"indeterminate"`
	RequiredUnmet []Requirement `json:"required_unmet"`
}

type RunResult struct {
	Input   InputInfo     `json:"input"`
	Summary Summary       `json:"summary"`
	Claims  []ClaimResult `json:"claims"`
}

func (r RunResult) ExitCode() int {
	if r.Summary.Contradicted > 0 || len(r.Summary.RequiredUnmet) > 0 {
		return 1
	}
	if r.Summary.Indeterminate > 0 || len(r.Claims) == 0 {
		return 2
	}
	return 0
}
