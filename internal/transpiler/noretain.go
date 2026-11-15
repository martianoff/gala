package transpiler

// The no-retain allow-list (docs/OWNERSHIP.MD#gala-calls-go)
//
// An `inout` place may be lent to Go only where Go provably keeps no
// reference to it: a Go method that neither retains its receiver pointer, or
// anything reachable from it, after it returns, nor returns memory reachable
// from it. Every other Go call that would receive the place's address is an
// escape (GALA-E0113).
//
// The list is built into the compiler and cannot be extended by a program:
// an entry a program supplied would be an unchecked trust point. Every
// entry is verified in CI against the escape analysis of the pinned Go
// compiler (noretain_escape_test.go): a Go upgrade that
// changes a verdict fails the build, not a user program.

// NoRetainMethod is one Go pointer method on the allow-list.
type NoRetainMethod struct {
	// Path is the Go import path of the type's package, Type its name and
	// Method the method's.
	Path, Type, Method string
	// Call is a call of the method on a receiver `x` in Go, for the escape
	// check (noretain_escape_test.go).
	Call string
}

// noRetainMethods is the allow-list. Accessors that return memory the
// receiver holds (strings.Builder.String, bytes.Buffer.Bytes) are not on
// it: their result aliases the place. Nor are methods the compiler reports
// as leaking their receiver's content: strings.Builder's writes store the
// receiver's own address (its copy check), and bytes.Buffer.WriteRune.
var noRetainMethods = []NoRetainMethod{
	{"strings", "Builder", "Grow", `x.Grow(8)`},
	{"strings", "Builder", "Len", `_ = x.Len()`},
	{"strings", "Builder", "Cap", `_ = x.Cap()`},
	{"strings", "Builder", "Reset", `x.Reset()`},
	{"bytes", "Buffer", "WriteString", `x.WriteString("s")`},
	{"bytes", "Buffer", "WriteByte", `x.WriteByte('b')`},
	{"bytes", "Buffer", "Grow", `x.Grow(8)`},
	{"bytes", "Buffer", "Len", `_ = x.Len()`},
	{"bytes", "Buffer", "Cap", `_ = x.Cap()`},
	{"bytes", "Buffer", "Reset", `x.Reset()`},
	{"bytes", "Buffer", "Truncate", `x.Truncate(0)`},
}

// NoRetainMethods is the allow-list, for its CI check.
func NoRetainMethods() []NoRetainMethod { return noRetainMethods }

// IsNoRetainMethod reports whether the Go method method of the type typeName
// of the package at path is on the allow-list.
func IsNoRetainMethod(path, typeName, method string) bool {
	for _, m := range noRetainMethods {
		if m.Path == path && m.Type == typeName && m.Method == method {
			return true
		}
	}
	return false
}
