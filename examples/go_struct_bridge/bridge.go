// Package go_struct_bridge provides Go types for testing GALA named-arg construction
// and Go interop patterns (method calls, pointer receivers, multi-return).
package go_struct_bridge

import "fmt"

// Cookie represents a simple HTTP cookie for testing.
type Cookie struct {
	Name  string
	Value string
	Path  string
}

// String returns a readable representation of the cookie.
func (c Cookie) String() string {
	return fmt.Sprintf("Cookie(%s=%s, path=%s)", c.Name, c.Value, c.Path)
}

// SessionData mimics a Go session store for testing Go interop in lambda bodies.
type SessionData struct {
	data map[string]string
}

// NewSessionData creates a session with initial data.
func NewSessionData() *SessionData {
	return &SessionData{data: map[string]string{"user": "alice", "role": "admin"}}
}

// Get retrieves a value by key. Returns (value, found).
func (s *SessionData) Get(key string) (string, bool) {
	v, ok := s.data[key]
	return v, ok
}

// MakeCookie constructs a Cookie from Go (for testing).
func MakeCookie(name, value, path string) Cookie {
	return Cookie{Name: name, Value: value, Path: path}
}

// Processor exposes a function-typed field. GALA code calling p.Process(x)
// (where Process is `func(int) (string, error)`) used to infer the call's
// return type as NilType — inferCallSelectorType never consulted the Go
// field type for func-typed fields after method lookups failed.
type Processor struct {
	Process func(int) (string, error)
}

// NewProcessor returns a *Processor whose Process field formats its
// argument and never errors.
func NewProcessor() *Processor {
	return &Processor{Process: func(x int) (string, error) {
		return fmt.Sprintf("processed-%d", x), nil
	}}
}

// CookieBuilder exposes a single-return function-typed field whose result
// type is a struct (Cookie). This shape exercises GALA-level method
// dispatch on the call's result — `b.Build(s).String()` requires the
// transpiler to know the inner call returns Cookie so it can resolve
// String on the receiver.
type CookieBuilder struct {
	Build func(string) Cookie
}

// NewCookieBuilder returns a *CookieBuilder whose Build field constructs
// a Cookie from its argument.
func NewCookieBuilder() *CookieBuilder {
	return &CookieBuilder{Build: func(s string) Cookie {
		return Cookie{Name: s, Value: s, Path: "/"}
	}}
}
