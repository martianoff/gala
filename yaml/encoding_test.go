package yaml

import (
	"math"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

func encodeValue(write func(e *YamlEncoderImpl)) string {
	e := NewYamlEncoder()
	e.WriteStartObject()
	e.WriteKey("v")
	write(e)
	e.WriteEndObject()
	return e.String()
}

// Floats use the shortest round-trip digits, in plain notation for
// 1e-6 <= |x| < 1e21 and in exponent notation outside it, so 1e300 is not
// written as a 301-digit literal. The exponent form keeps a '.' in the
// mantissa so YAML 1.1 resolvers read it as a float too.
func TestEncoder_FloatFormat(t *testing.T) {
	tests := []struct {
		name  string
		write func(e *YamlEncoderImpl)
		want  string
	}{
		{"zero", func(e *YamlEncoderImpl) { e.WriteFloat64(0) }, "0"},
		{"fraction", func(e *YamlEncoderImpl) { e.WriteFloat64(1.5) }, "1.5"},
		{"below 1e21", func(e *YamlEncoderImpl) { e.WriteFloat64(1e20) }, "100000000000000000000"},
		{"1e21", func(e *YamlEncoderImpl) { e.WriteFloat64(1e21) }, "1.0e+21"},
		{"huge", func(e *YamlEncoderImpl) { e.WriteFloat64(-1e300) }, "-1.0e+300"},
		{"1e-6", func(e *YamlEncoderImpl) { e.WriteFloat64(1e-6) }, "0.000001"},
		{"1e-7", func(e *YamlEncoderImpl) { e.WriteFloat64(1e-7) }, "1.0e-7"},
		{"one-digit negative exponent", func(e *YamlEncoderImpl) { e.WriteFloat64(1.5e-9) }, "1.5e-9"},
		{"tiny", func(e *YamlEncoderImpl) { e.WriteFloat64(5e-324) }, "5.0e-324"},
		{"float32 1e-6", func(e *YamlEncoderImpl) { e.WriteFloat32(1e-6) }, "0.000001"},
		{"float32 max", func(e *YamlEncoderImpl) { e.WriteFloat32(math.MaxFloat32) }, "3.4028235e+38"},
		{"NaN", func(e *YamlEncoderImpl) { e.WriteFloat64(math.NaN()) }, ".nan"},
		{"-Inf", func(e *YamlEncoderImpl) { e.WriteFloat32(float32(math.Inf(-1))) }, "-.inf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := encodeValue(tt.write), "v: "+tt.want; got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

// Every finite float reads back bit for bit.
func TestEncoderDecoder_FloatsRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	for i := 0; i < 5000; i++ {
		f64 := math.Float64frombits(r.Uint64())
		f32 := math.Float32frombits(r.Uint32())
		if !math.IsNaN(f64) && !math.IsInf(f64, 0) {
			d := NewYamlDecoder(encodeValue(func(e *YamlEncoderImpl) { e.WriteFloat64(f64) }))
			d.StartObject()
			d.ReadKey()
			if got := d.ReadFloat64(); math.Float64bits(got) != math.Float64bits(f64) {
				t.Fatalf("float64 %v read back as %v", f64, got)
			}
		}
		if !math.IsNaN(float64(f32)) && !math.IsInf(float64(f32), 0) {
			d := NewYamlDecoder(encodeValue(func(e *YamlEncoderImpl) { e.WriteFloat32(f32) }))
			d.StartObject()
			d.ReadKey()
			if got := d.ReadFloat32(); math.Float32bits(got) != math.Float32bits(f32) {
				t.Fatalf("float32 %v read back as %v", f32, got)
			}
		}
	}
}

// A YAML stream is Unicode text, so the encoder never writes invalid UTF-8:
// a string or key holding an invalid byte is double-quoted with each invalid
// byte written as \ufffd, the way the json encoder writes it.
func TestEncoder_InvalidUTF8(t *testing.T) {
	tests := []struct {
		name string
		key  string
		in   string
		want string
	}{
		{"invalid byte in value", "v", "a\xffb", `v: "a\ufffdb"`},
		{"truncated sequence", "v", "\xe2\x82", `v: "\ufffd\ufffd"`},
		{"encoded surrogate", "v", "\xed\xa0\x80", `v: "\ufffd\ufffd\ufffd"`},
		{"invalid byte next to multibyte", "v", "é\xff😀", `v: "é\ufffd😀"`},
		{"invalid byte in key", "k\xfe", "x", `"k\ufffd": x`},
		{"valid multibyte stays plain", "v", "café 😀", "v: café 😀"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewYamlEncoder()
			e.WriteStartObject()
			e.WriteKey(tt.key)
			e.WriteString(tt.in)
			e.WriteEndObject()
			got := e.String()
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("%q is not valid UTF-8", got)
			}
			d := NewYamlDecoder(got)
			d.StartObject()
			// string([]rune(s)) replaces each invalid byte with U+FFFD.
			if k, want := d.ReadKey(), string([]rune(tt.key)); k != want {
				t.Fatalf("key reads back as %q, want %q", k, want)
			}
			if v, want := d.ReadString(), string([]rune(tt.in)); v != want {
				t.Fatalf("value reads back as %q, want %q", v, want)
			}
		})
	}
}

// The decoder reads each invalid UTF-8 byte as U+FFFD, whatever the style of
// the scalar it sits in.
func TestDecoder_InvalidUTF8(t *testing.T) {
	want := "a" + string(utf8.RuneError) + "b"
	tests := []struct {
		name string
		in   string
	}{
		{"plain", "k: a\xffb"},
		{"single-quoted", "k: 'a\xffb'"},
		{"double-quoted", "k: \"a\xffb\""},
		{"literal block", "k: |\n  a\xffb\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewYamlDecoder(tt.in)
			d.StartObject()
			d.ReadKey()
			if got := strings.TrimSuffix(d.ReadString(), "\n"); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

// YAML 1.2 (section 5.1) allows only printable characters in a stream, and
// no byte order mark inside a scalar. DEL, the C1 controls U+0080-U+009F,
// U+FEFF, U+FFFE and U+FFFF are not allowed, so a string holding one is
// double-quoted with the character escaped: NEL as \N (YAML 1.1 readers take
// a raw NEL for a line break), the other one-byte characters as \xXX, and
// the rest as \uXXXX.
func TestEncoder_NonPrintableEscaped(t *testing.T) {
	const bs = "\\"
	// U+007E, U+00A0, U+FEFE, U+FFFD and U+10000 are printable.
	printableNeighbours := string([]rune{0x7e, 0xa0, 0xfefe, 0xfffd, 0x10000})
	tests := []struct {
		name string
		key  string
		in   string
		want string
	}{
		{"DEL", "v", "a\x7fb", `v: "a\x7fb"`},
		{"first C1 control", "v", "\u0080", `v: "\x80"`},
		{"NEL", "v", "line\u0085next", `v: "line\Nnext"`},
		{"last C1 control", "v", "x\u009f", `v: "x\x9f"`},
		{"byte order mark", "v", string(rune(0xfeff)) + "x", `v: "` + bs + `uFEFFx"`},
		{"U+FFFE", "v", "a" + string(rune(0xfffe)), `v: "a` + bs + `uFFFE"`},
		{"U+FFFF", "v", string(rune(0xffff)), `v: "` + bs + `uFFFF"`},
		{"mixed with other escapes", "v", "\"\x7f\\\u0085\t", `v: "\"\x7f\\\N\t"`},
		{"in a key", "k\u0085", "x", `"k\N": x`},
		// The printable characters next to the escaped ranges stay plain.
		{"printable neighbours", "v", printableNeighbours, "v: " + printableNeighbours},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewYamlEncoder()
			e.WriteStartObject()
			e.WriteKey(tt.key)
			e.WriteString(tt.in)
			e.WriteEndObject()
			got := e.String()
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			d := NewYamlDecoder(got)
			d.StartObject()
			if k := d.ReadKey(); k != tt.key {
				t.Fatalf("key reads back as %q, want %q", k, tt.key)
			}
			if v := d.ReadString(); v != tt.in {
				t.Fatalf("value reads back as %q, want %q", v, tt.in)
			}
		})
	}
}

// Only space and tab are white space around a scalar; a no-break space or
// another Unicode space at either end is part of the text.
func TestDecoder_UnicodeSpaceIsText(t *testing.T) {
	nbsp, ideographic := string(rune(0xa0)), string(rune(0x3000))
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain value", "k: " + nbsp + "x" + nbsp, nbsp + "x" + nbsp},
		{"value with comment", "k: x" + ideographic + " # note", "x" + ideographic},
		{"sequence item", "k:\n  - " + nbsp, nbsp},
		{"tabs and spaces still trimmed", "k: \t x \t", "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewYamlDecoder(tt.in)
			d.StartObject()
			d.ReadKey()
			if strings.Contains(tt.in, "- ") {
				d.StartArray()
			}
			if got := d.ReadString(); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
	d := NewYamlDecoder(nbsp + "k" + nbsp + ": v")
	d.StartObject()
	if k := d.ReadKey(); k != nbsp+"k"+nbsp {
		t.Fatalf("key reads as %q", k)
	}
}

// Every character from U+0000 to U+FFFF (surrogates aside) round-trips
// through the encoder and decoder, alone and between other text.
func TestEncoderDecoder_AllBMPCharactersRoundTrip(t *testing.T) {
	for r := rune(0); r <= 0xffff; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue
		}
		for _, s := range []string{string(r), "a" + string(r) + "b"} {
			got := encodeValue(func(e *YamlEncoderImpl) { e.WriteString(s) })
			d := NewYamlDecoder(got)
			d.StartObject()
			d.ReadKey()
			if v := d.ReadString(); v != s {
				t.Fatalf("%q encodes as %q, which reads back as %q", s, got, v)
			}
		}
	}
}
