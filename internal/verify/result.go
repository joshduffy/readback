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
		ReasonProviderUnreachable, ReasonHostNotAllowed, ReasonNoClaimsBlock, ReasonRequiredAssertionUnmet:
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
	Claim     Claim      `json:"-"`
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Status    string     `json:"status"`
	CheckedAt time.Time  `json:"checked_at"`
	Evidence  []Evidence `json:"evidence"`
	Reason    string     `json:"reason"`
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
