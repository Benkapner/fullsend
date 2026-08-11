package jira

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// asMap is a small helper to keep test assertions terse when reaching into
// nested ADF map[string]any structures.
func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T (%v)", v, v)
	}
	return m
}

func asSlice(t *testing.T, v any) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T (%v)", v, v)
	}
	return s
}

// mustADF calls MarkdownToADF and fails the test if it returns an error,
// for the vast majority of test cases that expect src to convert
// successfully.
func mustADF(t *testing.T, src string) map[string]any {
	t.Helper()
	doc, err := MarkdownToADF(src)
	if err != nil {
		t.Fatalf("MarkdownToADF(%q) returned unexpected error: %v", src, err)
	}
	return doc
}

// ---------------------------------------------------------------------------
// MarkdownToADF
// ---------------------------------------------------------------------------

func TestMarkdownToADF_PlainParagraph(t *testing.T) {
	doc := mustADF(t, "hello world")

	if doc["type"] != "doc" {
		t.Errorf("doc type = %v, want %q", doc["type"], "doc")
	}
	if doc["version"] != 1 {
		t.Errorf("doc version = %v, want 1", doc["version"])
	}

	content := asSlice(t, doc["content"])
	if len(content) != 1 {
		t.Fatalf("doc content len = %d, want 1", len(content))
	}
	para := asMap(t, content[0])
	if para["type"] != "paragraph" {
		t.Errorf("block type = %v, want %q", para["type"], "paragraph")
	}
	paraContent := asSlice(t, para["content"])
	if len(paraContent) != 1 {
		t.Fatalf("paragraph content len = %d, want 1", len(paraContent))
	}
	text := asMap(t, paraContent[0])
	if text["type"] != "text" || text["text"] != "hello world" {
		t.Errorf("text node = %+v, want type=text text=%q", text, "hello world")
	}
}

func TestMarkdownToADF_ResolvesBackslashEscapesAndEntities(t *testing.T) {
	doc := mustADF(t, `\*not em\* and &copy; and &amp;`)

	para := asMap(t, asSlice(t, doc["content"])[0])
	nodes := asSlice(t, para["content"])

	var got strings.Builder
	for _, n := range nodes {
		node := asMap(t, n)
		got.WriteString(fmt.Sprint(node["text"]))
	}
	want := "*not em* and \u00a9 and &"
	if got.String() != want {
		t.Errorf("text = %q, want %q (escapes/entities should be resolved, as goldmark's HTML renderer does)", got.String(), want)
	}
}

func TestMarkdownToADF_CodeSpanKeepsBackslashesLiteral(t *testing.T) {
	doc := mustADF(t, "`\\*literal\\*`")

	para := asMap(t, asSlice(t, doc["content"])[0])
	nodes := asSlice(t, para["content"])
	text := asMap(t, nodes[0])
	want := `\*literal\*`
	if text["text"] != want {
		t.Errorf("code span text = %v, want %q (raw/code text must not be unescaped)", text["text"], want)
	}
}

func TestMarkdownToADF_MultipleParagraphs(t *testing.T) {
	doc := mustADF(t, "first\n\nsecond")

	content := asSlice(t, doc["content"])
	if len(content) != 2 {
		t.Fatalf("doc content len = %d, want 2", len(content))
	}
	for i, want := range []string{"first", "second"} {
		para := asMap(t, content[i])
		text := asMap(t, asSlice(t, para["content"])[0])
		if text["text"] != want {
			t.Errorf("paragraph %d text = %v, want %q", i, text["text"], want)
		}
	}
}

func TestMarkdownToADF_HeadingLevels(t *testing.T) {
	for level := 1; level <= 6; level++ {
		src := strings.Repeat("#", level) + " Heading"
		doc := mustADF(t, src)
		content := asSlice(t, doc["content"])
		if len(content) != 1 {
			t.Fatalf("level %d: doc content len = %d, want 1", level, len(content))
		}
		heading := asMap(t, content[0])
		if heading["type"] != "heading" {
			t.Fatalf("level %d: block type = %v, want %q", level, heading["type"], "heading")
		}
		attrs := asMap(t, heading["attrs"])
		gotLevel, ok := attrs["level"].(int)
		if !ok || gotLevel != level {
			t.Errorf("level %d: attrs.level = %v, want %d", level, attrs["level"], level)
		}
	}
}

func TestMarkdownToADF_BoldItalicInlineCode(t *testing.T) {
	doc := mustADF(t, "**bold** and *italic* and `code`")

	para := asMap(t, asSlice(t, doc["content"])[0])
	nodes := asSlice(t, para["content"])

	var foundStrong, foundEm, foundCode bool
	for _, n := range nodes {
		node := asMap(t, n)
		marks, ok := node["marks"].([]any)
		if !ok {
			continue
		}
		for _, m := range marks {
			mark := asMap(t, m)
			switch mark["type"] {
			case "strong":
				if node["text"] != "bold" {
					t.Errorf("strong text = %v, want %q", node["text"], "bold")
				}
				foundStrong = true
			case "em":
				if node["text"] != "italic" {
					t.Errorf("em text = %v, want %q", node["text"], "italic")
				}
				foundEm = true
			case "code":
				if node["text"] != "code" {
					t.Errorf("code text = %v, want %q", node["text"], "code")
				}
				foundCode = true
			}
		}
	}
	if !foundStrong {
		t.Error("expected a text node with a strong mark")
	}
	if !foundEm {
		t.Error("expected a text node with an em mark")
	}
	if !foundCode {
		t.Error("expected a text node with a code mark")
	}
}

func TestMarkdownToADF_BoldInlineCodeDoesNotCombineMarks(t *testing.T) {
	doc := mustADF(t, "**`--flag`**")

	para := asMap(t, asSlice(t, doc["content"])[0])
	nodes := asSlice(t, para["content"])

	var found bool
	for _, n := range nodes {
		node := asMap(t, n)
		if node["text"] != "--flag" {
			continue
		}
		found = true
		marks := asSlice(t, node["marks"])
		var sawCode, sawStrong bool
		for _, m := range marks {
			switch asMap(t, m)["type"] {
			case "code":
				sawCode = true
			case "strong", "em":
				sawStrong = true
			}
		}
		if !sawCode {
			t.Errorf("marks = %v, want a code mark", marks)
		}
		if sawStrong {
			t.Errorf("marks = %v, want code combined only with link, not strong/em", marks)
		}
	}
	if !found {
		t.Fatalf("expected a text node with value %q", "--flag")
	}
}

func TestMarkdownToADF_NestedSameTypeEmphasisDoesNotDuplicateMarks(t *testing.T) {
	for _, src := range []string{"*outer _inner_ text*", "**outer __inner__ text**"} {
		doc := mustADF(t, src)

		para := asMap(t, asSlice(t, doc["content"])[0])
		nodes := asSlice(t, para["content"])

		var found bool
		for _, n := range nodes {
			node := asMap(t, n)
			if node["text"] != "inner" {
				continue
			}
			found = true
			marks := asSlice(t, node["marks"])
			if len(marks) != 1 {
				t.Errorf("%q: marks on %q = %v, want exactly one mark", src, "inner", marks)
			}
		}
		if !found {
			t.Fatalf("%q: expected a text node with value %q", src, "inner")
		}
	}
}

func TestMarkdownToADF_FencedCodeBlockWithLanguage(t *testing.T) {
	doc := mustADF(t, "```go\nfmt.Println(\"hi\")\n```")

	content := asSlice(t, doc["content"])
	if len(content) != 1 {
		t.Fatalf("doc content len = %d, want 1", len(content))
	}
	block := asMap(t, content[0])
	if block["type"] != "codeBlock" {
		t.Fatalf("block type = %v, want %q", block["type"], "codeBlock")
	}
	attrs := asMap(t, block["attrs"])
	if attrs["language"] != "go" {
		t.Errorf("attrs.language = %v, want %q", attrs["language"], "go")
	}
	codeContent := asSlice(t, block["content"])
	text := asMap(t, codeContent[0])
	if !strings.Contains(fmt.Sprint(text["text"]), "fmt.Println") {
		t.Errorf("code text = %v, want it to contain %q", text["text"], "fmt.Println")
	}
}

func TestMarkdownToADF_EmptyFencedCodeBlock(t *testing.T) {
	doc := mustADF(t, "```\n```")

	content := asSlice(t, doc["content"])
	if len(content) != 1 {
		t.Fatalf("doc content len = %d, want 1", len(content))
	}
	block := asMap(t, content[0])
	if block["type"] != "codeBlock" {
		t.Fatalf("block type = %v, want %q", block["type"], "codeBlock")
	}
	codeContent, _ := block["content"].([]any)
	for _, c := range codeContent {
		text := asMap(t, c)
		if text["text"] == "" {
			t.Errorf("codeBlock content = %v, want no zero-length text node (ADF forbids text.text minLength 0)", codeContent)
		}
	}
}

func TestMarkdownToADF_BulletList(t *testing.T) {
	doc := mustADF(t, "- one\n- two\n")

	content := asSlice(t, doc["content"])
	list := asMap(t, content[0])
	if list["type"] != "bulletList" {
		t.Fatalf("block type = %v, want %q", list["type"], "bulletList")
	}
	items := asSlice(t, list["content"])
	if len(items) != 2 {
		t.Fatalf("bulletList content len = %d, want 2", len(items))
	}
	for i, want := range []string{"one", "two"} {
		item := asMap(t, items[i])
		if item["type"] != "listItem" {
			t.Errorf("item %d type = %v, want %q", i, item["type"], "listItem")
		}
		itemPara := asMap(t, asSlice(t, item["content"])[0])
		text := asMap(t, asSlice(t, itemPara["content"])[0])
		if text["text"] != want {
			t.Errorf("item %d text = %v, want %q", i, text["text"], want)
		}
	}
}

func TestMarkdownToADF_OrderedListNonOneStart(t *testing.T) {
	doc := mustADF(t, "5. five\n6. six\n")

	content := asSlice(t, doc["content"])
	list := asMap(t, content[0])
	if list["type"] != "orderedList" {
		t.Fatalf("block type = %v, want %q", list["type"], "orderedList")
	}
	attrs := asMap(t, list["attrs"])
	if attrs["order"] != 5 {
		t.Errorf("attrs.order = %v, want 5", attrs["order"])
	}
	items := asSlice(t, list["content"])
	if len(items) != 2 {
		t.Fatalf("orderedList content len = %d, want 2", len(items))
	}
}

func TestMarkdownToADF_Blockquote(t *testing.T) {
	doc := mustADF(t, "> quoted text")

	content := asSlice(t, doc["content"])
	bq := asMap(t, content[0])
	if bq["type"] != "blockquote" {
		t.Fatalf("block type = %v, want %q", bq["type"], "blockquote")
	}
	para := asMap(t, asSlice(t, bq["content"])[0])
	text := asMap(t, asSlice(t, para["content"])[0])
	if text["text"] != "quoted text" {
		t.Errorf("blockquote text = %v, want %q", text["text"], "quoted text")
	}
}

func TestMarkdownToADF_Link(t *testing.T) {
	doc := mustADF(t, "see [the docs](https://example.com/docs)")

	para := asMap(t, asSlice(t, doc["content"])[0])
	nodes := asSlice(t, para["content"])

	var found bool
	for _, n := range nodes {
		node := asMap(t, n)
		marks, ok := node["marks"].([]any)
		if !ok {
			continue
		}
		for _, m := range marks {
			mark := asMap(t, m)
			if mark["type"] != "link" {
				continue
			}
			attrs := asMap(t, mark["attrs"])
			if attrs["href"] != "https://example.com/docs" {
				t.Errorf("link href = %v, want %q", attrs["href"], "https://example.com/docs")
			}
			if node["text"] != "the docs" {
				t.Errorf("link text = %v, want %q", node["text"], "the docs")
			}
			found = true
		}
	}
	if !found {
		t.Error("expected a text node with a link mark")
	}
}

func TestMarkdownToADF_LinkMarkDoesNotLeakToFollowingText(t *testing.T) {
	doc := mustADF(t, "before [link](https://example.com) after")

	para := asMap(t, asSlice(t, doc["content"])[0])
	nodes := asSlice(t, para["content"])

	var sawTrailingText bool
	for _, n := range nodes {
		node := asMap(t, n)
		text, _ := node["text"].(string)
		if !strings.Contains(text, "after") {
			continue
		}
		sawTrailingText = true
		if marks, ok := node["marks"].([]any); ok && len(marks) > 0 {
			t.Errorf("text node %q has marks %v, want none (link mark must not leak to a sibling)", text, marks)
		}
	}
	if !sawTrailingText {
		t.Fatal("expected a text node containing \"after\"")
	}
}

func TestMarkdownToADF_UnknownBlockFallsBackToPlainText(t *testing.T) {
	// convertBlockNode's default case previously returned nil for any
	// block-level node outside its supported vocabulary (e.g. a raw HTML
	// block), silently dropping the content with no trace. This repo's
	// own convention (internal/sticky, postcomment.go, postreview.go) of
	// wrapping output in <details><summary>...</summary>...</details>
	// HTML blocks would vanish entirely if posted to Jira. Mirror
	// walkInline's own default case, which falls back to plain text
	// rather than losing readable content.
	doc := mustADF(t, "before\n\n<details><summary>x</summary>\ny\n</details>\n\nafter")

	content := asSlice(t, doc["content"])
	var sawFallbackText bool
	for _, c := range content {
		block := asMap(t, c)
		if block["type"] != "paragraph" {
			continue
		}
		for _, n := range asSlice(t, block["content"]) {
			node := asMap(t, n)
			text, _ := node["text"].(string)
			if strings.Contains(text, "<details>") {
				sawFallbackText = true
			}
		}
	}
	if !sawFallbackText {
		t.Errorf("MarkdownToADF(html block) = %+v, want the raw HTML block content preserved as fallback text somewhere, not silently dropped", doc)
	}
}

// linkMarkHref returns the href of the first "link" mark found among
// para's inline content, and whether one was found at all.
func linkMarkHref(t *testing.T, doc map[string]any) (href string, found bool) {
	t.Helper()
	para := asMap(t, asSlice(t, doc["content"])[0])
	for _, n := range asSlice(t, para["content"]) {
		node := asMap(t, n)
		marks, ok := node["marks"].([]any)
		if !ok {
			continue
		}
		for _, m := range marks {
			mark := asMap(t, m)
			if mark["type"] != "link" {
				continue
			}
			attrs := asMap(t, mark["attrs"])
			return fmt.Sprint(attrs["href"]), true
		}
	}
	return "", false
}

func TestMarkdownToADF_LinkRejectsDangerousSchemes(t *testing.T) {
	for _, src := range []string{
		"[click me](javascript:alert(1))",
		"[click me](data:text/html,<script>alert(1)</script>)",
		"[click me](vbscript:msgbox(1))",
	} {
		doc := mustADF(t, src)
		if _, found := linkMarkHref(t, doc); found {
			t.Errorf("MarkdownToADF(%q) produced a link mark; want the dangerous-scheme href dropped", src)
		}
	}
}

func TestMarkdownToADF_LinkAllowsSafeSchemes(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"[docs](https://example.com/docs)", "https://example.com/docs"},
		{"[docs](http://example.com/docs)", "http://example.com/docs"},
		{"[me](mailto:me@example.com)", "mailto:me@example.com"},
		{"[section](#section)", "#section"},
		{"[rel](/path/to/page)", "/path/to/page"},
	} {
		doc := mustADF(t, tc.src)
		href, found := linkMarkHref(t, doc)
		if !found {
			t.Errorf("MarkdownToADF(%q): expected a link mark, got none", tc.src)
			continue
		}
		if href != tc.want {
			t.Errorf("MarkdownToADF(%q) href = %q, want %q", tc.src, href, tc.want)
		}
	}
}

func TestMarkdownToADF_LinkRejectsProtocolRelativeHost(t *testing.T) {
	doc := mustADF(t, "[click me](//evil.example)")
	if href, found := linkMarkHref(t, doc); found {
		t.Errorf("MarkdownToADF(protocol-relative link) produced a link mark with href %q; want it dropped", href)
	}
}

func TestMarkdownToADF_AutoLinkRejectsDangerousScheme(t *testing.T) {
	// goldmark's autolink extension isn't enabled by default, so this
	// exercises the CommonMark <...> autolink form instead.
	doc := mustADF(t, "<javascript:alert(1)>")
	if _, found := linkMarkHref(t, doc); found {
		t.Errorf("MarkdownToADF(autolink with javascript: scheme) produced a link mark; want it dropped")
	}
}

// assertNoEmptyContainers recursively checks that no blockquote,
// bulletList, orderedList, or listItem node in the ADF tree has empty
// content, which violates ADF's minItems: 1 for those node types.
func assertNoEmptyContainers(t *testing.T, node any) {
	t.Helper()
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	nodeType, _ := m["type"].(string)
	content, hasContent := m["content"].([]any)
	switch nodeType {
	case "blockquote", "bulletList", "orderedList", "listItem":
		if hasContent && len(content) == 0 {
			t.Errorf("%s node has content: [], which violates ADF minItems: 1: %+v", nodeType, m)
		}
	}
	for _, c := range content {
		assertNoEmptyContainers(t, c)
	}
}

func TestMarkdownToADF_HeadingInsideBlockquoteDegradesToBoldParagraph(t *testing.T) {
	doc := mustADF(t, "> # title")

	bq := asMap(t, asSlice(t, doc["content"])[0])
	if bq["type"] != "blockquote" {
		t.Fatalf("block type = %v, want %q", bq["type"], "blockquote")
	}
	children := asSlice(t, bq["content"])
	if len(children) != 1 {
		t.Fatalf("blockquote content len = %d, want 1", len(children))
	}
	para := asMap(t, children[0])
	if para["type"] != "paragraph" {
		t.Errorf("heading-in-blockquote degraded type = %v, want %q (ADF blockquote schema doesn't allow heading children)", para["type"], "paragraph")
	}
	text := asMap(t, asSlice(t, para["content"])[0])
	if text["text"] != "title" {
		t.Errorf("degraded heading text = %v, want %q", text["text"], "title")
	}
	marks := asSlice(t, text["marks"])
	var sawStrong bool
	for _, mk := range marks {
		if asMap(t, mk)["type"] == "strong" {
			sawStrong = true
		}
	}
	if !sawStrong {
		t.Errorf("degraded heading marks = %v, want a strong mark", marks)
	}
	assertNoEmptyContainers(t, doc)
}

func TestMarkdownToADF_NestedBlockquoteFlattens(t *testing.T) {
	doc := mustADF(t, "> > inner")

	outer := asMap(t, asSlice(t, doc["content"])[0])
	if outer["type"] != "blockquote" {
		t.Fatalf("block type = %v, want %q", outer["type"], "blockquote")
	}
	for _, c := range asSlice(t, outer["content"]) {
		if asMap(t, c)["type"] == "blockquote" {
			t.Fatalf("outer blockquote content = %+v, want the nested blockquote flattened away (ADF blockquote schema doesn't allow blockquote children)", outer["content"])
		}
	}
	assertNoEmptyContainers(t, doc)
}

func TestMarkdownToADF_HeadingInsideListItemDegradesToBoldParagraph(t *testing.T) {
	doc := mustADF(t, "- # h")

	list := asMap(t, asSlice(t, doc["content"])[0])
	item := asMap(t, asSlice(t, list["content"])[0])
	para := asMap(t, asSlice(t, item["content"])[0])
	if para["type"] != "paragraph" {
		t.Errorf("heading-in-listItem degraded type = %v, want %q (ADF listItem schema doesn't allow heading children)", para["type"], "paragraph")
	}
	assertNoEmptyContainers(t, doc)
}

func TestMarkdownToADF_BlockquoteInsideListItemFlattens(t *testing.T) {
	doc := mustADF(t, "- > q")

	list := asMap(t, asSlice(t, doc["content"])[0])
	item := asMap(t, asSlice(t, list["content"])[0])
	for _, c := range asSlice(t, item["content"]) {
		if asMap(t, c)["type"] == "blockquote" {
			t.Fatalf("listItem content = %+v, want the blockquote flattened away (ADF listItem schema doesn't allow blockquote children)", item["content"])
		}
	}
	assertNoEmptyContainers(t, doc)
}

func TestMarkdownToADF_ThematicBreakInsideBlockquoteDropped(t *testing.T) {
	doc := mustADF(t, "> above\n>\n> ---\n>\n> below")

	bq := asMap(t, asSlice(t, doc["content"])[0])
	if bq["type"] != "blockquote" {
		t.Fatalf("block type = %v, want %q", bq["type"], "blockquote")
	}
	for _, c := range asSlice(t, bq["content"]) {
		if asMap(t, c)["type"] == "rule" {
			t.Errorf("blockquote content = %+v, want the rule dropped (ADF blockquote schema doesn't allow a rule child)", bq["content"])
		}
	}
	assertNoEmptyContainers(t, doc)
}

func TestMarkdownToADF_DeepNestingProducesNoEmptyContainers(t *testing.T) {
	// The maxADFWriteDepth cutoff truncates a deeply nested blockquote's
	// content, which — before the container-emptiness check — left an
	// innermost {"type":"blockquote","content":[]} at the cap boundary:
	// schema-invalid ADF Jira would reject outright. A sibling paragraph
	// keeps the overall doc non-empty despite the nested blockquote
	// collapsing entirely, so this exercises the container-emptiness
	// check on its own, independent of MarkdownToADF's separate
	// empty-doc error (see TestMarkdownToADF_AllContentDroppedReturnsError).
	const depth = 60 // > maxADFWriteDepth (50)
	src := strings.Repeat("> ", depth) + "leaf" + "\n\nsibling paragraph"
	doc := mustADF(t, src)
	assertNoEmptyContainers(t, doc)
}

func TestMarkdownToADF_AllContentDroppedReturnsError(t *testing.T) {
	// A chain of nested blockquotes deeper than maxADFWriteDepth, with no
	// other top-level content, collapses all the way up to an empty doc:
	// each level past the cutoff drops its empty container, which empties
	// its parent, and so on to the top. Posting that to Jira would create
	// a visibly empty comment with no error. MarkdownToADF must fail
	// instead of returning {"type":"doc","content":[]}.
	const depth = 60 // > maxADFWriteDepth (50)
	src := strings.Repeat("> ", depth) + "leaf"

	_, err := MarkdownToADF(src)
	if err == nil {
		t.Error("MarkdownToADF(all-dropped nested blockquotes) returned no error; want one rather than an empty ADF doc")
	}
}

func TestMarkdownToADF_OversizedInputReturnsError(t *testing.T) {
	src := strings.Repeat("a", maxMarkdownParseBytes+1)

	_, err := MarkdownToADF(src)
	if err == nil {
		t.Errorf("MarkdownToADF(%d bytes) returned no error; want one for input over the %d byte limit", len(src), maxMarkdownParseBytes)
	}
}

func TestMarkdownToADF_ThematicBreak(t *testing.T) {
	doc := mustADF(t, "above\n\n---\n\nbelow")

	content := asSlice(t, doc["content"])
	if len(content) != 3 {
		t.Fatalf("doc content len = %d, want 3", len(content))
	}
	rule := asMap(t, content[1])
	if rule["type"] != "rule" {
		t.Errorf("block type = %v, want %q", rule["type"], "rule")
	}
}

func TestMarkdownToADF_DeepNestingIsBounded(t *testing.T) {
	// Mirrors TestADFToPlainText_DeepNestingIsBounded: MarkdownToADF's
	// block/inline converters recurse once per markdown nesting level with
	// no cap, so deeply nested input (e.g. thousands of ">" blockquote
	// markers) must not be walked to full depth. A sibling paragraph keeps
	// the doc non-empty despite the nested blockquote collapsing entirely
	// past the depth cap.
	const depth = 10000
	src := strings.Repeat("> ", depth) + "leaf" + "\n\nsibling paragraph"

	doc := mustADF(t, src)

	walked := 0
	content := doc["content"]
	for {
		items, ok := content.([]any)
		if !ok || len(items) == 0 {
			break
		}
		node, ok := items[0].(map[string]any)
		if !ok || node["type"] != "blockquote" {
			break
		}
		walked++
		content = node["content"]
	}
	if walked >= depth {
		t.Errorf("MarkdownToADF walked all %d nesting levels; want it capped well below that", depth)
	}
}

func TestMarkdownToADF_ParseTimeIsBounded(t *testing.T) {
	// maxADFWriteDepth only bounds the post-parse AST walk; goldmark's own
	// Parse() is ~O(N^2) on deeply nested blockquotes and dominates the
	// cost long before the walk-depth cap is ever reached (confirmed by
	// benchmark: 80,000 nesting levels take ~3.2s to parse alone). Without
	// an input size limit rejected before Parse(), MarkdownToADF blocks
	// for seconds on adversarial input regardless of maxADFWriteDepth.
	// This input is well over maxMarkdownParseBytes, so MarkdownToADF
	// returns an error rather than parsing it at all — both return values
	// are discarded since this test only cares about timing.
	const depth = 80000
	src := strings.Repeat("> ", depth) + "leaf"

	start := time.Now()
	MarkdownToADF(src) //nolint:errcheck // timing-only test, error is expected
	elapsed := time.Since(start)

	const budget = 750 * time.Millisecond
	if elapsed > budget {
		t.Errorf("MarkdownToADF(%d nesting levels) took %s, want under %s (parse time must be bounded independent of maxADFWriteDepth)", depth, elapsed, budget)
	}
}

func TestMarkdownToADF_HardBreak(t *testing.T) {
	doc := mustADF(t, "line one  \nline two")

	para := asMap(t, asSlice(t, doc["content"])[0])
	nodes := asSlice(t, para["content"])

	var foundHardBreak bool
	for _, n := range nodes {
		node := asMap(t, n)
		if node["type"] == "hardBreak" {
			foundHardBreak = true
		}
	}
	if !foundHardBreak {
		t.Errorf("expected a hardBreak node in paragraph content, got %+v", nodes)
	}
}

// ---------------------------------------------------------------------------
// ADFToPlainText
// ---------------------------------------------------------------------------

func TestADFToPlainText_String(t *testing.T) {
	got := ADFToPlainText("plain text body")
	if got != "plain text body" {
		t.Errorf("ADFToPlainText(string) = %q, want %q", got, "plain text body")
	}
}

func TestADFToPlainText_Paragraph(t *testing.T) {
	adf := map[string]any{
		"type":    "doc",
		"version": 1,
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{"type": "text", "text": "hello there"},
				},
			},
		},
	}
	got := ADFToPlainText(adf)
	if got != "hello there" {
		t.Errorf("ADFToPlainText(paragraph) = %q, want %q", got, "hello there")
	}
}

func TestADFToPlainText_MultiParagraphAndHeading(t *testing.T) {
	adf := map[string]any{
		"type":    "doc",
		"version": 1,
		"content": []any{
			map[string]any{
				"type": "heading",
				"content": []any{
					map[string]any{"type": "text", "text": "Title"},
				},
			},
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{"type": "text", "text": "Body"},
				},
			},
		},
	}
	got := ADFToPlainText(adf)
	want := "Title\nBody"
	if got != want {
		t.Errorf("ADFToPlainText(heading+paragraph) = %q, want %q", got, want)
	}
}

func TestADFToPlainText_List(t *testing.T) {
	adf := map[string]any{
		"type": "bulletList",
		"content": []any{
			map[string]any{
				"type": "listItem",
				"content": []any{
					map[string]any{
						"type": "paragraph",
						"content": []any{
							map[string]any{"type": "text", "text": "item one"},
						},
					},
				},
			},
			map[string]any{
				"type": "listItem",
				"content": []any{
					map[string]any{
						"type": "paragraph",
						"content": []any{
							map[string]any{"type": "text", "text": "item two"},
						},
					},
				},
			},
		},
	}
	got := ADFToPlainText(adf)
	want := "item one\nitem two"
	if got != want {
		t.Errorf("ADFToPlainText(list) = %q, want %q", got, want)
	}
}

func TestADFToPlainText_HardBreak(t *testing.T) {
	adf := map[string]any{
		"type": "paragraph",
		"content": []any{
			map[string]any{"type": "text", "text": "line one"},
			map[string]any{"type": "hardBreak"},
			map[string]any{"type": "text", "text": "line two"},
		},
	}
	got := ADFToPlainText(adf)
	want := "line one\nline two"
	if got != want {
		t.Errorf("ADFToPlainText(hardBreak) = %q, want %q", got, want)
	}
}

func TestADFToPlainText_DeepNestingIsBounded(t *testing.T) {
	// Mirrors jirapoll's TestExtractPlainText_DeepNestingIsBounded: the
	// depth cap must match since this is a fresh implementation of the
	// same defensive behavior for attacker-controlled ADF bodies.
	const depth = 10000
	root := map[string]any{
		"type": "paragraph",
		"text": "level-0",
	}
	leaf := root
	for i := 1; i < depth; i++ {
		child := map[string]any{
			"type": "paragraph",
			"text": fmt.Sprintf("level-%d", i),
		}
		leaf["content"] = []any{child}
		leaf = child
	}

	got := ADFToPlainText(root)
	if !strings.HasPrefix(got, "level-0") {
		t.Errorf("ADFToPlainText(deeply nested ADF) = %q, want it to start with %q", got, "level-0")
	}
	if strings.Contains(got, fmt.Sprintf("level-%d", depth-1)) {
		t.Errorf("ADFToPlainText(deeply nested ADF) walked all %d levels; want it capped well below that", depth)
	}
}

func TestADFToPlainText_UnexpectedType(t *testing.T) {
	got := ADFToPlainText(42)
	if got != "" {
		t.Errorf("ADFToPlainText(int) = %q, want empty string", got)
	}
}

// ---------------------------------------------------------------------------
// ADFToMarkdown
// ---------------------------------------------------------------------------

func TestADFToMarkdown_String(t *testing.T) {
	got := ADFToMarkdown("plain text body")
	if got != "plain text body" {
		t.Errorf("ADFToMarkdown(string) = %q, want %q", got, "plain text body")
	}
}

func TestADFToMarkdown_UnexpectedType(t *testing.T) {
	got := ADFToMarkdown(42)
	if got != "" {
		t.Errorf("ADFToMarkdown(int) = %q, want empty string", got)
	}
}

func TestADFToMarkdown_Paragraph(t *testing.T) {
	adf := map[string]any{
		"type":    "doc",
		"version": 1,
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{"type": "text", "text": "hello there"},
				},
			},
		},
	}
	got := ADFToMarkdown(adf)
	if got != "hello there" {
		t.Errorf("ADFToMarkdown(paragraph) = %q, want %q", got, "hello there")
	}
}

func TestADFToMarkdown_MultipleParagraphsSeparatedByBlankLine(t *testing.T) {
	// Unlike ADFToPlainText, which joins blocks with a single newline,
	// Markdown paragraphs must be separated by a blank line, or a
	// Markdown parser (including this package's own MarkdownToADF) reads
	// them back as one paragraph instead of two.
	adf := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type":    "paragraph",
				"content": []any{map[string]any{"type": "text", "text": "first"}},
			},
			map[string]any{
				"type":    "paragraph",
				"content": []any{map[string]any{"type": "text", "text": "second"}},
			},
		},
	}
	got := ADFToMarkdown(adf)
	want := "first\n\nsecond"
	if got != want {
		t.Errorf("ADFToMarkdown(two paragraphs) = %q, want %q", got, want)
	}
}

func TestADFToMarkdown_Heading(t *testing.T) {
	adf := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type":    "heading",
				"attrs":   map[string]any{"level": 2},
				"content": []any{map[string]any{"type": "text", "text": "Title"}},
			},
		},
	}
	got := ADFToMarkdown(adf)
	want := "## Title"
	if got != want {
		t.Errorf("ADFToMarkdown(heading level 2) = %q, want %q", got, want)
	}
}

func TestADFToMarkdown_StrongEmCodeMarks(t *testing.T) {
	adf := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{"type": "text", "text": "bold", "marks": []any{map[string]any{"type": "strong"}}},
					map[string]any{"type": "text", "text": " and "},
					map[string]any{"type": "text", "text": "italic", "marks": []any{map[string]any{"type": "em"}}},
					map[string]any{"type": "text", "text": " and "},
					map[string]any{"type": "text", "text": "code", "marks": []any{map[string]any{"type": "code"}}},
				},
			},
		},
	}
	got := ADFToMarkdown(adf)
	want := "**bold** and *italic* and `code`"
	if got != want {
		t.Errorf("ADFToMarkdown(marks) = %q, want %q", got, want)
	}
}

func TestADFToMarkdown_MultipleMarksNestInApplicationOrder(t *testing.T) {
	// MarkdownToADF builds a text node's marks slice outer-to-inner (see
	// walkInline's Emphasis/Link cases): the outermost enclosing mark is
	// appended first, so a "**[text](url)**" source produces
	// marks: [strong, link]. Rendering must reverse that order, applying
	// link (innermost) first and strong (outermost) last, or the
	// asymmetric marks (link, strong) round-trip into the wrong nesting.
	adf := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "text",
						"marks": []any{
							map[string]any{"type": "strong"},
							map[string]any{"type": "link", "attrs": map[string]any{"href": "https://example.com"}},
						},
					},
				},
			},
		},
	}
	got := ADFToMarkdown(adf)
	want := "**[text](https://example.com)**"
	if got != want {
		t.Errorf("ADFToMarkdown(nested marks) = %q, want %q", got, want)
	}
}

func TestADFToMarkdown_Link(t *testing.T) {
	adf := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "the docs",
						"marks": []any{
							map[string]any{"type": "link", "attrs": map[string]any{"href": "https://example.com/docs"}},
						},
					},
				},
			},
		},
	}
	got := ADFToMarkdown(adf)
	want := "[the docs](https://example.com/docs)"
	if got != want {
		t.Errorf("ADFToMarkdown(link) = %q, want %q", got, want)
	}
}

func TestADFToMarkdown_CodeBlockWithLanguage(t *testing.T) {
	adf := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type":  "codeBlock",
				"attrs": map[string]any{"language": "go"},
				"content": []any{
					map[string]any{"type": "text", "text": "fmt.Println(\"hi\")"},
				},
			},
		},
	}
	got := ADFToMarkdown(adf)
	want := "```go\nfmt.Println(\"hi\")\n```"
	if got != want {
		t.Errorf("ADFToMarkdown(codeBlock) = %q, want %q", got, want)
	}
}

func TestADFToMarkdown_CodeBlockWithoutLanguage(t *testing.T) {
	adf := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type":    "codeBlock",
				"content": []any{map[string]any{"type": "text", "text": "plain"}},
			},
		},
	}
	got := ADFToMarkdown(adf)
	want := "```\nplain\n```"
	if got != want {
		t.Errorf("ADFToMarkdown(codeBlock without language) = %q, want %q", got, want)
	}
}

func TestADFToMarkdown_BulletList(t *testing.T) {
	adf := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "bulletList",
				"content": []any{
					map[string]any{
						"type":    "listItem",
						"content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "one"}}}},
					},
					map[string]any{
						"type":    "listItem",
						"content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "two"}}}},
					},
				},
			},
		},
	}
	got := ADFToMarkdown(adf)
	want := "- one\n- two"
	if got != want {
		t.Errorf("ADFToMarkdown(bulletList) = %q, want %q", got, want)
	}
}

func TestADFToMarkdown_OrderedListStartsAtAttrsOrder(t *testing.T) {
	adf := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type":  "orderedList",
				"attrs": map[string]any{"order": 5},
				"content": []any{
					map[string]any{
						"type":    "listItem",
						"content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "five"}}}},
					},
					map[string]any{
						"type":    "listItem",
						"content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "six"}}}},
					},
				},
			},
		},
	}
	got := ADFToMarkdown(adf)
	want := "5. five\n6. six"
	if got != want {
		t.Errorf("ADFToMarkdown(orderedList) = %q, want %q", got, want)
	}
}

func TestADFToMarkdown_Blockquote(t *testing.T) {
	adf := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "blockquote",
				"content": []any{
					map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "quoted text"}}},
				},
			},
		},
	}
	got := ADFToMarkdown(adf)
	want := "> quoted text"
	if got != want {
		t.Errorf("ADFToMarkdown(blockquote) = %q, want %q", got, want)
	}
}

func TestADFToMarkdown_Rule(t *testing.T) {
	adf := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "above"}}},
			map[string]any{"type": "rule"},
			map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "below"}}},
		},
	}
	got := ADFToMarkdown(adf)
	want := "above\n\n---\n\nbelow"
	if got != want {
		t.Errorf("ADFToMarkdown(rule) = %q, want %q", got, want)
	}
}

func TestADFToMarkdown_HardBreak(t *testing.T) {
	adf := map[string]any{
		"type": "paragraph",
		"content": []any{
			map[string]any{"type": "text", "text": "line one"},
			map[string]any{"type": "hardBreak"},
			map[string]any{"type": "text", "text": "line two"},
		},
	}
	got := ADFToMarkdown(adf)
	// A backslash-newline hard break, rather than trailing spaces:
	// trailing whitespace is silently stripped by enough editors, git
	// diffs, and web forms that a hard break built from it wouldn't
	// reliably survive a copy/paste round trip.
	want := "line one\\\nline two"
	if got != want {
		t.Errorf("ADFToMarkdown(hardBreak) = %q, want %q", got, want)
	}
}

func TestADFToMarkdown_DeepNestingIsBounded(t *testing.T) {
	// Unlike ADFToPlainText's single generic node walker, ADFToMarkdown
	// only recurses through container types that can genuinely nest in
	// real ADF (blockquote, bulletList/orderedList, listItem); a
	// paragraph's own content is inline runs, not further blocks. So the
	// attacker-controlled-nesting vector here is a blockquote chain,
	// mirroring MarkdownToADF's own deep-blockquote-nesting tests, rather
	// than ADFToPlainText's paragraph-chain shape.
	const depth = 10000
	leaf := map[string]any{
		"type":    "paragraph",
		"content": []any{map[string]any{"type": "text", "text": "leaf"}},
	}
	nested := leaf
	for i := 0; i < depth; i++ {
		nested = map[string]any{"type": "blockquote", "content": []any{nested}}
	}
	doc := map[string]any{
		"type": "doc",
		"content": []any{
			nested,
			map[string]any{
				"type":    "paragraph",
				"content": []any{map[string]any{"type": "text", "text": "sibling"}},
			},
		},
	}

	got := ADFToMarkdown(doc)
	if strings.Contains(got, "leaf") {
		t.Errorf("ADFToMarkdown(%d nested blockquotes) walked all the way to the leaf; want it capped well below that", depth)
	}
	if !strings.Contains(got, "sibling") {
		t.Errorf("ADFToMarkdown(deeply nested blockquote + sibling) = %q, want it to still contain the sibling paragraph", got)
	}
}

func TestADFToMarkdown_Float64Attrs(t *testing.T) {
	// JSON-decoded ADF has float64 for numeric attrs, not int. Verify
	// ADFToMarkdown handles this correctly (the real shape from json.Decode).
	heading := map[string]any{
		"type":    "doc",
		"version": float64(1),
		"content": []any{
			map[string]any{
				"type":  "heading",
				"attrs": map[string]any{"level": float64(3)},
				"content": []any{
					map[string]any{"type": "text", "text": "Title"},
				},
			},
		},
	}
	got := ADFToMarkdown(heading)
	if got != "### Title" {
		t.Errorf("ADFToMarkdown(heading with float64 level 3) = %q, want %q", got, "### Title")
	}

	orderedList := map[string]any{
		"type":    "doc",
		"version": float64(1),
		"content": []any{
			map[string]any{
				"type":  "orderedList",
				"attrs": map[string]any{"order": float64(5)},
				"content": []any{
					map[string]any{
						"type": "listItem",
						"content": []any{
							map[string]any{
								"type":    "paragraph",
								"content": []any{map[string]any{"type": "text", "text": "item"}},
							},
						},
					},
				},
			},
		},
	}
	got = ADFToMarkdown(orderedList)
	if got != "5. item" {
		t.Errorf("ADFToMarkdown(orderedList with float64 order 5) = %q, want %q", got, "5. item")
	}
}

func TestADFToMarkdown_RoundTripsThroughMarkdownToADF(t *testing.T) {
	for _, src := range []string{
		"hello world",
		"**bold** and *italic* and `code`",
		"# Heading\n\nsome body text",
		"- one\n- two",
		"> quoted text",
		"[the docs](https://example.com/docs)",
	} {
		doc := mustADF(t, src)
		got := ADFToMarkdown(doc)
		if got != src {
			t.Errorf("ADFToMarkdown(MarkdownToADF(%q)) = %q, want the original markdown back unchanged", src, got)
		}
	}
}
