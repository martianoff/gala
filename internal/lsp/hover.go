package lsp

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"runtime"
	"slices"
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
	info := h.hoverInfo(text, uriToPath(uri), richAST, varTypes, line, char)
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
//
// path is the on-disk path of the document being hovered; it is what the
// analyzer recorded as DefinedIn, and is how a declaration in this file is told
// apart from a same-named one elsewhere in the package.
func (h *GalaHandler) hoverInfo(text, path string, richAST *transpiler.RichAST, varTypes map[string]string, line, char int) string {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return ""
	}
	word := wordAtPosition(text, line, char)
	if word == "" {
		return ""
	}

	// The cursor sits ON a declaration: a method name, a struct field, a sealed
	// case, or a type. Resolved by position against the metadata the analyzer
	// already recorded rather than by re-reading the source — the same anchor
	// go-to-definition uses.
	if info := declarationAt(richAST, path, line, char, word); info != "" {
		return info
	}

	// `qualifier.word`. typeAtDot resolves the qualifier for us, returning a
	// packagePrefix-marked name for a package and a type name otherwise — the
	// same single call completion dispatches on, so the two cannot disagree
	// about what an expression is.
	if recv := typeAtDot(text, line, char, richAST, varTypes); recv != "" {
		// Both branches return unconditionally. A selector whose receiver resolved
		// but whose member did not must produce nothing, not fall through to a
		// global name search — that answers `resp.Body` with whatever unrelated
		// type happens to be called Body. Go-to-definition guards the same class
		// of wrong answer.
		if pkg, isPkg := strings.CutPrefix(recv, packagePrefix); isPkg {
			return packageMemberHover(richAST, pkg, word)
		}
		return memberHover(richAST, recv, word)
	}

	// A local val/var carries no metadata entry — its type comes from the
	// transformer's resolved scope, the same source inlay hints read.
	funcScope := findEnclosingFunc(lines, line)
	if typStr := lookupVarType(varTypes, funcScope, word); typStr != "" {
		return localHover(richAST, word, typStr)
	}

	if info := packageHover(richAST, text, word); info != "" {
		return info
	}

	return lookupSymbol(richAST, word)
}

// declarationAt resolves the cursor against declaration positions recorded
// during analysis, returning "" when the cursor is not on one.
//
// Text-scanning for `func (r Recv) Name(` and friends is the thing this package
// tells itself not to do: the shapes are ambiguous (generic receivers, one-line
// bodies, comments, string literals) and the transpiler has already resolved
// them exactly. SourcePos is 1-based line, 0-based column, matching
// definition.go's locationAt.
func declarationAt(richAST *transpiler.RichAST, path string, line, char int, word string) string {
	if path == "" {
		return ""
	}
	for _, tm := range richAST.Types {
		if !sameSourceFile(tm.DefinedIn, path) {
			continue
		}
		if tm.Name == word && posCovers(tm.Pos, line, char, word) {
			return formatTypeMeta(tm)
		}
		if pos, ok := tm.FieldPositions[word]; ok && posCovers(pos, line, char, word) {
			if ft, ok := tm.Fields[word]; ok {
				return formatField(tm, word, ft)
			}
		}
		for i := range tm.SealedVariants {
			v := &tm.SealedVariants[i]
			if v.Name == word && posCovers(v.Pos, line, char, word) {
				return formatVariant(v, tm)
			}
		}
		if m, ok := tm.Methods[word]; ok && sameSourceFile(m.DefinedIn, path) && posCovers(m.Pos, line, char, word) {
			return formatMethodMeta(tm, m)
		}
	}
	for _, fm := range richAST.Functions {
		if fm.Name == word && sameSourceFile(fm.DefinedIn, path) && posCovers(fm.Pos, line, char, word) {
			return formatFuncMeta(fm)
		}
	}
	return ""
}

// posCovers reports whether an analyzer SourcePos names the identifier under an
// LSP cursor.
func posCovers(pos transpiler.SourcePos, line, char int, name string) bool {
	if pos.Line == 0 {
		return false
	}
	return pos.Line-1 == line && char >= pos.Column && char <= pos.Column+len(name)
}

// packageMemberHover renders a symbol accessed through a package qualifier.
func packageMemberHover(richAST *transpiler.RichAST, pkg, name string) string {
	qualified := pkg + "." + name
	// Scoped to pkg: an unscoped variant search here would answer `mypkg.Some`
	// with std's Some, defeating the qualifier the user typed.
	if variant, parent := findSealedVariant(richAST, name, pkg); variant != nil && parent != nil && parent.Package == pkg {
		return formatVariant(variant, parent)
	}
	if tm := findType(richAST, qualified); tm != nil {
		return formatTypeMeta(tm)
	}
	if fm := findFunction(richAST, qualified); fm != nil {
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

// findSealedVariant locates a `case` by name, preferring one declared in
// preferPkg.
//
// Iteration is over sorted keys, not Go map order: `std` alone contributes the
// case names Some, None, Success, Failure, Left and Right, so a file with a
// sealed type of its own reusing one of those would otherwise get a parent —
// and a rendered hover — that changed between invocations.
func findSealedVariant(richAST *transpiler.RichAST, name, preferPkg string) (*transpiler.SealedVariant, *transpiler.TypeMetadata) {
	var anyV *transpiler.SealedVariant
	var anyT *transpiler.TypeMetadata
	for _, key := range slices.Sorted(maps.Keys(richAST.Types)) {
		tm := richAST.Types[key]
		if tm == nil || !tm.IsSealed {
			continue
		}
		for i := range tm.SealedVariants {
			if tm.SealedVariants[i].Name != name {
				continue
			}
			if tm.Package == preferPkg {
				return &tm.SealedVariants[i], tm
			}
			if anyV == nil {
				anyV, anyT = &tm.SealedVariants[i], tm
			}
		}
	}
	return anyV, anyT
}

func lookupSymbol(richAST *transpiler.RichAST, name string) string {
	typeMeta, hasType := richAST.Types[name]
	// The transpiler generates a standalone companion type per sealed case,
	// carrying Apply and Unapply, and registers it under the case's own name. When
	// the exact-key type IS that artifact, the case is what the user actually
	// wrote and should win — reporting `type Circle` with plumbing nobody wrote
	// misrepresents the language.
	//
	// A same-named type from a DIFFERENT package is not an artifact and must not
	// be shadowed: `std` exports cases called Success, Failure, Some, Left and
	// Right, and a user's own `type Success struct` has to keep answering for
	// itself.
	if variant, parent := findSealedVariant(richAST, name, richAST.PackageName); variant != nil {
		if !hasType || (parent != nil && parent.Package == typeMeta.Package) {
			return formatVariant(variant, parent)
		}
	}
	if hasType {
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
		for _, name := range slices.Sorted(maps.Keys(meta.Methods)) {
			m := meta.Methods[name]
			b.WriteString(fmt.Sprintf("- `%s(%s) %s`\n", name, formatMethodParams(m), m.ReturnType))
		}
	}
	if meta.Package != "" {
		b.WriteString(fmt.Sprintf("\n*Package: %s*\n", meta.Package))
	}
	return b.String()
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

// localHover renders a local binding: its name and inferred type.
//
// Mutability is deliberately not claimed. The transformer knows whether a
// binding was `val` or `var` (scope.go's addVal/addVar), but that fact is not
// carried into the LSP's var channel, and deriving it from the cursor's line
// reports `val` at every reference to a `var` that is not its own declaration.
// Saying nothing beats saying something false; formatField gets this right
// because ImmutFlags is real metadata.
func localHover(richAST *transpiler.RichAST, name, typStr string) string {
	body := renderHover(name+" "+typStr, "", "")
	// The binding's own doc comment is not available, so the type's doc is
	// offered instead — attributed, so it does not read as documentation of the
	// binding itself.
	if tm := findType(richAST, stripTypeParams(typStr)); tm != nil && tm.Doc != "" {
		body += fmt.Sprintf("\n*Type* `%s` — %s\n", tm.Name, tm.Doc)
	}
	return body
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

// packageHover renders an import alias or package name.
//
// The import table comes from parseGalaImports, which understands the grouped
// `import ( ... )` form as well as single-line imports; hand-scanning the cursor
// line for a quoted path silently went dead inside a group.
func packageHover(richAST *transpiler.RichAST, text, word string) string {
	path, ok := parseGalaImports(text)[word]
	if !ok {
		// Not an alias — it may be the package's own name.
		for p, name := range richAST.Packages {
			if name == word {
				path, ok = p, true
				break
			}
		}
	}
	if !ok {
		return ""
	}
	pkg := word
	if name, found := richAST.Packages[path]; found {
		pkg = name
	}
	return renderHover("package "+pkg+"\nimport \""+path+"\"", "", "")
}

// sameSourceFile compares two paths for identity the way the analyzer does when
// it records DefinedIn: absolute, cleaned, and case-insensitively on Windows,
// where the client's URI casing need not match the analyzer's.
func sameSourceFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(absA), filepath.Clean(absB))
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}
