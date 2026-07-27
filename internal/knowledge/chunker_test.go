package knowledge

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestChunkDocumentMarkdownHeadingPaths(t *testing.T) {
	content := strings.Join([]string{
		"Intro paragraph before any heading.",
		"",
		"# Guide",
		"Guide overview.",
		"",
		"## Install",
		"Install basics.",
		"",
		"### Linux",
		"Linux steps.",
		"",
		"## Usage",
		"Usage notes.",
		"",
		"# Reference",
		"Reference body.",
	}, "\n")

	result := ChunkDocument("docs/guide.md", "text/markdown", content)
	if result.Truncated {
		t.Fatal("unexpected truncation")
	}
	wantPaths := []string{
		"",
		"Guide",
		"Guide > Install",
		"Guide > Install > Linux",
		"Guide > Usage",
		"Reference",
	}
	if len(result.Chunks) != len(wantPaths) {
		t.Fatalf("chunks = %d, want %d: %#v", len(result.Chunks), len(wantPaths), result.Chunks)
	}
	for index, chunk := range result.Chunks {
		if chunk.HeadingPath != wantPaths[index] {
			t.Fatalf("chunk %d heading path = %q, want %q", index, chunk.HeadingPath, wantPaths[index])
		}
		wantKey := fmt.Sprintf("chunk-%04d", index)
		if chunk.Key != wantKey {
			t.Fatalf("chunk %d key = %q, want %q", index, chunk.Key, wantKey)
		}
	}
	if !strings.Contains(result.Chunks[3].Content, "Linux steps.") || !strings.Contains(result.Chunks[3].Content, "### Linux") {
		t.Fatalf("section body must keep its heading line: %q", result.Chunks[3].Content)
	}
}

func TestChunkDocumentDeterministic(t *testing.T) {
	content := "# A\n" + strings.Repeat("word ", 5000) + "\n\n## B\nshort\n"
	first := ChunkDocument("a.md", "text/markdown", content)
	second := ChunkDocument("a.md", "text/markdown", content)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("chunking is not deterministic for identical input")
	}
}

func TestChunkDocumentFencedCodeHeadingsIgnored(t *testing.T) {
	content := "# Real\n\n```bash\n# not a heading\necho hi\n```\n\nAfter fence.\n"
	result := ChunkDocument("code.md", "text/markdown", content)
	if len(result.Chunks) != 1 {
		t.Fatalf("chunks = %d, want 1: %#v", len(result.Chunks), result.Chunks)
	}
	if result.Chunks[0].HeadingPath != "Real" {
		t.Fatalf("heading path = %q", result.Chunks[0].HeadingPath)
	}
	if !strings.Contains(result.Chunks[0].Content, "# not a heading") {
		t.Fatal("fenced content must stay in the chunk body")
	}
}

func TestChunkDocumentOversizedSectionSplitsByParagraph(t *testing.T) {
	paragraphA := strings.Repeat("alpha ", 1000) // ~6000 runes
	paragraphB := strings.Repeat("beta ", 1000)  // ~5000 runes
	content := "# Big\n" + paragraphA + "\n\n" + paragraphB + "\n"
	result := ChunkDocument("big.md", "text/markdown", content)
	if len(result.Chunks) < 2 {
		t.Fatalf("oversized section must split, chunks = %d", len(result.Chunks))
	}
	for index, chunk := range result.Chunks {
		if runeLen(chunk.Content) > maxChunkRunes {
			t.Fatalf("chunk %d exceeds cap: %d runes", index, runeLen(chunk.Content))
		}
		if chunk.HeadingPath != "Big" {
			t.Fatalf("split chunk lost heading path: %q", chunk.HeadingPath)
		}
	}
}

func TestChunkDocumentGiantParagraphHardSplit(t *testing.T) {
	content := strings.Repeat("x", maxChunkRunes*2+100)
	result := ChunkDocument("plain.txt", "text/plain", content)
	if len(result.Chunks) != 3 {
		t.Fatalf("chunks = %d, want 3", len(result.Chunks))
	}
	for _, chunk := range result.Chunks {
		if runeLen(chunk.Content) > maxChunkRunes {
			t.Fatalf("hard split chunk exceeds cap: %d", runeLen(chunk.Content))
		}
	}
}

func TestChunkDocumentTruncationCap(t *testing.T) {
	var builder strings.Builder
	for index := 0; index < maxChunksPerDocument+20; index++ {
		fmt.Fprintf(&builder, "# H%d\nbody %d\n\n", index, index)
	}
	result := ChunkDocument("many.md", "text/markdown", builder.String())
	if !result.Truncated {
		t.Fatal("expected truncation marker")
	}
	if len(result.Chunks) != maxChunksPerDocument {
		t.Fatalf("chunks = %d, want %d", len(result.Chunks), maxChunksPerDocument)
	}
}

func TestChunkDocumentNonMarkdownParagraphs(t *testing.T) {
	content := "first paragraph\n\nsecond paragraph\n"
	result := ChunkDocument("notes.txt", "text/plain", content)
	if len(result.Chunks) != 1 {
		t.Fatalf("small text should stay one chunk, got %d", len(result.Chunks))
	}
	if result.Chunks[0].HeadingPath != "" {
		t.Fatalf("non-markdown chunk must not have a heading path: %q", result.Chunks[0].HeadingPath)
	}
}

func TestChunkDocumentEmptyContent(t *testing.T) {
	result := ChunkDocument("empty.md", "text/markdown", "   \n\n  ")
	if len(result.Chunks) != 0 || result.Truncated {
		t.Fatalf("empty content must produce no chunks: %#v", result)
	}
}

func TestExtractMarkdownLinks(t *testing.T) {
	tests := []struct {
		name    string
		docPath string
		content string
		want    []string
	}{
		{
			name:    "relative sibling",
			docPath: "raw/guide.md",
			content: "[faq](faq.md)",
			want:    []string{"raw/faq.md"},
		},
		{
			name:    "dot slash and subdirectory",
			docPath: "raw/guide.md",
			content: "[a](./a.md) and [b](sub/b.md)",
			want:    []string{"raw/a.md", "raw/sub/b.md"},
		},
		{
			name:    "parent within package",
			docPath: "raw/deep/page.md",
			content: "[up](../top.md)",
			want:    []string{"raw/top.md"},
		},
		{
			name:    "parent escaping package ignored",
			docPath: "guide.md",
			content: "[escape](../outside.md)",
			want:    []string{},
		},
		{
			name:    "absolute path ignored",
			docPath: "raw/guide.md",
			content: "[abs](/etc/passwd)",
			want:    []string{},
		},
		{
			name:    "external and mailto ignored",
			docPath: "raw/guide.md",
			content: "[web](https://example.com/x.md) [mail](mailto:a@b.c) [proto](//cdn.example.com/y.md)",
			want:    []string{},
		},
		{
			name:    "fragment and query stripped",
			docPath: "raw/guide.md",
			content: "[section](faq.md#install) [tracked](howto.md?ref=1)",
			want:    []string{"raw/faq.md", "raw/howto.md"},
		},
		{
			name:    "pure fragment ignored",
			docPath: "raw/guide.md",
			content: "[here](#anchor)",
			want:    []string{},
		},
		{
			name:    "self link ignored",
			docPath: "raw/guide.md",
			content: "[me](guide.md#top)",
			want:    []string{},
		},
		{
			name:    "reference definition",
			docPath: "raw/guide.md",
			content: "See [faq][1].\n\n[1]: faq.md \"FAQ\"",
			want:    []string{"raw/faq.md"},
		},
		{
			name:    "image target counts",
			docPath: "raw/guide.md",
			content: "![diagram](assets/arch.png)",
			want:    []string{"raw/assets/arch.png"},
		},
		{
			name:    "angle bracket destination",
			docPath: "raw/guide.md",
			content: "[spaced](<faq.md>)",
			want:    []string{"raw/faq.md"},
		},
		{
			name:    "links in fenced code ignored",
			docPath: "raw/guide.md",
			content: "```\n[hidden](secret.md)\n```\n[real](faq.md)",
			want:    []string{"raw/faq.md"},
		},
		{
			name:    "deduplicated first seen order",
			docPath: "raw/guide.md",
			content: "[a](faq.md) [b](./faq.md) [c](other.md)",
			want:    []string{"raw/faq.md", "raw/other.md"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ExtractMarkdownLinks(test.docPath, test.content)
			if len(got) == 0 && len(test.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("links = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestExtractMarkdownLinksCap(t *testing.T) {
	var builder strings.Builder
	for index := 0; index < maxLinksPerDocument+50; index++ {
		fmt.Fprintf(&builder, "[l%d](target-%d.md)\n", index, index)
	}
	links := ExtractMarkdownLinks("guide.md", builder.String())
	if len(links) != maxLinksPerDocument {
		t.Fatalf("links = %d, want cap %d", len(links), maxLinksPerDocument)
	}
}

func TestExtractMarkdownLinksDeterministic(t *testing.T) {
	content := "[a](a.md) [b](b.md)\n[c]: c.md\n"
	first := ExtractMarkdownLinks("raw/x.md", content)
	second := ExtractMarkdownLinks("raw/x.md", content)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("link extraction is not deterministic")
	}
}

// TestChunkDocumentSkipsHeadingOnlySections guards a real-corpus defect: a
// parent heading immediately followed by a subheading produced a chunk whose
// whole body was the heading line, so retrieval could return a hit with no
// evidence. The heading context must instead be prepended to the next chunk.
func TestChunkDocumentSkipsHeadingOnlySections(t *testing.T) {
	content := "# 故障排查\n\n" +
		"常见问题见 faq.md。\n\n" +
		"## 检索问题\n\n" +
		"### 症状：搜不到\n\n" +
		"确认已执行索引。\n\n" +
		"## 空章节\n"

	result := ChunkDocument("raw/troubleshooting.md", "text/markdown", content)

	for _, chunk := range result.Chunks {
		if !hasProse(chunk.Content) {
			t.Fatalf("chunk %q has no prose: %q", chunk.Key, chunk.Content)
		}
	}
	if len(result.Chunks) != 2 {
		t.Fatalf("chunks = %d, want 2: %#v", len(result.Chunks), result.Chunks)
	}

	// The heading-only "## 检索问题" section must survive as context on the
	// following chunk, which keeps the more specific heading path.
	second := result.Chunks[1]
	if !strings.Contains(second.Content, "## 检索问题") {
		t.Fatalf("heading context was dropped: %q", second.Content)
	}
	if !strings.Contains(second.Content, "确认已执行索引。") {
		t.Fatalf("prose missing from merged chunk: %q", second.Content)
	}
	if second.HeadingPath != "故障排查 > 检索问题 > 症状：搜不到" {
		t.Fatalf("heading path = %q", second.HeadingPath)
	}

	// A trailing heading with no prose anywhere after it produces no chunk.
	for _, chunk := range result.Chunks {
		if strings.Contains(chunk.Content, "## 空章节") && !hasProse(chunk.Content) {
			t.Fatalf("trailing empty heading became a chunk: %q", chunk.Content)
		}
	}
}

func TestHasProse(t *testing.T) {
	for _, testCase := range []struct {
		body string
		want bool
	}{
		{body: "## Only heading\n\n", want: false},
		{body: "# A\n## B\n### C\n", want: false},
		{body: "", want: false},
		{body: "## Heading\n\ntext\n", want: true},
		{body: "plain text\n", want: true},
		{body: "## H\n\n- list item\n", want: true},
	} {
		if got := hasProse(testCase.body); got != testCase.want {
			t.Errorf("hasProse(%q) = %v, want %v", testCase.body, got, testCase.want)
		}
	}
}
