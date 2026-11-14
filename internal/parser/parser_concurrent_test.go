package parser

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// buildComplexSource generates a syntactically valid GALA source file whose
// declarations exercise the parser's adaptive-prediction machinery
// (match/lambda/generics/deeply-nested expressions). The seed varies the
// identifiers and branch counts so that distinct sources keep producing fresh
// prediction contexts, which is what forces the shared prediction-context and
// DFA caches to keep growing during a concurrent parse burst.
func buildComplexSource(seed int) string {
	var b strings.Builder
	b.WriteString("package main\n\n")
	for f := 0; f < 6; f++ {
		fmt.Fprintf(&b, "func classify%d_%d(n int) string = n match {\n", seed, f)
		for c := 0; c < 5; c++ {
			fmt.Fprintf(&b, "\tcase %d => \"v%d_%d_%d\"\n", c, seed, f, c)
		}
		b.WriteString("\tcase _ => \"other\"\n}\n\n")
		fmt.Fprintf(&b, "func compute%d_%d(a int, b int, c int) int = ((a + b) * c) - (a match {\n", seed, f)
		fmt.Fprintf(&b, "\tcase 0 => %d\n\tcase _ => a\n})\n\n", seed+f)
		fmt.Fprintf(&b, "val fn%d_%d = (val x int, var y int) => (x, y) match {\n", seed, f)
		b.WriteString("\tcase (p, q) => p + q\n\tcase _ => 0\n}\n\n")
	}
	fmt.Fprintf(&b, "func identity%d[T any](x T) T { return x }\n", seed)
	return b.String()
}

// TestParseConcurrentStress hammers the parser from many goroutines at once.
//
// galop's generated parser shares its prediction DFA caches across every
// parse, on whatever goroutine, through package-level state. (ANTLR's Go
// runtime, which GALA parsed with before, shared an unsynchronised
// prediction-context cache the same way, and concurrent parses corrupted it:
// a hard fault inside runtime.growslice/memmove or a "fatal error:
// concurrent map writes".)
//
// This test drives the concurrent parse path from a cold cache with a
// simultaneous start barrier, which surfaces such corruption; it also flags
// a race cleanly under `go test -race`.
func TestParseConcurrentStress(t *testing.T) {
	goroutines := runtime.GOMAXPROCS(0) * 4
	if goroutines < 8 {
		goroutines = 8
	}
	const iterations = 12

	// Pre-build distinct sources so the parse loop only measures parsing.
	sources := make([]string, goroutines*iterations)
	for i := range sources {
		sources[i] = buildComplexSource(i)
	}

	p := NewAntlrGalaParser()

	start := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*iterations)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			<-start // all goroutines hit the cold cache simultaneously
			for i := 0; i < iterations; i++ {
				src := sources[base*iterations+i]
				if err := p.ParseFile(src).Err(); err != nil {
					errCh <- err
					return
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		assert.NoError(t, err)
	}
}
