package scopewalk_test

import (
	"sort"
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler/scopewalk"
)

// collector records every unbound reference the walker reports.
type collector struct {
	names []string
	uses  map[string]scopewalk.Use
}

func newCollector() *collector { return &collector{uses: map[string]scopewalk.Use{}} }

func (c *collector) Reference(name string, tok antlr.Token, use scopewalk.Use) {
	if _, seen := c.uses[name]; !seen {
		c.names = append(c.names, name)
	}
	// Keep the strongest use: whole beats a field path.
	prev := c.uses[name]
	if use.Whole || prev.Whole {
		c.uses[name] = scopewalk.Use{Whole: true}
		return
	}
	c.uses[name] = use
}

func (c *collector) sorted() []string {
	out := append([]string(nil), c.names...)
	sort.Strings(out)
	return out
}

// walkFuncBody parses a whole source file and walks the body of its single
// top-level function, returning the unbound references.
func walkFuncBody(t *testing.T, src string, opts scopewalk.Options) *collector {
	t.Helper()
	p := parser.NewAntlrGalaParser()
	tree, errs := p.Parse(src)
	require.Empty(t, errs, "source must parse")
	sf, ok := tree.(*grammar.SourceFileContext)
	require.True(t, ok)

	c := newCollector()
	w := scopewalk.New(c, opts)
	w.PushScope()
	for _, td := range sf.AllTopLevelDeclaration() {
		if fd := td.FunctionDeclaration(); fd != nil {
			w.WalkFunctionDeclaration(fd)
		}
	}
	w.PopScope()
	return c
}

// TestScopeModel covers the binders every consumer relies on. A name bound by
// the construct under test must NOT surface as a reference; the sentinel
// `outer` always must, so a test that binds everything by accident still fails.
func TestScopeModel(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "parameters and locals are bound",
			body: "val a = p\nvar b = a\nreturn b + outer",
			want: []string{"outer"},
		},
		{
			name: "initializer sees the pre-binding scope",
			body: "val z = z + outer\nreturn z",
			want: []string{"outer", "z"},
		},
		{
			name: "lambda parameters shadow within their body",
			body: "val f = (q int) => q + outer\nreturn f(p)",
			want: []string{"outer"},
		},
		{
			name: "short var decl binds",
			body: "c := outer\nreturn c",
			want: []string{"outer"},
		},
		{
			name: "range vars bind, ranged value is a reference",
			body: "for i, v := range xs { Println(i + v) }\nreturn outer",
			want: []string{"Println", "outer", "xs"},
		},
		{
			name: "if-init binding does not leak past the statement",
			body: "if d := p; d > 0 { Println(d) }\nreturn outer",
			want: []string{"Println", "outer"},
		},
		{
			name: "case pattern binds its captures, not its constructor",
			body: "return p match {\n    case Some(v) => v + outer\n    case _ => 0\n}",
			want: []string{"outer"},
		},
		{
			name: "nested function name and params bind",
			body: "func helper(z int) int = z + outer\nreturn helper(p)",
			want: []string{"outer"},
		},
		{
			name: "selector and named-arg label are not references",
			body: "return p.field + g(name = p)",
			want: []string{"g"},
		},
		{
			name: "type annotations are not references",
			body: "val t Widget = mk()\nreturn p",
			want: []string{"mk"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nfunc subject(p int) int {\n    " + tc.body + "\n}\n"
			got := walkFuncBody(t, src, scopewalk.Options{})
			assert.Equal(t, tc.want, got.sorted())
		})
	}
}

// TestInterpolationDescent pins the behaviour that closed the interpolation
// blind spot: an embedded expression is walked in the current scope, so a local
// stays bound and an unknown name surfaces.
func TestInterpolationDescent(t *testing.T) {
	src := "package main\n\nfunc subject(p int) string {\n" +
		"    val local = p\n" +
		"    return s\"$local/$outer/${p + other}\"\n" +
		"}\n"

	off := walkFuncBody(t, src, scopewalk.Options{})
	assert.Empty(t, off.sorted(), "interpolation bodies are invisible when the option is off")

	on := walkFuncBody(t, src, scopewalk.Options{ParseInterpolations: true})
	assert.Equal(t, []string{"other", "outer"}, on.sorted())
}

// TestInterpolationFragmentParsing documents how far a re-parsed fragment is
// walked. ParseExpression is not anchored at end-of-input, so it consumes the
// longest valid PREFIX and reports no error for trailing garbage; only a
// fragment whose prefix is itself malformed produces errors, and that is what
// RequireCleanInterpolationParse filters.
func TestInterpolationFragmentParsing(t *testing.T) {
	strict := scopewalk.Options{
		ParseInterpolations:            true,
		RequireCleanInterpolationParse: true,
	}

	// Trailing garbage: the prefix `x` parses cleanly, so `x` is reported by
	// both modes. This is the honest limit of the option — it is not a
	// well-formedness gate on the whole fragment.
	prefixSrc := "package main\n\nfunc subject(p int) string {\n    return s\"${x +}\"\n}\n"
	assert.Equal(t, []string{"x"}, walkFuncBody(t, prefixSrc, strict).sorted())

	// A fragment whose prefix is malformed errors out and contributes nothing,
	// so ANTLR's error-recovered tree can never become a reference.
	badSrc := "package main\n\nfunc subject(p int) string {\n    return s\"${(}\"\n}\n"
	assert.Empty(t, walkFuncBody(t, badSrc, strict).sorted())
}

// TestUseClassification covers the field-path vs whole-value split the capture
// analysis depends on.
func TestUseClassification(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantWhole bool
		wantPath  string
	}{
		{name: "bare reference is whole", body: "return cfg", wantWhole: true},
		{name: "field read is a path", body: "return cfg.a", wantPath: "a"},
		{name: "nested field read is a dotted path", body: "return cfg.a.b", wantPath: "a.b"},
		{name: "method on the whole value is whole", body: "return cfg.m()", wantWhole: true},
		{name: "method on a field path keeps the path", body: "return cfg.a.b.m()", wantPath: "a.b"},
		{name: "index is whole", body: "return cfg[0]", wantWhole: true},
		{name: "match subject is whole", body: "return cfg match { case _ => 0 }", wantWhole: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nfunc subject(p int) int {\n    " + tc.body + "\n}\n"
			got := walkFuncBody(t, src, scopewalk.Options{})
			use, ok := got.uses["cfg"]
			require.True(t, ok, "cfg must be reported")
			assert.Equal(t, tc.wantWhole, use.Whole)
			if !tc.wantWhole {
				assert.Equal(t, tc.wantPath, use.Path)
			}
		})
	}
}

// TestCompositeLiteralKeyOption pins the one place the two consumers disagree
// about what counts as a value.
func TestCompositeLiteralKeyOption(t *testing.T) {
	src := "package main\n\nfunc subject(p int) Thing {\n    return Thing{Field: p, Other: q}\n}\n"

	keys := walkFuncBody(t, src, scopewalk.Options{})
	assert.Contains(t, keys.sorted(), "Field", "keys are references when the option is off")

	noKeys := walkFuncBody(t, src, scopewalk.Options{SkipCompositeLiteralKeys: true})
	assert.NotContains(t, noKeys.sorted(), "Field")
	assert.Contains(t, noKeys.sorted(), "q", "values are still walked")
}

// TestBindWholePatternOption shows the blunt fallback binding every identifier
// a pattern mentions, versus the precise split binding only its captures.
func TestBindWholePatternOption(t *testing.T) {
	src := "package main\n\nfunc subject(p int) int {\n" +
		"    return p match {\n        case Ctor(v) => v + Ctor\n    }\n}\n"

	precise := walkFuncBody(t, src, scopewalk.Options{})
	assert.Contains(t, precise.sorted(), "Ctor",
		"the precise split leaves a constructor name unbound, so the body use is reported")

	blunt := walkFuncBody(t, src, scopewalk.Options{BindWholePattern: true})
	assert.NotContains(t, blunt.sorted(), "Ctor",
		"binding the whole pattern masks the body use")
}

// TestEnterFunctionScopeHook pins the extension point the analyzer uses to bind
// type parameters and a method receiver.
func TestEnterFunctionScopeHook(t *testing.T) {
	src := "package main\n\nfunc subject(p int) int = p + extra\n"

	without := walkFuncBody(t, src, scopewalk.Options{})
	assert.Equal(t, []string{"extra"}, without.sorted())

	with := walkFuncBody(t, src, scopewalk.Options{
		EnterFunctionScope: func(w *scopewalk.Walker, fn grammar.IFunctionDeclarationContext) {
			w.Bind("extra")
		},
	})
	assert.Empty(t, with.sorted(), "the hook's bindings are in scope for the body")
}
