// Package textutil is a Go package of the budget test program's own module.
package textutil

import (
	"strings"
	"unicode/utf8"
)

// Reverse returns s with its runes in reverse order.
func Reverse(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		b.WriteRune(r)
		s = s[:len(s)-size]
	}
	return b.String()
}
