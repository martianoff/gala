package transformer

import (
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// TestReadTypeParamsOfPartialParse: an editor's buffer mid-edit
// (`sealed type S[T] {`, `sealed type S[] {`) parses with errors into a tree
// ANTLR's recovery leaves holes in: a type parameter without its constraint,
// or one with neither name nor constraint. Reading the sealed and opaque
// declarations off that tree, as the LSP does, does not panic.
func TestReadTypeParamsOfPartialParse(t *testing.T) {
	cases := []struct{ name, src string }{
		{name: "empty sealed list", src: "package main\n\nsealed type S[] {\n\tcase A\n}\n"},
		{name: "unclosed sealed list", src: "package main\n\nsealed type S[ {\n\tcase A\n}\n"},
		{name: "sealed parameter without constraint", src: "package main\n\nsealed type S[T] {\n\tcase A\n}\n"},
		{name: "opaque parameter without constraint", src: "package main\n\nopaque type Id[T] int64\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parsed := transpiler.NewAntlrGalaParser().ParseFile(c.src)
			require.NotEmpty(t, parsed.Errors, "the case parses cleanly")
			require.NotNil(t, parsed.Tree, "the parse gave no tree")
			var read int
			walkTree(parsed.Tree, func(n antlr.Tree) {
				switch ctx := n.(type) {
				case *grammar.SealedTypeDeclarationContext:
					require.NotPanics(t, func() { readSealedDecl(ctx) })
				case *grammar.OpaqueTypeDeclarationContext:
					require.NotPanics(t, func() { readOpaqueDecl(ctx) })
				default:
					return
				}
				read++
			})
			require.Equal(t, 1, read, "the partial parse holds no sealed or opaque declaration")
		})
	}
}
