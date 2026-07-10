package transformer_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// TestCrossModuleMatrix is the A3 fixture: a 4 (dep kind) × 3 (consumer
// kind) integration matrix that exercises the resolver / analyzer /
// transformer pipeline across the cross-module shapes that drove ~13 of
// the last 50 PRs (#208, #228, #229, #230, #234, #237, #239, #242,
// #244, #256, #258, plus #232 / #235 / #236 for sealed-case lowering).
//
// Each cell builds a hermetic temp-dir workspace with a single dep
// package and a sibling consumer package, then transpiles the consumer
// and asserts:
//   - transpilation succeeds (no panic, no resolver error, no inference
//     failure that the matrix' inputs should not provoke);
//   - the generated Go contains the expected symbol references
//     (qualified/dot/aliased per the consumer kind).
//
// The dimensions:
//
//	depKind        — what the dependency package looks like
//	  puregala       types + functions, no Go interop
//	  genericsealed  sealed type with one type param and a constructor
//	  funconly       functions only, no types (the B5 phantom-reexport
//	                 family lived here)
//	  mixedgo        GALA file + hand-written Go facade re-exporting
//	                 symbols via `var X = other.X`
//
//	consumerKind   — how the consumer references the dep
//	  dotimport      `import . "..."` ; uses bare names
//	  qualified      `import "..."`   ; uses `pkg.Name`
//	  aliased        `import alias "..."` ; uses `alias.Name`
//
// Total 12 cells. Cells that the current pipeline does not yet support
// document why and call t.Skip with a pointer at the relevant audit
// item (so they remain visible in the matrix instead of silently
// disappearing).
func TestCrossModuleMatrix(t *testing.T) {
	for _, dep := range crossModDepKinds() {
		dep := dep
		for _, cons := range crossModConsumerKinds() {
			cons := cons
			t.Run(dep.name+"/"+cons.name, func(t *testing.T) {
				if reason := skipReason(dep.name, cons.name); reason != "" {
					t.Skip(reason)
				}
				runCrossModuleCell(t, dep, cons)
			})
		}
	}
}

// runCrossModuleCell builds a fresh temp-dir workspace, writes the dep
// and consumer files, transpiles the consumer, and asserts on the
// output.
func runCrossModuleCell(t *testing.T, dep crossModDep, cons crossModConsumer) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "cross_module_matrix_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	const modulePath = "github.com/example/xmod"
	require.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "go.mod"),
		[]byte("module "+modulePath+"\n\ngo 1.22\n"),
		0644,
	))

	// Dep lives at <tempDir>/<dep.name>/.
	depDir := filepath.Join(tempDir, dep.name)
	require.NoError(t, os.MkdirAll(depDir, 0755))
	for fname, content := range dep.files {
		require.NoError(t, os.WriteFile(filepath.Join(depDir, fname), []byte(content), 0644))
	}

	// Consumer lives at <tempDir>/consumer/.
	consumerDir := filepath.Join(tempDir, "consumer")
	require.NoError(t, os.MkdirAll(consumerDir, 0755))
	consumerFile := filepath.Join(consumerDir, "consumer.gala")
	consumerSource := buildConsumerSource(modulePath, dep, cons)
	require.NoError(t, os.WriteFile(consumerFile, []byte(consumerSource), 0644))

	// Run the transpiler from the workspace root so the resolver finds
	// the dep on its search path.
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)
	require.NoError(t, os.Chdir(tempDir))

	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, nil)
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	out, err := trans.Transpile(consumerSource, consumerFile)
	require.NoError(t, err,
		"dep %q × consumer %q must transpile cleanly\n--- consumer source ---\n%s",
		dep.name, cons.name, consumerSource)

	for _, want := range cons.expectContains(dep) {
		require.True(t, strings.Contains(out, want),
			"dep %q × consumer %q: generated Go missing %q\n--- generated ---\n%s",
			dep.name, cons.name, want, out)
	}
}

// skipReason returns a non-empty string when the (dep, consumer) cell
// should be skipped, with a one-line rationale linking back to the
// audit item that tracks the limitation. Empty string = run the cell.
func skipReason(dep, cons string) string {
	// Currently no skips — every cell of the 4×3 matrix is expected to
	// pass against the post-#261 / #266 pipeline. Add entries here when
	// a new dep/consumer combination is intentionally deferred.
	return ""
}

// ────────────────────────────────────────────────────────────────────
// Dependency shapes
// ────────────────────────────────────────────────────────────────────

type crossModDep struct {
	name  string
	files map[string]string
	// exported names the consumer can reference, with their kind.
	// Used by the consumer-builder to emit valid uses.
	exportedFunc string // name of an exported zero-arg function returning Int
	exportedType string // name of an exported type (struct or sealed) — empty if none
	// constructorCall produces a GALA expression that builds a value of
	// exportedType using a bare name. The consumer-builder qualifies it.
	constructorCall func(qualifier string) string
}

func crossModDepKinds() []crossModDep {
	return []crossModDep{
		// 1) Pure-GALA dep: type + function, no Go interop.
		{
			name: "puregala",
			files: map[string]string{
				"dep.gala": `package puregala

type Box struct {
    Value Int
}

func MakeBox(v Int) Box = Box { Value: v }

func answer() Int = 42
`,
			},
			exportedFunc:    "answer",
			exportedType:    "Box",
			constructorCall: func(q string) string { return q + "MakeBox(7)" },
		},
		// 2) Generic sealed type with a constructor — the family that
		//    drove PR #232/#235/#257 (downward inference for sealed-case
		//    type args via cross-module references).
		{
			name: "genericsealed",
			files: map[string]string{
				"dep.gala": `package genericsealed

sealed type Cmd[T any] {
    case NoCmd()
    case RunCmd(arg T)
}

func answer() Int = 42
`,
			},
			exportedFunc:    "answer",
			exportedType:    "Cmd",
			constructorCall: func(q string) string { return q + "NoCmd[Int]()" },
		},
		// 3) Function-only dep — the B5 phantom-reexport family. No
		//    Types map entry, just functions.
		{
			name: "funconly",
			files: map[string]string{
				"dep.gala": `package funconly

func helper() Int = 11

func answer() Int = 42
`,
			},
			exportedFunc:    "answer",
			exportedType:    "",
			constructorCall: nil,
		},
		// 4) Mixed GALA + Go facade — covers the dot-import-detection
		//    family (PR #215) and the var-re-export shape that GAlA
		//    needs to recognize during cross-module analysis.
		{
			name: "mixedgo",
			files: map[string]string{
				"dep.gala": `package mixedgo

func answer() Int = 42
`,
				"facade.go": `package mixedgo

func Helper() int { return 11 }
`,
			},
			exportedFunc:    "answer",
			exportedType:    "",
			constructorCall: nil,
		},
	}
}

// ────────────────────────────────────────────────────────────────────
// Consumer shapes
// ────────────────────────────────────────────────────────────────────

type crossModConsumer struct {
	name string
	// importBlock returns the GALA import block lines for the dep at
	// `modulePath/depName`. Different consumer shapes produce dot,
	// qualified, or aliased imports.
	importBlock func(modulePath, depName string) string
	// qualifier returns the prefix used to reference dep symbols
	// ("pkg." for qualified, "alias." for aliased, "" for dot-import).
	qualifier func(depName string) string
	// expectContains returns substrings that must appear in the
	// generated Go for a successful cell. Allows the matrix to assert
	// the right shape of qualification per consumer kind.
	expectContains func(dep crossModDep) []string
}

func crossModConsumerKinds() []crossModConsumer {
	return []crossModConsumer{
		{
			name: "dotimport",
			importBlock: func(modulePath, depName string) string {
				return fmt.Sprintf("import . %q", modulePath+"/"+depName)
			},
			qualifier: func(_ string) string { return "" },
			expectContains: func(dep crossModDep) []string {
				// Dot-import: bare name in generated Go too.
				return []string{dep.exportedFunc + "()"}
			},
		},
		{
			name: "qualified",
			importBlock: func(modulePath, depName string) string {
				return fmt.Sprintf("import %q", modulePath+"/"+depName)
			},
			qualifier: func(depName string) string { return depName + "." },
			expectContains: func(dep crossModDep) []string {
				return []string{dep.name + "." + dep.exportedFunc + "()"}
			},
		},
		{
			name: "aliased",
			importBlock: func(modulePath, depName string) string {
				alias := depName + "alias"
				return fmt.Sprintf("import %s %q", alias, modulePath+"/"+depName)
			},
			qualifier: func(depName string) string { return depName + "alias." },
			expectContains: func(dep crossModDep) []string {
				return []string{dep.name + "alias." + dep.exportedFunc + "()"}
			},
		},
	}
}

// buildConsumerSource emits a minimal consumer.gala that imports the
// dep per the consumer shape and references the dep's exported function
// (and exported type, if any). The resulting source is valid GALA and
// should transpile cleanly against the dep.
func buildConsumerSource(modulePath string, dep crossModDep, cons crossModConsumer) string {
	q := cons.qualifier(dep.name)
	importLine := cons.importBlock(modulePath, dep.name)

	var b strings.Builder
	b.WriteString("package consumer\n\n")
	b.WriteString(importLine)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "func callDep() Int = %s%s()\n", q, dep.exportedFunc)

	// If the dep exports a type with a constructor, exercise that too.
	if dep.constructorCall != nil {
		fmt.Fprintf(&b, "\nfunc useType() %s%s = %s\n",
			q, dep.exportedType, dep.constructorCall(q))
	}

	b.WriteString("\nfunc main() { Println(callDep()) }\n")
	return b.String()
}
