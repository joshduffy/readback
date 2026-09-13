package verify

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var credentialQueryNames = map[string]bool{
	"accesstoken":        true,
	"apikey":             true,
	"auth":               true,
	"authorization":      true,
	"clientsecret":       true,
	"credential":         true,
	"googleaccessid":     true,
	"idtoken":            true,
	"jwt":                true,
	"key":                true,
	"passwd":             true,
	"password":           true,
	"refreshtoken":       true,
	"secret":             true,
	"securitytoken":      true,
	"sessiontoken":       true,
	"sig":                true,
	"signature":          true,
	"token":              true,
	"xamzcredential":     true,
	"xamzsecuritytoken":  true,
	"xamzsignature":      true,
	"xgoogcredential":    true,
	"xgoogsecuritytoken": true,
	"xgoogsignature":     true,
}

var (
	urlInText        = regexp.MustCompile(`(?:Location header|parse) "(?:\\.|[^"\\])*"|https?://[^\s"<>]+`)
	pseudonymHMACKey = randomPseudonymKey()
)

func URLContainsCredentials(raw string) (bool, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false, err
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false, err
	}
	if parsed.User != nil {
		return true, nil
	}
	for name := range query {
		if credentialQueryNames[normalizeCredentialName(name)] {
			return true, nil
		}
	}
	return false, nil
}

func URLAllowed(assertions *Assertions, raw string) (bool, error) {
	hasCredentials, err := URLContainsCredentials(raw)
	if err != nil {
		return false, err
	}
	if hasCredentials {
		return false, nil
	}
	return assertions == nil || assertions.HostAllowed(raw), nil
}

func SanitizeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return sanitizeMalformedURL(raw)
	}
	if parsed.User != nil {
		username := Pseudonymize("url-username", parsed.User.Username())
		if password, present := parsed.User.Password(); present {
			parsed.User = url.UserPassword(username, Pseudonymize("url-password", password))
		} else {
			parsed.User = url.User(username)
		}
	}
	parsed.RawQuery = sanitizeQuery(parsed.RawQuery)
	if parsed.Fragment != "" {
		parsed.Fragment = Pseudonymize("url-fragment", parsed.Fragment)
	}
	return parsed.String()
}

func SanitizeURLs(text string) string {
	return urlInText.ReplaceAllStringFunc(text, func(part string) string {
		if strings.HasPrefix(part, "http://") || strings.HasPrefix(part, "https://") {
			return SanitizeURL(part)
		}
		quote := strings.IndexByte(part, '"')
		raw, err := strconv.Unquote(part[quote:])
		if err != nil {
			return part[:quote] + strconv.Quote(Pseudonymize("url-error-value", part[quote:]))
		}
		return part[:quote] + strconv.Quote(SanitizeURL(raw))
	})
}

func sanitizeMalformedURL(raw string) string {
	withoutFragment, fragment, hasFragment := strings.Cut(raw, "#")
	path, rawQuery, hasQuery := strings.Cut(withoutFragment, "?")
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part != "" && part != "." && part != ".." {
			parts[i] = Pseudonymize("url-path-segment", part)
		}
	}
	clean := strings.Join(parts, "/")
	if hasQuery {
		clean += "?" + sanitizeQuery(rawQuery)
	}
	if hasFragment {
		clean += "#" + Pseudonymize("url-fragment", fragment)
	}
	return clean
}

func sanitizeQuery(raw string) string {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return "malformed=" + url.QueryEscape(Pseudonymize("url-query", raw))
	}
	for name, values := range query {
		if !credentialQueryNames[normalizeCredentialName(name)] {
			continue
		}
		for i, value := range values {
			values[i] = Pseudonymize("url-query-"+normalizeCredentialName(name), value)
		}
		query[name] = values
	}
	return query.Encode()
}

func Pseudonymize(kind, value string) string {
	digest := hmac.New(sha256.New, pseudonymHMACKey)
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(value))
	return "rb_" + hex.EncodeToString(digest.Sum(nil))
}

func normalizeCredentialName(name string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', '.':
			return -1
		default:
			return r
		}
	}, strings.ToLower(name))
}

func randomPseudonymKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Sprintf("readback: initialize URL redaction key: %v", err))
	}
	return key
}
