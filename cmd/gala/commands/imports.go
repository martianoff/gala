package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"

	"github.com/spf13/cobra"
)

// importInfo is a single import declaration extracted from a .gala file.
// Field order in the JSON output preserves the source order of the imports.
type importInfo struct {
	Path  string `json:"path"`
	Alias string `json:"alias"`
	Dot   bool   `json:"dot"`
}

// fileImports is the per-file result. Package is the file's package name,
// Imports preserves source order, and Error holds the parse error message
// (empty when the file parsed successfully).
type fileImports struct {
	File    string       `json:"file"`
	Package string       `json:"package"`
	Imports []importInfo `json:"imports"`
	Error   string       `json:"error"`
}

var importsJSON bool

var importsCmd = &cobra.Command{
	Use:   "imports --json <file1.gala> [file2.gala ...]",
	Short: "Extract package name and imports from .gala files (parse-only)",
	Long: `Extract the package name and import declarations from one or more .gala files.

This is a fast, dependency-free parse: it runs only the ANTLR parser, never the
analyzer or transpiler, so no Go SDK is required. It is intended as a helper for
build tooling such as the Gazelle extension.

Output is a JSON array, one entry per input file, with imports in source order.
Files that fail to parse produce an entry with a non-empty "error" field and an
empty "imports" array; processing continues and the exit code stays 0. The exit
code is non-zero only for usage errors (no files given, or a file not found).`,
	Args:          cobra.ArbitraryArgs,
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runImports,
}

func runImports(cmd *cobra.Command, args []string) error {
	if !importsJSON {
		return fmt.Errorf("--json is required (it is currently the only supported output format)")
	}
	if len(args) == 0 {
		return fmt.Errorf("no input files given")
	}

	results := make([]fileImports, 0, len(args))
	p := parser.NewAntlrGalaParser()

	for _, file := range args {
		src, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("cannot read %q: %w", file, err)
		}
		results = append(results, extractImports(p, file, string(src)))
	}

	out, err := json.Marshal(results)
	if err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(out))
	return nil
}

// extractImports parses a single file and returns its package name and imports.
// A parse failure is reported in the Error field with an empty Imports slice.
func extractImports(p *parser.AntlrGalaParser, file, src string) fileImports {
	return importsOfParse(file, p.ParseFile(src))
}

// importsOfParse is extractImports of parsed, a parse of file.
func importsOfParse(file string, parsed *parser.Parsed) fileImports {
	result := fileImports{
		File:    file,
		Imports: []importInfo{},
	}

	tree, _, err := parsed.Strict()
	if err != nil {
		result.Error = err.Error()
		return result
	}

	sourceFile, ok := tree.(grammar.ISourceFileContext)
	if !ok {
		result.Error = "unexpected parse tree type"
		return result
	}
	result.Package, result.Imports = importsOf(sourceFile, result.Imports)
	return result
}

// importsOf is the package name of sf and its imports, appended to imports,
// on ANTLR's tree.
func importsOf(sf grammar.ISourceFileContext, imports []importInfo) (string, []importInfo) {
	var pkgName string
	if pkg := sf.PackageClause(); pkg != nil {
		if pkgCtx, ok := pkg.(*grammar.PackageClauseContext); ok {
			if ident := pkgCtx.Identifier(); ident != nil {
				pkgName = ident.GetText()
			}
		}
	}

	for _, impDecl := range sf.AllImportDeclaration() {
		ctx, ok := impDecl.(*grammar.ImportDeclarationContext)
		if !ok {
			continue
		}
		for _, spec := range ctx.AllImportSpec() {
			s, ok := spec.(*grammar.ImportSpecContext)
			if !ok {
				continue
			}
			var alias *string
			if aliasIdent := s.Identifier(); aliasIdent != nil {
				text := aliasIdent.GetText()
				alias = &text
			}
			imports = append(imports, importOf(s.STRING().GetText(), alias, s.GetChildCount()))
		}
	}
	return pkgName, imports
}

// importOf is the import of an import spec whose path string literal is str,
// whose alias identifier's text is *alias (nil when it has none), and which
// has kids children.
func importOf(str string, alias *string, kids int) importInfo {
	info := importInfo{
		Path: strings.Trim(str, "\""),
	}
	if alias != nil {
		// Named alias (e.g. import m "math"). A "." identifier is not
		// produced here — dot imports have no Identifier child.
		if *alias != "." {
			info.Alias = *alias
		}
	} else if kids > 1 {
		// Dot import: no identifier, but an extra child — the '.' terminal.
		info.Dot = true
	}
	return info
}

func init() {
	importsCmd.Flags().BoolVar(&importsJSON, "json", false, "Emit JSON output (currently the only supported format)")
}
