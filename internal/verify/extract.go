package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrNoClaimsBlock = errors.New("no_claims_block")

// ExtractClaims reads JSON documents or explicitly marked top-level Markdown fences.
func ExtractClaims(src []byte) (Document, error) {
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(src, &fields); err == nil {
		_, hasVersion := fields["version"]
		_, hasClaims := fields["claims"]
		if hasVersion || hasClaims {
			return ParseDocument(src)
		}
	}

	doc := Document{Version: 1, Claims: []Claim{}}
	var fence byte
	var length, start, blocks int
	var claimsBlock bool
	var comment bool
	lines := strings.Split(string(src), "\n")
	for i, line := range lines {
		if fence == 0 {
			trimmed := strings.TrimLeft(line, " ")
			if comment || (len(line)-len(trimmed) <= 3 && strings.HasPrefix(trimmed, "<!--")) {
				comment = !strings.Contains(line, "-->")
				continue
			}
		}
		char, count, info := claimsFence(line)
		if fence == 0 {
			if count < 3 || (char == '`' && strings.ContainsRune(info, '`')) {
				continue
			}
			fence, length, start = char, count, i+1
			claimsBlock = strings.TrimRight(info, " \t\r") == "readback-claims"
			continue
		}
		if char != fence || count < length || strings.TrimSpace(info) != "" {
			continue
		}
		if claimsBlock {
			block, err := ParseDocument([]byte(strings.Join(lines[start:i], "\n")))
			if err != nil {
				var validation *ValidationError
				if errors.As(err, &validation) {
					for j := range validation.Problems {
						if validation.Problems[j].Index >= 0 {
							validation.Problems[j].Index += len(doc.Claims)
						}
					}
				}
				return doc, err
			}
			doc.Claims = append(doc.Claims, block.Claims...)
			blocks++
		}
		fence = 0
	}
	if fence != 0 && claimsBlock {
		return doc, &ValidationError{Problems: []Problem{{Index: -1, Field: "document", Message: "unclosed readback-claims fence"}}}
	}
	if blocks == 0 {
		return Document{}, fmt.Errorf("%w: use a fenced block with exactly this grammar:\n```readback-claims\n{\"version\":1,\"claims\":[...]}\n```", ErrNoClaimsBlock)
	}
	return doc, Validate(doc)
}

func claimsFence(line string) (byte, int, string) {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	if indent > 3 || indent == len(line) {
		return 0, 0, ""
	}
	line = line[indent:]
	char := line[0]
	if char != '`' && char != '~' {
		return 0, 0, ""
	}
	count := 0
	for count < len(line) && line[count] == char {
		count++
	}
	return char, count, line[count:]
}
