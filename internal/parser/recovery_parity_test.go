package parser

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
)

// parseAgreeing parses input with galop's engine, after checking that ANTLR's
// engine gives the same parse (compareEngines: the same doc comments, the
// same errors with their codes, hints and spans, the same native tree, clean
// or error-recovered). The syntax-error tests parse through it, so each of
// their inputs holds galop's recovery and hints to ANTLR's too.
func parseAgreeing(t *testing.T, input string) *Parsed {
	t.Helper()
	_, got, diffs := compareEngines(input, false)
	for _, d := range diffs {
		t.Errorf("%q: %s", input, d)
	}
	return got.parsed
}

// mutation is a way TestRecoveryParity breaks a source file: it returns src
// changed, given its tokens, or "" when it does not apply.
type mutation struct {
	name string
	run  func(r *rand.Rand, src string, toks []native.GalaToken) string
}

// mutationTokens is what the insert mutation puts between two tokens: the
// brackets and separators recovery conjures and skips most, and the shapes
// the coded hints look for (a Go slice type, an arrow, a string where a
// struct tag would go).
var mutationTokens = []string{
	"(", ")", "{", "}", "[", "]", ",", "=", "=>", ".", ":", "val", "case", "if", "else", "match",
	"[]byte", "map[string]int", `"json:\"x\""`, "`raw`", "+", "1", "x", "\n",
}

// strayCharacters are what the stray-character mutations insert: characters
// GALA's lexer has no token for, and ones that open a literal or a comment
// that may never close.
var strayCharacters = []string{"$", "#", "\\", "\"", "'", "`", "/*", "@", "?", "~"}

// pickIndex is one of the indexes i in [0, n) where at(i) holds, drawn by r,
// or false when there is none.
func pickIndex(r *rand.Rand, n int, at func(i int) bool) (int, bool) {
	var found []int
	for i := range n {
		if at(i) {
			found = append(found, i)
		}
	}
	if len(found) == 0 {
		return 0, false
	}
	return found[r.Intn(len(found))], true
}

// mutations are the ways TestRecoveryParity breaks a file: token edits,
// which keep the rest of the file lexing as before, byte drops and stray
// characters, which can break a literal or comment open (lexical errors), a
// cut multi-byte character (invalid UTF-8), and three that aim at the coded
// hints: a type argument made a Go slice type (GALA-E0040), a lambda's
// parameter unparenthesized (GALA-E0042), and a tag after a struct field
// (GALA-E0085).
var mutations = []mutation{
	{"delete a token", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		t := toks[r.Intn(len(toks))]
		return src[:t.Start] + src[t.End:]
	}},
	{"duplicate a token", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		t := toks[r.Intn(len(toks))]
		return src[:t.End] + " " + t.Text(src) + src[t.End:]
	}},
	{"swap two tokens", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		if len(toks) < 2 {
			return ""
		}
		i := r.Intn(len(toks) - 1)
		a, b := toks[i], toks[i+1]
		return src[:a.Start] + b.Text(src) + src[a.End:b.Start] + a.Text(src) + src[b.End:]
	}},
	{"insert a token", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		t := toks[r.Intn(len(toks))]
		return src[:t.Start] + mutationTokens[r.Intn(len(mutationTokens))] + " " + src[t.Start:]
	}},
	{"drop bytes", func(r *rand.Rand, src string, _ []native.GalaToken) string {
		return breakSource(r, src)
	}},
	{"stray character", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		t := toks[r.Intn(len(toks))]
		at := int(t.Start) + r.Intn(int(t.End-t.Start)+1)
		return src[:at] + strayCharacters[r.Intn(len(strayCharacters))] + src[at:]
	}},
	{"stray character, then a token edit", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		// Two edits on one line: the lexical error just after a syntax error,
		// where the order ANTLR reports them in depends on how far its parser
		// has read.
		t := toks[r.Intn(len(toks))]
		return src[:t.Start] + mutationTokens[r.Intn(len(mutationTokens))] + " " + t.Text(src) +
			strayCharacters[r.Intn(len(strayCharacters))] + src[t.End:]
	}},
	{"cut a multi-byte character, then a token edit", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		// Invalid UTF-8, which ANTLR reads as U+FFFD, with a syntax error
		// after it: the recovered tree's offsets index the decoded text.
		i, ok := pickIndex(r, len(src), func(i int) bool { return src[i] >= 0xC0 })
		if !ok {
			return ""
		}
		t := toks[len(toks)-1-r.Intn(min(len(toks), 20))]
		if int(t.Start) <= i+1 {
			return src[:i] + src[i+1:]
		}
		return src[:i] + src[i+1:t.Start] + "( " + src[t.Start:]
	}},
	{"Go slice type argument", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		// A '[' followed by an identifier: a type argument list.
		i, ok := pickIndex(r, len(toks)-1, func(i int) bool {
			return toks[i].Text(src) == "[" && toks[i+1].Type == native.GalaIDENTIFIER
		})
		if !ok {
			return ""
		}
		t := toks[i+1]
		return src[:t.Start] + "[]byte" + src[t.End:]
	}},
	{"bare lambda parameter", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		// `(x) =>`: drop the parentheses.
		i, ok := pickIndex(r, len(toks)-3, func(i int) bool {
			return toks[i].Text(src) == "(" && toks[i+1].Type == native.GalaIDENTIFIER &&
				toks[i+2].Text(src) == ")" && toks[i+3].Text(src) == "=>"
		})
		if !ok {
			return ""
		}
		return src[:toks[i].Start] + toks[i+1].Text(src) + src[toks[i+2].End:]
	}},
	{"struct tag", func(r *rand.Rand, src string, toks []native.GalaToken) string {
		// After an identifier ending a line inside a struct-like declaration:
		// a field's type.
		i, ok := pickIndex(r, len(toks)-1, func(i int) bool {
			return toks[i].Type == native.GalaIDENTIFIER && toks[i+1].Line > toks[i].Line
		})
		if !ok {
			return ""
		}
		t := toks[i]
		return src[:t.End] + ` "json:\"f\""` + src[t.End:]
	}},
}

// TestRecoveryParity is the differential test of galop's error recovery: it
// breaks every GALA source of the repository (examples/, std/ and the rest)
// with each of mutations, parses each mutant and brokenInputs with galop's
// engine and with ANTLR's, and holds the two to compareEngines: the same
// errors, in order, with the same codes, hints and spans, the same doc
// comments, and the same error-recovered native tree. Any difference is a
// bug. It logs the match rate and how often each coded hint fired.
func TestRecoveryParity(t *testing.T) {
	inputs, broken, lexical, mismatched := 0, 0, 0, 0
	codes := map[galaerr.ErrorCode]int{}
	compare := func(what, src string) {
		inputs++
		want, _, diffs := compareEngines(src, false)
		if strings.Contains(want.out, "token recognition error") {
			lexical++
		}
		if len(want.parsed.Errors) > 0 {
			broken++
		}
		for _, err := range want.parsed.Errors {
			var se *galaerr.SemanticError
			if errors.As(err, &se) && se.Code != "" {
				codes[se.Code]++
			}
		}
		if len(diffs) > 0 {
			mismatched++
			for _, d := range diffs {
				t.Errorf("%s: %s", what, d)
			}
		}
	}
	for k, src := range brokenInputs {
		compare(fmt.Sprintf("broken input %d", k), src)
	}
	for _, path := range gatherSources(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		ts := lexNative(galaerr.StripBOM(string(data)), 0, 0)
		src, toks := ts.Src, ts.Tokens[:len(ts.Tokens)-1] // without EOF
		if len(toks) == 0 {
			continue
		}
		h := fnv.New64a()
		h.Write(data)
		r := rand.New(rand.NewSource(int64(h.Sum64())))
		for _, m := range mutations {
			if mutant := m.run(r, src, toks); mutant != "" && mutant != src {
				compare(fmt.Sprintf("%s (%s)", path, m.name), mutant)
			}
		}
	}
	var hints []string
	for c, n := range codes {
		hints = append(hints, fmt.Sprintf("%s %d", c, n))
	}
	sort.Strings(hints)
	t.Logf("%d inputs, %d with errors, %d of them lexical; %d match (%.2f%%), %d differ; coded errors: %s",
		inputs, broken, lexical, inputs-mismatched, 100*float64(inputs-mismatched)/float64(inputs),
		mismatched, strings.Join(hints, ", "))
	for _, c := range []galaerr.ErrorCode{galaerr.CodeGoTypeInExpression, galaerr.CodeBareLambdaParam, galaerr.CodeStructTag} {
		if codes[c] == 0 {
			t.Errorf("no mutant gave %s: its hint was not compared", c)
		}
	}
	if broken < inputs/2 {
		t.Errorf("only %d of %d inputs have errors: the mutations break too little", broken, inputs)
	}
}
