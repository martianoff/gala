package interpolation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// exprs is the text of each embedded expression Split finds in content.
func exprs(content string) []string {
	var out []string
	for _, p := range Split(content, false) {
		if !p.IsLiteral {
			out = append(out, p.Text)
		}
	}
	return out
}

// TestSplitStringLiteralInEmbeddedExpr checks that a string literal inside
// `${...}` is opaque: a brace in it neither nests nor closes the block, and
// an escaped backslash in it does not unclose it (#1294).
func TestSplitStringLiteralInEmbeddedExpr(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{"close brace in string", `${strings.ReplaceAll(s, "}", "!")}`, []string{`strings.ReplaceAll(s, "}", "!")`}},
		{"open brace in string", `${strings.ReplaceAll(s, "{", "!")}`, []string{`strings.ReplaceAll(s, "{", "!")`}},
		{"both braces in strings", `[${f("{")}|${f("}")}]`, []string{`f("{")`, `f("}")`}},
		{"escaped backslash in string", `${strings.ReplaceAll(p, "\\", "/")}`, []string{`strings.ReplaceAll(p, "\\", "/")`}},
		{"escaped backslash then brace", `${f("\\", "}")}!`, []string{`f("\\", "}")`}},
		// Unchanged forms: escaped quotes outside a string literal, nested
		// braces, and string literals without braces.
		{"escaped-quote form", `${r.Get(\"k\").GetOrElse(\"none\")}`, []string{`r.Get("k").GetOrElse("none")`}},
		{"escaped-quote form with backslash", `${f(\"a\\\")}`, []string{`f("a\\")`}},
		{"nested braces", `${xs.Map((x) => { x + 1 })}`, []string{`xs.Map((x) => { x + 1 })`}},
		{"plain string", `${strings.Repeat("ab", 3)}`, []string{`strings.Repeat("ab", 3)`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, exprs(tt.content))
		})
	}
}

func TestEndOfEmbeddedExpr(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{"simple", `${x}`, 3},
		{"brace in string", `${f("}")}x`, 8},
		{"nested", `${{}}`, 4},
		{"unterminated", `${f(`, 4},
		{"unterminated string", `${f("}`, 6},
		{"trailing backslash", `${\`, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, EndOfEmbeddedExpr(tt.content, 0))
		})
	}
}
