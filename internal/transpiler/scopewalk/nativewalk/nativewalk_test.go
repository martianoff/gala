package nativewalk

import (
	"fmt"
	"os"
	"testing"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/testcorpus"
	"martianoff/gala/internal/transpiler/scopewalk"
)

// TestMain parses with galop's engine, whatever the build's default.
func TestMain(m *testing.M) {
	parser.RequestEngine(string(parser.EngineGalop))
	os.Exit(m.Run())
}

// recorder writes down everything a walk reports, in order: each reference
// with its site and use, and each call of the EnterFunctionScope hook.
type recorder struct{ events []string }

var (
	_ scopewalk.Visitor = (*recorder)(nil)
	_ scopewalk.Visitor = discard{}
)

func (r *recorder) Reference(name string, at scopewalk.Site, use scopewalk.Use) {
	r.events = append(r.events, fmt.Sprintf("%s at %d:%d %q %+v", name, at.Line, at.Column, at.Text, use))
}

// optionSet is one set of options the walks run under, and its hook.
type optionSet struct {
	opts  scopewalk.Options
	enter EnterFunctionScope
}

// optionSets are the option sets the walks run under: the capture analysis's,
// the undefined-symbol check's, and the blunt pattern binding. The hook
// records its call to r and binds the receiver's identifiers, as the
// analyzer's does, so a hook handed the wrong declaration changes what
// follows.
func optionSets(r *recorder) []optionSet {
	enter := func(w *scopewalk.Walker, fn native.FunctionDeclarationNode) {
		start := fn.Start()
		r.enter(parser.NativeText(fn.Identifier().GalaNodeRef), int(start.Line), int(start.Column))
		BindIdentifiersIn(w, fn.Receiver().GalaNodeRef)
	}
	return []optionSet{
		{opts: scopewalk.Options{ParseInterpolations: true}},
		{opts: scopewalk.Options{
			ParseInterpolations:            true,
			RequireCleanInterpolationParse: true,
			SkipCompositeLiteralKeys:       true,
			SkipTypedPatternArgument:       true,
		}, enter: enter},
		{opts: scopewalk.Options{BindWholePattern: true}, enter: enter},
	}
}

// enter records a call of the hook, on the function named name at line:col.
func (r *recorder) enter(name string, line, col int) {
	r.events = append(r.events, fmt.Sprintf("enter %s at %d:%d", name, line, col))
}

// walkInputs exercise what positions are counted over (non-ASCII text before
// a name, CRLF line ends) and the walk's less common forms. The CRLF input is
// spelled out: a raw string cannot hold its carriage returns.
var walkInputs = []string{
	`package main

func f(p int) string {
    val s = "héllo ✓ 日本"; val t = s"ü${p + q}ö$r"
    return s + t + u
}
`,
	"package main\r\n\r\nfunc f(p int) int {\r\n    for k = range lookup(\":=\") { Println(k) }\r\n    for i, v := range xs { Println(i + v) }\r\n    return p match {\r\n        case Some(x: int) if x > lim => x\r\n        case Pair(a, b...) => a\r\n        case _ => g(name = p, h...)\r\n    }\r\n}\r\n",
	`package main

func (b *Box[T]) m(x T = dflt) T = T{Field: x, Other: y}.v.w(z)

val h = { case n => n + k }

struct Point(x int = origin, y int = x)
`,
}

// discard is a Visitor that drops what it is told.
type discard struct{}

func (discard) Reference(string, scopewalk.Site, scopewalk.Use) {}

// BenchmarkWalk walks every source of the frozen corpus from its root with the
// undefined-symbol check's options: on the native tree converted within the
// timed walk, as the transpiler does ("native"), and on the native tree
// converted beforehand ("native-walk"). Each iteration
// parses the sources afresh, untimed.
func BenchmarkWalk(b *testing.B) {
	var texts []string
	for _, name := range testcorpus.GalaNames() {
		texts = append(texts, testcorpus.Files()[name])
	}
	opts := scopewalk.Options{ParseInterpolations: true, RequireCleanInterpolationParse: true, SkipCompositeLiteralKeys: true, SkipTypedPatternArgument: true}
	for _, mode := range []string{"native", "native-walk"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				parses := make([]*parser.Parsed, 0, len(texts))
				for _, text := range texts {
					if p := parser.NewAntlrGalaParser().ParseFile(text); p.Err() == nil {
						if mode == "native-walk" {
							p.Native.Tree()
						}
						parses = append(parses, p)
					}
				}
				b.StartTimer()
				for _, p := range parses {
					w := scopewalk.New(discard{}, opts)
					tree, err := p.Native.Tree()
					if err != nil {
						b.Fatal(err)
					}
					New(w, nil).Walk(tree.Root())
				}
			}
		})
	}
}

