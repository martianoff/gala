package gocoreexport_test

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"martianoff/gala/internal/formal/gocoreexport"
)

// The round trip is the exporter's main test: a document printed back as Go
// must parse to the same syntax tree as the source, positions, comments and
// the spelling of literals aside. So the exporter drops nothing and changes
// nothing: it is a plain syntax dump. The printer below works on the JSON,
// the form the Lean decoder reads. A document with an unsupported node has
// no source to print there, so the round trip covers the documents wholly
// inside GoCore; TestExamplesRoundTrip says how many.

type obj = map[string]any

// roundTrip exports src. When the document has no unsupported node it
// prints it back as Go and checks that it parses to src's tree. It returns
// the document and whether it was round-tripped.
func roundTrip(t *testing.T, name, src string) (obj, bool) {
	t.Helper()
	prog, err := gocoreexport.Export([]gocoreexport.PackageSource{{Path: "main", Files: []gocoreexport.Source{{Name: name, Text: src}}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(prog)
	if err != nil {
		t.Fatal(err)
	}
	var doc obj
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	clean := true
	walk(doc, func(n obj) {
		if _, ok := n["unsupported"]; ok {
			clean = false
		}
	})
	if !clean {
		return doc, false
	}
	pkg := doc["packages"].([]any)[0].(obj)
	var p printer
	p.file(str(pkg["name"]), pkg["files"].([]any)[0].(obj))
	if p.unsupported != "" {
		t.Fatalf("%s: the printer met %s", name, p.unsupported)
	}
	printed := p.b.String()
	want := parse(t, name, src)
	got := parse(t, name+" (printed back)", printed)
	if d := diffTrees(reflect.ValueOf(want), reflect.ValueOf(got), "file"); d != "" {
		t.Fatalf("%s: printed back, the document is not the source: %s\nprinted:\n%s", name, d, printed)
	}
	return doc, true
}

// parse parses src and normalizes what the round trip does not keep: import
// declarations (File.Imports is compared instead, which is flat) and the
// spelling of literals and tags.
func parse(t *testing.T, name, src string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var decls []ast.Decl
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); !ok || g.Tok != token.IMPORT {
			decls = append(decls, d)
		}
	}
	f.Decls = decls
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			if n.Tag != nil {
				tag, _ := strconv.Unquote(n.Tag.Value)
				n.Tag.Value = strconv.Quote(tag)
			}
		case *ast.BasicLit:
			v := constant.MakeFromLiteral(n.Value, n.Kind, 0)
			switch n.Kind {
			case token.INT:
				n.Value = v.ExactString()
			case token.STRING:
				n.Value = strconv.Quote(constant.StringVal(v))
			case token.CHAR:
				r, _ := constant.Int64Val(v)
				n.Value = strconv.QuoteRune(rune(r))
			}
		}
		return true
	})
	return f
}

// diffTrees compares two syntax trees, ignoring positions, comments, scopes
// and the file's derived lists. It names the first difference.
func diffTrees(a, b reflect.Value, path string) string {
	if a.Kind() != b.Kind() {
		return fmt.Sprintf("%s: %s vs %s", path, a.Kind(), b.Kind())
	}
	switch a.Kind() {
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				return fmt.Sprintf("%s: nil vs not nil", path)
			}
			return ""
		}
		if a.Elem().Type() != b.Elem().Type() {
			return fmt.Sprintf("%s: %s vs %s", path, a.Elem().Type(), b.Elem().Type())
		}
		return diffTrees(a.Elem(), b.Elem(), path)
	case reflect.Slice:
		if a.Len() != b.Len() {
			return fmt.Sprintf("%s: %d elements vs %d", path, a.Len(), b.Len())
		}
		for i := range a.Len() {
			if d := diffTrees(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
		return ""
	case reflect.Struct:
		for i := range a.NumField() {
			f := a.Type().Field(i)
			switch {
			case f.Type == reflect.TypeOf(token.NoPos),
				f.Type == reflect.TypeOf(&ast.CommentGroup{}),
				f.Type == reflect.TypeOf(&ast.Scope{}),
				f.Type == reflect.TypeOf(&ast.Object{}),
				f.Name == "Unresolved", f.Name == "Comments",
				f.Name == "FileStart", f.Name == "FileEnd", f.Name == "GoVersion":
				continue
			}
			if d := diffTrees(a.Field(i), b.Field(i), path+"."+f.Name); d != "" {
				return d
			}
		}
		return ""
	}
	if a.Interface() != b.Interface() {
		return fmt.Sprintf("%s: %v vs %v", path, a.Interface(), b.Interface())
	}
	return ""
}

// printer prints a GoCore JSON file back as Go source. unsupported is set
// when it met an unsupported node, which has no source to print.
type printer struct {
	b           strings.Builder
	unsupported string
}

func (p *printer) w(format string, args ...any) { fmt.Fprintf(&p.b, format, args...) }

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func (p *printer) file(pkg string, f obj) {
	p.w("package %s\n\n", pkg)
	for _, i := range list(f["imports"]) {
		imp := i.(obj)
		p.w("import %s %s\n", str(imp["name"]), strconv.Quote(str(imp["path"])))
	}
	for _, d := range list(f["decls"]) {
		p.decl(d.(obj))
		p.w("\n")
	}
}

func (p *printer) decl(d obj) {
	switch d["kind"] {
	case "func-decl":
		p.w("func ")
		if r, ok := d["recv"].(obj); ok {
			p.w("(")
			p.field(r)
			p.w(") ")
		}
		p.w("%s", str(d["name"]))
		p.typeParams(d["typeParams"])
		p.signature(d["type"].(obj))
		if body, ok := d["body"].(obj); ok {
			p.w(" ")
			p.stmt(body)
		}
	case "type-decl":
		p.w("type (\n")
		for _, s := range list(d["specs"]) {
			spec := s.(obj)
			p.w("%s", str(spec["name"]))
			p.typeParams(spec["typeParams"])
			if spec["alias"] == true {
				p.w(" =")
			}
			p.w(" ")
			p.expr(spec["type"])
			p.w("\n")
		}
		p.w(")")
	case "value-decl":
		if d["const"] == true {
			p.w("const (\n")
		} else {
			p.w("var (\n")
		}
		for _, s := range list(d["specs"]) {
			spec := s.(obj)
			p.w("%s", strings.Join(strs(spec["names"]), ", "))
			if spec["type"] != nil {
				p.w(" ")
				p.expr(spec["type"])
			}
			if vs := list(spec["values"]); len(vs) > 0 {
				p.w(" = ")
				p.exprs(vs)
			}
			p.w("\n")
		}
		p.w(")")
	default:
		p.node(d)
	}
}

func strs(v any) []string {
	var out []string
	for _, s := range list(v) {
		out = append(out, s.(string))
	}
	return out
}

func (p *printer) typeParams(v any) {
	if tps := list(v); len(tps) > 0 {
		p.w("[")
		p.fields(tps, false)
		p.w("]")
	}
}

func (p *printer) signature(d obj) {
	p.w("(")
	p.fields(list(d["params"]), d["variadic"] == true)
	p.w(")")
	if rs := list(d["results"]); len(rs) > 0 {
		p.w(" (")
		p.fields(rs, false)
		p.w(")")
	}
}

func (p *printer) fields(fs []any, variadic bool) {
	for i, f := range fs {
		if i > 0 {
			p.w(", ")
		}
		if variadic && i == len(fs)-1 {
			fo := f.(obj)
			p.w("%s ...", strings.Join(strs(fo["names"]), ", "))
			p.expr(fo["type"])
			continue
		}
		p.field(f.(obj))
	}
}

func (p *printer) field(f obj) {
	if names := strs(f["names"]); len(names) > 0 {
		p.w("%s ", strings.Join(names, ", "))
	}
	p.expr(f["type"])
	if tag := str(f["tag"]); tag != "" {
		p.w(" %s", strconv.Quote(tag))
	}
}

func (p *printer) exprs(es []any) {
	for i, e := range es {
		if i > 0 {
			p.w(", ")
		}
		p.expr(e)
	}
}

func (p *printer) node(n obj) {
	if u, ok := n["unsupported"]; ok {
		p.unsupported = str(u)
		return
	}
	p.unsupported = "unknown kind " + str(n["kind"])
}

func (p *printer) expr(v any) {
	e, ok := v.(obj)
	if !ok {
		return
	}
	switch e["kind"] {
	case "ident":
		p.w("%s", str(e["name"]))
	case "int-lit":
		p.w("%s", str(e["value"]))
	case "string-lit":
		p.w("%s", strconv.Quote(str(e["value"])))
	case "char-lit":
		p.w("%s", strconv.QuoteRune(rune(e["value"].(float64))))
	case "composite-lit":
		p.expr(e["type"])
		p.w("{")
		p.exprs(list(e["elts"]))
		p.w("}")
	case "key-value":
		p.expr(e["key"])
		p.w(": ")
		p.expr(e["value"])
	case "func-lit":
		p.w("func")
		p.signature(e["type"].(obj))
		p.w(" ")
		p.stmt(e["body"].(obj))
	case "paren":
		p.w("(")
		p.expr(e["x"])
		p.w(")")
	case "selector":
		p.expr(e["x"])
		p.w(".%s", str(e["sel"]))
	case "index":
		p.expr(e["x"])
		p.w("[")
		p.exprs(list(e["indices"]))
		p.w("]")
	case "type-assert":
		p.expr(e["x"])
		p.w(".(")
		p.expr(e["type"])
		p.w(")")
	case "call":
		p.expr(e["fun"])
		p.w("(")
		p.exprs(list(e["args"]))
		if e["ellipsis"] == true {
			p.w("...")
		}
		p.w(")")
	case "star":
		p.w("*")
		p.expr(e["x"])
	case "unary":
		// A space keeps `- -x` from reading as `--x`.
		p.w("%s ", str(e["op"]))
		p.expr(e["x"])
	case "binary":
		p.expr(e["x"])
		p.w(" %s ", str(e["op"]))
		p.expr(e["y"])
	case "struct-type":
		p.w("struct {\n")
		for _, f := range list(e["fields"]) {
			p.field(f.(obj))
			p.w("\n")
		}
		p.w("}")
	case "interface-type":
		p.w("interface {\n")
		for _, f := range list(e["elems"]) {
			fo := f.(obj)
			if names := strs(fo["names"]); len(names) == 1 {
				p.w("%s", names[0])
				p.signature(fo["type"].(obj))
			} else {
				p.expr(fo["type"])
			}
			p.w("\n")
		}
		p.w("}")
	case "func-type":
		p.w("func")
		p.signature(e)
	default:
		p.node(e)
	}
}

func (p *printer) stmts(ss []any) {
	for _, s := range ss {
		p.stmt(s.(obj))
		p.w("\n")
	}
}

func (p *printer) stmt(s obj) {
	switch s["kind"] {
	case "block":
		p.w("{\n")
		p.stmts(list(s["stmts"]))
		p.w("}")
	case "decl-stmt":
		p.decl(s["decl"].(obj))
	case "expr-stmt":
		p.expr(s["x"])
	case "inc-dec":
		p.expr(s["x"])
		p.w("%s", str(s["op"]))
	case "assign":
		p.exprs(list(s["lhs"]))
		p.w(" %s ", str(s["tok"]))
		p.exprs(list(s["rhs"]))
	case "return":
		p.w("return ")
		p.exprs(list(s["results"]))
	case "branch":
		p.w("%s", str(s["tok"]))
	case "if":
		p.w("if ")
		if init, ok := s["init"].(obj); ok {
			p.stmt(init)
			p.w("; ")
		}
		p.expr(s["cond"])
		p.w(" ")
		p.stmt(s["body"].(obj))
		if e, ok := s["else"].(obj); ok {
			p.w(" else ")
			p.stmt(e)
		}
	case "switch":
		p.w("switch ")
		if init, ok := s["init"].(obj); ok {
			p.stmt(init)
			p.w("; ")
		}
		p.expr(s["tag"])
		p.w(" {\n")
		for _, c := range list(s["clauses"]) {
			cc := c.(obj)
			if len(list(cc["list"])) == 0 {
				p.w("default:\n")
			} else {
				p.w("case ")
				p.exprs(list(cc["list"]))
				p.w(":\n")
			}
			p.stmts(list(cc["body"]))
		}
		p.w("}")
	case "for":
		p.w("for ")
		if s["init"] != nil || s["post"] != nil {
			if init, ok := s["init"].(obj); ok {
				p.stmt(init)
			}
			p.w("; ")
			p.expr(s["cond"])
			p.w("; ")
			if post, ok := s["post"].(obj); ok {
				p.stmt(post)
			}
		} else {
			p.expr(s["cond"])
		}
		p.w(" ")
		p.stmt(s["body"].(obj))
	case "range":
		p.w("for ")
		if s["key"] != nil {
			p.expr(s["key"])
			if s["value"] != nil {
				p.w(", ")
				p.expr(s["value"])
			}
			p.w(" %s ", str(s["tok"]))
		}
		p.w("range ")
		p.expr(s["x"])
		p.w(" ")
		p.stmt(s["body"].(obj))
	case "empty":
	default:
		p.node(s)
	}
}
