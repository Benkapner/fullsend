package jira

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// MarkdownToADF parses source as CommonMark and returns an Atlassian
// Document Format (ADF) "doc" node, since Jira's comment/description
// fields don't accept markdown directly. It supports the block/inline
// vocabulary ADFToPlainText (and the read-side walker it mirrors in
// internal/jirapoll) already recognizes: paragraphs, headings,
// bullet/ordered lists, blockquotes, fenced/indented code blocks,
// thematic breaks, and the strong/em/code/link/hardBreak inline marks.
// Block-level constructs outside that vocabulary (raw HTML, tables, ...)
// fall back to a plain-text paragraph of their raw source rather than
// vanishing outright — ADF has no equivalent node for most of them, but
// dropping the content entirely would silently lose whatever a caller
// wrote (e.g. this repo's own <details> convention for collapsing output).
//
// Returns an error, rather than a partial or empty result, if source is
// over maxMarkdownParseBytes or if it converts to no ADF content at all
// (e.g. markdown that's nothing but a chain of nested blockquotes deeper
// than maxADFWriteDepth): callers write the result straight to Jira, and
// silently posting a truncated or visibly empty comment is worse than
// failing the write.
func MarkdownToADF(source string) (map[string]any, error) {
	src := []byte(source)
	if len(src) > maxMarkdownParseBytes {
		return nil, fmt.Errorf("markdown body is %d bytes, over the %d byte limit", len(src), maxMarkdownParseBytes)
	}
	doc := goldmark.DefaultParser().Parse(text.NewReader(src))
	content := adfBlockContent(doc, src, 0, false)
	if len(content) == 0 {
		return nil, fmt.Errorf("markdown body converted to no ADF content")
	}
	return map[string]any{
		"version": 1,
		"type":    "doc",
		"content": content,
	}, nil
}

// maxADFWriteDepth caps how deep adfBlockContent/convertBlockNode/
// walkInline will recurse into parsed markdown, mirroring maxADFDepth's
// rationale on the read side below. MarkdownToADF's callers don't feed it
// attacker-controlled input today, but it ships as a public API
// (jira.MarkdownToADF, LiveClient.CreateComment/UpdateComment) that a
// future caller could feed external content into (e.g. quoting an issue
// or comment body) — the same unbounded-recursion risk applies once that
// happens.
const maxADFWriteDepth = 50

// maxMarkdownParseBytes caps the input size MarkdownToADF will parse.
// maxADFWriteDepth above only bounds the post-parse AST walk; Parse()
// itself is the actual bottleneck for adversarial input — benchmarking
// showed it costs ~O(N^2) on deeply nested blockquotes (~3.2s to parse
// 80,000 nesting levels, i.e. 160KB), independent of and unreached by the
// walk-depth cap. Rejecting oversized input outright keeps worst-case
// parse time to roughly a few hundred milliseconds even for
// pathologically nested input, while comfortably fitting any
// realistically hand-written comment or issue description; MarkdownToADF
// documents the limit for callers that may feed it larger,
// machine-generated bodies.
const maxMarkdownParseBytes = 32 * 1024

// adfBlockContent converts each block-level child of parent into zero or
// more ADF block nodes (a child can expand to several siblings, e.g. a
// flattened nested blockquote, or to none, e.g. a dropped thematic break).
// depth is the nesting depth of parent's children; past maxADFWriteDepth,
// remaining content is dropped rather than descended into. restricted
// indicates parent is itself a blockquote or listItem, whose ADF schema
// only accepts a narrower set of block types (paragraph, list, codeBlock)
// than convertBlockNode emits by default — see convertBlockNode.
func adfBlockContent(parent ast.Node, source []byte, depth int, restricted bool) []any {
	content := []any{}
	if depth > maxADFWriteDepth {
		return content
	}
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		content = append(content, convertBlockNode(c, source, depth, restricted)...)
	}
	return content
}

// convertBlockNode converts a single goldmark block node to zero or more
// ADF nodes: normally one, but zero for a dropped or empty node, or
// several when a nested blockquote is flattened into its parent's
// content. depth is n's own nesting depth. restricted indicates n is a
// direct child of a blockquote or listItem: ADF restricts both to
// paragraph/bulletList/orderedList/codeBlock/media content, so heading,
// thematic break, and nested blockquote — all valid at the top level or
// inside a list item's ordinary flow — must be degraded rather than
// emitted as-is, or Jira Cloud rejects the whole write with a 400.
func convertBlockNode(n ast.Node, source []byte, depth int, restricted bool) []any {
	switch v := n.(type) {
	case *ast.Paragraph:
		content := adfInlineContent(n, source, depth, nil)
		if len(content) == 0 {
			return nil
		}
		return oneNode(map[string]any{"type": "paragraph", "content": content})
	case *ast.TextBlock:
		// Tight list items wrap their text in a TextBlock rather than a
		// Paragraph; ADF's listItem schema still expects a paragraph child.
		content := adfInlineContent(n, source, depth, nil)
		if len(content) == 0 {
			return nil
		}
		return oneNode(map[string]any{"type": "paragraph", "content": content})
	case *ast.Heading:
		if restricted {
			// Degrade to a bold paragraph: ADF's blockquote/listItem
			// schema has no heading node.
			marks := []any{map[string]any{"type": "strong"}}
			return oneNode(map[string]any{"type": "paragraph", "content": adfInlineContent(n, source, depth, marks)})
		}
		return oneNode(map[string]any{
			"type":    "heading",
			"attrs":   map[string]any{"level": v.Level},
			"content": adfInlineContent(n, source, depth, nil),
		})
	case *ast.ThematicBreak:
		if restricted {
			// ADF's blockquote/listItem schema has no rule node; there's
			// no reasonable degradation, so it's dropped.
			return nil
		}
		return oneNode(map[string]any{"type": "rule"})
	case *ast.Blockquote:
		children := adfBlockContent(n, source, depth+1, true)
		if restricted {
			// ADF's blockquote schema doesn't allow a nested blockquote;
			// flatten by splicing this one's children directly into the
			// parent's content instead of wrapping them.
			return children
		}
		return containerNode("blockquote", children, nil)
	case *ast.CodeBlock:
		return oneNode(codeBlockNode(v.Lines().Value(source), ""))
	case *ast.FencedCodeBlock:
		return oneNode(codeBlockNode(v.Lines().Value(source), string(v.Language(source))))
	case *ast.List:
		listType := "bulletList"
		var attrs map[string]any
		if v.IsOrdered() {
			listType = "orderedList"
			if v.Start != 1 {
				attrs = map[string]any{"order": v.Start}
			}
		}
		// A List's own children are always ListItems, which are valid in
		// any context, so restricted doesn't propagate here — only into
		// each ListItem's own content below.
		return containerNode(listType, adfBlockContent(n, source, depth+1, false), attrs)
	case *ast.ListItem:
		return containerNode("listItem", adfBlockContent(n, source, depth+1, true), nil)
	default:
		// Node types without ADF-specific handling (e.g. HTMLBlock) fall
		// back to a plain-text paragraph of their raw source, mirroring
		// walkInline's own default case, so at least the readable content
		// isn't lost outright.
		if node := fallbackTextNode(n, source); node != nil {
			return oneNode(node)
		}
		return nil
	}
}

// oneNode wraps a single ADF node for convertBlockNode's []any return
// type.
func oneNode(node map[string]any) []any {
	return []any{node}
}

// containerNode builds an ADF container node (blockquote, bulletList,
// orderedList, listItem) of the given type, or returns no nodes at all if
// content is empty: ADF requires these types to have at least one child
// (minItems: 1), and an empty one — e.g. from a maxADFWriteDepth cutoff,
// or a listItem whose only child was dropped — would make Jira reject the
// entire write with a 400. Dropping the container is preferable to
// inserting a placeholder, since it doesn't fabricate visible content the
// original markdown never had.
func containerNode(nodeType string, content []any, attrs map[string]any) []any {
	if len(content) == 0 {
		return nil
	}
	node := map[string]any{"type": nodeType, "content": content}
	if attrs != nil {
		node["attrs"] = attrs
	}
	return oneNode(node)
}

// fallbackTextNode renders a block node's raw source lines as a plain-text
// paragraph, for block kinds convertBlockNode has no dedicated handling
// for. Returns nil if the node exposes no raw lines or they're blank.
func fallbackTextNode(n ast.Node, source []byte) map[string]any {
	lines, ok := n.(interface{ Lines() *text.Segments })
	if !ok {
		return nil
	}
	raw := strings.TrimSpace(string(lines.Lines().Value(source)))
	if raw == "" {
		return nil
	}
	return map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": raw}}}
}

// codeBlockNode builds an ADF codeBlock node. lang is set as the
// "language" attr only when non-empty (indented code blocks have none).
// The text child is omitted entirely for an empty block: ADF requires
// text nodes to be non-empty, but codeBlock content itself is optional, so
// a bare {"type":"codeBlock"} is the valid way to represent one.
func codeBlockNode(text []byte, lang string) map[string]any {
	node := map[string]any{"type": "codeBlock"}
	if trimmed := strings.TrimRight(string(text), "\n"); trimmed != "" {
		node["content"] = []any{map[string]any{"type": "text", "text": trimmed}}
	}
	if lang != "" {
		node["attrs"] = map[string]any{"language": lang}
	}
	return node
}

// adfInlineContent converts the inline children of a block node (paragraph
// or heading) into a flat sequence of ADF text/hardBreak nodes. depth is
// the block node's own nesting depth, threaded through to walkInline since
// inline marks (nested emphasis/links/code spans) recurse too. marks seeds
// the mark set every emitted text node starts with — nil normally, or e.g.
// a "strong" mark when a heading is being degraded to a bold paragraph
// inside a blockquote/listItem.
func adfInlineContent(parent ast.Node, source []byte, depth int, marks []any) []any {
	content := []any{}
	walkInline(parent, source, marks, &content, depth)
	return content
}

// walkInline recursively walks inline nodes, accumulating the marks
// (strong/em/code/link) implied by enclosing nodes and emitting a text (or
// hardBreak) ADF node for each leaf. Past maxADFWriteDepth, remaining
// content is dropped rather than descended into.
func walkInline(parent ast.Node, source []byte, marks []any, out *[]any, depth int) {
	if depth > maxADFWriteDepth {
		return
	}
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			appendADFText(out, textValue(v, source), marks)
			if v.HardLineBreak() {
				*out = append(*out, map[string]any{"type": "hardBreak"})
			} else if v.SoftLineBreak() {
				appendADFText(out, " ", marks)
			}
		case *ast.String:
			appendADFText(out, string(v.Value), marks)
		case *ast.CodeSpan:
			// ADF's code mark can only combine with link, per Atlassian's
			// mark-combination rules; drop any inherited strong/em (or
			// other) marks rather than emitting a schema-invalid
			// [strong, code] pair for input like "**`--flag`**".
			walkInline(v, source, withMark(onlyLinkMarks(marks), map[string]any{"type": "code"}), out, depth+1)
		case *ast.Emphasis:
			markType := "em"
			if v.Level >= 2 {
				markType = "strong"
			}
			walkInline(v, source, withMark(marks, map[string]any{"type": markType}), out, depth+1)
		case *ast.Link:
			dest := string(v.Destination)
			linkMarks := marks
			if isSafeHref(dest) {
				attrs := map[string]any{"href": dest}
				if len(v.Title) > 0 {
					attrs["title"] = string(v.Title)
				}
				linkMarks = withMark(marks, map[string]any{"type": "link", "attrs": attrs})
			}
			walkInline(v, source, linkMarks, out, depth+1)
		case *ast.AutoLink:
			dest := string(v.URL(source))
			linkMarks := marks
			if isSafeHref(dest) {
				linkMarks = withMark(marks, map[string]any{
					"type": "link", "attrs": map[string]any{"href": dest},
				})
			}
			appendADFText(out, string(v.Label(source)), linkMarks)
		default:
			// Node types without ADF-specific handling (e.g. Image,
			// RawHTML) fall through to walking their children as plain
			// text, so at least the readable content isn't lost.
			walkInline(c, source, marks, out, depth+1)
		}
	}
}

// isSafeHref reports whether dest is safe to emit as an ADF link's href.
// A missing scheme with no host (relative paths, "#fragment" anchors) is
// allowed, as are http/https/mailto; anything else — javascript:, data:,
// vbscript:, file:, protocol-relative "//host", ... — is rejected,
// mirroring the scheme allowlisting ValidateBaseURL already applies to
// Jira base URLs elsewhere in this package. dest that fails to parse as a
// URL at all is rejected too.
func isSafeHref(dest string) bool {
	if dest == "" {
		return true
	}
	u, err := url.Parse(dest)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "":
		return u.Host == ""
	case "http", "https", "mailto":
		return true
	default:
		return false
	}
}

// textValue returns the plain-text value of an *ast.Text node, resolving
// backslash escapes and HTML entity/numeric character references the same
// way goldmark's own HTML renderer does (via v.Value, which is raw source
// bytes and does neither). Raw text — inside a code span or code block —
// is returned unresolved, since goldmark's renderer leaves that content
// verbatim too: "\*not em\*" outside code becomes "*not em*", but
// "`\*x\*`" keeps its backslashes.
func textValue(v *ast.Text, source []byte) string {
	value := v.Value(source)
	if v.IsRaw() {
		return string(value)
	}
	value = util.UnescapePunctuations(value)
	value = util.ResolveNumericReferences(value)
	value = util.ResolveEntityNames(value)
	return string(value)
}

// withMark returns a new marks slice with mark appended, without mutating
// the caller's slice (siblings must not see marks added while walking a
// previous sibling's subtree). If marks already contains a mark of the
// same type — e.g. nested same-delimiter emphasis like
// "*outer _inner_ text*", which produces two nested *ast.Emphasis nodes
// with the same "em" markType — marks is returned unchanged rather than
// growing a duplicate: a text node with two identical marks is never
// meaningful, and ADF's validator behavior for it is unconfirmed.
func withMark(marks []any, mark map[string]any) []any {
	markType, _ := mark["type"].(string)
	for _, m := range marks {
		if existing, ok := m.(map[string]any); ok && existing["type"] == markType {
			return marks
		}
	}
	next := make([]any, len(marks), len(marks)+1)
	copy(next, marks)
	return append(next, mark)
}

// onlyLinkMarks filters marks down to just the "link" mark (if present),
// for use before appending a "code" mark: ADF documents that code can only
// be combined with link, not with strong/em or other marks.
func onlyLinkMarks(marks []any) []any {
	var kept []any
	for _, m := range marks {
		if mark, ok := m.(map[string]any); ok && mark["type"] == "link" {
			kept = append(kept, m)
		}
	}
	return kept
}

// appendADFText appends a "text" ADF node, or does nothing for empty text
// (e.g. the zero-length segment goldmark can produce around a hard break).
func appendADFText(out *[]any, value string, marks []any) {
	if value == "" {
		return
	}
	node := map[string]any{"type": "text", "text": value}
	if len(marks) > 0 {
		node["marks"] = marks
	}
	*out = append(*out, node)
}

// maxADFDepth caps how deep walkADFNode will recurse into an issue or
// comment body. Real Jira-UI-authored ADF documents are shallow (a
// handful of levels for nested lists at most); a body is
// attacker-controlled by any Jira user who can comment on or edit an
// issue tracker.Client reads, so without a cap, deeply nested JSON could
// exhaust the goroutine stack. Mirrors jirapoll's identical cap and logic
// (see extractADFText/walkADFNode in internal/jirapoll/discover.go) — the
// duplication is intentional for now, to avoid refactoring jirapoll's
// private helpers as part of this package's feature work; consolidating
// onto this exported ADFToPlainText is a reasonable follow-up once the
// Jira tracker integration stabilizes.
const maxADFDepth = 50

// ADFToPlainText extracts plain text from a Jira issue or comment body.
// body is either a plain string or an ADF document (map[string]any),
// matching the two shapes Jira's REST API returns for description/body
// fields; any other type (including nil) yields "".
func ADFToPlainText(body any) string {
	switch v := body.(type) {
	case string:
		return v
	case map[string]any:
		var sb strings.Builder
		walkADFNode(v, &sb, 0)
		return sb.String()
	default:
		return ""
	}
}

// walkADFNode recursively walks ADF nodes, extracting text, up to
// maxADFDepth levels deep.
func walkADFNode(node map[string]any, sb *strings.Builder, depth int) {
	if depth > maxADFDepth {
		return
	}

	// A hardBreak (Shift+Enter in the Jira editor) carries no text or
	// content; without emitting a newline here, words the author placed on
	// separate visual lines within one paragraph would fuse together.
	if nodeType, _ := node["type"].(string); nodeType == "hardBreak" {
		sb.WriteString("\n")
		return
	}

	if text, ok := node["text"].(string); ok {
		sb.WriteString(text)
	}

	nodeType, _ := node["type"].(string)

	content, ok := node["content"].([]any)
	if !ok {
		return
	}

	for i, child := range content {
		childMap, ok := child.(map[string]any)
		if !ok {
			continue
		}
		walkADFNode(childMap, sb, depth+1)

		// Add newline after paragraph/heading blocks (except the last one).
		childType, _ := childMap["type"].(string)
		if i < len(content)-1 && isBlockType(childType) && isBlockType(nodeType) {
			sb.WriteString("\n")
		}
	}
}

// isBlockType returns true for ADF block-level types that should be
// separated by newlines.
func isBlockType(nodeType string) bool {
	switch nodeType {
	case "doc", "paragraph", "heading", "blockquote", "codeBlock",
		"bulletList", "orderedList", "listItem", "panel", "rule":
		return true
	default:
		return false
	}
}

// ADFToMarkdown converts a Jira issue or comment body to CommonMark
// Markdown, the reverse of MarkdownToADF. body is either a plain string or
// an ADF document (map[string]any), matching the two shapes Jira's REST
// API returns for description/body fields; any other type (including nil)
// yields "".
//
// Unlike ADFToPlainText, this preserves formatting (bold/italic/code/
// links, headings, lists, blockquotes, code blocks) rather than
// discarding it: tracker.Body is documented as Markdown-formatted text,
// so a Jira-backed tracker.Client returning plain text there would
// silently drop content GitHub- and GitLab-backed implementations
// preserve.
func ADFToMarkdown(body any) string {
	switch v := body.(type) {
	case string:
		return v
	case map[string]any:
		// A real Jira description/comment body is always a "doc" node
		// whose own content is a list of sibling blocks. Some callers
		// (and this package's own tests, mirroring ADFToPlainText's)
		// instead pass a single block node — a bare paragraph or list —
		// directly; render that as one block rather than misreading its
		// inline/item content as if it were a list of sibling blocks.
		if nodeType, _ := v["type"].(string); isBlockType(nodeType) && nodeType != "doc" {
			return adfMarkdownBlock(v, 0)
		}
		return strings.Join(adfMarkdownBlocks(v, 0), "\n\n")
	default:
		return ""
	}
}

// adfMarkdownBlocks renders each block-level child of node into its own
// Markdown string, mirroring walkADFNode's traversal but block-aware:
// callers join siblings with a blank line rather than a single newline,
// since that's what separates Markdown blocks (a single newline reads
// back as one paragraph). depth mirrors walkADFNode's cap against
// attacker-controlled ADF bodies.
func adfMarkdownBlocks(node map[string]any, depth int) []string {
	if depth > maxADFDepth {
		return nil
	}
	content, ok := node["content"].([]any)
	if !ok {
		return nil
	}
	var blocks []string
	for _, c := range content {
		childMap, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if block := adfMarkdownBlock(childMap, depth+1); block != "" {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// adfMarkdownBlock renders a single ADF block-level node as Markdown.
// Unrecognized types (e.g. "panel", "media") fall back to rendering any
// inline text content flat, mirroring MarkdownToADF's own
// fallback-to-plain-text convention, so content isn't silently dropped.
func adfMarkdownBlock(node map[string]any, depth int) string {
	nodeType, _ := node["type"].(string)
	switch nodeType {
	case "paragraph":
		return adfMarkdownInline(node)
	case "heading":
		level := 1
		if attrs, ok := node["attrs"].(map[string]any); ok {
			if l, ok := attrs["level"].(int); ok {
				level = l
			} else if l, ok := attrs["level"].(float64); ok {
				level = int(l)
			}
		}
		return strings.Repeat("#", level) + " " + adfMarkdownInline(node)
	case "codeBlock":
		lang := ""
		if attrs, ok := node["attrs"].(map[string]any); ok {
			if l, ok := attrs["language"].(string); ok {
				lang = l
			}
		}
		return "```" + lang + "\n" + adfCodeBlockText(node) + "\n```"
	case "rule":
		return "---"
	case "blockquote":
		lines := strings.Split(strings.Join(adfMarkdownBlocks(node, depth), "\n\n"), "\n")
		for i, line := range lines {
			lines[i] = "> " + line
		}
		return strings.Join(lines, "\n")
	case "bulletList":
		return adfMarkdownList(node, depth, func(int) string { return "- " })
	case "orderedList":
		start := 1
		if attrs, ok := node["attrs"].(map[string]any); ok {
			if o, ok := attrs["order"].(int); ok {
				start = o
			} else if o, ok := attrs["order"].(float64); ok {
				start = int(o)
			}
		}
		return adfMarkdownList(node, depth, func(i int) string { return fmt.Sprintf("%d. ", start+i) })
	default:
		return adfMarkdownInline(node)
	}
}

// adfCodeBlockText concatenates a codeBlock node's text children verbatim
// (no marks are valid inside an ADF codeBlock).
func adfCodeBlockText(node map[string]any) string {
	var sb strings.Builder
	for _, c := range asADFNodes(node["content"]) {
		if text, ok := c["text"].(string); ok {
			sb.WriteString(text)
		}
	}
	return sb.String()
}

// adfMarkdownList renders a bulletList or orderedList's items, each
// prefixed by marker(i) for its zero-based index. A listItem's own block
// content is rendered with adfMarkdownBlocks and indented so continuation
// lines (a second paragraph, or a nested list) stay part of the same item.
func adfMarkdownList(node map[string]any, depth int, marker func(i int) string) string {
	items := asADFNodes(node["content"])
	lines := make([]string, 0, len(items))
	for i, item := range items {
		body := strings.Join(adfMarkdownBlocks(item, depth+1), "\n\n")
		prefix := marker(i)
		indented := strings.ReplaceAll(body, "\n", "\n"+strings.Repeat(" ", len(prefix)))
		lines = append(lines, prefix+indented)
	}
	return strings.Join(lines, "\n")
}

// asADFNodes type-asserts an ADF "content" field into a slice of child
// node maps, skipping (rather than failing on) any element that isn't a
// map[string]any.
func asADFNodes(content any) []map[string]any {
	items, ok := content.([]any)
	if !ok {
		return nil
	}
	nodes := make([]map[string]any, 0, len(items))
	for _, c := range items {
		if m, ok := c.(map[string]any); ok {
			nodes = append(nodes, m)
		}
	}
	return nodes
}

// adfMarkdownInline renders a block node's inline children (text/hardBreak)
// as a single line of Markdown, applying each text node's marks.
func adfMarkdownInline(node map[string]any) string {
	var sb strings.Builder
	for _, c := range asADFNodes(node["content"]) {
		if childType, _ := c["type"].(string); childType == "hardBreak" {
			// A backslash before the line ending, rather than the more
			// common trailing double-space: trailing whitespace is
			// silently stripped by enough editors, git diffs, and web
			// forms that a hard break built from it wouldn't reliably
			// survive a copy/paste round trip.
			sb.WriteString("\\\n")
			continue
		}
		text, _ := c["text"].(string)
		sb.WriteString(applyADFMarks(text, asADFNodes(c["marks"])))
	}
	return sb.String()
}

// applyADFMarks wraps text in the Markdown syntax for each of marks, in
// reverse order: MarkdownToADF's walkInline builds a text node's marks
// outer-to-inner (each enclosing mark is appended after the ones already
// inherited from its parent — see its Emphasis/Link cases), so marks[0] is
// the outermost mark and must be applied last to reproduce the original
// nesting.
func applyADFMarks(text string, marks []map[string]any) string {
	for i := len(marks) - 1; i >= 0; i-- {
		mark := marks[i]
		switch mark["type"] {
		case "strong":
			text = "**" + text + "**"
		case "em":
			text = "*" + text + "*"
		case "code":
			text = "`" + text + "`"
		case "link":
			href := ""
			if attrs, ok := mark["attrs"].(map[string]any); ok {
				href, _ = attrs["href"].(string)
			}
			text = "[" + text + "](" + href + ")"
		}
	}
	return text
}
