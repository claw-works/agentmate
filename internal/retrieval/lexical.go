package retrieval

import (
	"strings"
	"unicode"
)

// Lexical projection for CJK-capable full-text search.
//
// PostgreSQL's 'simple' text search configuration does not segment CJK script:
// a whole Chinese sentence collapses into a single token, so a query token
// never matches an indexed token and the lexical leg of hybrid retrieval
// returns nothing for CJK text. Hybrid search then silently degrades to
// semantic-only.
//
// Rather than depend on a database extension or a dictionary-based segmenter,
// text is projected into overlapping character bigrams. This is deliberately
// not word segmentation: there is no dictionary, so the rule can never go
// stale as vocabulary grows, and any substring of two or more characters stays
// matchable (a dictionary segmenter would bind "渐进披露" into one token and
// then fail to match the query "披露").
//
// ASCII runs are kept as whole lowercased words instead of being split, so
// identifiers such as "pax_global_header" or "text-embedding-v4" retain exact
// matching, which is the one thing embeddings are poor at.
//
// Index side and query side MUST use these same functions; that shared rule is
// the correctness condition of the whole scheme.

// isCJK reports whether a rune belongs to a script that the 'simple'
// configuration cannot segment.
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// lexicalTokens splits text into CJK bigrams and ASCII-ish words.
//
// Runs of CJK runes become overlapping bigrams ("知识库" -> "知识", "识库"); a
// lone CJK rune is emitted as-is so single-character terms stay searchable.
// Runs of letters or digits become one lowercased word. Everything else is a
// separator, which also guarantees that no token can contain a tsquery
// metacharacter.
func lexicalTokens(text string) (bigrams []string, words []string) {
	var cjkRun []rune
	var wordRun []rune

	flushCJK := func() {
		switch len(cjkRun) {
		case 0:
		case 1:
			bigrams = append(bigrams, string(cjkRun))
		default:
			for i := 0; i+1 < len(cjkRun); i++ {
				bigrams = append(bigrams, string(cjkRun[i:i+2]))
			}
		}
		cjkRun = cjkRun[:0]
	}
	flushWord := func() {
		if len(wordRun) > 0 {
			words = append(words, strings.ToLower(string(wordRun)))
			wordRun = wordRun[:0]
		}
	}

	for _, r := range text {
		switch {
		case isCJK(r):
			flushWord()
			cjkRun = append(cjkRun, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			wordRun = append(wordRun, r)
		default:
			flushCJK()
			flushWord()
		}
	}
	flushCJK()
	flushWord()
	return bigrams, words
}

// LexicalProjection renders text as the whitespace-separated token stream that
// is stored in retrieval_documents.lexical_text and fed to to_tsvector. It is
// a derived value: always recomputable from title and content.
func LexicalProjection(parts ...string) string {
	tokens := make([]string, 0, 64)
	for _, part := range parts {
		bigrams, words := lexicalTokens(part)
		tokens = append(tokens, bigrams...)
		tokens = append(tokens, words...)
	}
	return strings.Join(tokens, " ")
}

// LexicalTSQuery builds the tsquery text matching a user query against the
// projection. Returns "" when the query carries no searchable token, which
// tells callers to skip the lexical leg instead of issuing a query that
// matches everything.
//
// CJK bigrams are OR-ed while ASCII words are AND-ed. AND-ing bigrams would
// demand that the entire query appear verbatim, which for any sentence-length
// CJK query means zero hits — that is the failure mode being fixed. Keeping
// ASCII words conjunctive preserves precision for mixed queries such as
// "pax_global_header 报错", where the identifier must be present. Recall-heavy
// CJK matching is acceptable because the lexical leg only generates candidates
// for RRF fusion; it does not decide final ranking.
func LexicalTSQuery(query string) string {
	bigrams, words := lexicalTokens(query)
	if len(bigrams) == 0 && len(words) == 0 {
		return ""
	}
	clauses := make([]string, 0, 2)
	if len(bigrams) > 0 {
		quoted := make([]string, 0, len(bigrams))
		for _, bigram := range bigrams {
			quoted = append(quoted, quoteTSQueryToken(bigram))
		}
		clause := strings.Join(quoted, " | ")
		if len(quoted) > 1 {
			clause = "(" + clause + ")"
		}
		clauses = append(clauses, clause)
	}
	for _, word := range words {
		clauses = append(clauses, quoteTSQueryToken(word))
	}
	return strings.Join(clauses, " & ")
}

// quoteTSQueryToken wraps a token in single quotes. Tokens only ever contain
// letters, digits or CJK runes (see lexicalTokens), so no tsquery
// metacharacter can appear; the escape is kept as defence in depth.
func quoteTSQueryToken(token string) string {
	return "'" + strings.ReplaceAll(token, "'", "''") + "'"
}
