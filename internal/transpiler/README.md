# GALA Transpiler Architecture

This document describes the architecture of the GALA-to-Go transpiler for internal developers and LLM assistance.

## Pipeline Overview

The transpiler follows a classic 4-stage pipeline:

```
GALA Source Code
       ↓
   [Parser]      → ANTLR Parse Tree
       ↓
   [Analyzer]    → RichAST (enriched with type/function metadata)
       ↓
  [Transformer]  → Go AST
       ↓
  [Generator]    → Go Source Code
```

## Component Responsibilities

### 1. Parser (`parser.go`)

**Interface:** `GalaParser`

Thin wrapper around the ANTLR-generated parser. Converts GALA source text into an ANTLR parse tree.

**Key files:**
- `parser.go` - Adapter wrapping ANTLR parser

**Note:** Never modify `internal/parser/grammar/*.go` - these are ANTLR-generated.

### 2. Analyzer (`analyzer/`)

**Interface:** `Analyzer`

Performs semantic analysis on the parse tree to produce a `RichAST` containing:
- Type metadata (fields, methods, type parameters)
- Function metadata (parameters, return types)
- Package imports and aliases
- Companion object metadata (for pattern matching)

**Key files:**
- `analyzer/analyzer.go` - Main analysis logic

**Key behaviors:**
- Caches analyzed packages to prevent re-analysis
- Automatically loads prelude packages (std)
- Detects naming conflicts with prelude exports
- Resolves cross-package type references

### 3. Transformer (`transformer/`)

**Interface:** `ASTTransformer`

The largest component. Transforms the enriched GALA AST into a Go AST.

**Key files:**
- `transformer.go` - Entry point and orchestration
- `expressions.go` - Expression transformation (largest file)
- `types.go` - Type transformation and inference
- `match.go` - Pattern matching compilation
- `declarations.go` - Top-level declarations
- `statements.go` - Statement transformation
- `methods.go` - Method handling
- `scope.go` - Variable scope management
- `bridge.go` - Hindley-Milner ↔ transpiler.Type conversion

**Key responsibilities:**
- Type inference (dual-layer: manual + Hindley-Milner fallback)
- Immutable variable handling (auto-wrap/unwrap)
- Generic method transformation
- Pattern matching compilation
- Import alias management

### 4. Generator (`generator/`)

**Interface:** `CodeGenerator`

Minimal component that formats Go AST into Go source code using `go/format`.

**Key files:**
- `generator/generator.go` - Go code formatting

## Type System

Defined in `types.go`:

| Type | Description |
|------|-------------|
| `BasicType` | Primitives: int, string, bool |
| `NamedType` | Package-qualified: std.Option |
| `GenericType` | Parameterized: Immutable[int] |
| `ArrayType` | Slices: []T |
| `MapType` | Maps: map[K]V |
| `PointerType` | Pointers: *T |
| `FuncType` | Functions: func(A) B |
| `NilType` | Unknown/unresolved |
| `VoidType` | Side-effect only |

## Type Inference

Two-layer system:

1. **Manual inference** (`getExprTypeNameManual`) - Fast pattern-based inference for common cases
2. **Hindley-Milner fallback** (`inferExprType`) - Complex inference using unification

See `docs/TYPE_INFERENCE.md` for detailed rules.

## Key Data Structures

### RichAST

```go
type RichAST struct {
    Tree             antlr.Tree
    PackageName      string
    Types            map[string]*TypeMetadata
    Functions        map[string]*FunctionMetadata
    Packages         map[string]string  // importPath -> pkgName
    CompanionObjects map[string]*CompanionObjectMetadata
}
```

### TypeMetadata

```go
type TypeMetadata struct {
    Name       string
    Package    string
    Methods    map[string]*MethodMetadata
    Fields     map[string]Type
    FieldNames []string  // Preserve order
    TypeParams []string
    ImmutFlags []bool    // Per-field immutability
}
```

## Interfaces

All components implement well-defined interfaces for testability:

```go
type GalaParser interface {
    Parse(input string) (antlr.Tree, error)
}

type Analyzer interface {
    Analyze(tree antlr.Tree, filePath string) (*RichAST, error)
}

type ASTTransformer interface {
    Transform(richAST *RichAST) (*token.FileSet, *ast.File, error)
}

type CodeGenerator interface {
    Generate(fset *token.FileSet, file *ast.File) (string, error)
}
```

## Build Commands

```bash
# Build everything
bazel build //...

# Run all tests
bazel test //...

# Regenerate BUILD files after adding/removing files
bazel run //:gazelle
```

## Testing Strategy

- Unit tests in each package (`*_test.go`)
- Integration tests via examples in `examples/`
- Each example has expected `.out` file for verification

## Known Areas for Improvement

See the refactoring plan in `C:\Users\maxmr\.claude\plans\drifting-weaving-book.md` for detailed improvement areas including:

1. Module resolution consolidation
2. Package registry system (replacing hardcoded std)
3. Import manager unification
4. Type resolver consolidation
5. Large file splits
