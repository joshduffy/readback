package verify

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

const extractionDocument = `{"version":1,"claims":[{"type":"file_exists","path":"README.md"}]}`

func TestExtractStripsBOM(t *testing.T) {
	for _, src := range []string{
		extractionDocument,
		"```readback-claims\n" + extractionDocument + "\n```",
	} {
		doc, err := ExtractClaims([]byte("\xef\xbb\xbf" + src))
		if err != nil || doc.Version != 1 || len(doc.Claims) != 1 || doc.Claims[0].Path != "README.md" {
			t.Fatalf("input %q: got %#v, %v", src, doc, err)
		}
	}
}

func TestExtractIgnoresFenceInsideHTMLComment(t *testing.T) {
	for _, indent := range []string{"", " ", "  ", "   "} {
		src := indent + "<!--\n```readback-claims\n" + extractionDocument + "\n```\n-->"
		if _, err := ExtractClaims([]byte(src)); !errors.Is(err, ErrNoClaimsBlock) {
			t.Errorf("indent %q: got %v, want ErrNoClaimsBlock", indent, err)
		}
	}
}

func TestExtractFenceAfterClosedHTMLComment(t *testing.T) {
	src := "<!--\n```readback-claims\ninvalid JSON\n```\nclosed -->\n```readback-claims\n" + extractionDocument + "\n```"
	doc, err := ExtractClaims([]byte(src))
	if err != nil || len(doc.Claims) != 1 || doc.Claims[0].Path != "README.md" {
		t.Fatalf("got %#v, %v", doc, err)
	}
}

func TestExtractSameLineComment(t *testing.T) {
	src := "<!-- note -->\n```readback-claims\n" + extractionDocument + "\n```"
	doc, err := ExtractClaims([]byte(src))
	if err != nil || len(doc.Claims) != 1 || doc.Claims[0].Path != "README.md" {
		t.Fatalf("got %#v, %v", doc, err)
	}
}

func TestExtractSingleBlock(t *testing.T) {
	for _, fence := range []string{"```", "````", "   ```"} {
		t.Run(fence, func(t *testing.T) {
			doc, err := ExtractClaims([]byte("ignored prose\n" + fence + "readback-claims \t\r\n" + extractionDocument + "\n" + fence + "`\nmore prose"))
			if err != nil || doc.Version != 1 || len(doc.Claims) != 1 || doc.Claims[0].Path != "README.md" {
				t.Fatalf("got %#v, %v", doc, err)
			}
		})
	}
}

func TestExtractMultipleBlocksConcatenate(t *testing.T) {
	src := "```readback-claims\n" + extractionDocument + "\n```\nignored\n~~~readback-claims\n" + strings.ReplaceAll(extractionDocument, "README.md", "second.md") + "\n~~~"
	doc, err := ExtractClaims([]byte(src))
	if err != nil || len(doc.Claims) != 2 || doc.Claims[0].Path != "README.md" || doc.Claims[1].Path != "second.md" {
		t.Fatalf("got %#v, %v", doc, err)
	}
}

func TestExtractNoBlockIsNoClaimsBlock(t *testing.T) {
	src, err := os.ReadFile("../../testdata/verify/fabricated-handoff.md")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ExtractClaims(src)
	if !errors.Is(err, ErrNoClaimsBlock) {
		t.Fatalf("got %v, want ErrNoClaimsBlock", err)
	}
	if !strings.Contains(err.Error(), "```readback-claims\n{\"version\":1,\"claims\":[...]}\n```") {
		t.Fatalf("missing block grammar: %v", err)
	}
}

func TestExtractIgnoresProseClaims(t *testing.T) {
	for _, src := range []string{
		"merged PR #412\n" + extractionDocument,
		"    ```readback-claims\n    " + extractionDocument + "\n    ```",
		"\t```readback-claims\n\t" + extractionDocument + "\n\t```",
		"```readback-claims extra\n" + extractionDocument + "\n```",
		"``` readback-claims\n" + extractionDocument + "\n```",
	} {
		if _, err := ExtractClaims([]byte(src)); !errors.Is(err, ErrNoClaimsBlock) {
			t.Errorf("input %q: got %v, want ErrNoClaimsBlock", src, err)
		}
	}
}

func TestExtractJSONInputPassthrough(t *testing.T) {
	// A JSON object carrying "claims" but no "version" is a schema error, not a Markdown
	// document without a fence.
	for _, src := range []string{extractionDocument, `{"version":2,"claims":[]}`, `{"version":null}`, `{"version":1,"claims":[{"type":"file_exists","path":"../secret"}]}`, `{"claims":[]}`} {
		want, wantErr := ParseDocument([]byte(src))
		got, gotErr := ExtractClaims([]byte(src))
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotErr, wantErr) {
			t.Errorf("input %s: got %#v, %v; want %#v, %v", src, got, gotErr, want, wantErr)
		}
	}
}

func TestExtractNestedFenceNotExtracted(t *testing.T) {
	for _, outer := range []string{"````markdown", "~~~markdown"} {
		src := outer + "\n```readback-claims\n" + extractionDocument + "\n```\n" + strings.TrimSuffix(outer, "markdown")
		if _, err := ExtractClaims([]byte(src)); !errors.Is(err, ErrNoClaimsBlock) {
			t.Fatalf("got %v, want ErrNoClaimsBlock", err)
		}
		src += "\n```readback-claims\n" + extractionDocument + "\n```"
		if doc, err := ExtractClaims([]byte(src)); err != nil || len(doc.Claims) != 1 {
			t.Fatalf("got %#v, %v", doc, err)
		}
	}
}

func TestExtractVersionMismatchAcrossBlocks(t *testing.T) {
	for _, versions := range [][2]string{{"1", "2"}, {"2", "1"}, {"2", "2"}, {"1", "null"}} {
		src := ""
		for _, version := range versions {
			src += "```readback-claims\n{\"version\":" + version + ",\"claims\":[]}\n```\n"
		}
		_, err := ExtractClaims([]byte(src))
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("got %v, want ValidationError", err)
		}
		if len(validation.Problems) == 0 || validation.Problems[0].Index != -1 || validation.Problems[0].Field != "version" {
			t.Fatalf("want document-level version problem: %#v", validation.Problems)
		}
	}
}

func TestExtractTildeFence(t *testing.T) {
	doc, err := ExtractClaims([]byte("~~~~readback-claims\n" + extractionDocument + "\n~~~~~\t"))
	if err != nil || len(doc.Claims) != 1 {
		t.Fatalf("got %#v, %v", doc, err)
	}
	for _, closing := range []string{"~~~", "````", "~~~~ extra", ""} {
		_, err := ExtractClaims([]byte("~~~~readback-claims\n" + extractionDocument + "\n" + closing))
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Errorf("closing %q: got %v, want ValidationError", closing, err)
		}
	}
}
