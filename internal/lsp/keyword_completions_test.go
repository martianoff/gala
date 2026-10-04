package lsp

import (
	"regexp"
	"testing"

	"martianoff/gala/internal/parser/grammar"
)

// TestKeywordCompletions_CoverGrammarKeywords checks keyword completion against
// the generated parser's vocabulary: every word-like literal token in gala.g4
// ('val', 'sealed', 'opaque', …) must be offered, so adding a keyword to the
// grammar without syncing the LSP fails here.
func TestKeywordCompletions_CoverGrammarKeywords(t *testing.T) {
	offered := make(map[string]bool)
	for _, item := range keywordCompletions() {
		offered[item.Label] = true
	}

	grammar.GalaParserInit()
	word := regexp.MustCompile(`^'([a-z]+)'$`)
	for _, lit := range grammar.GalaParserStaticData.LiteralNames {
		m := word.FindStringSubmatch(lit)
		if m == nil {
			continue
		}
		if !offered[m[1]] {
			t.Errorf("keywordCompletions is missing grammar keyword %q", m[1])
		}
	}
}
