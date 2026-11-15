package vscode

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"martianoff/gala/internal/parser/native"
)

// TestGrammar_HighlightsEveryGrammarKeyword checks the TextMate grammar's
// keyword patterns against galop's generated vocabulary: every word-like
// literal token in gala.g4 ('val', 'sealed', 'opaque', …) must appear in a
// pattern of the `keywords` repository, so adding a keyword to the grammar
// without syncing the VS Code extension fails here. Contextual keywords keep
// their context in a lookahead (`default` before `case`, `map` before `[`).
func TestGrammar_HighlightsEveryGrammarKeyword(t *testing.T) {
	raw, err := os.ReadFile("syntaxes/gala.tmLanguage.json")
	if err != nil {
		t.Fatal(err)
	}
	var grammar struct {
		Repository map[string]struct {
			Patterns []struct {
				Match string `json:"match"`
			} `json:"patterns"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(raw, &grammar); err != nil {
		t.Fatal(err)
	}

	lookahead := regexp.MustCompile(`\(\?[=!][^)]*\)`)
	escape := regexp.MustCompile(`\\[a-zA-Z]`)
	word := regexp.MustCompile(`[a-z]+`)
	highlighted := make(map[string]bool)
	for _, p := range grammar.Repository["keywords"].Patterns {
		m := escape.ReplaceAllString(lookahead.ReplaceAllString(p.Match, ""), "")
		for _, w := range word.FindAllString(m, -1) {
			highlighted[w] = true
		}
	}

	literal := regexp.MustCompile(`^'([a-z]+)'$`)
	for _, lit := range native.GalaLiteralNames {
		m := literal.FindStringSubmatch(lit)
		if m == nil {
			continue
		}
		if !highlighted[m[1]] {
			t.Errorf("syntaxes/gala.tmLanguage.json does not highlight grammar keyword %q", m[1])
		}
	}
}
