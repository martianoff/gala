package generator

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// Doc comments on a synthetic AST
// -------------------------------
// The transformer builds position-less (token.NoPos) Go AST and records each
// declaration's documentation on its Doc field. go/printer prints the Doc of a
// top-level declaration correctly from that alone, but not two others: without
// line positions it runs a package doc into the `package` keyword and glues a
// struct-field or interface-method doc onto the end of the previous field's
// line. Those two kinds are detached before printing and spliced into the
// printed text at the right line instead; the final gofmt pass then lays them
// out canonically.
//
// This applies only to a synthetic AST (one with no File.Comments). A parsed
// file carries positioned comments that go/printer already places correctly.

// fieldDoc is a struct-field or interface-method doc detached for splicing.
type fieldDoc struct {
	typeName string     // the top-level type declaring the field
	index    int        // position of the field in its field list
	field    *ast.Field // the field, so its Doc can be restored
	doc      *ast.CommentGroup
}

// detachFieldDocs clears the Doc of every field of every top-level struct and
// interface type in file, returning what it removed.
func detachFieldDocs(file *ast.File) []fieldDoc {
	var out []fieldDoc
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			fields := fieldListOf(ts)
			if fields == nil {
				continue
			}
			for i, f := range fields.List {
				if f.Doc != nil {
					out = append(out, fieldDoc{typeName: ts.Name.Name, index: i, field: f, doc: f.Doc})
					f.Doc = nil
				}
			}
		}
	}
	return out
}

// fieldListOf returns the fields of a struct type or the methods of an
// interface type, or nil for any other type.
func fieldListOf(ts *ast.TypeSpec) *ast.FieldList {
	switch typ := ts.Type.(type) {
	case *ast.StructType:
		return typ.Fields
	case *ast.InterfaceType:
		return typ.Methods
	}
	return nil
}

// spliceFieldDocs inserts each detached field doc on the lines above its field
// in src, indented like the field. The fields are located by re-parsing src,
// so the splice is exact wherever the printer put them. If src does not parse
// (only the LSP's partial-input path produces such output) the docs are left
// out rather than guessed at.
func spliceFieldDocs(src []byte, docs []fieldDoc) []byte {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return src
	}
	specs := make(map[string]*ast.TypeSpec)
	for _, decl := range parsed.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.TYPE {
			for _, spec := range gd.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					specs[ts.Name.Name] = ts
				}
			}
		}
	}

	insert := make(map[int]*ast.CommentGroup) // 1-based line -> doc above it
	for _, d := range docs {
		ts := specs[d.typeName]
		if ts == nil {
			continue
		}
		fields := fieldListOf(ts)
		if fields == nil || d.index >= len(fields.List) {
			continue
		}
		insert[fset.Position(fields.List[d.index].Pos()).Line] = d.doc
	}
	if len(insert) == 0 {
		return src
	}

	lines := strings.Split(string(src), "\n")
	var out strings.Builder
	out.Grow(len(src))
	for i, line := range lines {
		if doc := insert[i+1]; doc != nil {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			for _, c := range doc.List {
				out.WriteString(indent)
				out.WriteString(c.Text)
				out.WriteByte('\n')
			}
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return []byte(out.String())
}

// commentText renders a comment group as source lines, one comment per line.
func commentText(g *ast.CommentGroup) []byte {
	var buf bytes.Buffer
	for _, c := range g.List {
		buf.WriteString(c.Text)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}
