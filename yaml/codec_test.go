package yaml

import (
	"strings"
	"testing"
)

// ----- Encoder unit tests --------------------------------------------------

func TestEncoder_FlatMapping(t *testing.T) {
	e := NewYamlEncoder()
	e.WriteStartObject()
	e.WriteKey("name")
	e.WriteString("auth")
	e.WriteKey("port")
	e.WriteInt(8080)
	e.WriteKey("tls")
	e.WriteBool(true)
	e.WriteEndObject()

	got := e.String()
	want := "name: auth\nport: 8080\ntls: true"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEncoder_NestedMapping(t *testing.T) {
	e := NewYamlEncoder()
	e.WriteStartObject()
	e.WriteKey("project")
	e.WriteStartObject()
	e.WriteKey("name")
	e.WriteString("auth")
	e.WriteKey("repo")
	e.WriteString(".")
	e.WriteEndObject()
	e.WriteKey("team")
	e.WriteString("Skunkworks")
	e.WriteEndObject()

	want := strings.Join([]string{
		"project:",
		"  name: auth",
		"  repo: .",
		"team: Skunkworks",
	}, "\n")
	if got := e.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEncoder_SequenceOfScalars(t *testing.T) {
	e := NewYamlEncoder()
	e.WriteStartObject()
	e.WriteKey("tags")
	e.WriteStartArray()
	e.WriteString("a")
	e.WriteString("b")
	e.WriteString("c")
	e.WriteEndArray()
	e.WriteEndObject()

	want := "tags:\n  - a\n  - b\n  - c"
	if got := e.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEncoder_SequenceOfMappings_Compact(t *testing.T) {
	e := NewYamlEncoder()
	e.WriteStartObject()
	e.WriteKey("members")
	e.WriteStartArray()

	e.WriteStartObject()
	e.WriteKey("role")
	e.WriteString("lead")
	e.WriteKey("name")
	e.WriteString("Iris")
	e.WriteEndObject()

	e.WriteStartObject()
	e.WriteKey("role")
	e.WriteString("engineer")
	e.WriteKey("name")
	e.WriteString("Felix")
	e.WriteEndObject()

	e.WriteEndArray()
	e.WriteEndObject()

	want := strings.Join([]string{
		"members:",
		"  - role: lead",
		"    name: Iris",
		"  - role: engineer",
		"    name: Felix",
	}, "\n")
	if got := e.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEncoder_QuotesReservedAndNumeric(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"true", `"true"`},
		{"42", `"42"`},
		{"3.14", `"3.14"`},
		{"null", `"null"`},
		{"yes", `"yes"`},
		{"", `""`},
		{"has: colon-space", `"has: colon-space"`},
		{"trailing space ", `"trailing space "`},
		{"line1\nline2", `"line1\nline2"`},
		{"#comment", `"#comment"`},
		// A trailing colon would read back as a mapping key.
		{"key:", `"key:"`},
		{"normal text", "normal text"},
	}
	for _, c := range cases {
		got := encodeString(c.in)
		if got != c.want {
			t.Errorf("encodeString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ----- Decoder unit tests --------------------------------------------------

func TestDecoder_FlatMapping(t *testing.T) {
	d := NewYamlDecoder("name: auth\nport: 8080\ntls: true\n")
	d.StartObject()

	if k := d.ReadKey(); k != "name" {
		t.Fatalf("key1 = %q", k)
	}
	if s := d.ReadString(); s != "auth" {
		t.Fatalf("name = %q", s)
	}
	if k := d.ReadKey(); k != "port" {
		t.Fatalf("key2 = %q", k)
	}
	if n := d.ReadInt(); n != 8080 {
		t.Fatalf("port = %d", n)
	}
	if k := d.ReadKey(); k != "tls" {
		t.Fatalf("key3 = %q", k)
	}
	if b := d.ReadBool(); !b {
		t.Fatalf("tls = false")
	}
	if d.HasMoreFields() {
		t.Fatalf("expected no more fields")
	}
	d.EndObject()
}

func TestDecoder_NestedAndSequences(t *testing.T) {
	src := strings.Join([]string{
		"project:",
		"  name: auth-service",
		"  repo: .",
		"members:",
		"  - role: team_lead",
		"    name: Iris",
		"  - role: engineer",
		"    name: Felix",
	}, "\n")

	d := NewYamlDecoder(src)
	d.StartObject()

	if k := d.ReadKey(); k != "project" {
		t.Fatalf("key project, got %q", k)
	}
	d.StartObject()
	d.ReadKey() // name
	if s := d.ReadString(); s != "auth-service" {
		t.Fatalf("project.name = %q", s)
	}
	d.ReadKey() // repo
	if s := d.ReadString(); s != "." {
		t.Fatalf("project.repo = %q", s)
	}
	d.EndObject()

	if k := d.ReadKey(); k != "members" {
		t.Fatalf("key members, got %q", k)
	}
	d.StartArray()

	d.StartObject()
	d.ReadKey()
	if s := d.ReadString(); s != "team_lead" {
		t.Fatalf("member0.role = %q", s)
	}
	d.ReadKey()
	if s := d.ReadString(); s != "Iris" {
		t.Fatalf("member0.name = %q", s)
	}
	d.EndObject()

	d.StartObject()
	d.ReadKey()
	if s := d.ReadString(); s != "engineer" {
		t.Fatalf("member1.role = %q", s)
	}
	d.ReadKey()
	if s := d.ReadString(); s != "Felix" {
		t.Fatalf("member1.name = %q", s)
	}
	d.EndObject()

	if d.HasMoreElements() {
		t.Fatalf("expected only 2 members")
	}
	d.EndArray()
	d.EndObject()
}

func TestDecoder_LiteralBlockScalar(t *testing.T) {
	src := strings.Join([]string{
		"personality: |",
		"  Calm and",
		"  decisive.",
		"name: Iris",
	}, "\n")
	d := NewYamlDecoder(src)
	d.StartObject()
	d.ReadKey() // personality
	got := d.ReadString()
	want := "Calm and\ndecisive."
	if got != want {
		t.Fatalf("personality = %q, want %q", got, want)
	}
	d.ReadKey() // name
	if s := d.ReadString(); s != "Iris" {
		t.Fatalf("name = %q", s)
	}
	d.EndObject()
}

func TestDecoder_QuotedAndComments(t *testing.T) {
	src := strings.Join([]string{
		`# comment line`,
		`label: "has: colon and \"quote\""    # trailing comment`,
		`note: 'single ''quoted'''`,
		`number: "42"`,
	}, "\n")
	d := NewYamlDecoder(src)
	d.StartObject()
	d.ReadKey()
	if s := d.ReadString(); s != `has: colon and "quote"` {
		t.Fatalf("label = %q", s)
	}
	d.ReadKey()
	if s := d.ReadString(); s != `single 'quoted'` {
		t.Fatalf("note = %q", s)
	}
	d.ReadKey()
	if s := d.ReadString(); s != `42` {
		t.Fatalf("number = %q", s)
	}
	d.EndObject()
}

// ----- Double-quoted escapes -------------------------------------------------

// Double-quoted scalars follow the YAML 1.2 escape set. \x, \u and \U name
// Unicode code points and decode to UTF-8; the one-character escapes cover the
// C0 controls and the Unicode line/space characters.
func TestDecoder_DoubleQuotedEscapes(t *testing.T) {
	tests := []struct {
		name, quoted, want string
	}{
		{"common escapes", `"a\tb\nc\r\"d\"\\e\/f"`, "a\tb\nc\r\"d\"\\e/f"},
		{"control escapes", `"\0\a\b\v\f\e"`, "\x00\a\b\v\f\x1b"},
		{"escaped space and tab", `"\ x\	y"`, " x\ty"},
		{"unicode line and space characters", `"\N\_\L\P"`, "\u0085\u00a0\u2028\u2029"},
		{"8-bit code point", `"caf\xe9"`, "café"},
		{"16-bit code point", `"caf\u00e9"`, "café"},
		{"32-bit code point", `"smile \U0001F600"`, "smile 😀"},
		{"ASCII control from the encoder", `"bell\x07"`, "bell\a"},
		{"invalid hex kept verbatim", `"bad\xZZ!"`, `bad\xZZ!`},
		{"truncated escape kept verbatim", `"short\u00e"`, `short\u00e`},
		{"surrogate code point kept verbatim", `"\uD800"`, `\uD800`},
		{"out-of-range code point kept verbatim", `"\U00110000"`, `\U00110000`},
		{"unknown escape kept verbatim", `"\q"`, `\q`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewYamlDecoder("value: " + tt.quoted + "\n" + tt.quoted + ": key")
			d.StartObject()
			d.ReadKey()
			if got := d.ReadString(); got != tt.want {
				t.Errorf("value %s = %q, want %q", tt.quoted, got, tt.want)
			}
			if got := d.ReadKey(); got != tt.want {
				t.Errorf("key %s = %q, want %q", tt.quoted, got, tt.want)
			}
			d.ReadString()
			d.EndObject()
		})
	}
}

// Strings the encoder double-quotes decode back to the original.
func TestEncoderDecoder_QuotedStringRoundTrip(t *testing.T) {
	for _, s := range []string{"tab\tand\nnewline", "ctrl\x01\x1f", `quote " and \ backslash`, "café 😀", " padded "} {
		e := NewYamlEncoder()
		e.WriteStartObject()
		e.WriteKey("v")
		e.WriteString(s)
		e.WriteEndObject()

		d := NewYamlDecoder(e.String())
		d.StartObject()
		d.ReadKey()
		if got := d.ReadString(); got != s {
			t.Errorf("round trip of %q through %q = %q", s, e.String(), got)
		}
		d.EndObject()
	}
}

// ----- Document roots and comments -------------------------------------------

// A key ending in ':' is quoted, so it reads back as the same key rather than
// as a nested mapping.
func TestEncoderDecoder_KeyWithTrailingColon(t *testing.T) {
	e := NewYamlEncoder()
	e.WriteStartObject()
	e.WriteKey("key:")
	e.WriteString("v")
	e.WriteEndObject()

	d := NewYamlDecoder(e.String())
	d.StartObject()
	if k := d.ReadKey(); k != "key:" {
		t.Fatalf("key = %q, document:\n%s", k, e.String())
	}
	if s := d.ReadString(); s != "v" {
		t.Fatalf("value = %q", s)
	}
	d.EndObject()
}

// A one-line document is a scalar, and a trailing comment is not part of it,
// as for a mapping value.
func TestDecoder_RootScalarComment(t *testing.T) {
	if s := NewYamlDecoder("abc # note").ReadString(); s != "abc" {
		t.Errorf("ReadString = %q, want %q", s, "abc")
	}
	if n := NewYamlDecoder("42 # answer").ReadInt(); n != 42 {
		t.Errorf("ReadInt = %d, want 42", n)
	}
	if s := NewYamlDecoder(`"a # b"`).ReadString(); s != "a # b" {
		t.Errorf("quoted ReadString = %q, want %q", s, "a # b")
	}
}

func TestDecoder_SequenceItemComment(t *testing.T) {
	d := NewYamlDecoder("- abc # note\n- 2 # two")
	d.StartArray()
	if s := d.ReadString(); s != "abc" {
		t.Errorf("item 0 = %q, want %q", s, "abc")
	}
	if n := d.ReadInt(); n != 2 {
		t.Errorf("item 1 = %d, want 2", n)
	}
	if d.HasMoreElements() {
		t.Fatalf("expected no more elements")
	}
	d.EndArray()
}

// An empty document is null — an Option root decodes it as None — and still
// reads as an empty mapping where a struct is expected.
func TestDecoder_EmptyDocumentIsNull(t *testing.T) {
	for _, src := range []string{"", "# only a comment\n"} {
		d := NewYamlDecoder(src)
		if !d.IsNull() {
			t.Fatalf("IsNull(%q) = false", src)
		}
		d.ReadNull()

		d = NewYamlDecoder(src)
		d.StartObject()
		if d.HasMoreFields() {
			t.Fatalf("empty document %q has fields", src)
		}
		d.EndObject()
	}
	if NewYamlDecoder("{}").IsNull() {
		t.Fatalf(`IsNull("{}") = true`)
	}
}
