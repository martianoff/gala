package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// utf8BOM is the UTF-8 encoding of U+FEFF. Written as explicit bytes because a
// literal BOM inside a Go source file is itself rejected by the Go parser
// ("illegal byte order mark") when it appears anywhere but the very first
// position.
const utf8BOM = "\xef\xbb\xbf"

func writeGala(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.gala")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// TestDetectPackageNameWithLeadingBOM pins the build-side half of BOM support:
// the parser accepting a BOM'd file is worthless if the builder cannot work out
// which package it declares. strings.TrimSpace does not remove U+FEFF, so the
// `package ` prefix check misses line 1 unless the BOM is stripped first.
func TestDetectPackageNameWithLeadingBOM(t *testing.T) {
	const src = "package mypkg\n\nval x = 10\n"

	assert.Equal(t, "mypkg", detectPackageName(writeGala(t, src)))
	assert.Equal(t, "mypkg", detectPackageName(writeGala(t, utf8BOM+src)))
}

// TestFindTestFunctionsWithLeadingBOM covers the test-discovery scanner, which
// reads the file line by line rather than through the parser.
func TestFindTestFunctionsWithLeadingBOM(t *testing.T) {
	const src = `package mypkg

func TestSomething(t T) T {
    return t
}
`

	plain, err := FindTestFunctions(writeGala(t, src))
	require.NoError(t, err)
	assert.Equal(t, []string{"TestSomething"}, plain)

	bommed, err := FindTestFunctions(writeGala(t, utf8BOM+src))
	require.NoError(t, err)
	assert.Equal(t, plain, bommed, "a leading BOM must not hide test functions")
}
