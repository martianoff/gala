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
