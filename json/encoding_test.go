package json

import (
	stdjson "encoding/json"
	"math"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

// ----- Encoder: strings ------------------------------------------------------

func encodeString(s string) string {
	e := NewJsonEncoder()
	e.WriteString(s)
	return e.String()
}

// The encoder always writes valid UTF-8: every invalid byte becomes \ufffd,
// as in encoding/json. Control characters, U+2028/U+2029 and <>& follow
// GALA's own choices, recorded here: controls other than \n \r \t use the
// \u00XX form (encoding/json writes \b and \f), and U+2028, U+2029 and <>&
// are written as is (encoding/json escapes them for embedding in HTML and
// JavaScript). Both spellings are valid JSON that decodes to the same text.
func TestEncoder_StringEscapes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain ASCII", "hello", `"hello"`},
		{"quote and backslash", `a"b\c`, `"a\"b\\c"`},
		{"newline, return, tab", "a\nb\rc\td", `"a\nb\rc\td"`},
		{"other controls", "\x00\x01\b\f\x1f", `"\u0000\u0001\u0008\u000c\u001f"`},
		{"DEL is not a control", "\x7f", "\"\x7f\""},
		{"multibyte UTF-8", "é😀", `"é😀"`},
		{"U+FFFD itself", "\ufffd", "\"\ufffd\""},
		{"line and paragraph separators", "\u2028\u2029", "\"\u2028\u2029\""},
		{"HTML characters", "<>&", `"<>&"`},
		{"invalid byte mid-string", "a\xffb", `"a\ufffdb"`},
		{"lone invalid byte", "\xff", `"\ufffd"`},
		{"truncated two-byte sequence", "\xc3", `"\ufffd"`},
		{"truncated sequence then ASCII", "\xc3(", `"\ufffd("`},
		{"truncated three-byte sequence", "\xe2\x82", `"\ufffd\ufffd"`},
		{"stray continuation bytes", "\x80\x80", `"\ufffd\ufffd"`},
		{"UTF-8 encoded surrogate", "\xed\xa0\x80", `"\ufffd\ufffd\ufffd"`},
		{"above U+10FFFF", "\xf4\x90\x80\x80", `"\ufffd\ufffd\ufffd\ufffd"`},
		{"invalid byte before multibyte", "\xffé", `"\ufffdé"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeString(tt.in)
			if got != tt.want {
				t.Fatalf("WriteString(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if !utf8.ValidString(got) || !stdjson.Valid([]byte(got)) {
				t.Fatalf("WriteString(%q) = %q is not valid UTF-8 JSON", tt.in, got)
			}
			// encoding/json reads it back as the text it would itself have
			// written for the input.
			var back string
			if err := stdjson.Unmarshal([]byte(got), &back); err != nil {
				t.Fatalf("encoding/json cannot read %q: %v", got, err)
			}
			if want := stdjsonRoundTrip(tt.in); back != want {
				t.Fatalf("decodes to %q, encoding/json's own encoding decodes to %q", back, want)
			}
		})
	}
}

// stdjsonRoundTrip is the text encoding/json reads back from its own encoding
// of s.
func stdjsonRoundTrip(s string) string {
	ref, _ := stdjson.Marshal(s)
	var back string
	_ = stdjson.Unmarshal(ref, &back)
	return back
}

// Keys go through the same escaper as values.
func TestEncoder_KeyWithInvalidUTF8(t *testing.T) {
	e := NewJsonEncoder()
	e.WriteStartObject()
	e.WriteKey("k\xfe")
	e.WriteString("v\xff")
	e.WriteEndObject()
	if got, want := e.String(), `{"k\ufffd":"v\ufffd"}`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Random byte strings, mostly invalid UTF-8, always encode to valid JSON
// that decodes to what encoding/json's own encoding decodes to, and our
// decoder reads back the same text.
func TestEncoder_RandomBytesStayValid(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 2000; i++ {
		b := make([]byte, r.Intn(12))
		for j := range b {
			b[j] = byte(r.Intn(256))
		}
		s := string(b)
		got := encodeString(s)
		if !utf8.ValidString(got) || !stdjson.Valid([]byte(got)) {
			t.Fatalf("WriteString(%q) = %q is not valid UTF-8 JSON", s, got)
		}
		if back, want := NewJsonDecoder(got).ReadString(), stdjsonRoundTrip(s); back != want {
			t.Fatalf("WriteString(%q) = %q reads back as %q, want %q", s, got, back, want)
		}
	}
}

// ----- Encoder: floats -------------------------------------------------------

// Expected values are encoding/json's output for the same inputs: the
// shortest round-trip digits, plain notation for 1e-6 <= |x| < 1e21 and
// exponent notation outside it, with a one-digit negative exponent written
// without its leading zero.
func TestEncoder_Float64Format(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{math.Copysign(0, -1), "-0"},
		{1, "1"},
		{-1, "-1"},
		{1.5, "1.5"},
		{0.1, "0.1"},
		{123.456, "123.456"},
		{1e20, "100000000000000000000"},
		{12345678901234567890.0, "12345678901234567000"},
		{1e21, "1e+21"},
		{3e21, "3e+21"},
		{123456789e15, "1.23456789e+23"},
		{1e300, "1e+300"},
		{-1e300, "-1e+300"},
		{math.MaxFloat64, "1.7976931348623157e+308"},
		{1e-6, "0.000001"},
		{1e-7, "1e-7"},
		{9.99e-7, "9.99e-7"},
		{1.5e-9, "1.5e-9"},
		{1e-100, "1e-100"},
		{math.SmallestNonzeroFloat64, "5e-324"},
	}
	for _, tt := range tests {
		e := NewJsonEncoder()
		e.WriteFloat64(tt.in)
		if got := e.String(); got != tt.want {
			t.Errorf("WriteFloat64(%v) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestEncoder_Float32Format(t *testing.T) {
	tests := []struct {
		in   float32
		want string
	}{
		{0, "0"},
		{1.5, "1.5"},
		{0.1, "0.1"},
		{16777216, "16777216"},
		{1e10, "10000000000"},
		{1e20, "100000000000000000000"},
		{1e21, "1e+21"},
		{math.MaxFloat32, "3.4028235e+38"},
		// float32(1e-6) is just below the float64 1e-6; the plain/exponent
		// boundary is checked at float32 precision, as encoding/json does.
		{1e-6, "0.000001"},
		{1e-7, "1e-7"},
		{9.99e-7, "9.99e-7"},
		{math.SmallestNonzeroFloat32, "1e-45"},
	}
	for _, tt := range tests {
		e := NewJsonEncoder()
		e.WriteFloat32(tt.in)
		if got := e.String(); got != tt.want {
			t.Errorf("WriteFloat32(%v) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

// Every finite bit pattern is spelled exactly as encoding/json spells it,
// and decodes back to the same value.
func TestEncoder_FloatsMatchEncodingJSON(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	for i := 0; i < 20000; i++ {
		f64 := math.Float64frombits(r.Uint64())
		f32 := math.Float32frombits(r.Uint32())
		if !math.IsNaN(f64) && !math.IsInf(f64, 0) {
			e := NewJsonEncoder()
			e.WriteFloat64(f64)
			want, _ := stdjson.Marshal(f64)
			if got := e.String(); got != string(want) {
				t.Fatalf("WriteFloat64(%v) = %s, encoding/json writes %s", f64, got, want)
			}
			if back := NewJsonDecoder(e.String()).ReadFloat64(); math.Float64bits(back) != math.Float64bits(f64) {
				t.Fatalf("WriteFloat64(%v) = %s reads back as %v", f64, e.String(), back)
			}
		}
		if !math.IsNaN(float64(f32)) && !math.IsInf(float64(f32), 0) {
			e := NewJsonEncoder()
			e.WriteFloat32(f32)
			want, _ := stdjson.Marshal(f32)
			if got := e.String(); got != string(want) {
				t.Fatalf("WriteFloat32(%v) = %s, encoding/json writes %s", f32, got, want)
			}
			if back := NewJsonDecoder(e.String()).ReadFloat32(); math.Float32bits(back) != math.Float32bits(f32) {
				t.Fatalf("WriteFloat32(%v) = %s reads back as %v", f32, e.String(), back)
			}
		}
	}
}

// JSON has no NaN or Infinity: writing one panics (the codec's Encode Try
// turns that into a Failure, as encoding/json returns UnsupportedValueError)
// and nothing is written.
func TestEncoder_NonFiniteFloatsPanic(t *testing.T) {
	tests := []struct {
		name  string
		write func(e *JsonEncoderImpl)
	}{
		{"float64 NaN", func(e *JsonEncoderImpl) { e.WriteFloat64(math.NaN()) }},
		{"float64 +Inf", func(e *JsonEncoderImpl) { e.WriteFloat64(math.Inf(1)) }},
		{"float64 -Inf", func(e *JsonEncoderImpl) { e.WriteFloat64(math.Inf(-1)) }},
		{"float32 NaN", func(e *JsonEncoderImpl) { e.WriteFloat32(float32(math.NaN())) }},
		{"float32 +Inf", func(e *JsonEncoderImpl) { e.WriteFloat32(float32(math.Inf(1))) }},
		{"float32 -Inf", func(e *JsonEncoderImpl) { e.WriteFloat32(float32(math.Inf(-1))) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewJsonEncoder()
			defer func() {
				if recover() == nil {
					t.Fatalf("no panic; wrote %q", e.String())
				}
				if e.String() != "" {
					t.Fatalf("wrote %q before failing", e.String())
				}
			}()
			tt.write(e)
		})
	}
}

// ----- Decoder: strings ------------------------------------------------------

// These cases follow encoding/json: every escape JSON defines is read
// (\b and \f included), an escaped surrogate pair is one character above
// U+FFFF, an unpaired surrogate becomes U+FFFD, and so does each invalid
// UTF-8 byte in the raw input.
func TestDecoder_StringContent(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"all short escapes", `"\"\\\/\b\f\n\r\t"`, "\"\\/\b\f\n\r\t"},
		{"BMP escape", `"\u00e9\u20ac"`, "é€"},
		{"surrogate pair", `"\ud83d\ude00"`, "😀"},
		{"upper-case surrogate pair", `"\uD83D\uDE00!"`, "😀!"},
		{"lone high surrogate at end", `"\ud83d"`, "\ufffd"},
		{"high surrogate then text", `"\ud83dx"`, "\ufffdx"},
		{"high surrogate then non-surrogate escape", `"\ud83d\u0041"`, "\ufffdA"},
		{"two high surrogates then pair", `"\ud83d\ud83d\ude00"`, "\ufffd😀"},
		{"lone low surrogate", `"\udc00"`, "\ufffd"},
		{"raw multibyte", "\"é😀\"", "é😀"},
		{"raw invalid byte", "\"a\xffb\"", "a\ufffdb"},
		{"raw truncated sequence", "\"\xe2\x82\"", "\ufffd\ufffd"},
		{"raw encoded surrogate", "\"\xed\xa0\x80\"", "\ufffd\ufffd\ufffd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var want string
			if err := stdjson.Unmarshal([]byte(tt.in), &want); err != nil || want != tt.want {
				t.Fatalf("test table disagrees with encoding/json: %q (%v)", want, err)
			}
			if got := NewJsonDecoder(tt.in).ReadString(); got != tt.want {
				t.Fatalf("ReadString(%s) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// A key is read by the same string reader as a value.
func TestDecoder_KeyEscapes(t *testing.T) {
	d := NewJsonDecoder("{\"\\ud83d\\ude00\\b\xff\":1}")
	d.StartObject()
	if got, want := d.ReadKey(), "😀\b\ufffd"; got != want {
		t.Fatalf("ReadKey = %q, want %q", got, want)
	}
}

// Malformed escapes still fail.
func TestDecoder_MalformedEscapesPanic(t *testing.T) {
	for _, in := range []string{`"\x41"`, `"\u12"`, `"\u12G4"`, `"\`, `"\ud83d\u12"`} {
		t.Run(in, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("ReadString(%s) did not fail", in)
				}
			}()
			NewJsonDecoder(in).ReadString()
		})
	}
}

// Strings written by the encoder read back unchanged when they are valid
// UTF-8.
func TestEncoderDecoder_ValidStringsRoundTrip(t *testing.T) {
	for _, s := range []string{"", "plain", "\x00\b\f\x1f\x7f", "é€😀\ufffd", "\u2028<>&\u2029", strings.Repeat("ü", 300)} {
		if got := NewJsonDecoder(encodeString(s)).ReadString(); got != s {
			t.Errorf("round trip of %q = %q", s, got)
		}
	}
}
