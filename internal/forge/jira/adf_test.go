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

// ---------------------------------------------------------------------------
// MarkdownToADF
// ---------------------------------------------------------------------------

func TestMarkdownToADF_PlainParagraph(t *testing.T) {
	doc := MarkdownToADF("hello world")

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
	doc := MarkdownToADF(`\*not em\* and &copy; and &amp;`)

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
	doc := MarkdownToADF("`\\*literal\\*`")

	para := asMap(t, asSlice(t, doc["content"])[0])
	nodes := asSlice(t, para["content"])
	text := asMap(t, nodes[0])
	want := `\*literal\*`
	if text["text"] != want {
		t.Errorf("code span text = %v, want %q (raw/code text must not be unescaped)", text["text"], want)
	}
}

func TestMarkdownToADF_MultipleParagraphs(t *testing.T) {
	doc := MarkdownToADF("first\n\nsecond")

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
		doc := MarkdownToADF(src)
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
	doc := MarkdownToADF("**bold** and *italic* and `code`")

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
	doc := MarkdownToADF("**`--flag`**")

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

func TestMarkdownToADF_FencedCodeBlockWithLanguage(t *testing.T) {
	doc := MarkdownToADF("```go\nfmt.Println(\"hi\")\n```")

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
	doc := MarkdownToADF("```\n```")

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
	doc := MarkdownToADF("- one\n- two\n")

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
	doc := MarkdownToADF("5. five\n6. six\n")

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
	doc := MarkdownToADF("> quoted text")

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
	doc := MarkdownToADF("see [the docs](https://example.com/docs)")

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
	doc := MarkdownToADF("before [link](https://example.com) after")

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
	doc := MarkdownToADF("before\n\n<details><summary>x</summary>\ny\n</details>\n\nafter")

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
		doc := MarkdownToADF(src)
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
		doc := MarkdownToADF(tc.src)
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

func TestMarkdownToADF_AutoLinkRejectsDangerousScheme(t *testing.T) {
	// goldmark's autolink extension isn't enabled by default, so this
	// exercises the CommonMark <...> autolink form instead.
	doc := MarkdownToADF("<javascript:alert(1)>")
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
	doc := MarkdownToADF("> # title")

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
	doc := MarkdownToADF("> > inner")

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
	doc := MarkdownToADF("- # h")

	list := asMap(t, asSlice(t, doc["content"])[0])
	item := asMap(t, asSlice(t, list["content"])[0])
	para := asMap(t, asSlice(t, item["content"])[0])
	if para["type"] != "paragraph" {
		t.Errorf("heading-in-listItem degraded type = %v, want %q (ADF listItem schema doesn't allow heading children)", para["type"], "paragraph")
	}
	assertNoEmptyContainers(t, doc)
}

func TestMarkdownToADF_BlockquoteInsideListItemFlattens(t *testing.T) {
	doc := MarkdownToADF("- > q")

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
	doc := MarkdownToADF("> above\n>\n> ---\n>\n> below")

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
	// schema-invalid ADF Jira would reject outright.
	const depth = 60 // > maxADFWriteDepth (50)
	src := strings.Repeat("> ", depth) + "leaf"
	doc := MarkdownToADF(src)
	assertNoEmptyContainers(t, doc)
}

func TestMarkdownToADF_ThematicBreak(t *testing.T) {
	doc := MarkdownToADF("above\n\n---\n\nbelow")

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
	// markers) must not be walked to full depth.
	const depth = 10000
	src := strings.Repeat("> ", depth) + "leaf"

	doc := MarkdownToADF(src)

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
	// an input size limit applied before Parse(), MarkdownToADF blocks for
	// seconds on adversarial input regardless of maxADFWriteDepth.
	const depth = 80000
	src := strings.Repeat("> ", depth) + "leaf"

	start := time.Now()
	MarkdownToADF(src)
	elapsed := time.Since(start)

	const budget = 750 * time.Millisecond
	if elapsed > budget {
		t.Errorf("MarkdownToADF(%d nesting levels) took %s, want under %s (parse time must be bounded independent of maxADFWriteDepth)", depth, elapsed, budget)
	}
}

func TestMarkdownToADF_HardBreak(t *testing.T) {
	doc := MarkdownToADF("line one  \nline two")

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
