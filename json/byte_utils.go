package json

// Minimal byte-slice utilities for GALA code that needs sub-slicing.
// GALA's parser doesn't support Go's slice syntax (data[from:to]),
// so these helpers bridge the gap.

// SliceBytes returns data[from:to].
func SliceBytes(data []byte, from int, to int) []byte {
	return data[from:to]
}

// BytesToString converts data[from:to] to a string.
func BytesToString(data []byte, from int, to int) string {
	return string(data[from:to])
}

// BytesString converts a full byte slice to a string.
func BytesString(data []byte) string {
	return string(data)
}

// RuneToString converts a rune to a single-character string.
func RuneToString(r rune) string {
	return string(r)
}

// TruncateBytes returns data[:to].
func TruncateBytes(data []byte, to int) []byte {
	return data[:to]
}

// TruncateBools returns data[:to].
func TruncateBools(data []bool, to int) []bool {
	return data[:to]
}

// toBytes converts a string to a byte slice.
func toBytes(s string) []byte {
	return []byte(s)
}

// CastAny performs a type assertion from any to T.
// Used internally by Codec.Decode to convert ReadFromAny result.
func CastAny[T any](v any) T {
	return v.(T)
}

// toRunes converts a string to a mutable rune slice.
// GALA's parser doesn't support []rune(s) type conversion.
func toRunes(s string) []rune {
	return []rune(s)
}

// runesToString converts a rune slice back to a string.
func runesToString(r []rune) string {
	return string(r)
}
