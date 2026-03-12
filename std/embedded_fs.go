package std

import "embed"

// EmbeddedFS wraps Go's embed.FS to provide a GALA-idiomatic API
// for accessing files embedded at compile time.
//
// Use the `embed val` declaration in GALA to create instances:
//
//	embed val static EmbeddedFS = "static/*"
//	embed val templates EmbeddedFS = ("templates/*.html", "templates/*.css")
type EmbeddedFS struct {
	fs embed.FS
}

// NewEmbeddedFS creates an EmbeddedFS from a Go embed.FS.
// This is called by transpiler-generated code; users should use `embed val` declarations.
func NewEmbeddedFS(fs embed.FS) EmbeddedFS {
	return EmbeddedFS{fs: fs}
}

// ReadString reads a file from the embedded filesystem and returns its content as a string.
// Returns a Try[string]: Success(content) on success, Failure(err) on error.
func (e EmbeddedFS) ReadString(path string) Try[string] {
	data, err := e.fs.ReadFile(path)
	if err != nil {
		return Try[string]{Err: NewImmutable[error](err), _variant: _Try_Failure}
	}
	return Try[string]{Value: NewImmutable(string(data)), _variant: _Try_Success}
}

// ReadBytes reads a file from the embedded filesystem and returns its raw bytes.
// Returns ([]byte, error) for Go interop compatibility.
func (e EmbeddedFS) ReadBytes(path string) ([]byte, error) {
	return e.fs.ReadFile(path)
}
