package commands

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

var docJSON bool

var docCmd = &cobra.Command{
	Use:   "doc <package>[.<Type>]",
	Short: "Show the types, methods and functions a package exports",
	Long: `Show the types, methods and functions a package exports.

Answers "what can I call on this?" without reading the source. Naming a type
narrows the output to that type's methods.

Examples:
  gala doc collection_immutable                  # every type and function
  gala doc collection_immutable.Array            # just Array's methods
  gala doc collection_immutable.Array --json     # the same, machine-readable`,
	Args: cobra.ExactArgs(1),
	Run:  runDoc,
}

func init() {
	docCmd.Flags().BoolVar(&docJSON, "json", false, "Emit JSON instead of text")
	rootCmd.AddCommand(docCmd)
}

// docType is one type's public surface.
type docType struct {
	Name       string       `json:"name"`
	Doc        string       `json:"doc,omitempty"`
	TypeParams []string     `json:"typeParams,omitempty"`
	Sealed     bool         `json:"sealed,omitempty"`
	Variants   []docVariant `json:"variants,omitempty"`
	Fields     []docField   `json:"fields,omitempty"`
	Methods    []docSignat  `json:"methods,omitempty"`
}

// docVariant is one case of a sealed type.
type docVariant struct {
	Name   string     `json:"name"`
	Doc    string     `json:"doc,omitempty"`
	Params []docField `json:"params,omitempty"`
}

type docField struct {
	Name string `json:"name"`
	Doc  string `json:"doc,omitempty"`
	Type string `json:"type"`
}

// docSignat is a callable: a method or a package-level function.
type docSignat struct {
	Name       string     `json:"name"`
	Doc        string     `json:"doc,omitempty"`
	TypeParams []string   `json:"typeParams,omitempty"`
	Params     []docField `json:"params"`
	Returns    string     `json:"returns,omitempty"`
}

type docPackage struct {
	Package   string      `json:"package"`
	Doc       string      `json:"doc,omitempty"`
	Types     []docType   `json:"types,omitempty"`
	Functions []docSignat `json:"functions,omitempty"`
}

func runDoc(cmd *cobra.Command, args []string) {
	pkgName, typeName := splitDocTarget(args[0])

	dir, err := findPackageDir(pkgName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	pkg, err := loadPackageDoc(pkgName, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if typeName != "" {
		narrowed, ok := narrowToType(pkg, typeName)
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: %s has no type %s\n", pkgName, typeName)
			os.Exit(1)
		}
		pkg = narrowed
	}

	if docJSON {
		out, marshalErr := json.MarshalIndent(pkg, "", "  ")
		if marshalErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", marshalErr)
			os.Exit(1)
		}
		fmt.Println(string(out))
		return
	}
	renderPackageDoc(cmd.OutOrStdout(), pkg)
}

// splitDocTarget splits `pkg.Type` into its parts. A target with no dot is a
// bare package; the type half is optional.
//
// The dot only separates a type when what follows it looks like one. Without
// that test a path-shaped argument — `gala doc ./mypkg`, which is how every
// other subcommand takes a package — split into an empty package name and a
// "type" of `/mypkg`, and reported `package "" not found`.
func splitDocTarget(target string) (pkgName, typeName string) {
	i := strings.LastIndex(target, ".")
	if i < 0 {
		return target, ""
	}
	candidate := target[i+1:]
	if candidate == "" || strings.ContainsAny(candidate, `/\`) || !ast.IsExported(candidate) {
		return target, ""
	}
	return target[:i], candidate
}

// findPackageDir locates a package directory among the resolved search paths —
// the project itself, the stdlib for this CLI version, and any GALA modules
// gala.mod requires. Reuses the same resolution `gala transpile` performs so
// `doc` sees exactly the packages a build would.
func findPackageDir(pkgName string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// autoResolveSearchPaths keys off a file path, so anchor it at a notional
	// file in the working directory.
	paths := autoResolveSearchPaths(filepath.Join(cwd, "doc.gala"), []string{cwd})

	for _, p := range paths {
		candidate := filepath.Join(p, pkgName)
		if files, globErr := filepath.Glob(filepath.Join(candidate, "*.gala")); globErr == nil && len(files) > 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("package %q not found in any search path; "+
		"check the name, or add it to gala.mod if it comes from a module", pkgName)
}

// loadPackageDoc analyzes every .gala file in dir as one package and collects
// the surface. The analyzer is given the sibling list so a type declared in one
// file and extended in another reports its whole method set.
func loadPackageDoc(pkgName, dir string) (*docPackage, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.gala"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no .gala files in %s", dir)
	}
	sort.Strings(files)

	p := transpiler.NewAntlrGalaParser()
	cwd, _ := os.Getwd()
	paths := autoResolveSearchPaths(files[0], []string{cwd, filepath.Dir(dir)})
	a := analyzer.NewGalaAnalyzerWithPackageFiles(p, paths, files)

	content, err := os.ReadFile(files[0])
	if err != nil {
		return nil, err
	}
	tree, docs, parseErrs := p.ParseLenient(string(content))
	if len(parseErrs) > 0 {
		return nil, parseErrs[0]
	}
	rich, err := a.Analyze(tree, docs, files[0])
	if err != nil {
		return nil, err
	}

	return collectPackageDoc(pkgName, rich), nil
}

// collectPackageDoc filters the analyzed metadata down to what this package
// declares, dropping anything reached only because it was imported.
func collectPackageDoc(pkgName string, rich *transpiler.RichAST) *docPackage {
	out := &docPackage{Package: pkgName, Doc: rich.PackageDoc}
	companions := generatedCaseCompanions(rich, pkgName)

	// Both maps are keyed by QUALIFIED name (`collection_immutable.Array`), so
	// the export test has to run on the bare half — otherwise every entry is
	// judged by the first letter of its package and silently dropped.
	seen := make(map[string]bool)
	for name, meta := range rich.Types {
		bare := bareTypeName(name)
		if meta == nil || !declaredBy(meta.Package, pkgName) || !ast.IsExported(bare) || seen[bare] {
			continue
		}
		if companions[bare] {
			continue
		}
		seen[bare] = true
		out.Types = append(out.Types, buildDocType(name, meta))
	}
	for name, meta := range rich.Functions {
		if meta == nil || !declaredBy(meta.Package, pkgName) {
			continue
		}
		bare := bareTypeName(name)
		if !ast.IsExported(bare) {
			continue
		}
		out.Functions = append(out.Functions, docSignat{
			Name:       bare,
			Doc:        meta.Doc,
			TypeParams: meta.TypeParams,
			Params:     namedTypes(meta.ParamNames, meta.ParamTypes),
			Returns:    typeString(meta.ReturnType),
		})
	}

	sort.Slice(out.Types, func(i, j int) bool { return out.Types[i].Name < out.Types[j].Name })
	sort.Slice(out.Functions, func(i, j int) bool { return out.Functions[i].Name < out.Functions[j].Name })
	return out
}

func buildDocType(name string, meta *transpiler.TypeMetadata) docType {
	dt := docType{
		Name:       bareTypeName(name),
		Doc:        meta.Doc,
		TypeParams: meta.TypeParams,
		Sealed:     meta.IsSealed,
	}
	for _, v := range meta.SealedVariants {
		dt.Variants = append(dt.Variants, docVariant{
			Name:   v.Name,
			Doc:    v.Doc,
			Params: namedTypes(v.FieldNames, v.FieldTypes),
		})
	}
	// A sealed type has no fields of its own: the grammar gives it only cases,
	// and the analyzer synthesises its struct fields by merging theirs, plus a
	// `_variant` discriminator. Listing them would repeat what each case already
	// shows — and when two cases declare the same field name with different
	// types the merged field is prefixed with the case name, so `Add(Left int)`
	// and `Scale(Left float64)` would surface as `AddLeft` and `ScaleLeft`,
	// names nobody wrote.
	if !meta.IsSealed {
		for _, fieldName := range meta.FieldNames {
			if !ast.IsExported(fieldName) {
				continue
			}
			dt.Fields = append(dt.Fields, docField{
				Name: fieldName,
				Doc:  meta.FieldDocs[fieldName],
				Type: typeString(meta.Fields[fieldName]),
			})
		}
	}
	for methodName, m := range meta.Methods {
		if m == nil || !ast.IsExported(methodName) {
			continue
		}
		dt.Methods = append(dt.Methods, docSignat{
			Name:       methodName,
			Doc:        m.Doc,
			TypeParams: m.TypeParams,
			Params:     namedTypes(m.ParamNames, m.ParamTypes),
			Returns:    typeString(m.ReturnType),
		})
	}
	sort.Slice(dt.Methods, func(i, j int) bool { return dt.Methods[i].Name < dt.Methods[j].Name })
	return dt
}

// declaredBy reports whether metadata belongs to the package being documented.
// An empty Package means the analyzer recorded it unqualified, which happens
// for the package currently being analyzed — i.e. this one.
func declaredBy(metaPackage, pkgName string) bool {
	return metaPackage == "" || metaPackage == pkgName
}

func bareTypeName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

func namedTypes(names []string, types []transpiler.Type) []docField {
	out := make([]docField, 0, len(types))
	for i, ty := range types {
		name := ""
		if i < len(names) {
			name = names[i]
		}
		out = append(out, docField{Name: name, Type: typeString(ty)})
	}
	return out
}

func typeString(ty transpiler.Type) string {
	if ty == nil || ty.IsNil() {
		return ""
	}
	return ty.String()
}

// narrowToType reduces the package to a single type.
//
// A sealed case resolves to the type that declares it. `Circle` is not a type a
// user can name — the transpiler's companion for it is filtered out as
// lowering detail — but asking about it is a reasonable thing to do, and the
// answer is the sealed type it belongs to. Reporting "no type Circle" would be
// literally true and useless.
func narrowToType(pkg *docPackage, typeName string) (*docPackage, bool) {
	for _, t := range pkg.Types {
		if t.Name == typeName {
			return &docPackage{Package: pkg.Package, Types: []docType{t}}, true
		}
	}
	for _, t := range pkg.Types {
		for _, v := range t.Variants {
			if v.Name == typeName {
				return &docPackage{Package: pkg.Package, Types: []docType{t}}, true
			}
		}
	}
	return nil, false
}

// renderPackageDoc writes the human-readable form of a package's API.
//
// It takes a writer so the rendering is testable. It previously printed
// straight to stdout, and the two things it silently failed to print — the
// package's own documentation and every function's — went unnoticed precisely
// because nothing could assert on the output.
func renderPackageDoc(w io.Writer, pkg *docPackage) {
	fmt.Fprintf(w, "package %s\n", pkg.Package)
	renderProse(w, pkg.Doc, "    ")

	for _, t := range pkg.Types {
		fmt.Fprintln(w)
		header := t.Name
		if len(t.TypeParams) > 0 {
			header += "[" + strings.Join(t.TypeParams, ", ") + "]"
		}
		if t.Sealed {
			header = "sealed type " + header
		} else {
			header = "type " + header
		}
		fmt.Fprintln(w, header)
		renderProse(w, t.Doc, "    ")

		for _, v := range t.Variants {
			fmt.Fprintf(w, "    case %s\n", formatSignature(docSignat{Name: v.Name, Params: v.Params}))
			renderProse(w, v.Doc, "        ")
		}
		for _, f := range t.Fields {
			fmt.Fprintf(w, "    %s %s\n", f.Name, f.Type)
			renderProse(w, f.Doc, "        ")
		}
		for _, m := range t.Methods {
			fmt.Fprintf(w, "    %s\n", formatSignature(m))
			renderProse(w, m.Doc, "        ")
		}
	}

	if len(pkg.Functions) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "functions")
		for _, f := range pkg.Functions {
			fmt.Fprintf(w, "    %s\n", formatSignature(f))
			renderProse(w, f.Doc, "        ")
		}
	}
}

// renderProse writes a doc comment under the declaration it documents, indented
// to sit beneath it.
//
// Nothing is written for an undocumented declaration: a blank line there would
// read as documentation that exists and is empty. Surrounding blank lines are
// trimmed too, since a doc comment ending in a bare `//` otherwise pushes a
// stray gap between one member and the next.
func renderProse(w io.Writer, doc, indent string) {
	doc = strings.Trim(doc, "\n")
	if doc == "" {
		return
	}
	for _, line := range strings.Split(doc, "\n") {
		if line == "" {
			fmt.Fprintln(w)
			continue
		}
		fmt.Fprintf(w, "%s%s\n", indent, line)
	}
}

func formatSignature(s docSignat) string {
	var b strings.Builder
	b.WriteString(s.Name)
	if len(s.TypeParams) > 0 {
		b.WriteString("[" + strings.Join(s.TypeParams, ", ") + "]")
	}
	b.WriteString("(")
	for i, p := range s.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		if p.Name != "" {
			b.WriteString(p.Name + " ")
		}
		b.WriteString(p.Type)
	}
	b.WriteString(")")
	if s.Returns != "" {
		b.WriteString(" " + s.Returns)
	}
	return b.String()
}

// generatedCaseCompanions names the types the transpiler synthesises for sealed
// cases — one per case, registered under the case's own name and carrying only
// Apply and Unapply.
//
// They are lowering detail, not API. Documenting them presents `type Circle`
// with two methods nobody wrote, alongside the `case Circle(...)` the user
// actually declared, and buries the real types among them.
func generatedCaseCompanions(rich *transpiler.RichAST, pkgName string) map[string]bool {
	companions := make(map[string]bool)
	for _, meta := range rich.Types {
		if meta == nil || !meta.IsSealed || !declaredBy(meta.Package, pkgName) {
			continue
		}
		for _, v := range meta.SealedVariants {
			companions[v.Name] = true
		}
	}
	return companions
}

// joinParams renders a case's fields as they were declared.
