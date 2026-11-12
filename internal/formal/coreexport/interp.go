package coreexport

import (
	"strconv"
	"strings"

	"martianoff/gala/internal/interpolation"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// interpolated exports an interpolated string `s"..."`. Its text is split
// into literal runs and embedded expressions by the lexical rules every
// consumer of the token shares (internal/interpolation): `$name`, `${expr}`
// and `$$`. Each embedded expression is parsed on its own, at its place in
// the file, and exported like any other expression.
func (e *exporter) interpolated(l native.LiteralNode) Node {
	tok := e.tree.Token(l.StartIndex())
	text := l.GetText()
	content := text[2 : len(text)-1]
	// Where the content starts: past the two bytes (and runes) of `s"`.
	contentOff, line, col := int(tok.Start)+2, int(tok.Line), int(tok.Column)+2
	// at is the position of content[from:to].
	at := func(from, to int) Pos {
		l, c := interpolation.Position(content, from, line, col)
		return e.pos(l, c, contentOff+from, contentOff+to)
	}
	textRun := func(from, to int, value string) Node {
		return &InterpText{Meta: Meta{Kind: "interp-text", Pos: at(from, to)}, Value: value}
	}

	out := []Node{}
	for _, p := range interpolation.Split(content) {
		if p.IsLiteral {
			// The run's text is a Go string literal's contents once `$$` is
			// read as `$`: the transpiler writes it into one.
			value, err := strconv.Unquote(`"` + p.Text + `"`)
			if err != nil {
				return e.unsupported(UnsupportedStringEscape, l.GalaNodeRef)
			}
			out = append(out, textRun(p.Start, p.End, value))
			continue
		}
		if strings.Contains(content[p.Offset:p.End], `\"`) {
			// `\"` inside `${...}`: the expression is not its source text.
			return e.unsupported(UnsupportedInterpEscape, l.GalaNodeRef)
		}
		pl, pc := interpolation.Position(content, p.Offset, line, col)
		// Parsed as the transpiler parses it: the engine's parser, ANTLR's as the fallback.
		expr, _, err := parser.ParseExpressionCheckedAt(p.Text, pl, pc, "interpolated expression")
		if err != nil {
			return e.unsupported(UnsupportedInterpolated, l.GalaNodeRef)
		}
		sub := &exporter{tree: expr.Tree(), opts: e.opts, bom: e.bom, base: e.base + contentOff + p.Offset}
		out = append(out, sub.expr(expr.GalaNodeRef))
		if p.FormatSpec != "" {
			// The lexical split reads `%d` after `$x` as a format spec, but
			// only f"..." has format specs: in s"..." it is literal text
			// (docs/GALA.MD, "String Interpolation"; the transpiler drops
			// it, #1042).
			out = append(out, textRun(p.End, p.End+len(p.FormatSpec), p.FormatSpec))
		}
	}
	return of(e, &Interp{Text: text, Parts: out}, l.GalaNodeRef)
}
