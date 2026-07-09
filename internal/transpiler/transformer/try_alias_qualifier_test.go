package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTryPreservesAliasedGoReturnQualifier is the transformer-tier guard for the
// mis-qualification bug: `Try(os.Stat(p))` / `Try(os.ReadDir(p))` must keep the
// package qualifier on the Go return type in the generated thunk, even when the
// current package's Go name collides with the return type's defining package.
//
// os.Stat returns (os.FileInfo, error) and os.ReadDir returns ([]os.DirEntry,
// error); os.FileInfo / os.DirEntry are aliases for io/fs.FileInfo /
// io/fs.DirEntry (package name "fs"). Transpiling in a package that is itself
// named `fs` (as the GALA `fs` stdlib package is) previously dropped that
// qualifier to a bare `FileInfo` / `DirEntry`, colliding with the package's own
// `FileInfo` struct ("cannot use _v0 (os.FileInfo) as FileInfo") or producing an
// undefined `DirEntry`. The fix qualifies it as `fs.FileInfo` / `fs.DirEntry`
// (with an io/fs import) and infers the Try type param without explicit args.
//
// Hermetic: `os` is Go stdlib, resolved via the Go SDK (available on CI through
// .bazelrc --action_env=GOROOT); no GALA package wiring beyond std for `Try`.
//
// Before the fixes (analyzer registerAliasTargetType + typeToExpr ImportPath
// guard + astTypeToTranspilerType ImportPath preservation + GetPath transitive
// lookup) this FAILS: the generated Go carries bare `FileInfo` / `DirEntry` (no
// `fs.` qualifier) and an uninstantiated `Try{}` for the element method call.
func TestTryPreservesAliasedGoReturnQualifier(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	t.Run("os.Stat return keeps its qualifier vs a colliding local struct", func(t *testing.T) {
		input := `package fs

import (
    . "martianoff/gala/std"
    "os"
)

struct FileInfo(n int)

func fromGo(fi os.FileInfo) FileInfo = FileInfo(n = 1)

func statInfo(p string) Try[FileInfo] = Try[FileInfo](() => {
    val fi = Try(os.Stat(p)).Get()
    return fromGo(fi)
})`

		out, err := trans.Transpile(input, "")
		require.NoError(t, err)

		// The inner Try over os.Stat must be qualified as fs.FileInfo (io/fs), not
		// collapsed to the bare local `FileInfo`.
		require.Contains(t, out, "Try[fs.FileInfo]",
			"the inner Try over os.Stat must infer the qualified fs.FileInfo, not a bare FileInfo:\n%s", out)
		require.Contains(t, out, `"io/fs"`,
			"io/fs must be imported for the qualified fs.FileInfo return:\n%s", out)
	})

	t.Run("os.ReadDir element method call keeps its qualifier and is instantiated", func(t *testing.T) {
		input := `package fs

import (
    . "martianoff/gala/std"
    "os"
)

func firstEntryInfo(p string) Try[os.FileInfo] = Try[os.FileInfo](() => {
    val entries = Try(os.ReadDir(p)).Get()
    return Try(entries[0].Info()).Get()
})`

		out, err := trans.Transpile(input, "")
		require.NoError(t, err)

		// os.ReadDir's []os.DirEntry return must qualify as fs.DirEntry, not a bare
		// (undefined) DirEntry.
		require.Contains(t, out, "fs.DirEntry",
			"os.ReadDir's slice element must qualify as fs.DirEntry, not a bare DirEntry:\n%s", out)
		// The element method call Try(entries[0].Info()) must resolve the method
		// set (fs.DirEntry.Info -> (fs.FileInfo, error)) so the Try is instantiated
		// and thunked — never a bare uninstantiated Try{}.
		require.Contains(t, out, "Try[fs.FileInfo]",
			"the inner Try over entries[0].Info() must infer fs.FileInfo:\n%s", out)
		require.False(t, strings.Contains(out, "Try{}"),
			"the element method Try must be instantiated, not a bare Try{}:\n%s", out)
	})
}
