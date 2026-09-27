package lsp

import (
	"fmt"
	"slices"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
)

// callAtArgumentStart reports the call in whose argument list the caret begins
// an argument — right after the call's `(` or one of its top-level commas, with
// at most an identifier typed since — together with that identifier.
//
// The call is found the way signature help finds it (findCallAtCaret), so an
// argument list still being typed resolves.
func (h *GalaHandler) callAtArgumentStart(uri, text string, line, char int) (call *callContext, typed string) {
	offset := lineCharToOffset(text, line, char)
	if offset < 0 || offset > len(text) {
		return nil, ""
	}
	start := offset
	for start > 0 && isIdentChar(text[start-1]) {
		start--
	}
	sep := skipTrailingWhitespace(text, start-1)
	if sep < 0 || (text[sep] != '(' && text[sep] != ',') {
		return nil, ""
	}
	// A `)` closing the call is only ever inserted at the caret, so the
	// separator's offset is the same in the text the call was found in.
	call = h.findCallAtCaret(uri, text, line, char)
	if call == nil || !slices.Contains(call.argStarts, sep) {
		return nil, ""
	}
	return call, text[start:offset]
}

// parameters returns the target's parameter names and types in declaration
// order, and the indexes of those that have a default.
func (t callTarget) parameters() (names []string, types []transpiler.Type, defaults map[int]transpiler.DefaultExpr) {
	switch {
	case t.fn != nil:
		return t.fn.ParamNames, t.fn.ParamTypes, t.fn.DefaultExprs
	case t.method != nil:
		return t.method.ParamNames, t.method.ParamTypes, t.method.DefaultExprs
	case t.typ != nil:
		types = make([]transpiler.Type, 0, len(t.typ.FieldNames))
		for _, fn := range t.typ.FieldNames {
			types = append(types, t.typ.Fields[fn])
		}
		return t.typ.FieldNames, types, nil
	case t.variant != nil:
		return t.variant.FieldNames, t.variant.FieldTypes, nil
	}
	return nil, nil, nil
}

// doc and fieldDocs are a constructor target's own doc comment and its fields'.
func (t callTarget) doc() string {
	switch {
	case t.typ != nil:
		return t.typ.Doc
	case t.variant != nil:
		return t.variant.Doc
	}
	return ""
}

func (t callTarget) fieldDocs() map[string]string {
	if t.typ != nil {
		return t.typ.FieldDocs
	}
	return nil
}

// parameterCompletions offers the parameters of a call that are not given yet,
// as named arguments in declaration order, ahead of everything else in the
// list.
func parameterCompletions(call *callContext, target callTarget) []lsp.CompletionItem {
	names, types, defaults := target.parameters()
	items := make([]lsp.CompletionItem, 0, len(names))
	for i, name := range names {
		if i < call.positional || call.named[name] {
			continue
		}
		detail := ""
		if i < len(types) && types[i] != nil && !types[i].IsNil() {
			detail = cleanGoTypeForDisplay(types[i].String())
		}
		if def, ok := defaults[i]; ok {
			detail += " = " + def.Text
		}
		item := lsp.CompletionItem{
			Label:      name,
			Kind:       kindPtr(lsp.CompletionItemKindField),
			Detail:     strings.TrimSpace(detail),
			InsertText: name + " = ",
			SortText:   fmt.Sprintf("0%04d", i),
		}
		if target.typ != nil {
			// A struct field's doc comment is attached on resolve, as for the
			// same field after a dot.
			item = withRef(item, completionRef{Kind: refKindMember, Key: typeKey(target.typ), Name: name})
		}
		items = append(items, item)
	}
	return items
}

// callInsertText is what completing a call to `name` inserts.
//
// A client that accepts snippets gets the required parameters written out as
// named arguments, with a tab stop for each value; parameters that have a
// default are left out, as a call may omit them. Any other client gets the call
// opened for the arguments to be typed.
func callInsertText(name string, paramNames []string, defaults map[int]transpiler.DefaultExpr, snippets bool) (string, *lsp.InsertTextFormat) {
	if len(paramNames) == 0 {
		return name + "()", nil
	}
	if !snippets {
		return name + "(", nil
	}
	args := make([]string, 0, len(paramNames))
	for i, p := range paramNames {
		if _, hasDefault := defaults[i]; hasDefault {
			continue
		}
		args = append(args, fmt.Sprintf("%s = $%d", p, len(args)+1))
	}
	format := lsp.InsertTextFormatSnippet
	if len(args) == 0 {
		return name + "($0)", &format
	}
	return name + "(" + strings.Join(args, ", ") + ")$0", &format
}
