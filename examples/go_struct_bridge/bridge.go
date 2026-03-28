// Package go_struct_bridge provides a simple Go struct for testing GALA named-arg construction.
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
