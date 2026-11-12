package parser

import (
	"os"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
)

// brokenBenchTexts are collection_immutable's array.gala, about 1,200 lines,
// broken two ways in its middle: an unfinished declaration, and a call left
// unclosed as when one is being typed.
// It fails b when array.gala no longer has the code it breaks. The language
// server's BenchmarkBrokenKeystroke makes the same edits.
func brokenBenchTexts(b *testing.B, src string) map[string]string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	find := func(from int, sub string) int {
		i := strings.Index(src[from:], sub)
		if i < 0 {
			b.Fatalf("array.gala has no %q after offset %d", sub, from)
		}
		return from + i
	}
	mid := find(len(src)/2, "\nfunc ")
	call := find(mid, "builder.Add(") + len("builder.Add(")
	end := find(call, ")\n")
	return map[string]string{
		"unfinished declaration": src[:mid] + "\nfunc broken(a int = a +\n" + src[mid:],
		"unclosed call":          src[:end] + src[end+1:],
	}
}

// BenchmarkParseBrokenFile parses a file with a syntax error with galop's
// engine and takes its error-recovered tree, as the language server and
// `gala build` do (see brokenBenchTexts).
func BenchmarkParseBrokenFile(b *testing.B) {
	path, err := bazel.Runfile("collection_immutable/array.gala")
	if err != nil {
		b.Skipf("collection_immutable/array.gala is not staged: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	for name, text := range brokenBenchTexts(b, string(data)) {
		b.Run(name, func(b *testing.B) {
			withEngine(EngineGalop, func() {
				if len(NewAntlrGalaParser().ParseFile(text).Errors) == 0 {
					b.Fatal("the broken text parses")
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if NewAntlrGalaParser().ParseFile(text).RecoveredNative().TreeOnly() == nil {
						b.Fatal("no recovered tree")
					}
				}
			})
		})
	}
}
