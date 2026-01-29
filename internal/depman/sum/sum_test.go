package sum

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_Empty(t *testing.T) {
	f, err := Parse("")
	require.NoError(t, err)
	assert.Empty(t, f.Entries)
}

func TestParse_SingleEntry(t *testing.T) {
	content := `github.com/example/utils v1.2.3 h1:abc123def456==`
	f, err := Parse(content)
	require.NoError(t, err)
	assert.Len(t, f.Entries, 1)
	assert.Equal(t, "github.com/example/utils", f.Entries[0].Path)
	assert.Equal(t, "v1.2.3", f.Entries[0].Version)
	assert.Equal(t, "", f.Entries[0].Suffix)
	assert.Equal(t, "h1:abc123def456==", f.Entries[0].Hash)
}

func TestParse_WithSuffix(t *testing.T) {
	content := `github.com/example/utils v1.2.3/gala.mod h1:xyz789==`
	f, err := Parse(content)
	require.NoError(t, err)
	assert.Len(t, f.Entries, 1)
	assert.Equal(t, "v1.2.3", f.Entries[0].Version)
	assert.Equal(t, "/gala.mod", f.Entries[0].Suffix)
}

func TestParse_MultipleEntries(t *testing.T) {
	content := `github.com/example/utils v1.2.3 h1:abc123==
github.com/example/utils v1.2.3/gala.mod h1:def456==
github.com/example/math v2.0.0 h1:xyz789==
`
	f, err := Parse(content)
	require.NoError(t, err)
	assert.Len(t, f.Entries, 3)

	entries := f.GetModuleEntries("github.com/example/utils", "v1.2.3")
	assert.Len(t, entries, 2)
}

func TestParse_Error_InvalidHash(t *testing.T) {
	content := `github.com/example/utils v1.2.3 invalidhash`
	_, err := Parse(content)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid hash format")
}

func TestParse_Error_MissingFields(t *testing.T) {
	content := `github.com/example/utils`
	_, err := Parse(content)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid format")
}

func TestFormat_Empty(t *testing.T) {
	f := NewFile()
	output := Format(f)
	assert.Equal(t, "", output)
}

func TestFormat_SingleEntry(t *testing.T) {
	f := NewFile()
	f.Add("github.com/example/utils", "v1.2.3", "", "h1:abc123==")

	output := Format(f)
	assert.Equal(t, "github.com/example/utils v1.2.3 h1:abc123==\n", output)
}

func TestFormat_WithSuffix(t *testing.T) {
	f := NewFile()
	f.Add("github.com/example/utils", "v1.2.3", "/gala.mod", "h1:abc123==")

	output := Format(f)
	assert.Equal(t, "github.com/example/utils v1.2.3/gala.mod h1:abc123==\n", output)
}

func TestFormat_Sorted(t *testing.T) {
	f := NewFile()
	f.Add("github.com/z/pkg", "v1.0.0", "", "h1:zzz==")
	f.Add("github.com/a/pkg", "v1.0.0", "", "h1:aaa==")
	f.Add("github.com/a/pkg", "v1.0.0", "/gala.mod", "h1:bbb==")

	output := Format(f)
	lines := []string{
		"github.com/a/pkg v1.0.0 h1:aaa==",
		"github.com/a/pkg v1.0.0/gala.mod h1:bbb==",
		"github.com/z/pkg v1.0.0 h1:zzz==",
	}
	expected := ""
	for _, line := range lines {
		expected += line + "\n"
	}
	assert.Equal(t, expected, output)
}

func TestRoundTrip(t *testing.T) {
	original := NewFile()
	original.Add("github.com/example/utils", "v1.2.3", "", "h1:abc123==")
	original.Add("github.com/example/utils", "v1.2.3", "/gala.mod", "h1:def456==")
	original.Add("github.com/example/math", "v2.0.0", "", "h1:xyz789==")

	formatted := Format(original)
	parsed, err := Parse(formatted)
	require.NoError(t, err)

	assert.Len(t, parsed.Entries, 3)

	// Check that entries match
	for _, orig := range original.Entries {
		found := parsed.Get(orig.Path, orig.Version, orig.Suffix)
		require.NotNil(t, found, "entry not found: %v", orig)
		assert.Equal(t, orig.Hash, found.Hash)
	}
}

func TestFile_Add_Update(t *testing.T) {
	f := NewFile()
	f.Add("github.com/example/utils", "v1.2.3", "", "h1:old==")
	f.Add("github.com/example/utils", "v1.2.3", "", "h1:new==")

	assert.Len(t, f.Entries, 1)
	assert.Equal(t, "h1:new==", f.Entries[0].Hash)
}

func TestFile_Get(t *testing.T) {
	f := NewFile()
	f.Add("github.com/example/utils", "v1.2.3", "", "h1:abc==")
	f.Add("github.com/example/utils", "v1.2.3", "/gala.mod", "h1:def==")

	entry := f.Get("github.com/example/utils", "v1.2.3", "")
	require.NotNil(t, entry)
	assert.Equal(t, "h1:abc==", entry.Hash)

	entry = f.Get("github.com/example/utils", "v1.2.3", "/gala.mod")
	require.NotNil(t, entry)
	assert.Equal(t, "h1:def==", entry.Hash)

	entry = f.Get("github.com/example/nonexistent", "v1.0.0", "")
	assert.Nil(t, entry)
}

func TestFile_Remove(t *testing.T) {
	f := NewFile()
	f.Add("github.com/example/utils", "v1.2.3", "", "h1:abc==")
	f.Add("github.com/example/utils", "v1.2.3", "/gala.mod", "h1:def==")
	f.Add("github.com/example/math", "v2.0.0", "", "h1:xyz==")

	removed := f.Remove("github.com/example/utils", "v1.2.3")
	assert.True(t, removed)
	assert.Len(t, f.Entries, 1)
	assert.Equal(t, "github.com/example/math", f.Entries[0].Path)

	removed = f.Remove("github.com/example/nonexistent", "v1.0.0")
	assert.False(t, removed)
}

func TestFile_Contains(t *testing.T) {
	f := NewFile()
	f.Add("github.com/example/utils", "v1.2.3", "", "h1:abc==")

	assert.True(t, f.Contains("github.com/example/utils", "v1.2.3"))
	assert.False(t, f.Contains("github.com/example/utils", "v2.0.0"))
	assert.False(t, f.Contains("github.com/example/other", "v1.2.3"))
}

func TestEntry_Key(t *testing.T) {
	tests := []struct {
		entry    Entry
		expected string
	}{
		{
			Entry{Path: "github.com/example/utils", Version: "v1.2.3", Suffix: ""},
			"github.com/example/utils v1.2.3",
		},
		{
			Entry{Path: "github.com/example/utils", Version: "v1.2.3", Suffix: "/gala.mod"},
			"github.com/example/utils v1.2.3/gala.mod",
		},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, tt.entry.Key())
	}
}

func TestHashDir(t *testing.T) {
	// Create a temporary directory with some test files
	tmpDir, err := os.MkdirTemp("", "gala-hash-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create test files
	err = os.WriteFile(filepath.Join(tmpDir, "lib.gala"), []byte("package lib\n"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "utils.gala"), []byte("package lib\nfunc foo() {}\n"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "gala.mod"), []byte("module github.com/test/lib\n"), 0644)
	require.NoError(t, err)

	// Hash should be deterministic
	hash1, err := HashDir(tmpDir)
	require.NoError(t, err)
	assert.True(t, len(hash1) > 3, "hash should not be empty")
	assert.True(t, hash1[:3] == "h1:", "hash should start with h1:")

	hash2, err := HashDir(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2, "hashes should be deterministic")
}

func TestHashDir_IgnoresHiddenDirs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gala-hash-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create test file
	err = os.WriteFile(filepath.Join(tmpDir, "lib.gala"), []byte("package lib\n"), 0644)
	require.NoError(t, err)

	hash1, err := HashDir(tmpDir)
	require.NoError(t, err)

	// Add hidden directory with files
	hiddenDir := filepath.Join(tmpDir, ".git")
	err = os.MkdirAll(hiddenDir, 0755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(hiddenDir, "config"), []byte("git config"), 0644)
	require.NoError(t, err)

	hash2, err := HashDir(tmpDir)
	require.NoError(t, err)

	assert.Equal(t, hash1, hash2, "hidden directories should be ignored")
}

func TestVerify_Success(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gala-verify-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	err = os.WriteFile(filepath.Join(tmpDir, "lib.gala"), []byte("package lib\n"), 0644)
	require.NoError(t, err)

	hash, err := HashDir(tmpDir)
	require.NoError(t, err)

	err = Verify(tmpDir, hash)
	assert.NoError(t, err)
}

func TestVerify_Failure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gala-verify-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	err = os.WriteFile(filepath.Join(tmpDir, "lib.gala"), []byte("package lib\n"), 0644)
	require.NoError(t, err)

	err = Verify(tmpDir, "h1:wronghash==")
	assert.Error(t, err)

	var mismatch *HashMismatchError
	assert.ErrorAs(t, err, &mismatch)
}

func TestNormalizeLineEndings(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello\nworld", "hello\nworld"},
		{"hello\r\nworld", "hello\nworld"},
		{"hello\rworld", "hello\nworld"},
		{"line1\r\nline2\r\nline3", "line1\nline2\nline3"},
		{"mixed\r\nlines\nhere", "mixed\nlines\nhere"},
	}

	for _, tt := range tests {
		result := normalizeLineEndings([]byte(tt.input))
		assert.Equal(t, tt.expected, string(result), "input: %q", tt.input)
	}
}
