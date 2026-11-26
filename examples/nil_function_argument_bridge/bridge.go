// Package nil_function_argument_bridge declares Go types no nil can be,
// though GALA cannot spell them: a named fixed-length array and an alias of
// a struct type.
package nil_function_argument_bridge

import "time"

// ID is a named array, as uuid.UUID is.
type ID [16]byte

// Stamp is an alias of a struct type.
type Stamp = time.Time
