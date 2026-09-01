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
// declarations exercise ANTLR's adaptive-prediction machinery
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
// The ANTLR-generated NewgalaParser/NewgalaLexer share a single
// PredictionContextCache (and DFA slice) across every instance via
// package-level static data. That prediction-context cache is not internally
// synchronised: its Put appends to a backing Go map/slice
// (`m.store[kh] = append(...)`). Concurrent parses therefore used to perform
// unsynchronised concurrent map writes and slice appends, corrupting the
// cache and crashing the process with either a hard fault inside
// runtime.growslice/memmove or a "fatal error: concurrent map writes".
//
// This test drives the concurrent parse path from a cold cache with a
// simultaneous start barrier, which surfaces that corruption; it also flags
// the race cleanly under `go test -race`. It must remain stable after the
// parser is made concurrency-safe.
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
				if _, _, err := p.Parse(src); err != nil {
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
