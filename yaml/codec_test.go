package yaml

import (
	"fmt"
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

// ----- Quoted scalars --------------------------------------------------------

// readKeyValue decodes a one-pair mapping and returns its key and the string
// read for its value, or the error the decode panicked with.
func readKeyValue(src string) (k string, s string, err string) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Sprint(r)
		}
	}()
	d := NewYamlDecoder(src)
	d.StartObject()
	k = d.ReadKey()
	s = d.ReadString()
	d.EndObject()
	return k, s, ""
}

// readValueOf decodes "k: <value>" and returns the string read for k, or the
// error the decode panicked with.
func readValueOf(value string) (string, string) {
	_, s, err := readKeyValue("k: " + value)
	return s, err
}

func unterminated(s string) string { return fmt.Sprintf("yaml: unterminated quoted scalar %q", s) }

func textAfter(s string) string {
	return fmt.Sprintf("yaml: unexpected text after quoted scalar %q", s)
}

// A scalar that starts with a quote is exactly one quoted scalar, optionally
// followed by a comment (#1296). Anything else is a decode error rather than
// plain text that keeps the quote.
func TestDecoder_QuotedScalarMustBeWellFormed(t *testing.T) {
	tests := []struct{ value, want, wantErr string }{
		{`"abc"`, "abc", ""},
		{`'abc'`, "abc", ""},
		{`""`, "", ""},
		{`''`, "", ""},
		{`"a\"b"`, `a"b`, ""},
		{`"a\\"`, `a\`, ""},
		{`'it''s'`, "it's", ""},
		{`''''`, "'", ""},
		{`"it's"`, "it's", ""},
		{`'say "hi"'`, `say "hi"`, ""},
		{`"a # b"`, "a # b", ""},
		{`"abc"   # comment`, "abc", ""},
		{`'abc' # comment`, "abc", ""},
		{`abc"`, `abc"`, ""},
		{`it's`, "it's", ""},

		{`"abc`, "", unterminated(`"abc`)},
		{`'abc`, "", unterminated(`'abc`)},
		{`"`, "", unterminated(`"`)},
		{`'`, "", unterminated(`'`)},
		{`"abc\"`, "", unterminated(`"abc\"`)},
		{`'it''s`, "", unterminated(`'it''s`)},
		{`"abc" trailing`, "", textAfter(`"abc" trailing`)},
		{`'abc' trailing`, "", textAfter(`'abc' trailing`)},
		{`"a" "b"`, "", textAfter(`"a" "b"`)},
		{`"abc"#x`, "", textAfter(`"abc"#x`)},
	}
	for _, tt := range tests {
		got, err := readValueOf(tt.value)
		if err != tt.wantErr || got != tt.want {
			t.Errorf("k: %s => (%q, error %q), want (%q, error %q)", tt.value, got, err, tt.want, tt.wantErr)
		}
	}
}

// The same rule holds for a sequence item, a quoted key's value and a
// one-line document.
func TestDecoder_QuotedScalarContexts(t *testing.T) {
	tests := []struct{ src, wantErr string }{
		{"- \"abc", unterminated(`"abc`)},
		{"- 'abc' trailing", textAfter(`'abc' trailing`)},
		{"\"k\": \"abc", unterminated(`"abc`)},
		{"\"abc", unterminated(`"abc`)},
	}
	for _, tt := range tests {
		func() {
			defer func() {
				if r := recover(); r != tt.wantErr {
					t.Errorf("NewYamlDecoder(%q) panicked with %v, want %q", tt.src, r, tt.wantErr)
				}
			}()
			NewYamlDecoder(tt.src)
		}()
	}
}

// A comment after a quoted key's value is not part of the value, as it is
// not after a plain key's. A '#' with no white space before it is text.
func TestDecoder_QuotedKeyValueComment(t *testing.T) {
	tests := []struct{ src, want string }{
		{`"k": "v" # note`, "v"},
		{`"k": v # note`, "v"},
		{`'k': 'v' # note`, "v"},
		{`"k":v # note`, "v"},
		{`"k":#v`, "#v"},
		{`"k":#v # note`, "#v"},
		{`"k": v#w`, "v#w"},
	}
	for _, tt := range tests {
		if k, s, err := readKeyValue(tt.src); k != "k" || s != tt.want || err != "" {
			t.Errorf("%s => (%q, %q, error %q), want (%q, %q)", tt.src, k, s, err, "k", tt.want)
		}
	}
}

// ----- Block sequences at their key's indentation ----------------------------

// dumpNode writes a parsed document in a compact flow form, so that two
// documents can be compared by their structure.
func dumpNode(n *yNode) string {
	switch {
	case n == nil:
		return "<nil>"
	case n.kind == yScalar && n.nullish:
		return "~"
	case n.kind == yScalar:
		return n.scalar
	case n.kind == ySeq:
		items := make([]string, len(n.seq))
		for i, c := range n.seq {
			items[i] = dumpNode(c)
		}
		return "[" + strings.Join(items, ", ") + "]"
	default:
		pairs := make([]string, len(n.mapKeys))
		for i, k := range n.mapKeys {
			pairs[i] = k + ": " + dumpNode(n.mapVals[i])
		}
		return "{" + strings.Join(pairs, ", ") + "}"
	}
}

// parseDump parses src and dumps it, or returns the error the parse
// panicked with.
func parseDump(src string) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = "error: " + fmt.Sprint(r)
		}
	}()
	return dumpNode(parseYAML(src))
}

// A block sequence that is a mapping value may sit at the key's own
// indentation (#1295), as kubectl writes it. Each document parses to the
// same structure as its indented form.
func TestDecoder_SequenceAtKeyIndent(t *testing.T) {
	tests := []struct{ name, zero, indented, want string }{
		{
			"root key",
			"items:\n- 1\n- 2",
			"items:\n  - 1\n  - 2",
			"{items: [1, 2]}",
		},
		{
			"keys before and after",
			"a: x\nitems:\n- 1\n- 2\nb: y",
			"a: x\nitems:\n  - 1\n  - 2\nb: y",
			"{a: x, items: [1, 2], b: y}",
		},
		{
			"two sequences in a row",
			"n:\n- 1\nm:\n- 2\n- 3",
			"n:\n  - 1\nm:\n  - 2\n  - 3",
			"{n: [1], m: [2, 3]}",
		},
		{
			"nested mapping key",
			"spec:\n  containers:\n  - name: app\n    image: img\n  - name: side\n  restart: Always",
			"spec:\n  containers:\n    - name: app\n      image: img\n    - name: side\n  restart: Always",
			"{spec: {containers: [{name: app, image: img}, {name: side}], restart: Always}}",
		},
		{
			"key inside a sequence item",
			"- name: app\n  ports:\n  - 80\n  - 443\n  image: img",
			"- name: app\n  ports:\n    - 80\n    - 443\n  image: img",
			"[{name: app, ports: [80, 443], image: img}]",
		},
		{
			"first key of a sequence item",
			"- args:\n  - run\n  image: img",
			"- args:\n    - run\n  image: img",
			"[{args: [run], image: img}]",
		},
		{
			"sequence of sequences",
			"m:\n-\n  - 1\n  - 2\n-\n  - 3",
			"m:\n  -\n    - 1\n    - 2\n  -\n    - 3",
			"{m: [[1, 2], [3]]}",
		},
		{
			"empty items and comments",
			"m:\n# note\n-\n- x # c\nk: v",
			"m:\n  # note\n  -\n  - x # c\nk: v",
			"{m: [~, x], k: v}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseDump(tt.zero); got != tt.want {
				t.Errorf("zero-indented:\n%s\n=> %s, want %s", tt.zero, got, tt.want)
			}
			if got := parseDump(tt.indented); got != tt.want {
				t.Errorf("indented:\n%s\n=> %s, want %s", tt.indented, got, tt.want)
			}
		})
	}
}

// A key of a sequence item whose value is on the lines below no longer ends
// the item: the keys after it belong to the same item.
func TestDecoder_SequenceItemKeysAfterBlockValue(t *testing.T) {
	tests := []struct{ src, want string }{
		{"- a:\n    x: 1\n  b: 2", "[{a: {x: 1}, b: 2}]"},
		{"- a:\n  b: 2", "[{a: ~, b: 2}]"},
		{"- a:\n    - 1\n  b: 2\n- c: 3", "[{a: [1], b: 2}, {c: 3}]"},
	}
	for _, tt := range tests {
		if got := parseDump(tt.src); got != tt.want {
			t.Errorf("%q => %s, want %s", tt.src, got, tt.want)
		}
	}
}

// What is not a value of the key before it still fails: a sequence after a
// key that has an inline value, and a sequence less indented than its key.
func TestDecoder_SequenceAtKeyIndentErrors(t *testing.T) {
	tests := []struct{ src, want string }{
		{"a: x\n- 1", "error: yaml: unexpected content at line 2"},
		{"a:\n  b:\n- 1", "error: yaml: unexpected content at line 3"},
		{"- a: x\n  - 1", "error: yaml: unexpected indent at line 2"},
	}
	for _, tt := range tests {
		if got := parseDump(tt.src); got != tt.want {
			t.Errorf("%q => %s, want %s", tt.src, got, tt.want)
		}
	}
}
