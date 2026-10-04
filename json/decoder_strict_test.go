package json

import (
	stdjson "encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// decodeErr runs f and returns the decode error it panicked with, or "" when
// it did not fail.
func decodeErr(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	f()
	return ""
}

// decodeDocument reads one JSON value of the given kind from s and checks
// that nothing follows it, as Decode does.
func decodeDocument(s string, read func(d *JsonDecoderImpl)) string {
	return decodeErr(func() {
		d := NewJsonDecoder(s)
		read(d)
		d.End()
	})
}

func readFloat(d *JsonDecoderImpl) { d.ReadFloat64() }
func readString(d *JsonDecoderImpl) { d.ReadString() }
func skipValue(d *JsonDecoderImpl)  { d.Skip() }

// ----- Decoder: number grammar ----------------------------------------------

// The decoder accepts exactly the RFC 8259 number grammar,
//
//	-? (0 | [1-9][0-9]*) (. [0-9]+)? ([eE] [+-]? [0-9]+)?
//
// as encoding/json does. Every other spelling is an error that names the
// offset where the number starts and the text it read there.
func TestDecoder_NumberGrammar(t *testing.T) {
	tests := []struct {
		in    string
		valid bool
	}{
		{"0", true},
		{"-0", true},
		{"0.0", true},
		{"-0.0", true},
		{"7", true},
		{"-7", true},
		{"10", true},
		{"1234567890", true},
		{"1.5", true},
		{"-0.25", true},
		{"1e2", true},
		{"1E2", true},
		{"1E+2", true},
		{"1e-2", true},
		{"0e0", true},
		{"-0E-0", true},
		{"1.5e+10", true},
		{"123.456e-78", true},
		{"1e308", true},
		{"1e-400", true},
		{"1e00001", true},

		{"+1", false},
		{"01", false},
		{"00", false},
		{"-01", false},
		{"007", false},
		{"0123.5", false},
		{".5", false},
		{"-.5", false},
		{"1.", false},
		{"-1.", false},
		{"0.", false},
		{"1.e5", false},
		{"1.5.2", false},
		{"--1", false},
		{"-", false},
		{"-+1", false},
		{"1e", false},
		{"1E+", false},
		{"1e-", false},
		{"1e+-2", false},
		{"1e5e5", false},
		{"1e2.5", false},
		{"e5", false},
		{"0x10", false},
		{"0X1F", false},
		{"1_000", false},
		{"1f", false},
		{"Infinity", false},
		{"-Infinity", false},
		{"NaN", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := stdjson.Valid([]byte(tt.in)); got != tt.valid {
				t.Fatalf("test table disagrees with encoding/json: Valid(%s) = %v", tt.in, got)
			}
			for _, read := range []struct {
				name string
				f    func(d *JsonDecoderImpl)
			}{{"ReadFloat64", readFloat}, {"Skip", skipValue}} {
				err := decodeDocument(tt.in, read.f)
				if tt.valid && err != "" {
					t.Fatalf("%s(%s) failed: %s", read.name, tt.in, err)
				}
				if !tt.valid && err == "" {
					t.Fatalf("%s(%s) did not fail", read.name, tt.in)
				}
			}
			if tt.valid {
				// A float that fits reads as the value encoding/json reads.
				var want float64
				if stdjson.Unmarshal([]byte(tt.in), &want) == nil {
					if got := NewJsonDecoder(tt.in).ReadFloat64(); math.Float64bits(got) != math.Float64bits(want) {
						t.Fatalf("ReadFloat64(%s) = %v, encoding/json reads %v", tt.in, got, want)
					}
				}
			}
		})
	}
}

// Every read path for a number shares the grammar check, and the error names
// the offset where the bad number starts.
func TestDecoder_InvalidNumberErrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		read func(d *JsonDecoderImpl)
		want string
	}{
		{"int", "01", func(d *JsonDecoderImpl) { d.ReadInt() }, `json at pos 0: invalid number "01"`},
		{"int64 after space", "  +5", func(d *JsonDecoderImpl) { d.ReadInt64() }, `json at pos 2: invalid number "+5"`},
		{"uint", "1.", func(d *JsonDecoderImpl) { d.ReadUintN(64) }, `json at pos 0: invalid number "1."`},
		{"float32", ".5", func(d *JsonDecoderImpl) { d.ReadFloat32() }, `json at pos 0: invalid number ".5"`},
		{"float64 hex", "0x10", readFloat, `json at pos 0: invalid number "0x10"`},
		{"second array element", "[1, 01]", func(d *JsonDecoderImpl) {
			d.StartArray()
			d.ReadInt()
			d.ReadInt()
		}, `json at pos 4: invalid number "01"`},
		{"object field", `{"n":--1}`, func(d *JsonDecoderImpl) {
			d.StartObject()
			d.ReadKey()
			d.ReadFloat64()
		}, `json at pos 5: invalid number "--1"`},
		{"skipped field", `{"n":1.e5}`, func(d *JsonDecoderImpl) {
			d.StartObject()
			d.ReadKey()
			d.Skip()
		}, `json at pos 5: invalid number "1.e5"`},
		{"nested in a skipped value", `{"n":[0, [1, 2.]]}`, func(d *JsonDecoderImpl) {
			d.StartObject()
			d.ReadKey()
			d.Skip()
		}, `json at pos 13: invalid number "2."`},
		{"no number", "[]", func(d *JsonDecoderImpl) { d.ReadInt() }, `json at pos 0: expected a number`},
		{"end of input", "", readFloat, `json at pos 0: expected a number`},
		// Range errors still come from the type the number is read into.
		{"int8 range", "300", func(d *JsonDecoderImpl) { d.ReadIntN(8) }, `json at pos 3: invalid int8 "300"`},
		{"float overflow", "1e400", readFloat, `json at pos 5: invalid float "1e400"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeErr(func() { tt.read(NewJsonDecoder(tt.in)) }); got != tt.want {
				t.Fatalf("error = %q, want %q", got, tt.want)
			}
		})
	}
}

// Random strings over the number alphabet are accepted exactly when
// encoding/json accepts them.
func TestDecoder_NumbersMatchEncodingJSON(t *testing.T) {
	const alphabet = "0123456789+-.eE"
	rng := rand.New(rand.NewSource(7))
	for n := 0; n < 20000; n++ {
		var sb strings.Builder
		for k := 1 + rng.Intn(6); k > 0; k-- {
			sb.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		in := sb.String()
		want := stdjson.Valid([]byte(in))
		if got := decodeDocument(in, skipValue) == ""; got != want {
			t.Fatalf("Skip(%s) accepted = %v, encoding/json accepts = %v", in, got, want)
		}
	}
}

// ----- Decoder: control characters in strings -------------------------------

// A raw control character (below 0x20) inside a string is an error, as in
// encoding/json; it has to be written as an escape. DEL and C1 controls are
// not JSON control characters and pass through.
func TestDecoder_RawControlCharactersRejected(t *testing.T) {
	for c := 0; c < 0x20; c++ {
		in := "\"ab" + string(rune(c)) + "\""
		t.Run(strconv.Quote(in), func(t *testing.T) {
			if stdjson.Valid([]byte(in)) {
				t.Fatalf("encoding/json accepts %q", in)
			}
			want := fmt.Sprintf("json at pos 3: unescaped control character %#02x in string", c)
			for _, read := range []func(d *JsonDecoderImpl){readString, skipValue} {
				if got := decodeErr(func() { read(NewJsonDecoder(in)) }); got != want {
					t.Fatalf("error = %q, want %q", got, want)
				}
			}
		})
	}
	for _, in := range []string{"\"\x7f\"", "\"\u0085\"", "\"\\u0001\\n\""} {
		if err := decodeDocument(in, readString); err != "" || !stdjson.Valid([]byte(in)) {
			t.Fatalf("ReadString(%q) failed: %s", in, err)
		}
	}
}

// Keys and strings inside skipped values are checked too.
func TestDecoder_ControlCharacterInKeyAndSkippedValue(t *testing.T) {
	tests := []struct {
		name string
		in   string
		read func(d *JsonDecoderImpl)
		want string
	}{
		{"key", "{\"a\tb\":1}", func(d *JsonDecoderImpl) {
			d.StartObject()
			d.ReadKey()
		}, "json at pos 3: unescaped control character 0x09 in string"},
		{"skipped nested string", "{\"a\":{\"b\":[\"x\ny\"]}}", func(d *JsonDecoderImpl) {
			d.StartObject()
			d.ReadKey()
			d.Skip()
		}, "json at pos 13: unescaped control character 0x0a in string"},
		{"skipped nested key", "[{\"\x00\":1}]", skipValue, "json at pos 3: unescaped control character 0x00 in string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeErr(func() { tt.read(NewJsonDecoder(tt.in)) }); got != tt.want {
				t.Fatalf("error = %q, want %q", got, tt.want)
			}
		})
	}
}

// Random byte strings in quotes decode exactly when encoding/json decodes
// them, and to the same text. The alphabet mixes plain ASCII, escapes,
// controls, multibyte UTF-8 and invalid bytes, so runs, escapes and
// replacement characters interleave.
func TestDecoder_StringsMatchEncodingJSON(t *testing.T) {
	pieces := []string{"a", "Z", " ", "~", "\x7f", "\\", "\"", "n", "u", "0", "D", "8", "d", "e", "\\n", "\\u00e9", "\\ud83d", "\\ude00", "\n", "\x01", "\x1f", "é", "😀", "\xff", "\xe2\x82"}
	rng := rand.New(rand.NewSource(11))
	for n := 0; n < 20000; n++ {
		var sb strings.Builder
		sb.WriteByte('"')
		for k := rng.Intn(10); k > 0; k-- {
			sb.WriteString(pieces[rng.Intn(len(pieces))])
		}
		sb.WriteByte('"')
		in := sb.String()
		var want string
		wantErr := stdjson.Unmarshal([]byte(in), &want)
		var got string
		err := decodeDocument(in, func(d *JsonDecoderImpl) { got = d.ReadString() })
		if (err != "") != (wantErr != nil) {
			t.Fatalf("ReadString(%q) error = %q, encoding/json error = %v", in, err, wantErr)
		}
		if wantErr == nil && got != want {
			t.Fatalf("ReadString(%q) = %q, encoding/json reads %q", in, got, want)
		}
	}
}

// ----- Decoder: skipping ----------------------------------------------------

// Skip steps over any one valid JSON value, and rejects what encoding/json
// rejects in it.
func TestDecoder_SkipValidatesValue(t *testing.T) {
	tests := []struct {
		in    string
		valid bool
	}{
		{`{}`, true},
		{`[]`, true},
		{` { "a" : [ 1 , -0.5e3 , "x\"]" , true , false , null , { } , [ ] ] , "b" : { "c" : "}" } } `, true},
		{`[[[[[]]]]]`, true},
		{`"plain"`, true},
		{`true`, true},
		{`null`, true},

		{`[1 2]`, false},
		{`[1,]`, false},
		{`[,1]`, false},
		{`{"a":1,}`, false},
		{`{"a" 1}`, false},
		{`{"a":}`, false},
		{`{a:1}`, false},
		{`{"a":1 "b":2}`, false},
		{`[1}`, false},
		{`{"a":1]`, false},
		{`[`, false},
		{`{"a":`, false},
		{`[tru]`, false},
		{`[nul]`, false},
		{`["a]`, false},
		{`[01]`, false},
		{`@`, false},
		{``, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := stdjson.Valid([]byte(tt.in)); got != tt.valid {
				t.Fatalf("test table disagrees with encoding/json: Valid(%s) = %v", tt.in, got)
			}
			err := decodeDocument(tt.in, skipValue)
			if tt.valid && err != "" {
				t.Fatalf("Skip(%s) failed: %s", tt.in, err)
			}
			if !tt.valid && err == "" {
				t.Fatalf("Skip(%s) did not fail", tt.in)
			}
		})
	}
}

// Skip consumes the element separator in an array, so skipped and read
// elements interleave.
func TestDecoder_SkipInsideArray(t *testing.T) {
	d := NewJsonDecoder(`[{"x":[1,2]}, 3, "s", [4]]`)
	d.StartArray()
	d.Skip()
	if got := d.ReadInt(); got != 3 {
		t.Fatalf("ReadInt = %d, want 3", got)
	}
	d.Skip()
	d.Skip()
	if d.HasMoreElements() {
		t.Fatalf("elements left after skipping all of them")
	}
	d.EndArray()
	d.End()
}
