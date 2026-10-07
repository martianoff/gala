package textstats

import (
	"encoding/json"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Write makes *Counter an io.Writer. A Go file in the same package can add
// a method to the GALA struct; this one works on raw bytes, which reads more
// simply in Go. A caller may split its input across several writes (io.Copy
// writes 32 KB at a time), so a word cut in two is counted once.
func (c *Counter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	text := string(p)
	c.Bytes += len(p)
	c.Lines += strings.Count(text, "\n")
	c.Words += countWords(text) // unexported GALA function, same package
	first, _ := utf8.DecodeRune(p)
	if c.inWord && !unicode.IsSpace(first) {
		c.Words-- // the previous write ended inside this word
	}
	last, _ := utf8.DecodeLastRune(p)
	c.inWord = !unicode.IsSpace(last)
	return len(p), nil
}

// CountReader copies r into a Counter with Go's io.Copy.
func CountReader(r io.Reader) (Counter, error) {
	var c Counter
	_, err := io.Copy(&c, r)
	return c, err
}

// MarshalJSON makes Stats a json.Marshaler with lowercase keys. Its fields are
// immutable, which Go sees as std.Immutable[T] values; Get reads them.
func (s Stats) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"lines":   s.Lines.Get(),
		"words":   s.Words.Get(),
		"bytes":   s.Bytes.Get(),
		"longest": s.Longest.Get(),
	})
}
