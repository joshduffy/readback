package verify

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

type Assertions struct {
	Version    int
	Require    []Requirement
	AllowHosts []string
}

type Requirement map[string]string

func LoadAssertions(path string) (Assertions, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Assertions{}, err
	}
	a, err := ParseAssertions(data)
	if err != nil {
		return Assertions{}, fmt.Errorf("%s: %w", path, err)
	}
	return a, nil
}

func FindAssertions(cwd string) (path string, ok bool) {
	path = filepath.Join(cwd, "readback.assertions.yaml")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

func ParseAssertions(data []byte) (Assertions, error) {
	var a Assertions
	seen := make(map[string]bool)
	section := ""
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, " \r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fail := func(message string) (Assertions, error) {
			return Assertions{}, fmt.Errorf("assertions line %d: %s", i+1, message)
		}
		if strings.ContainsAny(line, "\t\r") {
			return fail("tabs and embedded carriage returns are not supported")
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 {
			key, value, found := strings.Cut(line, ":")
			if !found || (value != "" && value[0] != ' ') {
				return fail("expected a top-level key: value")
			}
			if key != "version" && key != "require" && key != "allow_hosts" {
				return fail("unknown top-level key " + strconv.Quote(key))
			}
			if seen[key] {
				return fail("duplicate top-level key " + key)
			}
			seen[key] = true
			value = strings.TrimSpace(value)
			if key == "version" {
				v, err := assertionScalar(value)
				if err != nil || v != "1" {
					return fail("version must be 1")
				}
				a.Version = 1
				section = ""
			} else {
				if value != "" && !strings.HasPrefix(value, "#") {
					return fail(key + " must be an indented list")
				}
				section = key
			}
			continue
		}
		if section == "allow_hosts" {
			if indent != 2 || !strings.HasPrefix(trimmed, "- ") {
				return fail("allow_hosts entries must start with two spaces and '- '")
			}
			host, err := assertionScalar(trimmed[2:])
			if err != nil || host == "" {
				return fail("allow_hosts requires a non-empty scalar")
			}
			if strings.ContainsAny(host, ":/") {
				return fail(fmt.Sprintf("allow_hosts entry %q: hosts are bare hostnames", host))
			}
			if host == "*" || host == "*." {
				return fail(fmt.Sprintf("allow_hosts entry %q: a wildcard needs at least one label after '*.'", host))
			}
			a.AllowHosts = append(a.AllowHosts, host)
			continue
		}
		if section != "require" {
			return fail("unexpected indentation outside a list")
		}
		if indent == 2 && trimmed == "-" {
			return fail("require entries must have at least one key")
		}
		if indent == 2 && strings.HasPrefix(trimmed, "- ") {
			a.Require = append(a.Require, Requirement{})
			trimmed = trimmed[2:]
		} else if indent != 4 || len(a.Require) == 0 || strings.HasPrefix(trimmed, "-") {
			return fail("require entries need two-space list indentation and four-space field indentation")
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found || key == "" || strings.ContainsFunc(key, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r == '_')
		}) || value == "" || value[0] != ' ' {
			return fail("expected a scalar field as key: value")
		}
		v, err := assertionScalar(value)
		if err != nil {
			return fail(err.Error())
		}
		requirement := a.Require[len(a.Require)-1]
		if _, exists := requirement[key]; exists {
			return fail("duplicate requirement field " + key)
		}
		requirement[key] = v
	}
	if !seen["version"] {
		return Assertions{}, fmt.Errorf("assertions: version must be 1")
	}
	return a, nil
}

func assertionScalar(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	invalid := func() (string, error) {
		return "", fmt.Errorf("expected a string or integer scalar; unsupported syntax %q", raw)
	}
	if s == "" || s[0] == '#' {
		return invalid()
	}
	if s[0] == '\'' || s[0] == '"' {
		quote := s[0]
		for i := 1; i < len(s); i++ {
			if quote == '"' && s[i] == '\\' {
				i++
				continue
			}
			if s[i] != quote {
				continue
			}
			if quote == '\'' && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			tail := s[i+1:]
			if tail != "" && (!strings.HasPrefix(tail, " ") || !strings.HasPrefix(strings.TrimSpace(tail), "#")) {
				return invalid()
			}
			if quote == '\'' {
				return strings.ReplaceAll(s[1:i], "''", "'"), nil
			}
			value, err := strconv.Unquote(s[:i+1])
			if err != nil {
				return invalid()
			}
			return value, nil
		}
		return invalid()
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if s == "" || strings.ContainsAny(s, "[]{}") || strings.Contains(s, ": ") || strings.HasSuffix(s, ":") || strings.ContainsAny(s[:1], "&!>|%@`") || (strings.HasPrefix(s, "*") && !strings.HasPrefix(s, "*.")) || s == "-" || strings.HasPrefix(s, "- ") || strings.ContainsFunc(s, unicode.IsControl) {
		return invalid()
	}
	return s, nil
}

func (a Assertions) Unmet(results []ClaimResult) []Requirement {
	var unmet []Requirement
	for _, requirement := range a.Require {
		met := false
		for _, result := range results {
			if result.Status != "verified" {
				continue
			}
			fields := reflect.ValueOf(result.Claim)
			matches := len(requirement) > 0
			for key, expected := range requirement {
				found := false
				for i := 0; i < fields.NumField(); i++ {
					name := strings.Split(fields.Type().Field(i).Tag.Get("json"), ",")[0]
					if name != key || name == "" || name == "-" {
						continue
					}
					field := fields.Field(i)
					switch field.Kind() {
					case reflect.String:
						found = field.String() == expected
					case reflect.Int:
						found = strconv.FormatInt(field.Int(), 10) == expected
					}
					break
				}
				if !found {
					matches = false
					break
				}
			}
			if matches {
				met = true
				break
			}
		}
		if !met {
			unmet = append(unmet, requirement)
		}
	}
	return unmet
}

func (a Assertions) HostAllowed(rawURL string) bool {
	if len(a.AllowHosts) == 0 {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	for _, allowed := range a.AllowHosts {
		allowed = strings.ToLower(allowed)
		if strings.HasPrefix(allowed, "*.") {
			suffix := allowed[1:]
			if strings.HasSuffix(host, suffix) {
				prefix := strings.TrimSuffix(host, suffix)
				if prefix != "" && !strings.HasPrefix(prefix, ".") && !strings.HasSuffix(prefix, ".") && !strings.Contains(prefix, "..") {
					return true
				}
			}
		} else if host == allowed {
			return true
		}
	}
	return false
}
