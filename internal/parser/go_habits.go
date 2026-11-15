package parser

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
)

// The functions here find, in a parse's tokens, the Go constructs GALA has no
// form for — GALA-E0121 to GALA-E0132, and the `make` call of GALA-E0035 —
// and report each with the GALA spelling of what it does.
//
// Each used to stop the parser with a raw message ("mismatched input
// 'const'", "no viable alternative at input '[16'", "extraneous input ':'")
// that never named the GALA form, followed by a cascade of "extraneous input"
// errors for the tokens the recovery skipped. Each is found from its tokens
// alone, with shapes valid GALA never has (a Go keyword that GALA reserves as
// a name, `.(` after an operand, a `:` inside an index), so the grammar does
// not change and no program that parses today parses differently. The scan
// runs only on a parse with errors (nativeSyntaxErrors), and a construct is
// kept only in a top-level declaration with an error (inFailedDeclarations),
// so it never judges a program, or a declaration, the grammar accepts.
//
// A construct's syntax errors, and the cascade of the recovery after it, are
// replaced: the tokens are parsed again with each construct cut out, or
// respelled as the GALA token that takes its place (`const` read as `val`),
// and the syntax errors of that parse are the file's (withoutHabits). An
// unrelated syntax error elsewhere is still reported, at its own position.

// goHabit is one Go construct of a parse: its diagnostic, and how the tokens
// are parsed again without it.
type goHabit struct {
	err *galaerr.SemanticError
	// at is the construct's first token.
	at int
	// cut is the first and last token the parse is retried without; none
	// when cut[1] < cut[0].
	cut [2]int
	// retype is a token the retried parse reads as of type retypeTo, or -1.
	retype   int
	retypeTo int32
}

// habitScanner finds the Go constructs of ts's tokens (see goHabits).
type habitScanner struct {
	ts *native.GalaTokens
	// n is the number of tokens before EOF.
	n int
	// pairs is, for each bracket, the index of the bracket that matches it,
	// or -1; nil until a construct needs it.
	pairs []int32
	// tildeLines are the lines with a `~`, which the lexer has no token for
	// and reports as a token recognition error: Go's approximation
	// constraint, `~int`.
	tildeLines map[int32]bool
	// heads are the first token and the last of the head of each construct
	// reported; a `~` in one is part of it.
	heads [][2]int
	habits     []goHabit
	// claimed is the index of the first token after the head of the last
	// construct found: a `switch`'s header, a group's parentheses. A
	// construct found inside one is part of it and is not reported again
	// (the `x.(type)` of a type switch).
	claimed int
	// bodyEnd is the index of the `}` that closes the last struct or
	// interface body found; a name before it is a member's.
	bodyEnd int
}

// goHabits is the Go constructs of ts, in source order.
func goHabits(ts *native.GalaTokens) []goHabit {
	if ts == nil || len(ts.Tokens) == 0 {
		return nil
	}
	s := &habitScanner{ts: ts, n: len(ts.Tokens)}
	if ts.Tokens[s.n-1].Type == native.GalaEOF {
		s.n--
	}
	for _, e := range ts.Errors {
		if isTildeError(e) {
			if s.tildeLines == nil {
				s.tildeLines = map[int32]bool{}
			}
			s.tildeLines[int32(e.Line)] = true
		}
	}
	for i := 0; i < s.n; i++ {
		s.at(i)
	}
	s.tildes()
	// Not in source order: a construct reported from its operand (`x.(T)`,
	// `s[i:j]`) or from a `~` starts before the token it was found at;
	// nativeSyntaxErrors sorts the diagnostics.
	return s.habits
}

// isTildeError reports whether e, a lexical error, is the lexer's for a `~`,
// which GALA has no token for: Go's approximation constraint, `~int`.
func isTildeError(e native.GalaSyntaxError) bool {
	return e.Msg == "token recognition error at: '~'"
}

// at looks for a Go construct that starts at token i.
func (s *habitScanner) at(i int) {
	t := s.ts.Tokens[i].Type
	switch t {
	case native.GalaIDENTIFIER:
		// A name in a struct's or an interface's body is a member's, not a
		// statement's: `const int` is a field named const, GALA-E0055.
		if s.after(i, ".") || i < s.bodyEnd {
			return
		}
		switch s.text(i) {
		case "const":
			s.goConst(i)
		case "switch":
			s.goSwitch(i)
		case "make":
			s.goMake(i)
		case "break", "continue":
			s.labeledJump(i)
		default:
			s.label(i)
		}
	case native.GalaVAR, native.GalaVAL, native.GalaTYPE:
		s.declGroup(i)
	case native.GalaINTERFACE, native.GalaSTRUCT:
		// An unclosed body ends nowhere, and holds no later token.
		if end := s.match(i + 1); s.text(i+1) == "{" && end > i {
			s.bodyEnd = max(s.bodyEnd, end)
		}
		if t == native.GalaINTERFACE {
			s.goInterface(i)
		} else {
			s.structEmbedded(i)
		}
	case native.GalaFUNC:
		s.namedResults(i)
	case native.GalaRETURN:
		s.multiReturn(i)
	default:
		switch s.text(i) {
		case ".":
			s.typeAssertion(i)
		case "[":
			s.bracket(i)
		}
	}
}

// --- token helpers --------------------------------------------------------

func (s *habitScanner) text(i int) string {
	if i < 0 || i >= s.n {
		return ""
	}
	return s.ts.Text(i)
}

func (s *habitScanner) typ(i int) int32 {
	if i < 0 || i >= s.n {
		return native.GalaEOF
	}
	return s.ts.Tokens[i].Type
}

func (s *habitScanner) isIdent(i int) bool { return s.typ(i) == native.GalaIDENTIFIER }

func (s *habitScanner) line(i int) int32 {
	if i >= s.n {
		i = s.n - 1
	}
	return s.ts.Tokens[i].Line
}

// endLine is the line token i ends on: a raw string can span lines.
func (s *habitScanner) endLine(i int) int32 {
	return s.ts.Tokens[i].Line + int32(strings.Count(s.text(i), "\n"))
}

// after reports whether token i follows a token whose text is txt.
func (s *habitScanner) after(i int, txt string) bool { return i > 0 && s.text(i-1) == txt }

// startsStatement reports whether token i is the first of a statement: the
// first token of its line, or one after `{`, `;` or `=>`.
func (s *habitScanner) startsStatement(i int) bool {
	if i == 0 || s.endLine(i-1) < s.line(i) {
		return true
	}
	switch s.text(i - 1) {
	case "{", ";", "=>":
		return true
	}
	return false
}

// atLineEnd reports whether token i is the last of its statement: the next
// token is on a later line, closes a block, or is a `;` or the end.
func (s *habitScanner) atLineEnd(i int) bool {
	return i+1 >= s.n || s.line(i+1) > s.endLine(i) || s.text(i+1) == "}" || s.text(i+1) == ";"
}

// match is the index of the bracket matching the bracket at i, or -1.
func (s *habitScanner) match(i int) int {
	if s.pairs == nil {
		s.pairs = make([]int32, s.n)
		// A closing bracket pairs with the nearest open one of its kind;
		// the open ones above that are left unpaired, and a closing one with
		// none of its kind open pairs with nothing.
		var stack []int
		for k := 0; k < s.n; k++ {
			s.pairs[k] = -1
			switch txt := s.text(k); txt {
			case "(", "[", "{":
				stack = append(stack, k)
			case ")", "]", "}":
				open := "("
				if txt == "]" {
					open = "["
				} else if txt == "}" {
					open = "{"
				}
				for d := len(stack) - 1; d >= 0; d-- {
					if s.text(stack[d]) == open {
						s.pairs[stack[d]], s.pairs[k] = int32(k), int32(stack[d])
						stack = stack[:d]
						break
					}
				}
			}
		}
	}
	if i < 0 || i >= s.n {
		return -1
	}
	return int(s.pairs[i])
}

// closeOf is the index of the bracket matching the opening bracket at i, or,
// when it has none, the last token before EOF.
func (s *habitScanner) closeOf(i int) int {
	if c := s.match(i); c > i {
		return c
	}
	return s.n - 1
}

// isOpen reports whether token i opens a bracket.
func (s *habitScanner) isOpen(i int) bool {
	switch s.text(i) {
	case "(", "[", "{":
		return true
	}
	return false
}

// src is the source text from the start of token i to the end of token j,
// on one line; "" when j < i.
func (s *habitScanner) src(i, j int) string {
	if j < i || i < 0 || j >= s.n {
		return ""
	}
	return oneLine(s.ts.Src[s.ts.Tokens[i].Start:s.ts.Tokens[j].End])
}

// statementEnd is the last token of the statement or declaration whose first
// token is i: the last before a token on a later line, outside brackets, or
// before a `;` or a `}` that closes an enclosing block.
func (s *habitScanner) statementEnd(i int) int {
	k := i
	for k+1 < s.n {
		next := k + 1
		if s.isOpen(next) {
			k = s.closeOf(next)
			continue
		}
		switch s.text(next) {
		case ";", "}", ")", "]":
			return k
		}
		if s.line(next) > s.endLine(k) && s.text(k) != "," {
			return k
		}
		k = next
	}
	return k
}

// split is the tokens from i to j split at the commas outside brackets: each
// part's first and last token, a part with no tokens having last < first.
func (s *habitScanner) split(i, j int) [][2]int {
	var parts [][2]int
	start := i
	for k := i; k <= j; k++ {
		if s.isOpen(k) {
			k = s.closeOf(k)
			continue
		}
		if s.text(k) == "," {
			parts = append(parts, [2]int{start, k - 1})
			start = k + 1
		}
	}
	return append(parts, [2]int{start, j})
}

// endsExpression reports whether token i can end an operand, so that a `[`
// or `.` after it indexes or selects from it.
func (s *habitScanner) endsExpression(i int) bool {
	switch s.typ(i) {
	case native.GalaIDENTIFIER, native.GalaSTRING, native.GalaRAW_STRING,
		native.GalaINTERPOLATED_STRING, native.GalaFORMAT_STRING, native.GalaINT_LIT:
		return true
	}
	switch s.text(i) {
	case ")", "]", "}":
		return true
	}
	return false
}

// operand is the first token and the text of the operand that ends at token
// i — a name, a selector chain `a.b.c`, or one ending in a call or an index
// — or token i+1 and "x" when it does not start on token i's line.
func (s *habitScanner) operand(i int) (int, string) {
	start := i
	for {
		switch {
		case s.text(start) == ")" || s.text(start) == "]":
			open := s.match(start)
			if open < 0 {
				return i + 1, "x"
			}
			start = open
			if s.endsExpression(start - 1) {
				start--
				continue
			}
		case s.isIdent(start) && s.after(start, ".") && s.endsExpression(start-2):
			start -= 2
			continue
		}
		break
	}
	if !s.endsExpression(start) && s.text(start) != "(" || s.line(start) != s.line(i) {
		return i + 1, "x"
	}
	return start, s.src(start, i)
}

// typeEnd is the last token of the Go type that starts at token i: `*`s,
// `[]`s, `map[K]`, then a name with type arguments, or a function type. A
// type it cannot read ends at i.
func (s *habitScanner) typeEnd(i int) int {
	k := i
	for {
		switch {
		case s.text(k) == "*":
			k++
			continue
		case s.text(k) == "[" && s.text(k+1) == "]":
			k += 2
			continue
		case s.text(k) == "map" && s.text(k+1) == "[":
			k = s.closeOf(k+1) + 1
			continue
		}
		break
	}
	switch {
	case s.typ(k) == native.GalaFUNC && s.text(k+1) == "(":
		end := s.closeOf(k + 1)
		if s.line(end+1) == s.line(end) && (s.isIdent(end+1) || s.text(end+1) == "*" || s.text(end+1) == "[" || s.text(end+1) == "(") {
			if s.text(end+1) == "(" {
				return s.closeOf(end + 1)
			}
			return s.typeEnd(end + 1)
		}
		return end
	case s.isIdent(k):
		for s.text(k+1) == "." && s.isIdent(k+2) {
			k += 2
		}
		if s.text(k+1) == "[" && s.line(k+1) == s.line(k) {
			return s.closeOf(k + 1)
		}
		return k
	}
	return i
}

// add reports a construct from token from to token to, whose head — the
// tokens a construct inside it is part of — ends at token head, unless it
// is part of the head of the one before. The parse is retried without the
// tokens from from to to; the caller may change that on the habit add
// returns, or nil when it reports none.
func (s *habitScanner) add(code galaerr.ErrorCode, from, to, head int, msg, hint string) *goHabit {
	if from < s.claimed {
		return nil
	}
	start := s.ts.Tokens[from]
	err := galaerr.NewCodedSemanticError(code, int(start.Line), int(start.Column), msg, hint)
	if to >= from && s.endLine(to) == start.Line {
		err = err.WithSpan(int(start.Column) + len([]rune(s.ts.Src[start.Start:s.ts.Tokens[to].End])))
	}
	s.habits = append(s.habits, goHabit{err: err, at: from, cut: [2]int{from, to}, retype: -1})
	s.heads = append(s.heads, [2]int{from, head})
	s.claimed = head + 1
	return &s.habits[len(s.habits)-1]
}

// keep is no tokens to cut.
var keep = [2]int{1, 0}

// short is text, or its first runes and "..." when it is too long to quote in
// a hint.
func short(text string) string {
	const limit = 48
	if r := []rune(text); len(r) > limit {
		return string(r[:limit]) + "..."
	}
	return text
}

// --- E0121: const --------------------------------------------------------

// goConst is GALA-E0121 for `const N = 10` or `const ( ... )` at i.
func (s *habitScanner) goConst(i int) {
	if !s.startsStatement(i) {
		return // `const` as a name, GALA-E0055
	}
	// A block's first constant starts the next line or is `A = ...`; an
	// interface method named const, `const() int`, is GALA-E0055.
	if s.text(i+1) == "(" && (s.line(i+2) > s.line(i+1) || s.isIdent(i+2) && s.text(i+3) == "=") {
		end := s.closeOf(i + 1)
		hint := "declare each constant with `val`"
		if first := s.statementEnd(i + 2); i+2 < end && first < end {
			hint = fmt.Sprintf("declare each constant with `val`; here, `val %s`", short(s.src(i+2, first)))
		}
		for k := i + 2; k < end; k++ {
			if s.isIdent(k) && s.text(k) == "iota" {
				hint = "write an enumeration as a `sealed type`; Go constants whose values matter go in a `.go` file of the package"
				break
			}
		}
		s.add(galaerr.CodeGoConst, i, end, end,
			"Go `const` block: GALA declares a constant with `val`", hint)
		return
	}
	if !s.isIdent(i+1) || s.line(i+1) != s.line(i) {
		return
	}
	end := s.statementEnd(i)
	if h := s.add(galaerr.CodeGoConst, i, end, end,
		"Go `const` declaration: GALA declares a constant with `val`",
		fmt.Sprintf("write it with `val`; here, `val %s`", short(s.src(i+1, end)))); h != nil {
		h.cut, h.retype, h.retypeTo = keep, i, native.GalaVAL
	}
}

// --- E0122: var ( ... ), val ( ... ), type ( ... ) ------------------------

// declGroup is GALA-E0122 for a Go declaration group at i, `var ( ... )`. A
// `var (a, b) = t` destructures a tuple and is no group.
func (s *habitScanner) declGroup(i int) {
	if s.text(i+1) != "(" {
		return
	}
	kw := s.text(i)
	if kw != "type" {
		k := i + 2
		// `default` is a name there too (GALA-E0055).
		isName := func(k int) bool { return s.isIdent(k) || s.typ(k) == native.GalaDEFAULT }
		for isName(k) && s.text(k+1) == "," {
			k += 2
		}
		if isName(k) && s.text(k+1) == ")" {
			return // a tuple pattern
		}
	}
	end := s.closeOf(i + 1)
	hint := fmt.Sprintf("write one `%s` declaration per name, without the parentheses", kw)
	if first := s.statementEnd(i + 2); i+2 < end && first < end {
		hint = fmt.Sprintf("write one `%s` declaration per name, without the parentheses; here, `%s %s`", kw, kw, short(s.src(i+2, first)))
	}
	s.add(galaerr.CodeGoDeclGroup, i, end, end,
		fmt.Sprintf("Go declaration group `%s ( ... )`: GALA declares one name per `%s`", kw, kw), hint)
}

// --- E0123: switch -------------------------------------------------------

// goSwitch is GALA-E0123 for a Go `switch` statement at i: `switch x {`,
// `switch {`, `switch v := x.(type) {`.
func (s *habitScanner) goSwitch(i int) {
	if !s.startsStatement(i) {
		return
	}
	switch s.text(i + 1) {
	case ":=", "=", ",", ".", "+=", "-=", "*=", "/=", "++", "--", ")", "]":
		return // `switch` as a name, GALA-E0055
	}
	brace := -1
	for k := i + 1; k < s.n && s.line(k) == s.line(i); k++ {
		if s.text(k) == "{" {
			brace = k
			break
		}
		if s.isOpen(k) {
			k = s.closeOf(k)
		}
	}
	if brace < 0 {
		return
	}
	end := s.closeOf(brace)
	// The tag follows an init statement's `;`.
	tagFrom := i + 1
	for k := i + 1; k < brace; k++ {
		if s.text(k) == ";" {
			tagFrom = k + 1
		}
	}
	firstCase := s.firstCaseLabel(brace, end)
	msg, hint := "Go `switch` statement: GALA branches with `match` or `if`", ""
	switch {
	case s.text(brace-1) == ")" && s.text(brace-2) == "type" && s.text(brace-3) == "(" && s.text(brace-4) == ".":
		subject := s.src(tagFrom, brace-5)
		bind := "v"
		if s.text(tagFrom+1) == ":=" && s.isIdent(tagFrom) {
			bind = s.text(tagFrom)
			subject = s.src(tagFrom+2, brace-5)
		}
		msg = "Go type switch: GALA matches on type patterns"
		hint = fmt.Sprintf("write it as a match on type patterns; here, `%s match { case %s: %s => ... case _ => ... }`", short(subject), bind, cmp.Or(firstCase, "int"))
	case tagFrom == brace:
		hint = fmt.Sprintf("write a tagless switch as an `if` / `else if` chain; here, `if %s { ... } else { ... }`", short(cmp.Or(firstCase, "x < 0")))
	default:
		hint = fmt.Sprintf("write it as a match; here, `%s match { case ... => ... case _ => ... }`, where `case a, b:` is `case a | b =>` and `default:` is `case _ =>`", short(s.src(tagFrom, brace-1)))
	}
	s.add(galaerr.CodeGoSwitch, i, end, brace, msg, hint)
}

// firstCaseLabel is the text of the first Go `case` label of the switch body
// from brace to end, up to its first `,` or its `:`, or "".
func (s *habitScanner) firstCaseLabel(brace, end int) string {
	for k := brace + 1; k < end; k++ {
		if s.isOpen(k) {
			k = s.closeOf(k)
			continue
		}
		if s.typ(k) != native.GalaCASE {
			continue
		}
		for j := k + 1; j < end; j++ {
			if s.isOpen(j) {
				j = s.closeOf(j)
				continue
			}
			if s.text(j) == ":" || s.text(j) == "," {
				return s.src(k+1, j-1)
			}
		}
		return ""
	}
	return ""
}

// --- E0124: x.(T) --------------------------------------------------------

// typeAssertion is GALA-E0124 for a Go type assertion `x.(T)` whose `.` is
// at i.
func (s *habitScanner) typeAssertion(i int) {
	if s.text(i+1) != "(" || !s.endsExpression(i-1) {
		return
	}
	end := s.closeOf(i + 1)
	if s.text(i+2) != "type" && s.typeEnd(i+2) != end-1 {
		return // not a type between the parentheses
	}
	from, subject := s.operand(i - 1)
	typ := s.src(i+2, end-1)
	msg := fmt.Sprintf("Go type assertion `%s.(%s)`: GALA tests a value's type with `match`", short(subject), short(typ))
	if typ == "type" {
		msg = fmt.Sprintf("Go type switch guard `%s.(type)`: GALA matches on type patterns", short(subject))
		typ = "int"
	}
	bind := "v"
	if subject == "v" {
		bind = "n"
	}
	if h := s.add(galaerr.CodeGoTypeAssertion, from, end, end, msg,
		fmt.Sprintf("match on a type pattern; here, `%s match { case %s: %s => ... case _ => ... }`", short(subject), bind, short(typ))); h != nil {
		h.cut = [2]int{i, end} // the operand stays
	}
}

// --- E0125, E0126, E0127: interfaces ---------------------------------------

// goInterface is, for the `interface` at i, GALA-E0125 when it is written
// inline rather than declared, and, in a declared one, GALA-E0126 for an
// embedded interface and GALA-E0127 for a type-set element.
func (s *habitScanner) goInterface(i int) {
	if s.text(i+1) != "{" {
		return
	}
	end := s.closeOf(i + 1)
	if !s.declaresType(i) {
		msg, hint := "Go interface type written inline: GALA names an interface type",
			"declare it, `type Name interface { ... }`, and write its name here"
		if end == i+2 {
			msg, hint = "Go `interface{}`: GALA writes the empty interface as `any`", "write `any`"
		}
		if h := s.add(galaerr.CodeGoTypeForm, i, end, end, msg, hint); h != nil {
			// Read as a type name, as `any` is.
			h.cut, h.retype, h.retypeTo = [2]int{i + 1, end}, i, native.GalaIDENTIFIER
		}
		return
	}
	members := s.members(i+2, end-1)
	// A method's signature may go on over lines: `M` then `(x int) int`, or
	// `M()` then its result `*int`. needsResult is whether the method
	// before has its parameters and no result yet.
	needsResult := false
	for k, m := range members {
		from, to := m[0], m[1]
		switch {
		case s.text(from) == "(":
			// The parameters of the method named on the line before.
			needsResult = s.closeOf(from) == to
		case needsResult && s.startsType(from):
			needsResult = false // its result
		case s.isUnion(from, to):
			needsResult = false
			s.typeSet(from, to)
		case s.isSoleType(from, to) && (k+1 == len(members) || s.text(members[k+1][0]) != "("):
			needsResult = false
			name := s.src(from, to)
			if s.tildeLines[s.line(from)] || isGoPredeclaredType(name) {
				s.typeSet(from, to)
				continue
			}
			s.add(galaerr.CodeGoEmbedded, from, to, to,
				fmt.Sprintf("Go embedded interface `%s`: a GALA interface lists its methods", short(name)),
				fmt.Sprintf("write the methods of `%s` in this interface", short(name)))
		default:
			// A method: its name, type parameters and parameters, and
			// perhaps its result.
			needsResult = s.text(to) == ")" && s.text(s.match(to)-1) != "func"
		}
	}
}

// startsType reports whether token i can start a type.
func (s *habitScanner) startsType(i int) bool {
	switch {
	case s.isIdent(i), s.typ(i) == native.GalaFUNC:
		return true
	}
	switch s.text(i) {
	case "*", "[", "map":
		return true
	}
	return false
}

// declaresType reports whether the `interface` or `struct` at i is the type
// of a type declaration, `type Name[T any] interface`.
func (s *habitScanner) declaresType(i int) bool {
	p := i - 1
	if s.text(p) == "]" {
		p = s.match(p) - 1
	}
	return s.isIdent(p) && s.typ(p-1) == native.GalaTYPE
}

// members is the first and last token of each line of the body from i to j
// of a struct or interface: the tokens from one that starts a line, outside
// brackets, to the next such one, or to a `;`.
func (s *habitScanner) members(i, j int) [][2]int {
	var out [][2]int
	from := i
	for k := i; k <= j; {
		if s.text(k) == ";" {
			if from < k {
				out = append(out, [2]int{from, k - 1})
			}
			k++
			from = k
			continue
		}
		last := k
		if s.isOpen(k) {
			last = min(s.closeOf(k), j)
		}
		next := last + 1
		if next > j || s.line(next) > s.endLine(last) {
			out = append(out, [2]int{from, last})
			from = next
		}
		k = next
	}
	return out
}

// isUnion reports whether the member from i to j has a `|` outside brackets.
func (s *habitScanner) isUnion(i, j int) bool {
	for k := i; k <= j; k++ {
		if s.isOpen(k) {
			k = s.closeOf(k)
			continue
		}
		if s.text(k) == "|" {
			return true
		}
	}
	return false
}

// isSoleType reports whether the tokens from i to j are a type name alone,
// `*`? `pkg.Name[T]`: an embedded field or interface.
func (s *habitScanner) isSoleType(i, j int) bool {
	if s.text(i) == "*" {
		i++
	}
	if !s.isIdent(i) {
		return false
	}
	for s.text(i+1) == "." && s.isIdent(i+2) {
		i += 2
	}
	if s.text(i+1) == "[" {
		i = s.closeOf(i + 1)
	}
	return i == j
}

// typeSet is GALA-E0127 for the type-set constraint from token i to j.
func (s *habitScanner) typeSet(i, j int) *goHabit {
	text := s.src(i, j)
	// A `~` before the first type is no token; it starts the constraint.
	tilde := s.ts.Tokens[i].Start > 0 && s.ts.Src[s.ts.Tokens[i].Start-1] == '~'
	if tilde {
		text = "~" + text
	}
	h := s.add(galaerr.CodeGoTypeSetConstraint, i, j, j, typeSetMessage(text), typeSetHint)
	if h == nil {
		return nil
	}
	if tilde {
		h.err.Column--
	}
	return h
}

const typeSetHint = "use a constraint from Go, such as `cmp.Ordered`; a type-set interface of your own is declared in a `.go` file of the package"

func typeSetMessage(text string) string {
	return fmt.Sprintf("Go type-set constraint `%s`: GALA has no union or `~` constraints", short(text))
}

// tildes is GALA-E0127 for each `~` outside a type-set constraint already
// reported: `[T ~int]`, whose constraint otherwise parses as `int`.
func (s *habitScanner) tildes() {
	for e, l := range s.ts.Errors {
		if !isTildeError(l) {
			continue
		}
		at := min(int(s.ts.ErrorAt[e]), s.n-1)
		if slices.ContainsFunc(s.heads, func(r [2]int) bool { return r[0] <= at && at <= r[1] }) {
			continue
		}
		end := at
		if s.isIdent(at) {
			end = s.typeEnd(at)
		}
		err := galaerr.NewCodedSemanticError(galaerr.CodeGoTypeSetConstraint, l.Line, l.Column,
			typeSetMessage("~"+s.src(at, end)), typeSetHint)
		s.habits = append(s.habits, goHabit{err: err, at: at, cut: keep, retype: -1})
	}
}

// isGoPredeclaredType reports whether name is one of Go's predeclared types
// that is no interface, which, alone in an interface, is a type-set element.
// `error`, `any` and `comparable` are interfaces, embedded.
func isGoPredeclaredType(name string) bool {
	switch name {
	case "bool", "string", "byte", "rune", "uintptr",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "complex64", "complex128":
		return true
	}
	return false
}

// --- E0126: struct embedding ----------------------------------------------

// structEmbedded is GALA-E0126 for each embedded field, `Base` or `*Base`,
// of the struct type at i.
func (s *habitScanner) structEmbedded(i int) {
	if s.text(i+1) != "{" {
		return
	}
	end := s.closeOf(i + 1)
	members := s.members(i+2, end-1)
	for k := 0; k < len(members); k++ {
		from, to := members[k][0], members[k][1]
		if !s.isSoleType(from, to) {
			continue
		}
		if next := k + 1; from == to && next < len(members) && (s.isSoleType(members[next][0], members[next][1]) ||
			!s.isIdent(members[next][0]) && s.startsType(members[next][0])) {
			// A field whose type is on the next line, `Name` then `string`.
			k++
			continue
		}
		typ := s.src(from, to)
		name := strings.TrimPrefix(typ, "*")
		if k := strings.IndexByte(name, '['); k >= 0 {
			name = name[:k]
		}
		if k := strings.LastIndexByte(name, '.'); k >= 0 {
			name = name[k+1:]
		}
		s.add(galaerr.CodeGoEmbedded, from, to, to,
			fmt.Sprintf("Go embedded field `%s`: GALA struct fields are named", short(typ)),
			fmt.Sprintf("name the field; here, `%s %s`, and reach its members through it (`v.%s.Field`)", name, short(typ), name))
	}
}

// --- E0125, E0128, E0131, E0127: brackets ------------------------------------

// bracket looks at the `[` at i for a fixed-size array type (GALA-E0125), a
// slice expression (GALA-E0128), and, in a declaration's type parameters,
// grouped type parameters (GALA-E0131) and a type-set constraint
// (GALA-E0127).
func (s *habitScanner) bracket(i int) {
	if s.declaresTypeParams(i) {
		s.typeParams(i)
		return
	}
	if s.fixedArray(i) {
		return
	}
	if s.endsExpression(i - 1) {
		s.sliceExpr(i)
	}
}

// fixedArray is GALA-E0125 for a Go array type `[16]byte` or `[...]T` at i,
// reporting whether it is one.
func (s *habitScanner) fixedArray(i int) bool {
	size := s.text(i + 1)
	if s.typ(i+1) != native.GalaINT_LIT && size != "..." || s.text(i+2) != "]" || s.line(i+3) != s.line(i) {
		return false
	}
	// The element type, after every array dimension: `[4][2]int` is one.
	elem := i + 3
	for s.text(elem) == "[" && (s.typ(elem+1) == native.GalaINT_LIT || s.text(elem+1) == "...") && s.text(elem+2) == "]" {
		elem += 3
	}
	if !s.isIdent(elem) && (s.endsExpression(i-1) || s.text(elem) != "*" && s.text(elem) != "[") {
		return false
	}
	end := s.typeEnd(elem)
	t := s.src(i+3, end)
	hint := fmt.Sprintf("use a Go slice, `[]%s` (`go_interop.SliceWithSize[%s](%s)` makes one of %s elements), or an `Array[%s]`; a Go API that needs `[%s]%s` keeps it in a `.go` file of the package",
		short(t), short(t), size, size, short(t), size, short(t))
	if size == "..." {
		// The length is the literal's element count: the elements make it.
		hint = fmt.Sprintf("use a Go slice of the elements, `go_interop.SliceOf(...)`, or an `Array[%s]` from `ArrayOf(...)`; a Go API that needs an array keeps it in a `.go` file of the package", short(t))
	}
	if h := s.add(galaerr.CodeGoTypeForm, i, end, end,
		fmt.Sprintf("Go array type `[%s]%s`: GALA has no fixed-size arrays", size, short(t)), hint); h != nil {
		h.cut = [2]int{i, elem - 1} // the element type stays
	}
	return true
}

// sliceExpr is GALA-E0128 for a Go slice expression `s[i:j]` whose `[` is at
// i.
func (s *habitScanner) sliceExpr(i int) {
	end := s.match(i)
	if end < 0 {
		return
	}
	var colons []int
	for k := i + 1; k < end; k++ {
		if s.isOpen(k) {
			k = s.closeOf(k)
			continue
		}
		if s.text(k) == ":" {
			colons = append(colons, k)
		}
	}
	if len(colons) == 0 {
		return
	}
	from, subject := s.operand(i - 1)
	lo := s.src(i+1, colons[0]-1)
	hiEnd := end - 1
	if len(colons) > 1 {
		hiEnd = colons[1] - 1
	}
	hi := s.src(colons[0]+1, hiEnd)
	var method, interop string
	switch {
	case lo != "" && hi != "":
		method = fmt.Sprintf(".Slice(%s, %s)", lo, hi)
		interop = fmt.Sprintf("go_interop.Slice(%s, %s, %s)", subject, lo, hi)
	case lo != "":
		method = fmt.Sprintf(".Drop(%s)", lo)
		interop = fmt.Sprintf("go_interop.SliceDrop(%s, %s)", subject, lo)
	case hi != "":
		method = fmt.Sprintf(".Take(%s)", hi)
		interop = fmt.Sprintf("go_interop.SliceTake(%s, %s)", subject, hi)
	}
	text := s.src(i, end)
	msg := fmt.Sprintf("Go slice expression `%s%s`: GALA has no slice syntax", short(subject), short(text))
	hint := fmt.Sprintf("write `%s` itself: a slice of the whole is the value", short(subject))
	if method != "" {
		hint = fmt.Sprintf("call a method instead; here, `%s%s` on an `Array` or a `List`, `%s` on a Go slice", short(subject), method, short(interop))
	}
	if h := s.add(galaerr.CodeGoSliceExpr, from, end, end, msg, hint); h != nil {
		h.cut = [2]int{i, end} // the operand stays
	}
}

// declaresTypeParams reports whether the `[` at i opens the type parameters
// of a declaration: after the name of a `func`, a method, a `type` or a
// shorthand `struct`.
func (s *habitScanner) declaresTypeParams(i int) bool {
	if !s.isIdent(i - 1) {
		return false
	}
	switch s.typ(i - 2) {
	case native.GalaFUNC, native.GalaTYPE, native.GalaSTRUCT:
		return true
	}
	// An interface method's, `Get[K any](k K) V` at the start of a line of
	// an interface body.
	if end := s.match(i); i-1 < s.bodyEnd && s.startsStatement(i-1) && end > i && s.text(end+1) == "(" {
		return true
	}
	// A method's, `func (r R) Name[T any](`; a function type's result,
	// `func(B) Either[A, C]`, is no declaration.
	if s.text(i-2) == ")" {
		open := s.match(i - 2)
		end := s.match(i)
		return open > 0 && s.typ(open-1) == native.GalaFUNC && s.startsStatement(open-1) &&
			end > i && s.text(end+1) == "("
	}
	return false
}

// typeParams is GALA-E0131 for grouped type parameters, `[K, V any]`, and
// GALA-E0127 for a type-set constraint, in the type parameters at i.
func (s *habitScanner) typeParams(i int) {
	end := s.match(i)
	if end < 0 {
		return
	}
	parts := s.split(i+1, end-1)
	grouped := false
	for k, p := range parts {
		if p[1] < p[0] || !s.isIdent(p[0]) {
			return
		}
		if p[0] == p[1] && k < len(parts)-1 {
			grouped = true
		}
	}
	if grouped {
		// Each name takes the constraint of its group: the next one written.
		spelled := make([]string, len(parts))
		constraint := "any"
		for k := len(parts) - 1; k >= 0; k-- {
			p := parts[k]
			if p[1] > p[0] {
				constraint = s.src(p[0]+1, p[1])
			}
			spelled[k] = s.text(p[0]) + " " + constraint
		}
		// The head is the `[` alone, so a type-set constraint in the list
		// is reported too.
		s.add(galaerr.CodeGoGroupedTypeParams, i, end, i,
			fmt.Sprintf("Go grouped type parameters `%s`: each GALA type parameter has its own constraint", short(s.src(i, end))),
			fmt.Sprintf("give each its constraint; here, `[%s]`", short(strings.Join(spelled, ", "))))
	}
	for _, p := range parts {
		if !s.isUnion(p[0]+1, p[1]) {
			continue
		}
		if h := s.typeSet(p[0]+1, p[1]); h != nil {
			// The first type of the union stays the constraint.
			h.cut = [2]int{s.typeEnd(p[0]+1) + 1, p[1]}
		}
	}
}

// --- E0035: make --------------------------------------------------------------

// goMake is GALA-E0035 for a call of Go's `make` on a slice, map or channel
// type at i, `make([]int, n)`, whose type argument no GALA expression can
// spell. A `make` call that does parse is the transformer's GALA-E0035.
func (s *habitScanner) goMake(i int) {
	if s.text(i+1) != "(" {
		return
	}
	end := s.closeOf(i + 1)
	parts := s.split(i+2, end-1)
	typFrom := parts[0][0]
	args := make([]string, 0, 2)
	for _, p := range parts[1:] {
		args = append(args, s.src(p[0], p[1]))
	}
	var hint string
	switch {
	case s.text(typFrom) == "[" && s.text(typFrom+1) == "]":
		elem := s.src(typFrom+2, parts[0][1])
		switch {
		case len(args) == 0:
			hint = fmt.Sprintf("use `go_interop.SliceEmpty[%s]()`", elem)
		case len(args) == 1:
			hint = fmt.Sprintf("use `go_interop.SliceWithSize[%s](%s)`", elem, args[0])
		case args[0] == "0":
			hint = fmt.Sprintf("use `go_interop.SliceWithCapacity[%s](%s)`", elem, args[1])
		default:
			hint = fmt.Sprintf("use `go_interop.SliceWithSizeAndCapacity[%s](%s, %s)`", elem, args[0], args[1])
		}
		hint += "; an `Array` from collection_immutable needs no size"
	case s.text(typFrom) == "map" && s.text(typFrom+1) == "[":
		keyEnd := s.closeOf(typFrom + 1)
		kv := s.src(typFrom+2, keyEnd-1) + ", " + s.src(keyEnd+1, parts[0][1])
		if len(args) == 0 {
			hint = fmt.Sprintf("use `go_interop.MapEmpty[%s]()`", kv)
		} else {
			hint = fmt.Sprintf("use `go_interop.MapWithCapacity[%s](%s)`", kv, args[0])
		}
		hint += "; a `HashMap` from collection_immutable starts with `EmptyHashMap`"
	case s.text(typFrom) == "chan":
		hint = "GALA has no channels of its own: use a `Future` or `Promise`, or an `EventBus` from `concurrent`; a channel a Go API needs is made in a `.go` file of the package"
	default:
		return // parses: the transformer's GALA-E0035
	}
	if h := s.add(galaerr.CodeForbiddenGoBuiltin, i, end, end,
		fmt.Sprintf("bare Go builtin %q is not part of GALA's surface", "make(...)"), hint); h != nil {
		h.cut = [2]int{i + 1, end} // `make` stays, as a value
	}
}

// --- E0129: labels -----------------------------------------------------------

const labelHint = "leave the outer loop through a flag `var` that its condition tests, or move the loops into a function and `return`; `break` and `continue` alone act on the innermost loop"

// label is GALA-E0129 for a Go label, `outer:`, at i: a name that starts its
// line, followed by `:` and the loop it labels.
func (s *habitScanner) label(i int) {
	if s.text(i+1) != ":" || !s.startsStatement(i) {
		return
	}
	switch next := s.text(i + 2); {
	// Not `{`: a keyed element's value can be a partial function,
	// `handler: { case 1 => a }`.
	case s.typ(i+2) == native.GalaFOR, next == "switch", next == "select":
	default:
		return
	}
	s.add(galaerr.CodeGoLabel, i, i+1, i+1,
		fmt.Sprintf("Go label `%s:`: GALA has no labeled statements", s.text(i)), labelHint)
}

// labeledJump is GALA-E0129 for `break outer` or `continue outer` at i.
func (s *habitScanner) labeledJump(i int) {
	if !s.startsStatement(i) || !s.isIdent(i+1) || s.line(i+1) != s.line(i) || !s.atLineEnd(i+1) {
		return
	}
	if h := s.add(galaerr.CodeGoLabel, i, i+1, i+1,
		fmt.Sprintf("Go labeled `%s %s`: GALA's `%s` takes no label", s.text(i), s.text(i+1), s.text(i)), labelHint); h != nil {
		h.cut = [2]int{i + 1, i + 1} // the `break` stays
	}
}

// --- E0130: named results ------------------------------------------------------

// namedResults is GALA-E0130 for the named results of the `func` at i,
// `func f() (n int, err error)`.
func (s *habitScanner) namedResults(i int) {
	k := i + 1
	if s.text(k) == "(" {
		// A receiver, when a name follows it on its line; else the
		// parameters of a function literal or type.
		close := s.closeOf(k)
		if s.isIdent(close+1) && s.line(close+2) == s.line(close) && (s.text(close+2) == "(" || s.text(close+2) == "[") {
			k = close + 1
		} else {
			s.resultList(close + 1)
			return
		}
	}
	if !s.isIdent(k) {
		return
	}
	k++
	if s.text(k) == "[" {
		k = s.closeOf(k) + 1
	}
	if s.text(k) != "(" {
		return
	}
	s.resultList(s.closeOf(k) + 1)
}

// resultList is GALA-E0130 when the `(` at i, after a signature's
// parameters on their line, opens a result list with names.
func (s *habitScanner) resultList(i int) {
	if s.text(i) != "(" || s.line(i) != s.line(i-1) {
		return
	}
	end := s.closeOf(i)
	parts := s.split(i+1, end-1)
	named := false
	for _, p := range parts {
		if p[1] > p[0] && s.isIdent(p[0]) && s.namesResult(p[0]+1) {
			named = true
		}
	}
	if !named {
		return
	}
	// A name alone takes the type of its group: the next one written.
	types := make([]string, len(parts))
	typ := ""
	for k := len(parts) - 1; k >= 0; k-- {
		if p := parts[k]; p[1] > p[0] {
			typ = s.src(p[0]+1, p[1])
		}
		types[k] = typ
	}
	spelled := types[0]
	if len(types) > 1 {
		spelled = "(" + strings.Join(types, ", ") + ")"
	}
	value := "the value"
	if len(types) > 1 {
		value = "one value, a tuple or a `Try` for `(T, error)`,"
	}
	s.add(galaerr.CodeGoNamedResults, i, end, end,
		fmt.Sprintf("Go named results `%s`: GALA results have no names", short(s.src(i, end))),
		fmt.Sprintf("write the result types alone; here, `%s`, with the body giving %s rather than setting names", short(spelled), value))
}

// namesResult reports whether token i, after a name in a result list, starts
// that name's type: a name, `*`, `[]`, `map`, `func` or `chan`, but not the
// `[` of a generic type's arguments or the `.` of a qualified one.
func (s *habitScanner) namesResult(i int) bool {
	switch {
	case s.isIdent(i), s.text(i) == "*", s.text(i) == "map", s.typ(i) == native.GalaFUNC:
		return true
	case s.text(i) == "[":
		return s.text(i+1) == "]"
	}
	return false
}

// --- E0132: return a, b ---------------------------------------------------------

// multiReturn is GALA-E0132 for a Go multiple return, `return a, b`, at i.
func (s *habitScanner) multiReturn(i int) {
	if i+1 >= s.n || s.line(i+1) != s.line(i) {
		return
	}
	end := s.statementEnd(i + 1)
	parts := s.split(i+1, end)
	if len(parts) < 2 {
		return
	}
	values := make([]string, len(parts))
	for k, p := range parts {
		values[k] = s.src(p[0], p[1])
	}
	last := values[len(values)-1]
	var value string
	switch {
	case last == "nil" && len(values) == 2:
		value = fmt.Sprintf("Success(%s)", values[0])
	case last == "nil":
		value = fmt.Sprintf("Success((%s))", strings.Join(values[:len(values)-1], ", "))
	case isErrorValue(last) && len(values) == 2:
		value = fmt.Sprintf("Failure(%s)", last)
	default:
		value = "(" + strings.Join(values, ", ") + ")"
	}
	// The head is the `return` alone: a construct in a value is reported too.
	if h := s.add(galaerr.CodeGoMultiReturn, i, end, i,
		fmt.Sprintf("Go multiple return `return %s`: a GALA function returns one value", short(strings.Join(values, ", "))),
		fmt.Sprintf("return one value; here, `return %s`, a tuple for a Go result list `(A, B)` and a `Try` for `(T, error)`", short(value))); h != nil {
		h.cut = [2]int{parts[1][0] - 1, end} // `return` and the first value stay
	}
}

// isErrorValue reports whether text, the last value of a Go `return`, reads
// as an error: `err`, `e`, or a call of `errors.New` or `fmt.Errorf`.
// Another value, `hasError` among them, gives the tuple.
func isErrorValue(text string) bool {
	switch {
	case text == "err", text == "e",
		strings.HasPrefix(text, "errors.New("), strings.HasPrefix(text, "fmt.Errorf("):
		return true
	}
	return false
}

// --- the retried parse -------------------------------------------------------------

// withoutHabits is ts with the tokens of habits cut out and retyped, for the
// parse that is retried without them, and without the lexer's errors for a
// `~` (GALA-E0127). Token positions are unchanged, so that parse's errors
// point into the source as the first one's do. Comments are left out: the
// parse does not read them.
func withoutHabits(ts *native.GalaTokens, habits []goHabit) *native.GalaTokens {
	cut := make([]bool, len(ts.Tokens))
	tokens := slices.Clone(ts.Tokens)
	for _, h := range habits {
		for k := max(h.cut[0], 0); k <= h.cut[1] && k < len(cut); k++ {
			cut[k] = tokens[k].Type != native.GalaEOF
		}
		if h.retype >= 0 {
			tokens[h.retype].Type = h.retypeTo
		}
	}
	out := &native.GalaTokens{Src: ts.Src}
	// at is, for each token, the index of the first kept one at or after it.
	at := make([]int32, len(tokens)+1)
	for k, t := range tokens {
		at[k] = int32(len(out.Tokens))
		if cut[k] {
			continue
		}
		out.Tokens = append(out.Tokens, t)
		if ts.Texts != nil {
			out.Texts = append(out.Texts, ts.Texts[k])
		}
	}
	at[len(tokens)] = int32(len(out.Tokens))
	for e, l := range ts.Errors {
		if !isTildeError(l) {
			out.Errors = append(out.Errors, l)
			out.ErrorAt = append(out.ErrorAt, at[min(int(ts.ErrorAt[e]), len(tokens))])
		}
	}
	return out
}

// inFailedDeclarations is the habits that lie in a top-level declaration
// with a syntax error of ts (parseErrs, or the lexer's): a declaration that
// parses is never judged by the scan, whatever fails elsewhere in the file.
// A declaration starts at a token in the first column that can start one (a
// declaration keyword, or Go's `const`), and runs to the next such token.
func inFailedDeclarations(ts *native.GalaTokens, habits []goHabit, parseErrs []native.GalaParseError) []goHabit {
	if len(habits) == 0 {
		return nil
	}
	// decl is, for each token, the index of its declaration.
	decl := make([]int32, len(ts.Tokens))
	d := int32(0)
	for k, t := range ts.Tokens {
		if k > 0 && t.Column == 0 && startsDeclaration(ts, k) {
			d++
		}
		decl[k] = d
	}
	failed := map[int32]bool{}
	mark := func(tok int) {
		if tok >= 0 && tok < len(decl) {
			failed[decl[tok]] = true
		}
	}
	for _, e := range parseErrs {
		mark(e.Token)
	}
	for _, at := range ts.ErrorAt {
		mark(int(at))
	}
	return slices.DeleteFunc(habits, func(h goHabit) bool { return !failed[decl[h.at]] })
}

// startsDeclaration reports whether token k of ts can start a top-level
// declaration.
func startsDeclaration(ts *native.GalaTokens, k int) bool {
	switch ts.Tokens[k].Type {
	case native.GalaFUNC, native.GalaTYPE, native.GalaVAL, native.GalaVAR, native.GalaSTRUCT,
		native.GalaSEALED, native.GalaOPAQUE, native.GalaEMBED, native.GalaIMPORT, native.GalaEOF:
		return true
	case native.GalaIDENTIFIER:
		return ts.Text(k) == "const"
	}
	return false
}
