package coreexport

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
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

// kindSections is the level-3 sections of body, by the code span of their
// title: "### `int`" is section "int".
func kindSections(body string) map[string]string {
	out := map[string]string{}
	parts := regexp.MustCompile("(?m)^### `([^`]+)`\\s*$").FindAllStringSubmatchIndex(body, -1)
	for i, m := range parts {
		end := len(body)
		if i+1 < len(parts) {
			end = parts[i+1][0]
		}
		out[body[m[2]:m[3]]] = body[m[1]:end]
	}
	return out
}

// jsonFields is the JSON names of n's fields, its Meta's excepted.
func jsonFields(n Node) []string {
	var out []string
	typ := reflect.TypeOf(n).Elem()
	for i := range typ.NumField() {
		if f := typ.Field(i); !f.Anonymous {
			out = append(out, strings.Split(f.Tag.Get("json"), ",")[0])
		}
	}
	return out
}

// TestSchemaDocumentsEveryKind checks SCHEMA.md against the Go types: every
// node kind has a section that names each of its fields and shows an example
// of it, and every section is a kind.
func TestSchemaDocumentsEveryKind(t *testing.T) {
	sections := kindSections(section(t, readSchema(t), "Node kinds"))
	for _, n := range nodeTypes {
		kind := n.Kind()
		text, ok := sections[kind]
		if !ok {
			t.Errorf("SCHEMA.md documents no kind %q", kind)
			continue
		}
		for _, field := range jsonFields(n) {
			if !strings.Contains(text, "`"+field+"`") {
				t.Errorf("SCHEMA.md does not document field %q of kind %q", field, kind)
			}
		}
		if !strings.Contains(text, "```json") || !strings.Contains(text, `"kind": "`+kind+`"`) {
			t.Errorf("SCHEMA.md has no JSON example of kind %q", kind)
		}
	}
	for kind := range sections {
		if !slices.Contains(Kinds(), kind) {
			t.Errorf("SCHEMA.md documents kind %q, which the Go types do not have", kind)
		}
	}
}

// TestSchemaDocumentsEveryUnsupported checks that SCHEMA.md's table of
// unsupported constructs lists exactly UnsupportedKinds.
func TestSchemaDocumentsEveryUnsupported(t *testing.T) {
	var listed []string
	table := section(t, readSchema(t), "Unsupported constructs")
	for _, m := range regexp.MustCompile("(?m)^\\| `([^`]+)` \\|").FindAllStringSubmatch(table, -1) {
		listed = append(listed, m[1])
	}
	slices.Sort(listed)
	reported := slices.Sorted(slices.Values(UnsupportedKinds))
	if !slices.Equal(listed, reported) {
		t.Errorf("SCHEMA.md lists unsupported constructs\n%v\nthe exporter reports\n%v", listed, reported)
	}
}

// TestUnsupportedKindsListsEveryConstant checks that UnsupportedKinds holds
// the value of every Unsupported constant in schema.go, and nothing else.
func TestUnsupportedKindsListsEveryConstant(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "schema.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var consts []string
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || !strings.HasPrefix(spec.Names[0].Name, "Unsupported") {
			return true
		}
		if lit, ok := spec.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatal(err)
			}
			consts = append(consts, value)
		}
		return true
	})
	slices.Sort(consts)
	if listed := slices.Sorted(slices.Values(UnsupportedKinds)); !slices.Equal(consts, listed) {
		t.Errorf("the Unsupported constants are\n%v\nUnsupportedKinds is\n%v", consts, listed)
	}
}
