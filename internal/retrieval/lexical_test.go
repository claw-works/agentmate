package retrieval

import (
	"strings"
	"testing"
)

func TestLexicalTokensSegmentsCJKIntoBigrams(t *testing.T) {
	bigrams, words := lexicalTokens("知识库")
	want := []string{"知识", "识库"}
	if strings.Join(bigrams, ",") != strings.Join(want, ",") {
		t.Fatalf("bigrams = %v, want %v", bigrams, want)
	}
	if len(words) != 0 {
		t.Fatalf("words = %v, want none", words)
	}
}

func TestLexicalTokensKeepsSingleCJKRune(t *testing.T) {
	bigrams, _ := lexicalTokens("库")
	if len(bigrams) != 1 || bigrams[0] != "库" {
		t.Fatalf("bigrams = %v, want [库]", bigrams)
	}
}

// Identifiers must stay whole: exact matching on them is the reason the
// lexical leg exists, since embeddings map near-identical identifiers to
// near-identical vectors.
func TestLexicalTokensKeepsIdentifiersWhole(t *testing.T) {
	_, words := lexicalTokens("pax_global_header and text-embedding-v4")
	joined := strings.Join(words, ",")
	for _, want := range []string{"pax", "global", "header", "text", "embedding", "v4"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("words = %v, missing %q", words, want)
		}
	}
	for _, unwanted := range []string{"pa", "ax"} {
		for _, word := range words {
			if word == unwanted {
				t.Fatalf("ASCII run was split into bigrams: %v", words)
			}
		}
	}
}

func TestLexicalTokensHandlesMixedScript(t *testing.T) {
	bigrams, words := lexicalTokens("修复 pax_global_header 报错")
	if len(bigrams) == 0 {
		t.Fatalf("expected CJK bigrams, got none (words=%v)", words)
	}
	if strings.Join(words, ",") != "pax,global,header" {
		t.Fatalf("words = %v", words)
	}
	// CJK runs must not bridge across the ASCII run between them.
	for _, bigram := range bigrams {
		if bigram == "复报" || bigram == "复p" {
			t.Fatalf("bigram bridged a script boundary: %v", bigrams)
		}
	}
}

func TestLexicalProjectionJoinsEveryPart(t *testing.T) {
	projection := LexicalProjection("标题", "正文 abc")
	for _, want := range []string{"标题", "正文", "abc"} {
		if !strings.Contains(projection, want) {
			t.Fatalf("projection %q missing %q", projection, want)
		}
	}
}

// The projection must make a partial CJK term match a longer indexed term:
// searching "披露" has to hit a document containing "渐进披露". A dictionary
// segmenter would bind "渐进披露" into one token and miss this.
func TestProjectionMatchesPartialCJKTerm(t *testing.T) {
	indexed := LexicalProjection("渐进披露机制")
	queryBigrams, _ := lexicalTokens("披露")
	if len(queryBigrams) == 0 {
		t.Fatal("query produced no bigrams")
	}
	for _, bigram := range queryBigrams {
		if !strings.Contains(indexed, bigram) {
			t.Fatalf("indexed projection %q does not contain query bigram %q", indexed, bigram)
		}
	}
}

func TestLexicalTSQueryOrsBigramsAndAndsWords(t *testing.T) {
	query := LexicalTSQuery("知识库 pax")
	if !strings.Contains(query, "|") {
		t.Fatalf("expected CJK bigrams to be OR-ed: %q", query)
	}
	if !strings.Contains(query, "&") {
		t.Fatalf("expected ASCII word to be AND-ed: %q", query)
	}
	if !strings.Contains(query, "'pax'") {
		t.Fatalf("expected quoted identifier: %q", query)
	}
}

func TestLexicalTSQuerySingleBigramNeedsNoGroup(t *testing.T) {
	query := LexicalTSQuery("知识")
	if query != "'知识'" {
		t.Fatalf("query = %q, want '知识'", query)
	}
}

// A query with nothing searchable must yield "" so callers skip the lexical
// leg rather than issuing a query that matches every row.
func TestLexicalTSQueryEmptyForUnsearchableInput(t *testing.T) {
	for _, input := range []string{"", "   ", "!!! ???", "-- ,,"} {
		if got := LexicalTSQuery(input); got != "" {
			t.Fatalf("LexicalTSQuery(%q) = %q, want empty", input, got)
		}
	}
}

// Tokens feed a ::tsquery cast, so they must never carry metacharacters.
func TestLexicalTSQueryRejectsMetacharacters(t *testing.T) {
	query := LexicalTSQuery("a & b | c ! d ( e ) f : g ' h")
	for _, meta := range []string{"&&", "||", "!", "(", ")", ":"} {
		if strings.Contains(strings.ReplaceAll(query, " & ", " "), meta) {
			t.Fatalf("query %q leaked metacharacter %q", query, meta)
		}
	}
}
