// Package go_int_constraint_bridge provides Go constraints with an integer
// core type, to verify that a range over a type parameter they constrain
// binds the type parameter (#1378).
package go_int_constraint_bridge

// Int64ish is every type whose underlying type is int64, time.Duration
// included.
type Int64ish interface{ ~int64 }

// Sized embeds Int64ish: its core type is int64 too.
type Sized interface {
	Int64ish
}
