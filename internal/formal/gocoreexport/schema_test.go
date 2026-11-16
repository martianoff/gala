package gocoreexport

import (
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func readSchema(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("SCHEMA.md")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// section is the body of doc's level-2 section heading, up to the next one.
func section(t *testing.T, doc, heading string) string {
	t.Helper()
	start := strings.Index(doc, "\n## "+heading+"\n")
	if start < 0 {
		t.Fatalf("SCHEMA.md has no %q section", heading)
	}
	body := doc[start+len(heading)+5:]
	if end := strings.Index(body, "\n## "); end >= 0 {
		body = body[:end]
	}
	return body
}

// TestSchemaDocumentsEveryKind checks SCHEMA.md against the Go types: every
// node kind has a section that names each of its fields, and every section
// is a kind.
func TestSchemaDocumentsEveryKind(t *testing.T) {
	body := section(t, readSchema(t), "Node kinds")
	sections := map[string]string{}
	parts := regexp.MustCompile("(?m)^### `([^`]+)`\\s*$").FindAllStringSubmatchIndex(body, -1)
	for i, m := range parts {
		end := len(body)
		if i+1 < len(parts) {
			end = parts[i+1][0]
		}
		sections[body[m[2]:m[3]]] = body[m[1]:end]
	}
	for _, n := range nodeTypes {
		kind := n.Kind()
		text, ok := sections[kind]
		if !ok {
			t.Errorf("SCHEMA.md documents no kind %q", kind)
			continue
		}
		typ := reflect.TypeOf(n).Elem()
		for i := range typ.NumField() {
			f := typ.Field(i)
			if f.Anonymous {
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if !strings.Contains(text, "`"+name+"`") {
				t.Errorf("SCHEMA.md does not document field %q of kind %q", name, kind)
			}
		}
	}
	for kind := range sections {
		if !slices.Contains(Kinds(), kind) {
			t.Errorf("SCHEMA.md documents %q, which is not a kind", kind)
		}
	}
}

// TestSchemaListsEveryUnsupportedConstruct checks the table of unsupported
// constructs against unsupportedKinds, both ways.
func TestSchemaListsEveryUnsupportedConstruct(t *testing.T) {
	body := section(t, readSchema(t), "Unsupported constructs")
	var documented []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([^`]+)` \\|").FindAllStringSubmatch(body, -1) {
		documented = append(documented, m[1])
	}
	want := UnsupportedKinds()
	slices.Sort(documented)
	slices.Sort(want)
	if !slices.Equal(documented, want) {
		t.Errorf("SCHEMA.md lists %v, the exporter reports %v", documented, want)
	}
}
