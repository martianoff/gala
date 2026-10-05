package transformer

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/infer"
	"martianoff/gala/internal/transpiler/registry"
	"martianoff/gala/internal/transpiler/resolver"
)

// TypeTraceEntry records a single type resolution event for diagnostics.
type TypeTraceEntry struct {
	ExprStr string          // formatted expression
	Result  transpiler.Type // resolved type
	Method  string          // which resolution method was used
	File    string          // current file being transpiled
	Line    int             // line in source
}

type galaASTTransformer struct {
	currentScope      *scope
	packageName       string
	immutFields       map[string]bool
	structImmutFields map[string][]bool
	needsStdImport    bool
	needsFmtImport    bool
	needsUtf8Import   bool
	activeTypeParams  map[string]bool // type parameters bound by the enclosing generic declarations; see type_params.go
	typeParamNames    map[string]bool // names declared as a type parameter by any known generic; built lazily per file, see declaredTypeParamNames
	structFields      map[string][]string
	structFieldTypes  map[string]map[string]transpiler.Type // structName -> fieldName -> typeName
	genericMethods    map[string]map[string]bool            // receiverType -> methodName -> isGeneric
	// resultGenericMethods holds the name of every known method whose own
	// type parameters its result mentions (see isResultGenericMethodCall).
	resultGenericMethods map[string]bool
	functions            map[string]*transpiler.FunctionMetadata
	// galaPkgPaths is the set of import paths that are GALA packages, taken from
	// richAST.Packages (the analyzer fills that map only inside its GALA-package
	// branch). It lets a qualified call tell whether its qualifier names a GALA
	// package or a Go one. `functions` is keyed by package NAME and merged across
	// every file in the package, so GALA's `strings` and Go's `strings` collide
	// there — as do io, path, json, crypto and fs. Membership here is metadata,
	// not an import-path heuristic.
	galaPkgPaths           map[string]bool
	typeMetas              map[string]*transpiler.TypeMetadata
	variantNames           map[string]bool                                // bare name of every sealed variant in typeMetas (GALA-E0061 pre-filter)
	companionObjects       map[string]*transpiler.CompanionObjectMetadata // companion name -> metadata
	importManager          *ImportManager                                 // unified import tracking (includes transitive imports and dot-import usage)
	cachedTypeResolver     *resolver.TypeResolver
	cachedTypeResolverRev  uint64 // import-manager revision cachedTypeResolver was built at
	tempVarCount           int
	inferer                *infer.Inferer
	returnSlot             returnSlot                                    // result type of the innermost function or lambda body (see return_slot.go)
	typeAliases            map[string]transpiler.Type                    // type alias name -> underlying type (e.g., "Handler" -> func(string) Future[string])
	fileTypeDeclTargets    map[string]transpiler.Type                    // this file's `type X Y` declarations, name -> target parsed as written; complete before any declaration is transformed
	hasOpaque              bool                                          // some known type is an opaque type; gates every opaque-type check
	goTypeInfo             *transpiler.GoTypeInfo                        // type info from Go packages (stdlib, local Go files, third-party)
	filePath               string                                        // source file path (for error reporting)
	richAST                *transpiler.RichAST                           // reference to the primary RichAST for live metadata access
	traceTypeResolution    bool                                          // when true, type resolution events are recorded
	typeTraces             []TypeTraceEntry                              // recorded type resolution events (only when tracing is enabled)
	exprTypeCache          map[ast.Expr]transpiler.Type                  // cache for getExprTypeNameManual results
	goResults              map[*ast.CallExpr]*goResult                   // Go calls converted to one GALA value, keyed by the wrapping helper call (see go_results.go)
	tryThunkLambda         *grammar.LambdaExpressionContext              // the lambda being lowered as the thunk of Try(...) (see tryThunkValue)
	needsEmbedImport       bool                                          // true when embed val declarations require import "embed"
	warnTypeInference      bool                                          // when true, log warnings about type inference fallbacks
	inferenceWarnings      []string                                      // collected type inference warnings
	unresolvedTypes        []UnresolvedType                              // expressions whose type could not be determined; collected only under GALA_WARN_TYPES=1. See unresolved_types.go.
	unresolvedSeen         map[ast.Expr]bool                             // AST nodes already recorded, so a re-queried expression is rendered once; diagnostics only
	unrecordedCallee       ast.Expr                                      // callee whose type the HM bridge is querying, kept out of the inventory; see toInferCallee
	diagPackageNames       map[string]bool                               // package qualifiers derived from Go type info, for the unresolved-type filter; built lazily, diagnostics only
	structMetas            map[string]*structMetaConfig                  // StructMeta configs, keyed by the resolved name of their struct
	valueMetas             map[string]*valueMetaConfig                   // generated ValueMeta structs (keyed by the value type as spelled in Go)
	instanceInterfaceNames map[string]string                             // type name -> actual generated interface name (for collision avoidance)
	defaultTrees           map[defaultTreeKey]grammar.IExpressionContext // parse trees of declared default values, one per default per file (see defaultExprTree)
	loweringDefault        *defaultLowering                              // non-nil while a declared default value is being lowered at a use site (see transformDefaultExpr)
	expectedArgTypes       expectedArgTypeStack                          // (B1) LIFO stack of expected-type hints for downward inference; replaces a single-field side-channel. See expected_arg_stack.go for the contract.
	matchInStatementPos    bool                                          // set when transforming a `subject match { ... }` whose value is discarded (statement-position match); causes the IIFE to be lowered as void so void-returning arm calls do not appear as `return d.Skip()`
	methodReceivers        []methodReceiver                              // receivers collected during the walk, validated once the file is complete (see method_receiver_alias.go)
	loopControlSites       map[*ast.BranchStmt]loopControlSite           // source position of each `break` / `continue` lowered from source, checked by checkLoopControl once the file is complete
	branchingCalls         map[*ast.CallExpr]branchingSite               // the function-literal call each match or if-expression whose value is used lowered to, checked by checkBranchingCalls once the file is complete
	userReturns            map[*ast.ReturnStmt]loopControlSite           // source position of each `return` lowered from source, checked by checkBranchingCalls
	hoisted                map[*ast.Ident]hoistedValue                   // a match or if-expression lowered as statements, by the placeholder that stands for it until its consumer takes it (see hoisted_value.go)
	escapeCache            map[antlr.Tree]bool                           // whether a parse subtree holds control flow that would leave a construct lowered to a function literal (see escapesConstruct)
	hoistedPre             []ast.Stmt                                    // statements the local declaration being lowered needs before it, set when its initializer is lowered as statements (see hoisted_value.go)
	localDeclaration       bool                                          // set while transformStatement lowers a val, var or assignment statement in a function body, whose single value may be lowered as statements (see lowerDeclarationInitializers)
	userLoops              map[ast.Stmt]bool                             // the for / range loops written in source: the only loops a source `break` / `continue` may control (see loop_control.go)
	synthesizedReturns     map[*ast.ReturnStmt]bool                      // tracks ReturnStmt nodes synthesized by lowering match-arm tail expressions (vs. user-written `return X`). Used to inline a statement-position match whose arms contain user returns: stripReturnStatements would otherwise convert user `return X` into a bare return that only exits the synthetic match-IIFE, leaving the enclosing function — and any surrounding `for` loop — to spin without the intended exit.
	pendingMatchStmtBlock  *ast.BlockStmt                                // side-channel: when buildMatchExpressionFromClauses detects a statement-position match with user-written returns inside arm bodies, it stores the inlined block here and returns a placeholder expression. transformBlock consumes this field and replaces the placeholder ExprStmt with the inlined block, so the user's `return X` becomes a real Go return from the enclosing function.
	lspVarTypes            map[string]transpiler.Type                    // LSP: collects all resolved var types during transformation
	lspCurrentFunc         string                                        // LSP: name of the function currently being transformed (for scoping)
	lspLambdaParamHints    []transpiler.LambdaParamHint                  // LSP: positions of lambda params with inferred types
	lastLine               int                                           // last known ANTLR source line (for error reporting in deeply-nested helpers)
	lastCol                int                                           // last known ANTLR source column (for error reporting in deeply-nested helpers)
	typeEnvEpoch           uint32                                        // invalidates funcTypeEnv; see invalidateTypeEnv
	funcTypeEnv            infer.TypeEnv                                 // cached function-derived half of the Hindley-Milner environment for the current file; see functionTypeEnv
	funcTypeEnvEpoch       uint32                                        // typeEnvEpoch the cache above was built at
	funcTypeEnvImportRev   uint64                                        // importManager.Revision the cache above was built at, so an import change rebuilds it without being announced
	typeNameCache          typeNameMemo                                  // name-normalization memo shared by functionTypeEnv and buildTypeEnv; see sharedTypeNameMemo
	typeNameCacheEpoch     uint32                                        // typeEnvEpoch the memo above was filled at
	typeNameCacheImportRev uint64                                        // importManager.Revision the memo above was filled at

	// patternDefineTypes records the Go type of each name a pattern's `:=`
	// declares, so a sub-pattern's statements can be split into declarations
	// outside a guard and assignments inside it (see hoistPatternDecls).
	patternDefineTypes map[*ast.AssignStmt][]ast.Expr
}

// NewGalaASTTransformer creates a new instance of ASTTransformer for GALA.
//
// Capacity hints reduce map reallocations during the transform pass. The
// values are sized for a representative GALA package: ~50 types, ~50
// functions, and a few hundred expressions in the type-inference cache.
// Small packages waste a kilobyte or two of bucket space; large ones
// avoid the doubling-and-rehash cycle that an empty map walks through
// at 8, 16, 32, ... entries. CPU profile of an apex-shaped transpile
// flagged growing these maps as a contributor to allocation pressure
// (gcDrain / scanobject 18% of cumulative samples).
func NewGalaASTTransformer() transpiler.ASTTransformer {
	return &galaASTTransformer{
		immutFields:       make(map[string]bool, 64),
		structImmutFields: make(map[string][]bool, 32),
		activeTypeParams:  make(map[string]bool, 16),
		structFields:      make(map[string][]string, 32),
		structFieldTypes:  make(map[string]map[string]transpiler.Type, 32),
		genericMethods:    make(map[string]map[string]bool, 16),
		functions:         make(map[string]*transpiler.FunctionMetadata, 64),
		galaPkgPaths:      make(map[string]bool, 16),
		typeMetas:         make(map[string]*transpiler.TypeMetadata, 64),
		companionObjects:  make(map[string]*transpiler.CompanionObjectMetadata, 16),
		importManager:     NewImportManager(),
		inferer:           infer.NewInferer(),
		typeAliases:       make(map[string]transpiler.Type, 16),
		exprTypeCache:     make(map[ast.Expr]transpiler.Type, 256),
		structMetas:       make(map[string]*structMetaConfig, 16),
		valueMetas:        make(map[string]*valueMetaConfig, 4),
	}
}

func (t *galaASTTransformer) TransformForLSP(richAST *transpiler.RichAST) (*transpiler.TransformResult, error) {
	fset, file, transformErr := t.transform(richAST, true)
	// Always return collected var types, even on error (partial results)
	varTypes := make(map[string]transpiler.Type, len(t.lspVarTypes))
	for name, typ := range t.lspVarTypes {
		varTypes[name] = typ
	}
	hints := make([]transpiler.LambdaParamHint, len(t.lspLambdaParamHints))
	copy(hints, t.lspLambdaParamHints)
	return &transpiler.TransformResult{
		Fset:             fset,
		File:             file,
		VarTypes:         varTypes,
		LambdaParamHints: hints,
	}, transformErr
}

func (t *galaASTTransformer) resetExprTypeCache() {
	if t.exprTypeCache == nil {
		t.exprTypeCache = make(map[ast.Expr]transpiler.Type, 256)
	} else {
		clear(t.exprTypeCache)
	}
}

func (t *galaASTTransformer) Transform(richAST *transpiler.RichAST) (*token.FileSet, *ast.File, error) {
	return t.transform(richAST, false)
}

func (t *galaASTTransformer) transform(richAST *transpiler.RichAST, collectLSPMetadata bool) (fset *token.FileSet, file *ast.File, err error) {
	defer func() {
		if r := recover(); r != nil {
			if semErr, ok := r.(*galaerr.SemanticError); ok {
				err = semErr
				return
			}
			if os.Getenv("GALA_PANIC_STACK") == "1" {
				fmt.Fprintf(os.Stderr, "%v\n%s\n", r, debug.Stack())
			}
			// B4: convert any other panic into a coded internal error so CLI
			// users see a single search target (GALA-E0017) instead of a raw
			// Go stack trace. The recovered value is preserved in the message
			// for issue-filing context.
			//
			// t.lastLine/t.lastCol is wherever the transformer happened to be,
			// which for a panic raised on a concurrent parse worker is not even
			// approximately the cause. The hint says so — see
			// galaerr.InternalTransformerPanicHint for why its clause order
			// matters.
			err = galaerr.NewCodedSemanticError(
				galaerr.CodeInternalTransformerPanic,
				t.lastLine, t.lastCol,
				fmt.Sprintf("internal transpiler panic: %v", r),
				galaerr.InternalTransformerPanicHint,
			)
		}
	}()
	tree := richAST.Tree
	t.currentScope = nil
	t.resetExprTypeCache()
	t.goResults = nil
	t.loopControlSites = nil
	t.branchingCalls = nil
	t.userReturns = nil
	t.hoisted = nil
	t.escapeCache = nil
	t.userLoops = nil
	if collectLSPMetadata {
		t.lspVarTypes = make(map[string]transpiler.Type)
		t.lspLambdaParamHints = t.lspLambdaParamHints[:0]
	} else {
		t.lspVarTypes = nil
		t.lspLambdaParamHints = nil
	}
	t.needsStdImport = false
	t.needsFmtImport = false
	t.needsUtf8Import = false
	t.immutFields = make(map[string]bool)
	t.structImmutFields = make(map[string][]bool)
	t.activeTypeParams = make(map[string]bool)
	t.typeParamNames = nil
	t.structFields = make(map[string][]string)
	t.structFieldTypes = make(map[string]map[string]transpiler.Type)
	t.patternDefineTypes = nil
	t.genericMethods = make(map[string]map[string]bool)
	t.resultGenericMethods = make(map[string]bool)
	t.functions = richAST.Functions
	t.typeMetas = richAST.Types
	t.hasOpaque = anyOpaque(richAST.Types)
	t.companionObjects = richAST.CompanionObjects
	if t.companionObjects == nil {
		t.companionObjects = make(map[string]*transpiler.CompanionObjectMetadata)
	}
	t.importManager = NewImportManager()
	t.cachedTypeResolver = nil
	t.cachedTypeResolverRev = 0
	t.typeAliases = make(map[string]transpiler.Type)
	// Load type aliases from sibling files, and imported packages' under
	// their qualified names (extracted by analyzer)
	for name, underlyingType := range richAST.TypeAliases {
		t.typeAliases[name] = underlyingType
	}
	// t.functions, t.typeMetas, t.typeAliases and the import manager have all
	// just been replaced, so a cached function environment no longer matches.
	// The epoch is what covers the import manager too: its replacement starts
	// its revision back at zero, which a revision stamped before it could
	// match by accident.
	t.invalidateTypeEnv()
	t.goTypeInfo = richAST.GoTypeInfo
	t.tempVarCount = 0
	t.structMetas = make(map[string]*structMetaConfig)
	t.valueMetas = make(map[string]*valueMetaConfig)
	t.defaultTrees = nil
	t.richAST = richAST
	t.traceTypeResolution = os.Getenv("GALA_TRACE_TYPES") == "1"
	t.warnTypeInference = os.Getenv("GALA_WARN_TYPES") == "1"
	t.typeTraces = nil
	t.inferenceWarnings = nil
	t.unresolvedTypes = nil
	t.methodReceivers = nil
	t.unresolvedSeen = nil
	t.diagPackageNames = nil
	t.filePath = richAST.FilePath

	// Populate imports from richAST.Packages (includes implicit std import from analyzer)
	t.importManager.AddFromPackages(richAST.Packages)
	for path := range richAST.Packages {
		t.galaPkgPaths[path] = true
	}

	// Populate metadata from RichAST
	for typeName, meta := range richAST.Types {
		t.structFieldTypes[typeName] = meta.Fields
		t.structFields[typeName] = meta.FieldNames
		t.structImmutFields[typeName] = meta.ImmutFlags
		if _, ok := t.genericMethods[typeName]; !ok {
			t.genericMethods[typeName] = make(map[string]bool)
		}
		for methodName, methodMeta := range meta.Methods {
			if len(methodMeta.TypeParams) > 0 || methodMeta.IsGeneric {
				t.genericMethods[typeName][methodName] = true
			}
			if len(methodMeta.TypeParams) > 0 && typeMentionsTypeParam(methodMeta.ReturnType, methodMeta.TypeParams) {
				t.resultGenericMethods[methodName] = true
			}
		}
	}

	// Fail hard if GALA package analysis had unresolved imports.
	// Without resolved package metadata, type inference degrades to `any` and
	// the generated Go code will not compile.
	if len(richAST.AnalysisWarnings) > 0 {
		var msgs []string
		for _, w := range richAST.AnalysisWarnings {
			msgs = append(msgs, "  - "+w)
		}
		errLine, errCol := 1, 0
		if rootCtx, ok := any(tree).(antlr.ParserRuleContext); ok && rootCtx.GetStart() != nil {
			errLine = rootCtx.GetStart().GetLine()
			errCol = rootCtx.GetStart().GetColumn()
		}
		return nil, nil, galaerr.NewSemanticErrorAt(errLine, errCol,
			fmt.Sprintf("cannot transpile: %d imported package(s) could not be resolved:\n%s\n"+
				"Hint: ensure all GALA dependencies are available via --search paths or gala.mod",
				len(richAST.AnalysisWarnings), strings.Join(msgs, "\n")))
	}

	// Register EmbeddedFS method metadata (Go-defined type, not available from GALA analysis).
	// This enables type inference for ReadString/ReadBytes calls on embedded filesystems.
	t.registerEmbeddedFSMetadata()

	t.pushScope() // Global scope
	defer t.popScope()

	// Pre-register package-level val/var symbols from every file in this
	// package (this file plus its siblings, collected by the analyzer). This
	// lets a reference to a package-level `val` declared in another file unwrap
	// its std.Immutable[T] wrapper at the use site, exactly as a same-file
	// reference does. Same-file declarations refine these entries with their
	// precisely-inferred type when their own declaration is transformed below.
	for name, meta := range richAST.PackageVals {
		if meta != nil {
			t.registerPackageVal(name, meta)
		}
	}

	fset = token.NewFileSet()
	sourceFile, ok := any(tree).(*grammar.SourceFileContext)
	if !ok {
		errLine, errCol := 1, 0
		if rootCtx, ok := any(tree).(antlr.ParserRuleContext); ok && rootCtx.GetStart() != nil {
			errLine = rootCtx.GetStart().GetLine()
			errCol = rootCtx.GetStart().GetColumn()
		}
		return nil, nil, galaerr.NewSemanticErrorAt(errLine, errCol, fmt.Sprintf("expected *grammar.SourceFileContext, got %T", tree))
	}

	pkgName := sourceFile.PackageClause().(*grammar.PackageClauseContext).Identifier().GetText()
	t.packageName = pkgName
	file = &ast.File{
		// This file's own package doc, from the per-file table: the merged
		// RichAST.PackageDoc can come from a sibling file of the package.
		Doc:  t.docFor(sourceFile.PackageClause().GetStart()),
		Name: ast.NewIdent(pkgName),
	}

	// Imports
	for _, importCtx := range sourceFile.AllImportDeclaration() {
		decl, err := t.transformImportDeclaration(importCtx.(*grammar.ImportDeclarationContext))
		if err != nil {
			return nil, nil, err
		}
		file.Decls = append(file.Decls, decl)
	}

	// Update actual package names from richAST.Packages for better type resolution
	for path, actualPkgName := range richAST.Packages {
		t.importManager.UpdateActualPackageName(path, actualPkgName)
	}
	t.importManager.ClaimGalaPackageNames(t.galaPkgPaths)

	// Error on symbol clashes between dot-imported packages.
	// Use the first import declaration's position for error reporting.
	importLine, importCol := 1, 0
	if imports := sourceFile.AllImportDeclaration(); len(imports) > 0 {
		importLine = imports[0].GetStart().GetLine()
		importCol = imports[0].GetStart().GetColumn()
	}
	if err := t.importManager.ValidateDotImports(richAST, importLine, importCol); err != nil {
		return nil, nil, err
	}
	// Order matters here, though not for the revision check:
	// registerDotImportedVals only reads the import manager and writes the
	// current scope, so it cannot invalidate the snapshot below. It still
	// belongs first, because cacheTypeResolver snapshots name resolution
	// against the import set and is meant to be taken once every
	// import-derived setup step has run.
	t.registerDotImportedVals()
	t.cacheTypeResolver()

	t.variantNames = collectVariantNames(t.typeMetas)
	if err := t.checkVariantTypeNames(sourceFile); err != nil {
		return nil, nil, err
	}

	// t.typeAliases fills as declarations are walked, so a declaration above
	// `type Millis int64` would not see it. Record every alias target up front
	// for the lookups that must not depend on declaration order.
	t.fileTypeDeclTargets = make(map[string]transpiler.Type)
	for _, topDeclCtx := range sourceFile.AllTopLevelDeclaration() {
		typeDecl, ok := topDeclCtx.TypeDeclaration().(*grammar.TypeDeclarationContext)
		if !ok || typeDecl == nil || typeDecl.Identifier() == nil || typeDecl.TypeAlias() == nil {
			continue
		}
		t.fileTypeDeclTargets[typeDecl.Identifier().GetText()] = transpiler.ParseType(typeDecl.TypeAlias().GetText())
	}

	for _, topDeclCtx := range sourceFile.AllTopLevelDeclaration() {
		decls, err := t.transformTopLevelDeclaration(topDeclCtx)
		if err != nil {
			return nil, nil, err
		}
		if decls != nil {
			t.attachDocComments(topDeclCtx, decls)
			// Source-mapped `//line` directive: mark the declaration with its
			// originating GALA line so panics in top-level initializers report a
			// GALA position (see line_directives.go).
			if t.emitLineMarkers() && topDeclCtx.GetStart() != nil {
				marker := lineMarkerDecl(topDeclCtx.GetStart().GetLine())
				// The marker turns into the `//line` directive, which has to be
				// laid out around the first declaration's doc comment (see
				// transpiler.insertLineDirectives). The doc moves onto the
				// marker so the rewrite can find it next to the marker.
				doc := declDoc(decls[0])
				marker.Doc, *doc = *doc, nil
				file.Decls = append(file.Decls, marker)
			}
			file.Decls = append(file.Decls, decls...)
		}
	}

	// Method receivers are validated here, not as each method is transformed,
	// because t.typeAliases fills as declarations are walked: a method written
	// above its own alias would otherwise see an empty table and escape the
	// check. By this point every alias in the file is registered.
	if err := t.checkMethodReceivers(); err != nil {
		return nil, nil, err
	}

	// Every source `break` / `continue` must reach a source loop in its own
	// Go function; only the finished file shows which function each sits in.
	if err := t.checkLoopControl(file); err != nil {
		return nil, nil, err
	}

	// Whether the value of a match or if-expression lowered to a function
	// literal is used, and whether a `return` in it leaves only that
	// function, also shows only in the finished file.
	if err := t.checkBranchingCalls(file); err != nil {
		return nil, nil, err
	}

	// A converted Go call whose value is discarded goes back to the plain call.
	t.dropDiscardedGoResults(file)

	// Finalize codec/StructMeta declarations (generate Go AST for all collected intrinsics)
	if err := t.finalizeCodecs(file); err != nil {
		return nil, nil, err
	}

	if t.needsStdImport && t.packageName != registry.StdPackageName {
		// Check if std is already imported (e.g., as a dot import)
		stdAlreadyImported := t.importManager.IsDotImported(registry.StdPackageName)
		// ... or written in the source under its own name, `import
		// "martianoff/gala/std"`, which is emitted as written. Adding a
		// second one would redeclare `std`.
		if e, ok := t.importManager.GetByPath(registry.StdImportPath); ok &&
			!e.Implicit() && !e.IsDot && e.Alias == registry.StdPackageName {
			stdAlreadyImported = true
		}
		if !stdAlreadyImported {
			// Add import at the beginning
			importDecl := &ast.GenDecl{
				Tok: token.IMPORT,
				Specs: []ast.Spec{
					&ast.ImportSpec{
						Path: &ast.BasicLit{
							Kind:  token.STRING,
							Value: fmt.Sprintf("\"%s\"", registry.StdImportPath),
						},
					},
				},
			}
			file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
		}
	}

	if t.needsFmtImport {
		_, hasFmt := t.importManager.GetByPath("fmt")

		if !hasFmt {
			importDecl := &ast.GenDecl{
				Tok: token.IMPORT,
				Specs: []ast.Spec{
					&ast.ImportSpec{
						Path: &ast.BasicLit{
							Kind:  token.STRING,
							Value: "\"fmt\"",
						},
					},
				},
			}
			// If std was added, it's at index 0. We want fmt to be there too.
			file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
		}
	}

	if t.needsUtf8Import {
		if _, hasUtf8 := t.importManager.GetByPath("unicode/utf8"); !hasUtf8 {
			importDecl := &ast.GenDecl{
				Tok: token.IMPORT,
				Specs: []ast.Spec{
					&ast.ImportSpec{
						Path: &ast.BasicLit{
							Kind:  token.STRING,
							Value: "\"unicode/utf8\"",
						},
					},
				},
			}
			file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
		}
	}

	// Add transitive imports needed by type inference (e.g., when lambda parameter
	// types are inferred from a dependency's method signature and reference packages
	// not explicitly imported in the current file).
	t.importManager.AddTransitiveImportsToFile(file, richAST.ImportPathMap)

	// Add import "embed" when embed val declarations with EmbeddedFS type are present.
	// For string embeds, Go requires import _ "embed" (blank import).
	if t.needsEmbedImport {
		hasEmbedImport := false
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.IMPORT {
				continue
			}
			for _, spec := range genDecl.Specs {
				importSpec, ok := spec.(*ast.ImportSpec)
				if !ok {
					continue
				}
				if strings.Trim(importSpec.Path.Value, "\"") == "embed" {
					hasEmbedImport = true
					break
				}
			}
			if hasEmbedImport {
				break
			}
		}
		if !hasEmbedImport {
			// Determine if we need a blank import or a named import.
			// EmbeddedFS uses embed.FS directly, so we need the named import.
			// string/[]byte embeds only need _ "embed".
			hasEmbeddedFS := false
			for _, ed := range richAST.EmbedDirectives {
				if ed.TypeName == transpiler.TypeEmbeddedFS || ed.TypeName == "std.EmbeddedFS" {
					hasEmbeddedFS = true
					break
				}
			}
			spec := &ast.ImportSpec{
				Path: &ast.BasicLit{
					Kind:  token.STRING,
					Value: "\"embed\"",
				},
			}
			if !hasEmbeddedFS {
				// Blank import for string/[]byte embeds
				spec.Name = ast.NewIdent("_")
			}
			importDecl := &ast.GenDecl{
				Tok:   token.IMPORT,
				Specs: []ast.Spec{spec},
			}
			file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
		}
	}

	// Dump type resolution traces to stderr when tracing is enabled.
	if t.traceTypeResolution && len(t.typeTraces) > 0 {
		fmt.Fprintf(os.Stderr, "=== Type Resolution Trace (%s) ===\n", t.filePath)
		t.DumpTypeTrace(os.Stderr)
		fmt.Fprintf(os.Stderr, "=== End Trace (%d entries) ===\n", len(t.typeTraces))
	}

	// Dump type inference warnings if enabled
	if t.warnTypeInference && len(t.inferenceWarnings) > 0 {
		fmt.Fprintf(os.Stderr, "=== Type Inference Warnings (%s) ===\n", t.filePath)
		for _, w := range t.inferenceWarnings {
			fmt.Fprintf(os.Stderr, "  WARN: %s\n", w)
		}
		fmt.Fprintf(os.Stderr, "=== End Warnings (%d) ===\n", len(t.inferenceWarnings))
	}

	// Dump the unresolved-type inventory if enabled. See unresolved_types.go
	// for why every give-up is listed and not just the ones that went on to
	// produce a visible failure.
	if t.warnTypeInference && len(t.unresolvedTypes) > 0 {
		unresolved := dedupeUnresolved(t.unresolvedTypes)
		w := bufio.NewWriter(os.Stderr)
		fmt.Fprintf(w, "=== Unresolved Types (%s) ===\n", t.filePath)
		for _, u := range unresolved {
			fmt.Fprintf(w, "  UNRESOLVED %s:%d:%d %s\n", t.filePath, u.Line, u.Col, u.Expr)
		}
		fmt.Fprintf(w, "=== End Unresolved (%d) ===\n", len(unresolved))
		w.Flush()
	}

	// Remove unused imports from the generated AST. PruneUnused rewrites the
	// file without touching the import manager, so no cache is invalidated.
	t.importManager.PruneUnused(file, richAST)
	if err := CheckImportPaths(file); err != nil {
		return nil, nil, err
	}

	return fset, file, nil
}

// markDotImportUsed records that a symbol from a dot-imported package was referenced.
// Delegates to ImportManager.MarkDotImportUsed for unified tracking.
func (t *galaASTTransformer) markDotImportUsed(pkgName string) {
	t.importManager.MarkDotImportUsed(pkgName)
}

// trackPosition records the current ANTLR source position for use by deeply-nested
// helpers (e.g., isImmutableType) that may need to report errors but lack direct
// access to an ANTLR context. Callers that have a context should call this before
// invoking such helpers.
func (t *galaASTTransformer) trackPosition(ctx antlr.ParserRuleContext) {
	if ctx != nil && ctx.GetStart() != nil {
		t.lastLine = ctx.GetStart().GetLine()
		t.lastCol = ctx.GetStart().GetColumn()
	}
}

// raiseSemanticError panics with a SemanticError positioned at the last tracked
// source location. It is used by deeply-nested Type-returning helpers that cannot
// thread an error return through their signatures. The panic is caught by the
// recover() at the top of Transform and converted back to a normal error return.
//
// Do NOT add intermediate recover() calls in callers of this helper — they would
// swallow the error and leave the transformer in an inconsistent state.
func (t *galaASTTransformer) raiseSemanticError(msg string) {
	panic(galaerr.NewCodedSemanticError(galaerr.CodeRecursiveImmutable, t.lastLine, t.lastCol, msg, ""))
}

// semanticErrorAt creates a SemanticError with position info from an ANTLR context.
func (t *galaASTTransformer) semanticErrorAt(ctx antlr.ParserRuleContext, msg string) *galaerr.SemanticError {
	if ctx != nil && ctx.GetStart() != nil {
		line := ctx.GetStart().GetLine()
		col := ctx.GetStart().GetColumn()
		return galaerr.NewSemanticErrorInFile(t.filePath, line, col, msg)
	}
	// Fall back to last tracked position from enclosing transformation
	return galaerr.NewSemanticErrorAt(t.lastLine, t.lastCol, msg)
}

var _ transpiler.ASTTransformer = (*galaASTTransformer)(nil)

// resolveTypeName is a unified type resolution function that searches for a type name
// using a consistent resolution order. It takes a check function to determine if a
// candidate name exists in the target data structure.
//
// Resolution Order (documented and consistent):
//  1. Exact match
//  2. If name has package prefix: try replacing prefix with std/current/imported packages
//     (but NOT for external Go packages like "time", "fmt", etc.)
//  3. Try current package prefix
//  4. Try std package prefix
//  5. Try dot-imported packages
//  6. Try all explicitly imported packages (non-dot)
//
// Returns the resolved name and whether resolution succeeded.
func (t *galaASTTransformer) resolveTypeName(typeName string, exists func(string) bool) (string, bool) {
	// A type parameter of the enclosing generic declaration shadows every
	// name outside it — std's, an import's, the package's own — as in Go, so
	// a bare name it binds resolves to no declared type.
	if t.activeTypeParams[typeName] {
		return "", false
	}

	// 1. Try exact match first
	if exists(typeName) {
		return typeName, true
	}

	// 2. If typeName has a package prefix (e.g., "pkg.Type"), the qualifier is
	// authoritative — the user explicitly anchored the lookup to that package.
	// Resolve any alias on the qualifier and re-check; if still unresolved, do
	// NOT fall through to a simple-name search across other packages. Falling
	// back would let `session.Snapshot` (a struct in `session`) silently match
	// a same-named symbol in a dot-imported sibling (`gala_tui.Snapshot`
	// function), producing a misleading "unknown parameter" error at the call
	// site.
	if idx := strings.LastIndex(typeName, "."); idx != -1 {
		pkgPrefix := typeName[:idx]
		simpleName := typeName[idx+1:]

		// Resolve import alias to actual package name (e.g., "libalias" -> "lib",
		// "im" -> "collection_immutable") since types are registered under actual
		// package names, not user-chosen aliases.
		if actualPkg, ok := t.importManager.ResolveAlias(pkgPrefix); ok && actualPkg != pkgPrefix {
			resolvedName := actualPkg + "." + simpleName
			if exists(resolvedName) {
				return resolvedName, true
			}
		}

		// Qualifier was provided but no match in that package. Stop here
		// rather than allowing a cross-package shadow match.
		return "", false
	}

	// 3. Unqualified name — try resolving through all package prefixes
	// (current package, std, dot imports, named imports).
	if resolved, found := t.tryResolveSimpleName(typeName, exists); found {
		return resolved, true
	}

	return "", false
}

// tryResolveSimpleName attempts to resolve a simple (unqualified) type name
// by trying various package prefixes in order of precedence.
// Delegates to the shared resolver.TypeResolver for consistent resolution logic.
func (t *galaASTTransformer) tryResolveSimpleName(name string, exists func(string) bool) (string, bool) {
	// Validity is derived rather than announced: the snapshot records the
	// import manager's revision, and every mutation of the entry set moves
	// that revision. A site that adds, renames or drops an import therefore
	// cannot leave resolution reading a stale copy, whether or not it knows
	// this cache exists. Rebuilding here rather than only on a nil pointer
	// also puts the cache back, so an import added mid-walk costs one rebuild
	// instead of a fresh allocation on every later resolution in the file.
	if t.cachedTypeResolver == nil || t.cachedTypeResolverRev != t.importManager.Revision() {
		t.cacheTypeResolver()
	}
	return t.cachedTypeResolver.Resolve(name, exists)
}

// cacheTypeResolver snapshots the imports this file resolves names against,
// stamped with the revision they were taken at.
func (t *galaASTTransformer) cacheTypeResolver() {
	t.cachedTypeResolver = t.buildTypeResolver()
	t.cachedTypeResolverRev = t.importManager.Revision()
}

// buildTypeResolver creates a resolver.TypeResolver from the transformer's current state.
func (t *galaASTTransformer) buildTypeResolver() *resolver.TypeResolver {
	entries := t.importManager.All()
	imports := make([]resolver.PackageInfo, len(entries))
	for i, entry := range entries {
		imports[i] = resolver.PackageInfo{
			PkgName: entry.PkgName,
			IsDot:   entry.IsDot,
		}
	}
	return &resolver.TypeResolver{
		PackageName: t.packageName,
		Imports:     imports,
	}
}

// lookupTypeAlias resolves name to the type it aliases. typeAliases is keyed by
// simple name while callers often hold a package-qualified one, so a qualified
// miss retries on the bare half.
func (t *galaASTTransformer) lookupTypeAlias(name string) (transpiler.Type, bool) {
	if underlying, ok := t.typeAliases[name]; ok {
		return underlying, true
	}
	if dotIdx := strings.LastIndex(name, "."); dotIdx != -1 {
		bare := name[dotIdx+1:]
		// std's type is never the package's own alias that shadows its name.
		if name[:dotIdx] == registry.StdPackageName && t.packageDeclaresType(bare) {
			return transpiler.NilType{}, false
		}
		if underlying, ok := t.typeAliases[bare]; ok {
			return underlying, true
		}
	}
	return transpiler.NilType{}, false
}

// followAliasChain walks typ through successive alias declarations and returns
// the type the chain ends at. `type A int64; type B A` resolves B to int64,
// which is the type Go sees as the base of any receiver or literal naming B.
//
// The hop count bounds a chain that refers back to itself.
func (t *galaASTTransformer) followAliasChain(typ transpiler.Type) transpiler.Type {
	return t.walkAliasChain(typ, nil)
}

// walkAliasChain is followAliasChain stopping early at the first type stop
// accepts (a nil stop never does). The alias lookup runs first, so a type
// that names no alias costs one map lookup and never reaches stop.
func (t *galaASTTransformer) walkAliasChain(typ transpiler.Type, stop func(transpiler.Type) bool) transpiler.Type {
	for hop := 0; hop < len(t.typeAliases); hop++ {
		next, ok := t.aliasTarget(typ)
		if !ok || next.BaseName() == typ.BaseName() || stop != nil && stop(typ) {
			break
		}
		typ = next
	}
	return typ
}

// unaliased is typ followed to the end of its alias chain, and whether that
// reached a type that is not itself an alias. A chain that loops back on
// itself (malformed input) never does, so a caller recursing on the result
// cannot cycle.
func (t *galaASTTransformer) unaliased(typ transpiler.Type) (transpiler.Type, bool) {
	end := t.followAliasChain(typ)
	if end.BaseName() == typ.BaseName() {
		return typ, false
	}
	_, stillAlias := t.aliasTarget(end)
	return end, !stillAlias
}

// returnShape is the current result slot's type as the type an alias names,
// for readers that match its structure. returnSlot.typ keeps the alias
// spelling for what is emitted and reported.
func (t *galaASTTransformer) returnShape() transpiler.Type {
	return t.followAliasChain(t.returnSlot.typ)
}

// aliasTarget returns the type the alias typ names, one step down the chain,
// and false when typ does not name an alias or names a generic alias without
// its type arguments. A generic alias's target has the alias's type arguments
// substituted: `Res[int]` for `type Res[T any] Try[T]` is `Try[int]`.
//
// Exact keys only. lookupTypeAlias falls back to the bare half of a qualified
// name, which on a chain would let `geom.Point` continue through an unrelated
// local alias that happens to be called `Point`. This package's own aliases
// are keyed by bare name, so a name qualified with this package drops the
// qualifier.
func (t *galaASTTransformer) aliasTarget(typ transpiler.Type) (transpiler.Type, bool) {
	if typ == nil {
		return nil, false
	}
	key := typ.BaseName()
	if pkg := typ.GetPackage(); pkg != "" && pkg == t.packageName {
		key = strings.TrimPrefix(key, pkg+".")
	}
	return t.aliasTargetByKey(key, typ)
}

// aliasTargetByKey is aliasTarget for typ looked up under the typeAliases key
// key: a bare name for this package's aliases, `pkg.Name` for another's.
// Callers that resolve names in a package other than this one (the codec,
// reading an imported struct's fields) build the key themselves.
func (t *galaASTTransformer) aliasTargetByKey(key string, typ transpiler.Type) (transpiler.Type, bool) {
	next, ok := t.typeAliases[key]
	if !ok || next.IsNil() {
		return nil, false
	}
	meta := t.getTypeMeta(key)
	gen, isGeneric := typ.(transpiler.GenericType)
	if !isGeneric {
		if meta != nil && len(meta.TypeParams) > 0 {
			// Its target would hand the parameter names on as types.
			return nil, false
		}
		return next, true
	}
	if meta == nil || len(meta.TypeParams) != len(gen.Params) {
		// The target cannot be instantiated: returning it as declared would
		// hand its parameter names (`T`) on as if they were types.
		return nil, false
	}
	return t.substituteConcreteTypes(next, meta.TypeParams, gen.Params), true
}

// resolveStructTypeName resolves a type name to the key used in structFields/structImmutFields maps.
// Returns the original typeName if not found (for backward compatibility).
func (t *galaASTTransformer) resolveStructTypeName(typeName string) string {
	declares := func(name string) bool {
		_, ok := t.structFields[name]
		return ok
	}
	resolved, found := t.resolveTypeName(typeName, declares)
	if !found {
		resolved = typeName
	} else if len(t.structFields[resolved]) > 0 {
		return resolved
	}

	// Every declared type has a structFields entry, empty for a `type Coord
	// Point` alias, so the lookup above stops at the alias itself. Follow the
	// alias chain to reach the original's field list, which is what lets an
	// alias be constructed through. The walk starts from the name as written
	// because typeAliases is keyed by simple name.
	//
	// A chain longer than the alias map has revisited a name, so that length
	// bounds it.
	name := typeName
	for hop := 0; hop < len(t.typeAliases); hop++ {
		alias, ok := t.lookupTypeAlias(name)
		if !ok || alias.IsNil() {
			break
		}
		// BaseName drops the type arguments: `type Coords Point[int]` has to
		// look up `Point`, which is the name the field map is keyed by.
		next := alias.BaseName()
		// A primitive target has no fields and never will, so the resolver
		// sweep it would trigger can only miss.
		if next == "" || next == name || transpiler.IsPrimitiveType(next) {
			break
		}
		if r, ok := t.resolveTypeName(next, declares); ok && len(t.structFields[r]) > 0 {
			return r
		}
		name = next
	}

	return resolved
}

// isOwnGoType reports whether name is a bare type name declared by a
// hand-written .go file of the package being compiled.
func (t *galaASTTransformer) isOwnGoType(name string) bool {
	return t.richAST != nil && t.richAST.OwnGoTypes[name]
}

// packageDeclaresType reports whether the package being compiled declares a type
// named by the bare name, in a .gala file or a hand-written .go one. Such a
// name is the package's own type wherever it is written unqualified: a
// package-level declaration outranks every import, the implicit std import
// included, so a same-named std type is reachable only as `std.Name`.
func (t *galaASTTransformer) packageDeclaresType(name string) bool {
	if name == "" || strings.Contains(name, ".") {
		return false
	}
	if t.isOwnGoType(name) {
		return true
	}
	key := name
	if t.packageName != "" && t.packageName != "main" && t.packageName != "test" {
		key = t.packageName + "." + name
	}
	meta, ok := t.typeMetas[key]
	return ok && meta != nil && meta.Package == t.packageName
}

// resolveTypeMetaName resolves a type name to the key used in typeMetas map.
// Returns empty string if not found. A bare name the package's own .go files
// declare resolves to its `pkg.Name` key though typeMetas has no entry for it:
// the name is taken, so no other type — an import's, through the fallbacks
// of getTypeMeta — may answer for it.
func (t *galaASTTransformer) resolveTypeMetaName(typeName string) string {
	// A type parameter of the enclosing generic declaration is no declared
	// type, whatever else shares its name (see resolveTypeName).
	if t.activeTypeParams[typeName] {
		return ""
	}
	// A bare name the package's own hand-written .go files declare is that
	// type, whatever an import also exports under the name: Go resolves it in
	// the package scope first, and so must the transpiler. It has no GALA
	// metadata, so the key it resolves to names it without being in typeMetas;
	// the type is emitted unqualified, as every current-package type is.
	if t.isOwnGoType(typeName) {
		return t.packageName + "." + typeName
	}
	resolved, _ := t.resolveTypeName(typeName, func(name string) bool {
		_, ok := t.typeMetas[name]
		return ok
	})
	return resolved
}

// getTypeMeta resolves a type name and returns the corresponding TypeMetadata.
// This is the preferred method for accessing type metadata - it handles all
// resolution scenarios including package prefixes, std library fallback, and imports.
//
// Resolution precedence:
//  1. Exact match
//  2. Current package prefix (the package's own types shadow std's)
//  3. std package prefix (for standard library types)
//  4. Dot-imported packages
//  5. Explicitly imported packages
//
// Returns nil if the type is not found.
func (t *galaASTTransformer) getTypeMeta(typeName string) *transpiler.TypeMetadata {
	resolved := t.resolveTypeMetaName(typeName)
	if resolved == "" {
		// Fallback: check the primary RichAST directly in case metadata was added
		// after the initial copy (e.g., by sibling scanning or late analysis).
		if t.richAST != nil {
			resolved2, found := t.resolveTypeName(typeName, func(name string) bool {
				_, ok := t.richAST.Types[name]
				return ok
			})
			if found {
				return t.richAST.Types[resolved2]
			}
		}
		return nil
	}
	return t.typeMetas[resolved]
}

// userDefinedMethodFlags reports whether the user has explicitly declared
// Copy / Equal / Unapply methods on the given type. The transpiler auto-generates
// each of these methods on every struct (and sealed parent), but a user-supplied
// definition must take precedence — emitting both would produce a duplicate-method
// error in the generated Go.
func (t *galaASTTransformer) userDefinedMethodFlags(typeName string) (hasCopy, hasEqual, hasUnapply bool) {
	declared := t.declaresMethods(typeName, "Copy", "Equal", "Unapply")
	return declared["Copy"], declared["Equal"], declared["Unapply"]
}

// declaresMethods reports, keyed by method name, which of names the type
// declares itself — in GALA, or in a hand-written .go file of its package — so
// a method the transformer would synthesize is skipped instead of colliding.
func (t *galaASTTransformer) declaresMethods(typeName string, names ...string) map[string]bool {
	declared := make(map[string]bool, len(names))
	meta := t.getTypeMeta(typeName)
	if meta == nil {
		return declared
	}
	goMethods := t.goMethodsOnGalaType(meta)
	for _, name := range names {
		_, inGala := meta.Methods[name]
		_, inGo := goMethods[name]
		declared[name] = inGala || inGo
	}
	return declared
}

// getTypeMetaResolved returns the type metadata and the resolved (canonical) type name.
// Use this when you need both the metadata and the resolved name to avoid double resolution.
func (t *galaASTTransformer) getTypeMetaResolved(typeName string) (*transpiler.TypeMetadata, string) {
	resolved := t.resolveTypeMetaName(typeName)
	if resolved == "" {
		// Fallback: check the primary RichAST directly for late-added metadata.
		if t.richAST != nil {
			resolved2, found := t.resolveTypeName(typeName, func(name string) bool {
				_, ok := t.richAST.Types[name]
				return ok
			})
			if found {
				return t.richAST.Types[resolved2], resolved2
			}
		}
		return nil, ""
	}
	return t.typeMetas[resolved], resolved
}

// traceType records a type resolution event when tracing is enabled.
// This is a no-op when traceTypeResolution is false, so it is safe to
// call on every resolution path without measurable overhead.
func (t *galaASTTransformer) traceType(expr ast.Expr, result transpiler.Type, method string) {
	if !t.traceTypeResolution {
		return
	}
	line := 0
	// We don't have a Go token.FileSet for position mapping (the GALA source
	// positions come from ANTLR, not from Go AST nodes), so line stays 0 for
	// Go AST expressions. The file path is still useful for multi-file builds.
	t.typeTraces = append(t.typeTraces, TypeTraceEntry{
		ExprStr: formatExprForTrace(expr),
		Result:  result,
		Method:  method,
		File:    t.filePath,
		Line:    line,
	})
}

// registerEmbeddedFSMetadata adds type metadata for std.EmbeddedFS.
// EmbeddedFS is defined in Go (std/embedded_fs.go), so it's not discovered
// by the GALA analyzer. We register its method signatures manually so that
// type inference works for ReadString/ReadBytes calls.
func (t *galaASTTransformer) registerEmbeddedFSMetadata() {
	fullName := "std." + transpiler.TypeEmbeddedFS
	if _, exists := t.typeMetas[fullName]; exists {
		return // Already registered (e.g., from sibling analysis)
	}
	meta := &transpiler.TypeMetadata{
		Name:    transpiler.TypeEmbeddedFS,
		Package: "std",
		Methods: map[string]*transpiler.MethodMetadata{
			"ReadString": {
				Name:       "ReadString",
				Package:    "std",
				ParamTypes: []transpiler.Type{transpiler.BasicType{Name: "string"}},
				ReturnType: transpiler.GenericType{
					Base:   transpiler.NamedType{Package: "std", Name: "Try"},
					Params: []transpiler.Type{transpiler.BasicType{Name: "string"}},
				},
			},
		},
		Fields:     make(map[string]transpiler.Type),
		FieldNames: nil,
	}
	t.typeMetas[fullName] = meta
	// Also register without package prefix for dot-imported std
	if _, exists := t.typeMetas[transpiler.TypeEmbeddedFS]; !exists {
		t.typeMetas[transpiler.TypeEmbeddedFS] = meta
	}
	t.invalidateTypeEnv()
}

// DumpTypeTrace writes all recorded type resolution events to w.
// Each line has the format: [file:line] exprStr -> result (via method)
func (t *galaASTTransformer) DumpTypeTrace(w io.Writer) {
	for _, entry := range t.typeTraces {
		resultStr := "<nil>"
		if entry.Result != nil {
			resultStr = entry.Result.String()
			if resultStr == "" {
				resultStr = "<NilType>"
			}
		}
		fmt.Fprintf(w, "[%s:%d] %s -> %s (via %s)\n", entry.File, entry.Line, entry.ExprStr, resultStr, entry.Method)
	}
}

// formatExprForTrace produces a short human-readable representation of a Go AST expression.
func formatExprForTrace(expr ast.Expr) string {
	if expr == nil {
		return "<nil>"
	}
	// Use go/printer for a compact representation, falling back to type name.
	var buf strings.Builder
	fset := token.NewFileSet()
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return fmt.Sprintf("<%T>", expr)
	}
	s := buf.String()
	// Truncate very long expressions for readability.
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}
