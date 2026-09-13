package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type Document struct {
	Version int     `json:"version"`
	Claims  []Claim `json:"claims"`
}

type Claim struct {
	Type           string            `json:"type"`
	ID             string            `json:"id,omitempty"`
	Repo           string            `json:"repo,omitempty"`
	PR             int               `json:"pr,omitempty"`
	Into           string            `json:"into,omitempty"`
	SHA            string            `json:"sha,omitempty"`
	Require        []string          `json:"require,omitempty"`
	Branch         string            `json:"branch,omitempty"`
	Path           string            `json:"path,omitempty"`
	Contains       string            `json:"contains,omitempty"`
	URL            string            `json:"url,omitempty"`
	ExpectStatus   int               `json:"expect_status,omitempty"`
	Marker         string            `json:"marker,omitempty"`
	Header         map[string]string `json:"header,omitempty"`
	Provider       string            `json:"provider,omitempty"`
	Account        string            `json:"account,omitempty"`
	Worker         string            `json:"worker,omitempty"`
	Health         string            `json:"health,omitempty"`
	Smoke          []Claim           `json:"smoke,omitempty"`
	Raw            map[string]any    `json:"-"`
	decodeProblems []Problem
}

// Index is zero-based; -1 identifies a document-level problem. Nested fields
// use paths such as smoke[0].url under the enclosing claim's index.
type Problem struct {
	Index   int
	Field   string
	Message string
}

type ValidationError struct{ Problems []Problem }

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = fmt.Sprintf("claim[%d].%s: %s", p.Index, p.Field, p.Message)
	}
	return "invalid claims document: " + strings.Join(parts, "; ")
}

func (c Claim) Supported() bool {
	switch c.Type {
	case "pr_merged", "checks_passed", "commit_on_branch", "file_exists", "url_serving", "deployment_serving":
		return true
	}
	return false
}

var forbiddenFields = []string{"command", "cmd", "exec", "shell", "token", "secret", "credential", "env", "args"}
var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// UnmarshalJSON retains source fields so presence, forbidden names, and type
// errors survive decoding instead of being silently lost by encoding/json.
func (c *Claim) UnmarshalJSON(data []byte) error {
	return c.unmarshalJSON(data, true)
}

func (c *Claim) unmarshalJSON(data []byte, allowSmoke bool) error {
	*c = Claim{ExpectStatus: 200}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		c.decodeProblems = append(c.decodeProblems, Problem{Field: "type", Message: "claim must be an object"})
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&c.Raw); err != nil {
		return err
	}
	targets := map[string]any{
		"type": &c.Type, "id": &c.ID, "repo": &c.Repo, "pr": &c.PR,
		"into": &c.Into, "sha": &c.SHA, "require": &c.Require,
		"branch": &c.Branch, "path": &c.Path, "contains": &c.Contains,
		"url": &c.URL, "expect_status": &c.ExpectStatus, "marker": &c.Marker,
		"header": &c.Header, "provider": &c.Provider, "account": &c.Account,
		"worker": &c.Worker, "health": &c.Health, "smoke": &c.Smoke,
	}
	keys := make([]string, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		raw, present := fields[key]
		if !present {
			continue
		}
		if key == "smoke" {
			if !allowSmoke {
				c.decodeProblems = append(c.decodeProblems, Problem{Field: key, Message: "nested smoke not allowed"})
				continue
			}
			var items []json.RawMessage
			if err := json.Unmarshal(raw, &items); err != nil || items == nil {
				c.decodeProblems = append(c.decodeProblems, Problem{Field: key, Message: "invalid field type"})
				continue
			}
			c.Smoke = make([]Claim, len(items))
			for i, item := range items {
				if err := c.Smoke[i].unmarshalJSON(item, false); err != nil {
					return err
				}
			}
			continue
		}
		err := json.Unmarshal(raw, targets[key])
		invalid := bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
		// encoding/json otherwise accepts null as a string array/map element.
		switch value := c.Raw[key].(type) {
		case []any:
			if key == "require" {
				for _, item := range value {
					if item == nil {
						invalid = true
					}
				}
			}
		case map[string]any:
			if key == "header" {
				for _, item := range value {
					if item == nil {
						invalid = true
					}
				}
			}
		}
		if err != nil || invalid {
			c.decodeProblems = append(c.decodeProblems, Problem{Field: key, Message: "invalid field type"})
		}
	}
	return nil
}

func ParseDocument(data []byte) (Document, error) {
	var doc Document
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return doc, &ValidationError{Problems: []Problem{{Index: -1, Field: "document", Message: err.Error()}}}
	}
	if fields == nil {
		return doc, &ValidationError{Problems: []Problem{{Index: -1, Field: "document", Message: "must be an object"}}}
	}
	var problems []Problem
	for _, field := range []struct {
		name   string
		target any
	}{{"version", &doc.Version}, {"claims", &doc.Claims}} {
		raw, present := fields[field.name]
		if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			problems = append(problems, Problem{-1, field.name, "required field is missing or null"})
		} else if err := json.Unmarshal(raw, field.target); err != nil {
			problems = append(problems, Problem{-1, field.name, "invalid field type"})
		}
	}
	if err := Validate(doc); err != nil {
		for _, p := range err.(*ValidationError).Problems {
			duplicate := false
			for _, existing := range problems {
				if existing.Index == p.Index && existing.Field == p.Field {
					duplicate = true
				}
			}
			if !duplicate {
				problems = append(problems, p)
			}
		}
	}
	if len(problems) > 0 {
		return doc, &ValidationError{Problems: problems}
	}
	return doc, nil
}

func Validate(doc Document) error {
	var problems []Problem
	if doc.Version != ClaimsSchemaVersion {
		problems = append(problems, Problem{-1, "version", "must be 1"})
	}
	if doc.Claims == nil {
		problems = append(problems, Problem{-1, "claims", "must be an array"})
	}
	for i, claim := range doc.Claims {
		validateClaim(claim, i, "", &problems)
	}
	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

func validateClaim(c Claim, index int, prefix string, problems *[]Problem) {
	invalid := make(map[string]bool)
	add := func(field, message string) {
		if !invalid[field] {
			*problems = append(*problems, Problem{index, prefix + field, message})
			invalid[field] = true
		}
	}
	if _, present := c.Raw["smoke"]; prefix != "" && (present || c.Smoke != nil) {
		add("smoke", "nested smoke not allowed")
	}
	for _, p := range c.decodeProblems {
		add(p.Field, p.Message)
	}
	fields := make([]string, 0, len(c.Raw))
	for field := range c.Raw {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		for _, forbidden := range forbiddenFields {
			if strings.EqualFold(field, forbidden) {
				add(field, "forbidden authority field")
				break
			}
		}
	}
	required := map[string]bool{"type": true}
	switch c.Type {
	case "pr_merged":
		required["repo"], required["pr"] = true, true
	case "checks_passed":
		required["repo"], required["sha"] = true, true
	case "commit_on_branch":
		required["repo"], required["sha"], required["branch"] = true, true, true
	case "file_exists":
		required["path"] = true
	case "url_serving":
		required["url"] = true
	case "deployment_serving":
		required["provider"], required["sha"], required["url"] = true, true, true
	}
	values := []struct{ name, value string }{
		{"type", c.Type}, {"repo", c.Repo}, {"sha", c.SHA}, {"branch", c.Branch},
		{"path", c.Path}, {"url", c.URL}, {"health", c.Health}, {"provider", c.Provider},
	}
	for _, field := range values {
		_, present := c.Raw[field.name]
		if !required[field.name] && !present && field.value == "" {
			continue
		}
		if required[field.name] && field.value == "" {
			add(field.name, "required non-empty field")
			continue
		}
		switch field.name {
		case "branch":
			if field.value == "" {
				add(field.name, "must not be empty")
			}
		case "sha":
			if !shaPattern.MatchString(field.value) {
				add(field.name, "must be exactly 40 hex characters")
			}
		case "repo":
			parts := strings.Split(field.value, "/")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsFunc(field.value, unicode.IsSpace) {
				add(field.name, "must be owner/name with no whitespace")
			}
		case "url", "health":
			parsed, err := url.Parse(field.value)
			if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
				add(field.name, "must be an absolute https URL with a host")
			} else if hasCredentials, queryErr := URLContainsCredentials(field.value); queryErr != nil {
				add(field.name, "must contain valid query parameters")
			} else if hasCredentials {
				add(field.name, "must not contain credentials")
			}
		case "path":
			if field.value == "" {
				add(field.name, "must not be empty")
			}
			for _, part := range strings.FieldsFunc(field.value, func(r rune) bool { return r == '/' || r == '\\' }) {
				if part == ".." {
					add(field.name, "must not contain a .. segment")
				}
			}
		case "provider":
			if c.Type == "deployment_serving" && field.value != "cloudflare-workers" {
				add(field.name, "must be cloudflare-workers")
			}
		}
	}
	if _, present := c.Raw["pr"]; (required["pr"] || present || c.PR != 0) && c.PR <= 0 {
		add("pr", "must be a positive integer")
	}
	if prefix != "" {
		return
	}
	for i, smoke := range c.Smoke {
		nested := fmt.Sprintf("%ssmoke[%d].", prefix, i)
		validateClaim(smoke, index, nested, problems)
		if smoke.Type != "url_serving" && smoke.Type != "" {
			*problems = append(*problems, Problem{index, nested + "type", "must be url_serving"})
		}
	}
}
