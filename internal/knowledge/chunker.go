package knowledge

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// K2 chunking bounds. Chunks are rebuildable derivations of immutable
// knowledge documents, so all limits may change between releases without
// affecting package identity.
const (
	maxChunkRunes        = 8000
	maxChunksPerDocument = 256
	maxLinksPerDocument  = 128
	maxHeadingTitleRunes = 200
	snippetRunes         = 240
)

// DocumentChunk is one deterministic retrieval unit derived from a knowledge
// document. Key is stable for identical input: the same document content
// always produces the same ordered chunk keys.
type DocumentChunk struct {
	Key         string
	HeadingPath string
	Content     string
}

// ChunkResult carries the bounded chunk list plus a truncation marker set
// when the per-document chunk cap cut content off.
type ChunkResult struct {
	Chunks    []DocumentChunk
	Truncated bool
}

// ChunkDocument splits one text document into bounded retrieval chunks.
// Markdown documents are split by heading hierarchy with a heading path like
// "Guide > Install > Linux"; oversized sections fall back to paragraph
// packing and finally to fixed rune windows. Non-Markdown text uses
// paragraph packing only. Deterministic: same input, same output.
func ChunkDocument(docPath, mimeType, content string) ChunkResult {
	var sections []section
	if isMarkdownDocument(docPath, mimeType) {
		sections = splitMarkdownSections(content)
	} else {
		sections = []section{{headingPath: "", body: content}}
	}

	chunks := make([]DocumentChunk, 0, len(sections))
	truncated := false
	sequence := 0
	for _, currentSection := range sections {
		for _, piece := range splitOversized(currentSection.body) {
			if strings.TrimSpace(piece) == "" {
				continue
			}
			if sequence >= maxChunksPerDocument {
				truncated = true
				return ChunkResult{Chunks: chunks, Truncated: truncated}
			}
			chunks = append(chunks, DocumentChunk{
				Key:         fmt.Sprintf("chunk-%04d", sequence),
				HeadingPath: currentSection.headingPath,
				Content:     piece,
			})
			sequence++
		}
	}
	return ChunkResult{Chunks: chunks, Truncated: truncated}
}

type section struct {
	headingPath string
	body        string
}

type headingFrame struct {
	level int
	title string
}

// splitMarkdownSections cuts a Markdown document at ATX headings while
// tracking the heading hierarchy. Headings inside fenced code blocks are
// ignored. The heading line itself stays in its section body for context.
func splitMarkdownSections(content string) []section {
	lines := strings.Split(content, "\n")
	sections := make([]section, 0, 8)
	stack := make([]headingFrame, 0, 6)
	var body strings.Builder
	currentPath := ""
	inFence := false
	fenceMarker := ""

	flush := func() {
		sections = append(sections, section{headingPath: currentPath, body: body.String()})
		body.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inFence {
			if strings.HasPrefix(trimmed, fenceMarker) {
				inFence = false
			}
			body.WriteString(line)
			body.WriteString("\n")
			continue
		}
		if marker := fenceOpenMarker(trimmed); marker != "" {
			inFence = true
			fenceMarker = marker
			body.WriteString(line)
			body.WriteString("\n")
			continue
		}
		if level, title, ok := parseATXHeading(trimmed); ok {
			flush()
			for len(stack) > 0 && stack[len(stack)-1].level >= level {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, headingFrame{level: level, title: title})
			titles := make([]string, 0, len(stack))
			for _, frame := range stack {
				titles = append(titles, frame.title)
			}
			currentPath = strings.Join(titles, " > ")
			body.WriteString(line)
			body.WriteString("\n")
			continue
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	flush()

	result := make([]section, 0, len(sections))
	for _, item := range sections {
		if strings.TrimSpace(item.body) == "" {
			continue
		}
		result = append(result, item)
	}
	return result
}

func fenceOpenMarker(trimmedLine string) string {
	for _, marker := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmedLine, marker) {
			return marker
		}
	}
	return ""
}

func parseATXHeading(trimmedLine string) (int, string, bool) {
	if !strings.HasPrefix(trimmedLine, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(trimmedLine) && trimmedLine[level] == '#' {
		level++
	}
	if level > 6 {
		return 0, "", false
	}
	rest := trimmedLine[level:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	title := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(rest), "#"))
	title = strings.TrimSpace(title)
	if title == "" {
		title = "(untitled)"
	}
	return level, truncateChunkRunes(title, maxHeadingTitleRunes), true
}

// splitOversized returns the section body as one piece when it fits, or
// paragraph-packed windows otherwise. A single paragraph longer than the
// chunk cap is hard-split into fixed rune windows.
func splitOversized(body string) []string {
	if runeLen(body) <= maxChunkRunes {
		return []string{body}
	}
	paragraphs := splitParagraphs(body)
	pieces := make([]string, 0, len(paragraphs))
	var window strings.Builder
	windowRunes := 0
	flush := func() {
		if window.Len() > 0 {
			pieces = append(pieces, window.String())
			window.Reset()
			windowRunes = 0
		}
	}
	for _, paragraph := range paragraphs {
		paragraphRunes := runeLen(paragraph)
		if paragraphRunes > maxChunkRunes {
			flush()
			pieces = append(pieces, splitRuneWindows(paragraph, maxChunkRunes)...)
			continue
		}
		// +2 accounts for the blank-line separator between packed paragraphs.
		if windowRunes > 0 && windowRunes+2+paragraphRunes > maxChunkRunes {
			flush()
		}
		if windowRunes > 0 {
			window.WriteString("\n\n")
			windowRunes += 2
		}
		window.WriteString(paragraph)
		windowRunes += paragraphRunes
	}
	flush()
	return pieces
}

func splitParagraphs(body string) []string {
	rawParagraphs := strings.Split(body, "\n\n")
	paragraphs := make([]string, 0, len(rawParagraphs))
	for _, paragraph := range rawParagraphs {
		if strings.TrimSpace(paragraph) == "" {
			continue
		}
		paragraphs = append(paragraphs, strings.Trim(paragraph, "\n"))
	}
	return paragraphs
}

func splitRuneWindows(value string, size int) []string {
	runes := []rune(value)
	windows := make([]string, 0, len(runes)/size+1)
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		windows = append(windows, string(runes[start:end]))
	}
	return windows
}

func isMarkdownDocument(docPath, mimeType string) bool {
	if strings.EqualFold(strings.TrimSpace(mimeType), "text/markdown") {
		return true
	}
	switch strings.ToLower(path.Ext(docPath)) {
	case ".md", ".mdx":
		return true
	}
	return false
}

// ─── Markdown link extraction ───

var (
	inlineLinkPattern    = regexp.MustCompile(`!?\[[^\]]*\]\(\s*<?([^)<>\s]+)>?(?:\s+"[^"]*")?\s*\)`)
	referenceDefPattern  = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:\s*<?(\S+?)>?\s*(?:"[^"]*")?\s*$`)
	uriSchemePattern     = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
	protocolRelativePref = "//"
)

// ExtractMarkdownLinks parses Markdown inline and reference-definition links
// pointing at package-internal relative paths. External http(s)/mailto/other
// scheme URLs, protocol-relative URLs, absolute paths, pure fragments, and
// targets escaping the package root are ignored. Fragments and query strings
// are stripped, and targets resolve relative to the source document
// directory. Results are first-seen ordered, deduplicated, and capped at
// maxLinksPerDocument. Deterministic: same input, same output.
func ExtractMarkdownLinks(docPath, content string) []string {
	targets := make([]string, 0, 16)
	seen := make(map[string]struct{})
	appendTarget := func(raw string) bool {
		resolved, ok := resolveRelativeLink(docPath, raw)
		if !ok {
			return true
		}
		if _, duplicate := seen[resolved]; duplicate {
			return true
		}
		seen[resolved] = struct{}{}
		targets = append(targets, resolved)
		return len(targets) < maxLinksPerDocument
	}

	inFence := false
	fenceMarker := ""
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if inFence {
			if strings.HasPrefix(trimmed, fenceMarker) {
				inFence = false
			}
			continue
		}
		if marker := fenceOpenMarker(trimmed); marker != "" {
			inFence = true
			fenceMarker = marker
			continue
		}
		if match := referenceDefPattern.FindStringSubmatch(line); match != nil {
			if !appendTarget(match[1]) {
				return targets
			}
			continue
		}
		for _, match := range inlineLinkPattern.FindAllStringSubmatch(line, -1) {
			if !appendTarget(match[1]) {
				return targets
			}
		}
	}
	return targets
}

// resolveRelativeLink normalizes one raw Markdown destination into a
// package-relative path, or reports it as out of scope.
func resolveRelativeLink(docPath, raw string) (string, bool) {
	target := strings.TrimSpace(raw)
	target = strings.TrimPrefix(strings.TrimSuffix(target, ">"), "<")
	if target == "" {
		return "", false
	}
	if uriSchemePattern.MatchString(target) || strings.HasPrefix(target, protocolRelativePref) {
		return "", false
	}
	if index := strings.IndexAny(target, "#?"); index >= 0 {
		target = target[:index]
	}
	if target == "" || strings.HasPrefix(target, "/") {
		return "", false
	}
	resolved := path.Clean(path.Join(path.Dir(docPath), target))
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", false
	}
	if resolved == docPath {
		return "", false
	}
	return resolved, true
}

func runeLen(value string) int {
	return len([]rune(value))
}

func truncateChunkRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
