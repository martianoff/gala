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
	want := "Calm and\ndecisive.\n"
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
		{"highest code point", `"\U0010FFFF"`, "\U0010FFFF"},
		{"escaped backslash before a letter", `"\\q"`, `\q`},
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

// An escape YAML does not define fails the decode (#1316), in a value and in
// a key alike, instead of reaching the string as written.
func TestDecoder_UndefinedEscapesFail(t *testing.T) {
	tests := []struct{ quoted, escape string }{
		{`"\q"`, `\q`},
		{`"a\qb"`, `\q`},
		{`"\'"`, `\'`},
		{`"bad\xZZ!"`, `\xZZ`},
		{`"\x4"`, `\x4`},
		{`"\x4\n"`, `\x4`},
		{`"\u12 abc"`, `\u12`},
		{`"short\u00e"`, `\u00e`},
		{`"\u00g9"`, `\u00g9`},
		{`"\uD800"`, `\uD800`},
		{`"\U00110000"`, `\U00110000`},
		{`"\U0000"`, `\U0000`},
		{`"\U80000000"`, `\U80000000`},
		{`"\UFFFFFFFF"`, `\UFFFFFFFF`},
		{`"\é"`, `\é`},
		{`"\x4é"`, `\x4é`},
		{`"\uDE00"`, `\uDE00`},
		{`"\uD83D x"`, `\uD83D`},
		{`"\uD83D\uD83D"`, `\uD83D`},
		{"\"\\\x7f\"", `"\\\x7f"`},
		{`"\uD83D\uFFFD"`, `\uD83D`},
	}
	for _, tt := range tests {
		want := "yaml: invalid escape " + tt.escape + " in double-quoted scalar"
		for _, src := range []string{"k: " + tt.quoted, tt.quoted + ": v"} {
			_, _, err := readKeyValue(src)
			if err != want {
				t.Errorf("%s: error %q, want %q", src, err, want)
			}
		}
	}
}

// Bodies the parser never passes on still fail cleanly: one that ends in a
// lone backslash, and one with an invalid UTF-8 byte (the decoder replaces
// those before parsing), whose escape is quoted in the error.
func TestUnescapeDoubleQuoted_UnreachableBodies(t *testing.T) {
	tests := []struct{ body, want string }{
		{`a\`, `yaml: invalid escape \ in double-quoted scalar`},
		{"a\\\xff", `yaml: invalid escape "\\\xff" in double-quoted scalar`},
	}
	for _, tt := range tests {
		func() {
			defer func() {
				if r := fmt.Sprint(recover()); r != tt.want {
					t.Errorf("unescapeDoubleQuoted(%q) panicked with %q, want %q", tt.body, r, tt.want)
				}
			}()
			unescapeDoubleQuoted(tt.body)
		}()
	}
}

// A high and a low surrogate written as two escapes, as JSON writes a
// character above U+FFFF, decode to that character; either half alone is an
// error. The escapes are built from bytes, and their length checked, so that
// no editor can turn them into the character they name.
func TestDecoder_SurrogatePairEscape(t *testing.T) {
	high := string([]byte{'\\', 'u', 'D', '8', '3', 'D'})
	low := string([]byte{'\\', 'u', 'D', 'E', '0', '0'})
	if len(high) != 6 || len(low) != 6 {
		t.Fatalf("escape lengths %d and %d, want 6 and 6", len(high), len(low))
	}
	want := string(rune(0x1F600))
	for _, pair := range []string{high + low, strings.ToLower(high + low)} {
		if _, s, err := readKeyValue(`k: "` + pair + ` x"`); s != want+" x" || err != "" {
			t.Errorf("value %s => (%q, error %q), want %q", pair, s, err, want+" x")
		}
		if k, _, err := readKeyValue(`"` + pair + ` x": v`); k != want+" x" || err != "" {
			t.Errorf("key %s => (%q, error %q), want %q", pair, k, err, want+" x")
		}
	}
	for _, half := range []string{high, low, low + high} {
		wantErr := "yaml: invalid escape " + half[:6] + " in double-quoted scalar"
		for _, src := range []string{`k: "` + half + `"`, `"` + half + `": v`} {
			if _, _, err := readKeyValue(src); err != wantErr {
				t.Errorf("%s => error %q, want %q", src, err, wantErr)
			}
		}
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
		return "<null>"
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
			"quoted key with a comment",
			"\"items\": # list\n- 1\n- 2",
			"\"items\": # list\n  - 1\n  - 2",
			"{items: [1, 2]}",
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
			"{m: [<null>, x], k: v}",
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
		{"- a:\n  b: 2", "[{a: <null>, b: 2}]"},
		{"- a:\n    - 1\n  b: 2\n- c: 3", "[{a: [1], b: 2}, {c: 3}]"},
		// The item's keys line up with the first key, wherever the spaces
		// after the dash put it.
		{"-   a: 1\n    b: 2", "[{a: 1, b: 2}]"},
		{"-   a:\n    - 1\n    b: 2", "[{a: [1], b: 2}]"},
		// A line at the first key's column is a sibling key, not its value.
		{"-  a:\n   x: 1", "[{a: <null>, x: 1}]"},
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
		{"-   a:\n  - 1", "error: yaml: unexpected indent at line 2"},
		{"-  a:\n  - 1\n  b: 2", "error: yaml: unexpected indent at line 2"},
		// The item's later keys must line up with its first key.
		{"-  a: 1\n  b: 2", "error: yaml: unexpected indent at line 2"},
		{"-   a:\n   x: 1", "error: yaml: unexpected indent at line 2"},
		{"-   a:\n   - 1", "error: yaml: unexpected indent at line 2"},
	}
	for _, tt := range tests {
		if got := parseDump(tt.src); got != tt.want {
			t.Errorf("%q => %s, want %s", tt.src, got, tt.want)
		}
	}
}

// ----- Quotes inside keys and plain scalars ----------------------------------

// A quote starts a quoted scalar only as its first character (#1314): inside
// a plain key or value it is text, so it hides neither the key's colon nor a
// comment. A single-quoted key may hold a quote written twice (#1315).
func TestDecoder_QuotesInsideScalars(t *testing.T) {
	tests := []struct{ src, key, value string }{
		{"it's: 1", "it's", "1"},
		{`say "hi: there`, `say "hi`, "there"},
		{`a"b: c # note`, `a"b`, "c"},
		{"S: it's # note", "S", "it's"},
		{`S: a"b # note`, "S", `a"b`},
		{"S: it's", "S", "it's"},
		{"'it''s': 1", "it's", "1"},
		{"'a''b''': v", "a'b'", "v"},
		{"'': v", "", "v"},
		{`"k\"q": v`, `k"q`, "v"},
		{"S: 'it''s' # note", "S", "it's"},
		{"'k': 'v # w' # note", "k", "v # w"},
		{`"k": "a # b" # note`, "k", "a # b"},
	}
	for _, tt := range tests {
		if k, s, err := readKeyValue(tt.src); k != tt.key || s != tt.value || err != "" {
			t.Errorf("%s => (%q, %q, error %q), want (%q, %q)", tt.src, k, s, err, tt.key, tt.value)
		}
	}
}

// The same holds for a sequence item, and for a key the encoder writes
// plain: a map with the key it's reads back.
func TestDecoder_QuotesInsideScalarsElsewhere(t *testing.T) {
	d := NewYamlDecoder("- it's # note\n- it's: 1")
	d.StartArray()
	if s := d.ReadString(); s != "it's" {
		t.Errorf("item 0 = %q, want %q", s, "it's")
	}
	d.StartObject()
	if k := d.ReadKey(); k != "it's" {
		t.Errorf("item 1 key = %q, want %q", k, "it's")
	}
	if n := d.ReadInt(); n != 1 {
		t.Errorf("item 1 value = %d, want 1", n)
	}
	d.EndObject()
	d.EndArray()

	e := NewYamlEncoder()
	e.WriteStartObject()
	e.WriteKey("it's")
	e.WriteInt(1)
	e.WriteEndObject()
	d = NewYamlDecoder(e.String())
	d.StartObject()
	if k := d.ReadKey(); k != "it's" {
		t.Errorf("encoded %q: key = %q, want %q", e.String(), k, "it's")
	}
	if n := d.ReadInt(); n != 1 {
		t.Errorf("encoded value = %d, want 1", n)
	}
	d.EndObject()
}

// A "- key: value" item splits the pair before it drops a comment, so a
// quoted value holding " #" stays whole, as in a mapping line.
func TestDecoder_CompactItemQuotedValueWithHash(t *testing.T) {
	tests := []struct{ src, want string }{
		{`- k: "a # b"`, "[{k: a # b}]"},
		{`- k: "a # b" # note`, "[{k: a # b}]"},
		{`- "k": 'a # b' # note`, "[{k: a # b}]"},
		{`- k: it's # note`, "[{k: it's}]"},
		{"- k: v\n  j: \"c # d\" # note", "[{k: v, j: c # d}]"},
		{"- # note: x", "[<null>]"},
	}
	for _, tt := range tests {
		if got := parseDump(tt.src); got != tt.want {
			t.Errorf("%q => %s, want %s", tt.src, got, tt.want)
		}
	}

	// An item whose first value the encoder quotes for its " #" reads back.
	e := NewYamlEncoder()
	e.WriteStartArray()
	e.WriteStartObject()
	e.WriteKey("S")
	e.WriteString("a # b")
	e.WriteEndObject()
	e.WriteEndArray()
	if got, want := parseDump(e.String()), "[{S: a # b}]"; got != want {
		t.Errorf("encoded %q => %s, want %s", e.String(), got, want)
	}
}

// A ": " inside a comment is not a key's colon.
func TestDecoder_ColonInCommentIsNotAKey(t *testing.T) {
	tests := []struct{ src, want string }{
		{"hello # note: x", "hello"},
		{"k: v # note: x", "{k: v}"},
		{"k: v\ntitle # the user's: x", "error: yaml: expected 'key:' at line 2, got \"title # the user's: x\""},
	}
	for _, tt := range tests {
		if got := parseDump(tt.src); got != tt.want {
			t.Errorf("%q => %s, want %s", tt.src, got, tt.want)
		}
	}
}

// Quotes in the middle of a plain scalar hide nothing (YAML 1.2): a ": "
// after them still makes a key, and a " #" after them still starts a
// comment. A value with either has to be quoted as a whole.
func TestDecoder_QuotedPartOfPlainScalar(t *testing.T) {
	tests := []struct{ src, want string }{
		{`- echo "Status: ok"`, `[{echo "Status: ok"}]`},
		{`k: echo "a # b"`, `{k: echo "a}`},
		{`- "echo Status: ok"`, `[echo Status: ok]`},
		{`k: 'echo "a # b"'`, `{k: echo "a # b"}`},
	}
	for _, tt := range tests {
		if got := parseDump(tt.src); got != tt.want {
			t.Errorf("%q => %s, want %s", tt.src, got, tt.want)
		}
	}
}

// Flow-style text, which this parser keeps as a string, keeps its quoted
// items whole: a "#" or ": " inside one is neither a comment nor a key's
// colon. A comment after the flow text is still dropped.
func TestDecoder_FlowTextKeepsQuotedItems(t *testing.T) {
	tests := []struct{ src, want string }{
		{`key: [a, "b # c"]`, `{key: [a, "b # c"]}`},
		{`key: [a, "b # c"] # note`, `{key: [a, "b # c"]}`},
		{`k: {"a": "b # c"}`, `{k: {"a": "b # c"}}`},
		{`- [1, "a # b"]`, `[[1, "a # b"]]`},
		{`- ["x: y"]`, `[["x: y"]]`},
		{`- ['it''s: x']`, `[['it''s: x']]`},
		{`"k":[a, "b # c"]`, `{k: [a, "b # c"]}`},
		{`"k":{"a": "b # c"} # note`, `{k: {"a": "b # c"}}`},
	}
	for _, tt := range tests {
		if got := parseDump(tt.src); got != tt.want {
			t.Errorf("%q => %s, want %s", tt.src, got, tt.want)
		}
	}
}

// A document that is a literal block reads as a string, whatever its body
// holds: a ": " in it does not make a key.
func TestDecoder_BareLiteralBlockIsAString(t *testing.T) {
	if got, want := parseDump("|\n  a: b\n  c\n"), "a: b\nc\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ----- Blocks opened on a sequence item's line ------------------------------

// A sequence item can open a literal block ("- |") or a nested sequence
// ("- - x") on the dash's line, and a literal block on an item's first key
// ends where the item's next key starts (#1320).
func TestDecoder_BlocksOnSequenceItemLine(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"literal item", "script:\n- |\n  echo hi\n  echo bye\n- make", "{script: [echo hi\necho bye\n, make]}"},
		{"literal item, strip", "- |-\n  a\n- b", "[a, b]"},
		{"literal item, indented", "script:\n  - |\n    echo hi\n  - make", "{script: [echo hi\n, make]}"},
		{"literal on the first key", "- run: |\n    echo hi\n  name: x", "[{run: echo hi\n, name: x}]"},
		{"empty literal on the first key", "- run: |\n  name: x", "[{run: , name: x}]"},
		{"literal on a later key", "- name: x\n  run: |\n    echo hi\n  shell: sh", "[{name: x, run: echo hi\n, shell: sh}]"},
		{"nested sequence", "- - x\n  - y\n- z", "[[x, y], z]"},
		{"nested sequence of mappings", "- - a: 1\n    b: 2\n  - a: 3", "[[{a: 1, b: 2}, {a: 3}]]"},
		{"nested sequence under a key", "m:\n- - 1\n  - 2\n- - 3", "{m: [[1, 2], [3]]}"},
		{"nested bare dash", "- -\n  - x", "[[<null>, x]]"},
		{"three levels", "- - - x", "[[[x]]]"},
		{"literal item one level down", "- - |\n    a\n- b", "[[a\n], b]"},
		{"literal on a nested item's first key", "- - run: |\n    name: x", "[[{run: , name: x}]]"},
		{"literal on a nested item's first key, with body", "- - run: |\n      echo hi\n    name: x", "[[{run: echo hi\n, name: x}]]"},
	}
	for _, tt := range tests {
		if got := parseDump(tt.src); got != tt.want {
			t.Errorf("%s: %q => %s, want %s", tt.name, tt.src, got, tt.want)
		}
	}
}

// A comment after a block indicator does not hide it, and a comment that
// ends in ": |" does not open a block.
func TestDecoder_LiteralBlockAndComments(t *testing.T) {
	tests := []struct{ src, want string }{
		{"script: | # shell\n  echo hi", "{script: echo hi\n}"},
		{"- | # note\n  a", "[a\n]"},
		{"parent: # list: |\n  child: 1", "{parent: {child: 1}}"},
		{"- k: v # ex: |\n  j: w", "[{k: v, j: w}]"},
	}
	for _, tt := range tests {
		if got := parseDump(tt.src); got != tt.want {
			t.Errorf("%q => %s, want %s", tt.src, got, tt.want)
		}
	}
}

// A tab between an item's dash and a block indicator ("- \t|") does not
// open a block: the item's text is not decoded as the block's body.
func TestDecoder_TabBeforeItemBlockIndicator(t *testing.T) {
	for _, src := range []string{"- \t|\n  a\n- b", "- - \t|\n    a"} {
		if got := parseDump(src); !strings.HasPrefix(got, "error: ") {
			t.Errorf("%q => %s, want an error", src, got)
		}
	}
}

// A line of nested dashes parses to that many nested sequences, up to a cap
// on the items one line may open; past it the decode fails instead of
// recursing until the stack runs out, which would end the process.
func TestDecoder_DeeplyNestedSequenceLine(t *testing.T) {
	want := fmt.Sprintf("error: yaml: more than %d nested sequence items on line 1", maxItemsPerLine)
	for _, dashes := range []int{maxItemsPerLine + 1, 4_000_000} {
		if got := parseDump(strings.Repeat("- ", dashes) + "x"); got != want {
			t.Fatalf("%d nested dashes: %.100s, want %s", dashes, got, want)
		}
	}
	depth := maxItemsPerLine
	src := strings.Repeat("- ", depth) + "x"
	n := parseYAML(src)
	for level := 0; level < depth; level++ {
		if n.kind != ySeq || len(n.seq) != 1 {
			t.Fatalf("level %d: %s, want a one-item sequence", level, n.kindName())
		}
		n = n.seq[0]
	}
	if n.kind != yScalar || n.scalar != "x" {
		t.Fatalf("innermost value %q, want %q", n.scalar, "x")
	}
}

// A "# " or ": |" inside a quoted or flow value, or inside a quoted key, is
// neither a comment nor a block indicator; a quoted key may still open a
// block.
func TestDecoder_BlockIndicatorInsideQuotes(t *testing.T) {
	tests := []struct{ src, want string }{
		{"k: [a, \"b: | # c\"]\nm: 1", `{k: [a, "b: | # c"], m: 1}`},
		{"k: 'x: | # y'\nm: 1", "{k: x: | # y, m: 1}"},
		{"- 'a: | # b'\n- c", "[a: | # b, c]"},
		{"- \"a # b\": |\n    body\n  c: 1", "[{a # b: body\n, c: 1}]"},
		{"- - 'k # x': |\n      body", "[[{k # x: body\n}]]"},
		{"\"a|b\": | # note\n  body", "{a|b: body\n}"},
		// With no space after a quoted key's colon, "|" is the plain value.
		{"\"k\":|", "{k: |}"},
	}
	for _, tt := range tests {
		if got := parseDump(tt.src); got != tt.want {
			t.Errorf("%q => %s, want %s", tt.src, got, tt.want)
		}
	}
}

// ----- Literal block chomping ------------------------------------------------

// A literal block's final line breaks follow its chomping indicator (YAML
// 1.2, section 8.1.1.2, #1349): "|" keeps one, "|-" none and "|+" all, the
// blank lines after the text included. Blank lines before the text are
// kept.
func TestDecoder_LiteralBlockChomping(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"clip", "k: |\n  a\n  b\nj: 1", "a\nb\n"},
		{"clip, blank lines after", "k: |\n  a\n\n\nj: 1", "a\n"},
		{"clip, end of document", "k: |\n  a\n", "a\n"},
		{"clip, no final line break", "k: |\n  a", "a\n"},
		{"strip", "k: |-\n  a\n  b\nj: 1", "a\nb"},
		{"strip, blank lines after", "k: |-\n  a\n\n\nj: 1", "a"},
		{"keep", "k: |+\n  a\nj: 1", "a\n"},
		{"keep, blank lines after", "k: |+\n  a\n\n\nj: 1", "a\n\n\n"},
		{"keep, end of document", "k: |+\n  a\n\n", "a\n\n"},
		{"blank lines inside", "k: |\n  a\n\n  b\nj: 1", "a\n\nb\n"},
		{"blank lines before", "k: |\n\n\n  a\nj: 1", "\n\na\n"},
		{"empty, clip", "k: |\nj: 1", ""},
		{"empty, keep", "k: |+\n\n\nj: 1", "\n\n"},
		{"keep, comment after", "k: |+ # note\n  a\n\nj: 1", "a\n\n"},
		{"keep, sequence item", "- |+\n  a\n\n- b", "a\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewYamlDecoder(tt.src)
			if d.root.kind == ySeq {
				d.StartArray()
			} else {
				d.StartObject()
				d.ReadKey()
			}
			if got := d.ReadString(); got != tt.want {
				t.Errorf("%q => %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}
