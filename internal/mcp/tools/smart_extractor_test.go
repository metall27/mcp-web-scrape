package tools

import (
	"strings"
	"testing"
)

// TestSplitParagraphs verifies the paragraph splitter drops empty/whitespace-only
// lines and trims surrounding whitespace.
func TestSplitParagraphs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "hello world", []string{"hello world"}},
		{"multi", "one\ntwo\nthree", []string{"one", "two", "three"}},
		{"blank_lines_dropped", "a\n\n  \n\nb", []string{"a", "b"}},
		{"whitespace_trimmed", "  spaced  \n  pad  ", []string{"spaced", "pad"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitParagraphs(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len: got %d, want %d (got=%v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestCountWords verifies word counting via strings.Fields semantics.
func TestCountWords(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"one", 1},
		{"one two three", 3},
		{"  spaced   out   ", 2},
		{"привет мир", 2},
	}
	for _, tc := range cases {
		if got := countWords(tc.in); got != tc.want {
			t.Errorf("countWords(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestStripTags verifies HTML tag removal.
func TestStripTags(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"<p>hello</p>", "hello"},
		{"<b>foo</b> <i>bar</i>", "foo bar"},
		{"no tags here", "no tags here"},
		{"<div class=\"x\"><span>nested</span></div>", "nested"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := stripTags(tc.in); got != tc.want {
			t.Errorf("stripTags(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestHtmlToParagraphs verifies extraction of block-level elements from HTML.
func TestHtmlToParagraphs(t *testing.T) {
	html := `<div>
		<p>First paragraph with enough text to be meaningful.</p>
		<h2>A heading</h2>
		<p>Second paragraph here, also with content.</p>
		<p></p>
		<ul><li>List item one</li><li>List item two</li></ul>
	</div>`

	got := htmlToParagraphs(html)

	// We expect at least the 2 paragraphs, 1 heading, 2 list items.
	// Empty <p> should be dropped.
	if len(got) < 4 {
		t.Fatalf("got %d paragraphs, want >= 4: %v", len(got), got)
	}

	joined := strings.Join(got, " ")
	for _, want := range []string{"First paragraph", "A heading", "Second paragraph", "List item one", "List item two"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected paragraph %q in results, got: %v", want, got)
		}
	}
}

// TestHtmlToParagraphsEmptyInput verifies graceful handling of empty/garbage HTML.
func TestHtmlToParagraphsEmptyInput(t *testing.T) {
	got := htmlToParagraphs("")
	if len(got) != 0 {
		t.Errorf("empty input: got %d paragraphs, want 0", len(got))
	}
}
