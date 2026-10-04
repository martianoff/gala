package own_go_func_multi_results

import "errors"

// CountInt returns a value and an error: (T, error).
func CountInt(n int) (int, error) {
	if n < 0 {
		return 0, errors.New("negative count")
	}
	return n * 2, nil
}

// IntAndFlag returns two values: (A, B).
func IntAndFlag(n int) (int, bool) {
	return n + 1, n%2 == 0
}

// MakeBox builds a Box, a type this package declares in GALA: (T, error).
func MakeBox(n int) (Box, error) {
	if n == 0 {
		return Box{}, errors.New("empty box")
	}
	return Box{N: n}, nil
}
