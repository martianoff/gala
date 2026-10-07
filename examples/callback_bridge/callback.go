// Package callback_bridge is a Go package whose functions take callbacks that
// return several results, for the go_multi_result_callbacks example.
package callback_bridge

import (
	"fmt"
	"strings"
)

// RunWith calls f and reports its value, or -1 when it fails.
func RunWith(f func() (int, error)) int {
	n, err := f()
	if err != nil {
		return -1
	}
	return n
}

// Describe calls f with each of names and reports what it found.
func Describe(names []string, f func(name string) (string, bool)) string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		if v, ok := f(name); ok {
			parts = append(parts, name+"="+v)
		} else {
			parts = append(parts, name+"=?")
		}
	}
	return strings.Join(parts, " ")
}

// Retry calls f until it succeeds, at most attempts times, and returns the
// value and the number of the attempt that gave it.
func Retry(attempts int, f func(attempt int) (string, int, error)) string {
	var last error
	for i := 1; i <= attempts; i++ {
		s, n, err := f(i)
		if err == nil {
			return fmt.Sprintf("%s/%d after %d", s, n, i)
		}
		last = err
	}
	return "gave up: " + last.Error()
}
