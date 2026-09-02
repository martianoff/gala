package lsp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
)

// Hover resolves the symbol under the cursor and renders its signature and
// documentation.
//
// Resolution is context-aware rather than a bare-word lookup: `Map` means
// something different in `arr.Map(...)`, `im.Map`, and a top-level `func Map`,
// and a bare-word search over the type table answers all three the same way (or,
// for a method, not at all). The dispatcher below therefore establishes what the
// word is qualified by before it looks anything up, reusing the same resolvers
// completion uses (resolveChainTypeN / resolveReceiverType / findType), so hover
// and completion cannot disagree about what an expression's type is.
func (h *GalaHandler) Hover(ctx context.Context, params *lsp.HoverParams) (*lsp.Hover, error) {
	uri := string(params.TextDocument.URI)

	h.mu.Lock()
	text := h.documents[uri]
	richAST := h.richASTs[uri]
	varTypes := h.varTypes[uri]
	h.mu.Unlock()

	if text == "" || richAST == nil {
		return nil, nil
	}

	line, char := int(params.Position.Line), int(params.Position.Character)
	info := h.hoverInfo(text, richAST, varTypes, line, char)
	if info == "" {
		return nil, nil
	}

	return &lsp.Hover{
		Contents: lsp.MarkupContent{
			Kind:  lsp.Markdown,
			Value: info,
		},
	}, nil
}

// hoverInfo renders the hover body for a position, or "" when nothing resolves.
func (h *GalaHandler) hoverInfo(text string, richAST *transpiler.RichAST, varTypes map[string]string, line, char int) string {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return ""
	}
	word := wordAtPosition(text, line, char)
	if word == "" {
		return ""
	}
	funcScope := findEnclosingFunc(lines, line)

	// An import line names a package rather than a symbol; the word under the
	// cursor is a path segment, not something in the type table.
	if isImportLine(lines[line]) {
		return packageHover(richAST, word, lines[line])
	}

	// `qualifier.word` — the qualifier decides where to look.
	if qualifier, wordStart, ok := qualifierBefore(lines[line], char); ok {
		if pkg := resolvePackageQualifier(richAST, qualifier); pkg != "" {
			if info := packageMemberHover(richAST, pkg, word); info != "" {
				return info
			}
		}
		// typeAtDot expects the cursor just past the dot, which is where the
		// word starts.
		if recv := typeAtDot(text, line, wordStart, richAST, varTypes); recv != "" {
			if info := memberHover(richAST, recv, word); info != "" {
				return info
			}
		}
	}

	// A method or field declaration: the receiver is the enclosing type, not
	// anything to the left of the cursor.
	if info := declarationSiteHover(richAST, lines, line, word); info != "" {
		return info
	}

	// A local val/var carries no metadata entry — its type comes from the
	// transformer's resolved scope, the same source inlay hints read.
	if typStr := lookupVarType(varTypes, funcScope, word); typStr != "" {
		return localHover(richAST, word, typStr, lines[line])
	}

	if pkg := resolvePackageQualifier(richAST, word); pkg != "" {
		return packageHover(richAST, word, lines[line])
	}

	return lookupSymbol(richAST, word)
}

// qualifierBefore returns the identifier chain qualifying the word at `char`,
// the column the word starts at, and whether the word was dot-qualified at all.
func qualifierBefore(line string, char int) (qualifier string, wordStart int, ok bool) {
	if char > len(line) {
		return "", 0, false
	}
	start := char
	for start > 0 && isIdentChar(line[start-1]) {
		start--
	}
	if start == 0 || line[start-1] != '.' {
		return "", start, false
	}
	end := start - 1
	i := end - 1
	for i >= 0 && (isIdentChar(line[i]) || line[i] == '.') {
		i--
	}
	return line[i+1 : end], start, true
}

// resolvePackageQualifier maps an import alias or package name to its package
// name, or "" when the qualifier is a value rather than a package.
func resolvePackageQualifier(richAST *transpiler.RichAST, qualifier string) string {
	if qualifier == "" || strings.Contains(qualifier, ".") {
		return ""
	}
	if pkg, ok := richAST.ImportAliases[qualifier]; ok {
		return pkg
	}
	for _, pkg := range richAST.Packages {
		if pkg == qualifier {
			return pkg
		}
	}
	return ""
}

// packageMemberHover renders a symbol accessed through a package qualifier.
func packageMemberHover(richAST *transpiler.RichAST, pkg, name string) string {
	qualified := pkg + "." + name
	if variant, parent := findSealedVariant(richAST, name); variant != nil {
		return formatVariant(variant, parent)
	}
	if tm, ok := richAST.Types[qualified]; ok {
		return formatTypeMeta(tm)
	}
	if fm, ok := richAST.Functions[qualified]; ok {
		return formatFuncMeta(fm)
	}
	return ""
}

// memberHover renders a method or field selected on a receiver of known type.
func memberHover(richAST *transpiler.RichAST, recvType, name string) string {
	tm := findType(richAST, recvType)
	if tm == nil {
		return ""
	}
	if m, ok := tm.Methods[name]; ok {
		return formatMethodMeta(tm, m)
	}
	if ft, ok := tm.Fields[name]; ok {
		return formatField(tm, name, ft)
	}
	return ""
}

// declarationSiteHover handles the cursor sitting on a declaration rather than a
// use: a method name in `func (r Recv) Name(...)`, or a field in a struct body.
// Neither has a receiver expression to the cursor's left, so the enclosing
// declaration supplies it.
func declarationSiteHover(richAST *transpiler.RichAST, lines []string, line int, word string) string {
	if recv := receiverTypeOnLine(lines[line], word); recv != "" {
		if tm := findType(richAST, recv); tm != nil {
			if m, ok := tm.Methods[word]; ok {
				return formatMethodMeta(tm, m)
			}
		}
	}
	if enclosing := enclosingTypeDecl(lines, line); enclosing != "" {
		if tm := findType(richAST, enclosing); tm != nil {
			if ft, ok := tm.Fields[word]; ok && isFieldDeclLine(lines[line], word) {
				return formatField(tm, word, ft)
			}
			for _, v := range tm.SealedVariants {
				if v.Name == word {
					return formatVariant(&v, tm)
				}
			}
		}
	}
	return ""
}

// receiverTypeOnLine extracts `Recv` from `func (r Recv) word(...)`, or "" when
// the line is not that method's declaration.
func receiverTypeOnLine(line, word string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "func (") {
		return ""
	}
	close := strings.Index(trimmed, ")")
	if close < 0 {
		return ""
	}
	after := strings.TrimSpace(trimmed[close+1:])
	if !strings.HasPrefix(after, word) {
		return ""
	}
	recv := strings.Fields(trimmed[len("func ("):close])
	if len(recv) < 2 {
		return ""
	}
	return stripTypeParams(recv[1])
}

// enclosingTypeDecl scans upward for the `type`/`sealed type` header whose body
// the given line sits in.
func enclosingTypeDecl(lines []string, line int) string {
	for i := line; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if i != line && trimmed == "}" {
			return ""
		}
		rest, ok := strings.CutPrefix(trimmed, "sealed type ")
		if !ok {
			rest, ok = strings.CutPrefix(trimmed, "type ")
		}
		if !ok {
			continue
		}
		name := rest
		for j := 0; j < len(name); j++ {
			if !isIdentChar(name[j]) {
				name = name[:j]
				break
			}
		}
		return name
	}
	return ""
}

// isFieldDeclLine reports whether the line declares `word` as a struct field.
func isFieldDeclLine(line, word string) bool {
	trimmed := strings.TrimSpace(line)
	for _, kw := range []string{"val ", "var "} {
		if rest, ok := strings.CutPrefix(trimmed, kw); ok {
			return strings.HasPrefix(rest, word)
		}
	}
	return false
}

// findSealedVariant locates a `case` by name across every sealed type.
func findSealedVariant(richAST *transpiler.RichAST, name string) (*transpiler.SealedVariant, *transpiler.TypeMetadata) {
	for _, tm := range richAST.Types {
		if !tm.IsSealed {
			continue
		}
		for i := range tm.SealedVariants {
			if tm.SealedVariants[i].Name == name {
				return &tm.SealedVariants[i], tm
			}
		}
	}
	return nil, nil
}

func lookupSymbol(richAST *transpiler.RichAST, name string) string {
	// Sealed cases are checked before the type table, not after. The transpiler
	// generates a standalone type per variant, carrying Apply and Unapply, and
	// registers it under the variant's own name — so an exact-key lookup finds
	// that lowering artifact first and reports `type Circle` with plumbing the
	// user never wrote, instead of `case Circle(radius float64)`.
	if variant, parent := findSealedVariant(richAST, name); variant != nil {
		return formatVariant(variant, parent)
	}
	if typeMeta, ok := richAST.Types[name]; ok {
		return formatTypeMeta(typeMeta)
	}
	for key, typeMeta := range richAST.Types {
		if strings.HasSuffix(key, "."+name) {
			return formatTypeMeta(typeMeta)
		}
	}
	if fm := findFunction(richAST, name); fm != nil {
		return formatFuncMeta(fm)
	}
	if companion, ok := richAST.CompanionObjects[name]; ok {
		return fmt.Sprintf("```gala\n%s — sealed case constructor\n```\n", companion.Name)
	}
	for key, companion := range richAST.CompanionObjects {
		if strings.HasSuffix(key, "."+name) {
			return fmt.Sprintf("```gala\n%s — sealed case constructor\n```\n", companion.Name)
		}
	}

	// Built-in functions
	if info, ok := builtinFuncDocs[name]; ok {
		return info
	}

	return ""
}

var builtinFuncDocs = map[string]string{
	"Println": "```gala\nPrintln(args ...any)\n```\n\n*Built-in* — prints arguments followed by a newline. Rewritten to `fmt.Println`.\n",
	"Print":   "```gala\nPrint(args ...any)\n```\n\n*Built-in* — prints arguments. Rewritten to `fmt.Print`.\n",
	"SliceOf": "```gala\nSliceOf[T](elems ...T) []T\n```\n\n*Built-in* — creates a Go slice from the given elements.\n",
	"len":     "```gala\nlen(v) int\n```\n\n*Go built-in* — returns the length of a string, slice, array, map, or channel.\n",
	"cap":     "```gala\ncap(v) int\n```\n\n*Go built-in* — returns the capacity of a slice or channel.\n",
	"make":    "```gala\nmake(T, size ...int) T\n```\n\n*Go built-in* — allocates and initializes a slice, map, or channel.\n",
	"append":  "```gala\nappend(slice []T, elems ...T) []T\n```\n\n*Go built-in* — appends elements to a slice.\n",
	"copy":    "```gala\ncopy(dst []T, src []T) int\n```\n\n*Go built-in* — copies elements from src to dst slice.\n",
	"delete":  "```gala\ndelete(m map[K]V, key K)\n```\n\n*Go built-in* — deletes a key from a map.\n",
	"close":   "```gala\nclose(ch chan T)\n```\n\n*Go built-in* — closes a channel.\n",
	"panic":   "```gala\npanic(v any)\n```\n\n*Go built-in* — stops normal execution and begins panicking.\n",
	"recover": "```gala\nrecover() any\n```\n\n*Go built-in* — regains control of a panicking goroutine.\n",
}

// renderHover assembles the standard hover body: a fenced signature, the doc
// comment, then the owning package.
func renderHover(signature, doc, pkg string) string {
	var b strings.Builder
	b.WriteString("```gala\n")
	b.WriteString(signature)
	b.WriteString("\n```\n")
	if doc != "" {
		b.WriteString("\n" + doc + "\n")
	}
	if pkg != "" {
		b.WriteString(fmt.Sprintf("\n*Package: %s*\n", pkg))
	}
	return b.String()
}

func formatTypeMeta(meta *transpiler.TypeMetadata) string {
	var b strings.Builder
	b.WriteString("```gala\n")
	if meta.IsSealed {
		b.WriteString("sealed type " + meta.Name)
	} else {
		b.WriteString("type " + meta.Name)
	}
	if len(meta.TypeParams) > 0 {
		b.WriteString("[" + strings.Join(meta.TypeParams, ", ") + "]")
	}
	b.WriteString("\n```\n")

	if meta.Doc != "" {
		b.WriteString("\n" + meta.Doc + "\n")
	}

	if len(meta.FieldNames) > 0 {
		b.WriteString("\n**Fields:**\n")
		for i, fn := range meta.FieldNames {
			ft := meta.Fields[fn]
			mut := "val"
			if i < len(meta.ImmutFlags) && !meta.ImmutFlags[i] {
				mut = "var"
			}
			b.WriteString(fmt.Sprintf("- `%s %s %s`\n", mut, fn, ft))
		}
	}
	if len(meta.SealedVariants) > 0 {
		b.WriteString("\n**Cases:**\n")
		for _, v := range meta.SealedVariants {
			b.WriteString(fmt.Sprintf("- `%s`\n", variantSignature(&v)))
		}
	}
	if len(meta.Methods) > 0 {
		b.WriteString("\n**Methods:**\n")
		for _, name := range sortedMethodNames(meta.Methods) {
			m := meta.Methods[name]
			b.WriteString(fmt.Sprintf("- `%s(%s) %s`\n", name, formatMethodParams(m), m.ReturnType))
		}
	}
	if meta.Package != "" {
		b.WriteString(fmt.Sprintf("\n*Package: %s*\n", meta.Package))
	}
	return b.String()
}

// sortedMethodNames keeps the method list stable across hovers; Go map order is
// randomized, so an unsorted list reshuffles every time the popup opens.
func sortedMethodNames(methods map[string]*transpiler.MethodMetadata) []string {
	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func formatFuncMeta(meta *transpiler.FunctionMetadata) string {
	var b strings.Builder
	b.WriteString("func " + meta.Name)
	if len(meta.TypeParams) > 0 {
		b.WriteString("[" + strings.Join(meta.TypeParams, ", ") + "]")
	}
	b.WriteString("(")
	for i, pn := range meta.ParamNames {
		if i > 0 {
			b.WriteString(", ")
		}
		if i < len(meta.ParamTypes) {
			b.WriteString(pn + " " + meta.ParamTypes[i].String())
		}
	}
	b.WriteString(")")
	if meta.ReturnType != nil && !meta.ReturnType.IsNil() {
		b.WriteString(" " + meta.ReturnType.String())
	}
	return renderHover(b.String(), meta.Doc, meta.Package)
}

// formatMethodMeta renders a method with its receiver, so the popup says which
// type the method belongs to.
func formatMethodMeta(owner *transpiler.TypeMetadata, m *transpiler.MethodMetadata) string {
	var b strings.Builder
	b.WriteString("func (" + owner.Name + ") " + m.Name)
	if len(m.TypeParams) > 0 {
		b.WriteString("[" + strings.Join(m.TypeParams, ", ") + "]")
	}
	b.WriteString("(" + formatMethodParams(m) + ")")
	if m.ReturnType != nil && !m.ReturnType.IsNil() {
		b.WriteString(" " + m.ReturnType.String())
	}
	pkg := m.Package
	if pkg == "" {
		pkg = owner.Package
	}
	return renderHover(b.String(), m.Doc, pkg)
}

func formatField(owner *transpiler.TypeMetadata, name string, ft transpiler.Type) string {
	mut := "val"
	for i, fn := range owner.FieldNames {
		if fn == name && i < len(owner.ImmutFlags) && !owner.ImmutFlags[i] {
			mut = "var"
		}
	}
	sig := fmt.Sprintf("%s %s %s", mut, name, ft)
	doc := owner.FieldDocs[name]
	body := renderHover(sig, doc, owner.Package)
	return body + fmt.Sprintf("\n*Field of `%s`*\n", owner.Name)
}

// formatVariant renders a sealed case as a case of its parent type. The
// transpiler also generates a standalone type per variant, carrying Apply and
// Unapply; that is lowering detail, and surfacing it as though the user wrote it
// misrepresents the language.
func formatVariant(v *transpiler.SealedVariant, parent *transpiler.TypeMetadata) string {
	pkg := ""
	parentName := ""
	if parent != nil {
		pkg = parent.Package
		parentName = parent.Name
		if len(parent.TypeParams) > 0 {
			parentName += "[" + strings.Join(parent.TypeParams, ", ") + "]"
		}
	}
	body := renderHover(variantSignature(v), v.Doc, pkg)
	if parentName != "" {
		body += fmt.Sprintf("\n*Case of `%s`*\n", parentName)
	}
	return body
}

func variantSignature(v *transpiler.SealedVariant) string {
	var fields []string
	for i, fn := range v.FieldNames {
		if i < len(v.FieldTypes) {
			fields = append(fields, fn+" "+v.FieldTypes[i].String())
		} else {
			fields = append(fields, fn)
		}
	}
	return fmt.Sprintf("case %s(%s)", v.Name, strings.Join(fields, ", "))
}

// localHover renders a local val/var. The declaration keyword comes from the
// source line so `val` and `var` are not conflated.
func localHover(richAST *transpiler.RichAST, name, typStr, line string) string {
	kw := "val"
	if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "var ") {
		kw = "var"
	}
	body := renderHover(fmt.Sprintf("%s %s %s", kw, name, typStr), "", "")
	// Carry the type's own documentation, so hovering a binding explains what it
	// holds rather than only naming it.
	if tm := findType(richAST, stripTypeParams(typStr)); tm != nil && tm.Doc != "" {
		body += "\n" + tm.Doc + "\n"
	}
	return body
}

// packageHover renders an import alias or a package named in an import line.
func packageHover(richAST *transpiler.RichAST, word, line string) string {
	pkg := resolvePackageQualifier(richAST, word)
	path := importPathOnLine(line)
	if pkg == "" {
		// On an import line the word is a path segment; the package is whatever
		// that path resolves to.
		if path != "" {
			if p, ok := richAST.Packages[path]; ok {
				pkg = p
			}
		}
		if pkg == "" {
			return ""
		}
	}
	if path == "" {
		for p, name := range richAST.Packages {
			if name == pkg {
				path = p
				break
			}
		}
	}
	sig := "package " + pkg
	if path != "" {
		sig += "\nimport \"" + path + "\""
	}
	return renderHover(sig, richAST.PackageDoc, "")
}

func isImportLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "import ")
}

func importPathOnLine(line string) string {
	first := strings.Index(line, `"`)
	if first < 0 {
		return ""
	}
	rest := line[first+1:]
	last := strings.Index(rest, `"`)
	if last < 0 {
		return ""
	}
	return rest[:last]
}

func formatMethodParams(m *transpiler.MethodMetadata) string {
	var parts []string
	for i, name := range m.ParamNames {
		if i < len(m.ParamTypes) {
			parts = append(parts, name+" "+m.ParamTypes[i].String())
		}
	}
	return strings.Join(parts, ", ")
}

func wordAtPosition(text string, line, char int) string {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return ""
	}
	l := lines[line]
	if char >= len(l) {
		return ""
	}
	start, end := char, char
	for start > 0 && isIdentChar(l[start-1]) {
		start--
	}
	for end < len(l) && isIdentChar(l[end]) {
		end++
	}
	if start == end {
		return ""
	}
	return l[start:end]
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func isExported(name string) bool {
	return len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'
}
