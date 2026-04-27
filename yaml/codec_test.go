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
