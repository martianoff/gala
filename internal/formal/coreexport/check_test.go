package coreexport_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/coreexport/analyzerhints"
)

var hinted = coreexport.Options{LiteralType: analyzerhints.LiteralType}

// exportJSON exports srcs and marshals the document.
func exportJSON(srcs ...coreexport.Source) ([]byte, error) {
	p, err := coreexport.Export(srcs, hinted)
	if err != nil {
		return nil, err
	}
	return coreexport.Marshal(p)
}

// checkDocument checks the properties every export has: it is the current version;
// exporting again gives the same bytes; every kind and unsupported name is in
// the schema; every position points into its file, its line and column
// agree with its offset, and it lies within its parent's; and a leaf's span
// is exactly its text.
func checkDocument(data []byte, srcs ...coreexport.Source) error {
	again, err := exportJSON(srcs...)
	if err != nil {
		return fmt.Errorf("second export: %w", err)
	}
	if !bytes.Equal(data, again) {
		return fmt.Errorf("exporting twice gave different output")
	}
	var doc struct {
		Core  int              `json:"core"`
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	if doc.Core != coreexport.Version {
		return fmt.Errorf("core = %d, want %d", doc.Core, coreexport.Version)
	}
	if len(doc.Files) != len(srcs) {
		return fmt.Errorf("%d files, want %d", len(doc.Files), len(srcs))
	}
	for i, f := range doc.Files {
		c := &posChecker{src: srcs[i].Text}
		if err := c.node(f, pos{0, len(c.src)}); err != nil {
			return fmt.Errorf("%s: %w", srcs[i].Path, err)
		}
	}
	return nil
}

// The schema's kinds and unsupported names, read once.
var (
	schemaKinds       = coreexport.Kinds()
	schemaUnsupported = coreexport.UnsupportedKinds()
)

type pos struct{ off, end int }

type posChecker struct{ src string }

// node checks object v and everything in it, v lying within outer.
func (c *posChecker) node(v map[string]any, outer pos) error {
	kind, isKind := v["kind"].(string)
	what, isUnsupported := v["unsupported"].(string)
	switch {
	case isKind == isUnsupported:
		return fmt.Errorf("node with neither or both of kind and unsupported: %v", v)
	case isKind && !slices.Contains(schemaKinds, kind):
		return fmt.Errorf("kind %q is not in the schema", kind)
	case isUnsupported && !slices.Contains(schemaUnsupported, what):
		return fmt.Errorf("unsupported %q is not in the schema", what)
	}
	p, err := c.pos(v)
	if err != nil {
		return fmt.Errorf("%s%s: %w", kind, what, err)
	}
	if p.off < outer.off || p.end > outer.end {
		return fmt.Errorf("%s%s at %v lies outside its parent at %v", kind, what, p, outer)
	}
	text := c.src[p.off:p.end]
	want := ""
	switch kind {
	case "name", "pat-name", "type-name":
		want = v["name"].(string)
	case "int", "string":
		want = v["text"].(string)
	case "bool":
		want = fmt.Sprint(v["value"])
	case "pat-wild":
		want = "_"
	}
	if want != "" && text != want {
		return fmt.Errorf("%s spans %q, want %q", kind, text, want)
	}
	for key, child := range v {
		if key == "pos" || key == "hint" {
			continue
		}
		if err := c.value(child, p); err != nil {
			return err
		}
	}
	return nil
}

func (c *posChecker) value(v any, outer pos) error {
	switch v := v.(type) {
	case map[string]any:
		return c.node(v, outer)
	case []any:
		for _, x := range v {
			if err := c.value(x, outer); err != nil {
				return err
			}
		}
	}
	return nil
}

// pos is v's span, checked against the source.
func (c *posChecker) pos(v map[string]any) (pos, error) {
	m, ok := v["pos"].(map[string]any)
	if !ok {
		return pos{}, fmt.Errorf("no pos")
	}
	line, col, off, end := int(m["line"].(float64)), int(m["col"].(float64)), int(m["off"].(float64)), int(m["end"].(float64))
	if off < 0 || off >= end || end > len(c.src) {
		return pos{}, fmt.Errorf("span %d..%d is empty or outside the file (%d bytes)", off, end, len(c.src))
	}
	before := c.src[:off]
	wantLine := 1 + strings.Count(before, "\n")
	lineStart := 1 + strings.LastIndexByte(before, '\n')
	wantCol := 1 + utf8.RuneCountInString(c.src[lineStart:off])
	if line != wantLine || col != wantCol {
		return pos{}, fmt.Errorf("at %d:%d, but offset %d is %d:%d", line, col, off, wantLine, wantCol)
	}
	return pos{off, end}, nil
}
