package lsp

import (
	"path/filepath"
	"testing"
)

// TestUriToPath_PreservesLeadingSlash is the focused regression for the
// cross-file LSP outage: uriToPath must keep the leading slash of POSIX
// absolute paths so sibling-directory discovery (filepath.Dir + ReadDir)
// resolves the package's other .gala files. Dropping it produced a relative
// path, disabled cross-file type resolution, and surfaced as bogus
// "unused variable" / "cannot infer type of matched expression" diagnostics.
func TestUriToPath_PreservesLeadingSlash(t *testing.T) {
	// POSIX cases are meaningful on every platform: the URI grammar is the
	// same and filepath.FromSlash is a no-op for "/" on POSIX. We assert with
	// filepath.FromSlash so the expectations stay correct on Windows too.
	cases := []struct {
		name string
		uri  string
		want string
	}{
		{"posix abs", "file:///tmp/proj/render.gala", filepath.FromSlash("/tmp/proj/render.gala")},
		{"posix abs nested", "file:///Users/x/p/server.gala", filepath.FromSlash("/Users/x/p/server.gala")},
		{"percent-encoded space", "file:///tmp/my%20proj/a.gala", filepath.FromSlash("/tmp/my proj/a.gala")},
		{"root file", "file:///a.gala", filepath.FromSlash("/a.gala")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := uriToPath(tc.uri); got != tc.want {
				t.Errorf("uriToPath(%q) = %q, want %q", tc.uri, got, tc.want)
			}
		})
	}
}

// TestUriToPath_WindowsDriveLetters verifies the Windows shapes are handled
// without the leading-slash artifact ("/C:/..." -> "C:\\...").
func TestUriToPath_WindowsDriveLetters(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		want string
	}{
		{"drive plain", "file:///C:/Users/x/render.gala", "C:" + string(filepath.Separator) + filepath.Join("Users", "x", "render.gala")},
		{"drive encoded colon", "file:///c%3A/Users/x/render.gala", "c:" + string(filepath.Separator) + filepath.Join("Users", "x", "render.gala")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := uriToPath(tc.uri)
			// Normalize via filepath.FromSlash-equivalent: the drive form must
			// not start with a slash and must contain the drive colon at [1].
			if len(got) < 2 || got[1] != ':' {
				t.Fatalf("uriToPath(%q) = %q, want a drive-letter path like %q", tc.uri, got, tc.want)
			}
			if got[0] == filepath.Separator || got[0] == '/' {
				t.Errorf("uriToPath(%q) = %q still has a leading slash before the drive letter", tc.uri, got)
			}
		})
	}
}
