package yaml

import (
	"math"
	"math/rand"
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
// 1e-6 <= |x| < 1e21 and in exponent notation (a YAML 1.2 core-schema float)
// outside it, so 1e300 is not written as a 301-digit literal.
func TestEncoder_FloatFormat(t *testing.T) {
	tests := []struct {
		name  string
		write func(e *YamlEncoderImpl)
		want  string
	}{
		{"zero", func(e *YamlEncoderImpl) { e.WriteFloat64(0) }, "0"},
		{"fraction", func(e *YamlEncoderImpl) { e.WriteFloat64(1.5) }, "1.5"},
		{"below 1e21", func(e *YamlEncoderImpl) { e.WriteFloat64(1e20) }, "100000000000000000000"},
		{"1e21", func(e *YamlEncoderImpl) { e.WriteFloat64(1e21) }, "1e+21"},
		{"huge", func(e *YamlEncoderImpl) { e.WriteFloat64(-1e300) }, "-1e+300"},
		{"1e-6", func(e *YamlEncoderImpl) { e.WriteFloat64(1e-6) }, "0.000001"},
		{"one-digit negative exponent", func(e *YamlEncoderImpl) { e.WriteFloat64(1.5e-9) }, "1.5e-9"},
		{"tiny", func(e *YamlEncoderImpl) { e.WriteFloat64(5e-324) }, "5e-324"},
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
			if k, want := d.ReadKey(), toValidUTF8(tt.key); k != want {
				t.Fatalf("key reads back as %q, want %q", k, want)
			}
			if v, want := d.ReadString(), toValidUTF8(tt.in); v != want {
				t.Fatalf("value reads back as %q, want %q", v, want)
			}
		})
	}
}

// toValidUTF8 replaces each invalid byte with U+FFFD.
func toValidUTF8(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		out = append(out, r)
	}
	return string(out)
}
