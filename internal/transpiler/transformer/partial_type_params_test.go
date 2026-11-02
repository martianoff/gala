package transformer

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// TestReadTypeParamsOfPartialParse: an editor's buffer mid-edit
// (`sealed type S[T] {`, `sealed type S[] {`) parses with errors into a tree
// ANTLR's recovery leaves holes in: a type parameter without its constraint,
// or one with neither name nor constraint. Reading the sealed and opaque
// declarations off that tree's native tree (parser.Parsed.RecoveredNative)
// does not panic.
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
			tree, err := parsed.RecoveredNative().Tree()
			require.NoError(t, err, "the recovered tree has no native tree")
			var read int
			tree.Root().Walk(func(n native.GalaNodeRef) bool {
				switch n.Kind() {
				case native.GalaKIND_sealedTypeDeclaration:
					require.NotPanics(t, func() { readSealedDeclNative(native.SealedTypeDeclarationNode{GalaNodeRef: n}) })
				case native.GalaKIND_opaqueTypeDeclaration:
					require.NotPanics(t, func() { readOpaqueDeclNative(native.OpaqueTypeDeclarationNode{GalaNodeRef: n}) })
				default:
					return true
				}
				read++
				return true
			})
			require.Equal(t, 1, read, "the partial parse holds no sealed or opaque declaration")
		})
	}
}
