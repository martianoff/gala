package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsGoTestName(t *testing.T) {
	for name, want := range map[string]bool{
		"Test":          true,
		"TestSquare":    true,
		"Test_square":   true,
		"Test1":         true,
		"BenchmarkX":    true,
		"FuzzParse":     true,
		"Testable":      false,
		"Benchmarking":  false,
		"testSquare":    false,
		"MyTestSquare":  false,
		"gala_TestMain": false,
	} {
		assert.Equal(t, want, isGoTestName(name), name)
	}
}

// renameGoTestFuncs renames a package's test functions and every reference to
// them across its files, and nothing that only shares the name.
func TestRenameGoTestFuncs(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a_test.gen_test.go")
	b := filepath.Join(dir, "b_test.gen_test.go")
	const aSrc = `package p

//line a_test.gala:3
func TestSquare(t T) T { return helper(t) }

func BenchmarkSquare(n int) int { return n }

type S struct{ TestSquare int }

func (S) TestSquare() int { return 0 }

func helper(t T) T {
	TestSquare := 1
	_ = TestSquare
	s := S{TestSquare: 2}
	_ = s.TestSquare
	return TestOther(t)
}
`
	const bSrc = `package p

func TestOther(t T) T { return t }

var all = []func(T) T{TestSquare, TestOther}
`
	require.NoError(t, os.WriteFile(a, []byte(aSrc), 0o644))
	require.NoError(t, os.WriteFile(b, []byte(bSrc), 0o644))

	require.NoError(t, renameGoTestFuncs([]string{a, b}))

	gotA, err := os.ReadFile(a)
	require.NoError(t, err)
	assert.Equal(t, `package p

//line a_test.gala:3
func gala_TestSquare(t T) T { return helper(t) }

func gala_BenchmarkSquare(n int) int { return n }

type S struct{ TestSquare int }

func (S) TestSquare() int { return 0 }

func helper(t T) T {
	TestSquare := 1
	_ = TestSquare
	s := S{TestSquare: 2}
	_ = s.TestSquare
	return gala_TestOther(t)
}
`, string(gotA))

	gotB, err := os.ReadFile(b)
	require.NoError(t, err)
	assert.Equal(t, `package p

func gala_TestOther(t T) T { return t }

var all = []func(T) T{gala_TestSquare, gala_TestOther}
`, string(gotB))
}
