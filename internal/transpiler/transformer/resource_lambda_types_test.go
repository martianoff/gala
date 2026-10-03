package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resSibling declares, in a hand-written .go file, a resource type and its
// constructor.
const resSibling = `package main

type Res struct{ name string }

func (r Res) Close() error { return nil }
func (r Res) Name() string { return r.name }

func OpenRes(p string) Res { return Res{name: p} }
`

// TestPartialExplicitTypeArgs covers a generic call that writes only its
// leading type arguments (#618). Go infers the rest; the transpiler must too,
// rather than lowering the lambda against the bare type-parameter name (it
// emitted `func(r Res) A`, and `undefined: A` from go build).
func TestPartialExplicitTypeArgs(t *testing.T) {
	cases := []struct {
		name   string
		goSrc  string
		gala   string
		want   []string
		absent []string
	}{
		{
			name:  "resource.Using over a Go-declared resource",
			goSrc: resSibling,
			gala: "import \"martianoff/gala/resource\"\n\n" +
				"func nameLen() int = resource.Using[Res](OpenRes(\"x\"), (r) => r.Name().Size())\n",
			want:   []string{"resource.Using[Res](", "func(r Res) int", "utf8.RuneCountInString(r.Name())"},
			absent: []string{") A {", "r any"},
		},
		{
			name:  "resource.Using over a GALA-declared resource",
			goSrc: "package main\n",
			gala: "import \"martianoff/gala/resource\"\n\n" +
				"struct Res(Name string)\n\n" +
				"func (r Res) Close() error = nil\n\n" +
				"func nameLen() int = resource.Using[Res](Res(\"x\"), (r) => r.Name.Size())\n",
			want:   []string{"func(r Res) int"},
			absent: []string{") A {", "r any"},
		},
		{
			name:  "a GALA generic function",
			goSrc: "package main\n",
			gala: "func pick[A any, B any](a A, f func(A) B) B = f(a)\n\n" +
				"func twice() int = pick[int](1, (x) => x * 2)\n",
			want: []string{"pick[int](1, func(x int) int"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, galaFile := samePackageModule(".", tc.goSrc, "package main\n\n"+tc.gala)
			out, err := transpileInModule(t, files, galaFile)
			require.NoError(t, err)
			for _, w := range tc.want {
				assert.Contains(t, out, w)
			}
			for _, a := range tc.absent {
				assert.NotContains(t, out, a)
			}
		})
	}
}

// TestLambdaParamWithUnknownSlotIsAnError covers a call-argument lambda whose
// callee's signature is unknown, so its parameter has no expected type at all.
// It used to be lowered to `any` — `func(r any) any`, which go build rejects
// against the real signature — and is GALA-E0033 instead (#618). Here the
// resource's constructor is in a .go file a build constraint excludes, so the
// transpiler knows nothing of it.
func TestLambdaParamWithUnknownSlotIsAnError(t *testing.T) {
	files, galaFile := samePackageModule(".",
		"//go:build ignore\n\n"+resSibling,
		"package main\n\nimport \"martianoff/gala/resource\"\n\n"+
			"func nameLen() int = resource.Using(OpenRes(\"x\"), (r) => r.Name().Size())\n")
	out, err := transpileInModule(t, files, galaFile)
	require.Error(t, err, "generated:\n%s", out)
	assert.Contains(t, err.Error(), "GALA-E0033")
	assert.Contains(t, err.Error(), `lambda parameter "r"`)
}

// TestLambdaParamSlotTypedAny covers a call-argument lambda whose slot the
// callee types `any`: it is lowered as declared, not rejected.
func TestLambdaParamSlotTypedAny(t *testing.T) {
	files, galaFile := samePackageModule(".", "package main\n\nfunc Register(h any) {}\n",
		"package main\n\nfunc reg() = Register((a, b) => a)\n")
	out, err := transpileInModule(t, files, galaFile)
	require.NoError(t, err)
	assert.Contains(t, out, "func(a any, b any)")
}

// TestLambdaArityMismatchNamesIt covers a lambda with more parameters than
// the function type it stands for: the diagnostic names the mismatch rather
// than asking for an annotation that would not help.
func TestLambdaArityMismatchNamesIt(t *testing.T) {
	files, galaFile := samePackageModule(".", "package main\n\nfunc RunWith(f func(int)) {}\n",
		"package main\n\nfunc run() = RunWith((x, y) => Println(x))\n")
	_, err := transpileInModule(t, files, galaFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0033")
	assert.Contains(t, err.Error(), "takes 2 parameters where a function of 1 is expected")
}

// TestLambdaArgOfLocalFuncBinding covers a lambda passed to a local binding of
// function type whose parameter types name the enclosing declaration's type
// parameter: it is typed by them (`func(v T)`), not left without a type.
func TestLambdaArgOfLocalFuncBinding(t *testing.T) {
	files, galaFile := samePackageModule(".", "package main\n",
		"package main\n\nfunc each[T any](forEach func(func(T)), g func(T)) {\n    forEach((v) => g(v))\n}\n")
	out, err := transpileInModule(t, files, galaFile)
	require.NoError(t, err)
	assert.Contains(t, out, "forEach(func(v T) {")

	// A binding of a generic alias with type arguments is typed by the
	// alias's signature instantiated with them, never the declared `func(T)`.
	files[galaFile] = "package main\n\ntype Visitor[T any] func(func(T))\n\n" +
		"func run(visit Visitor[int]) = visit((v) => Println(v))\n"
	out, err = transpileInModule(t, files, galaFile)
	require.NoError(t, err)
	assert.Contains(t, out, "visit(func(v int) {")
	assert.NotContains(t, out, "func(v T)")
}

// TestConversionToGoNamedFuncType covers a conversion to an imported Go
// named function type: the lambda is typed by its signature.
func TestConversionToGoNamedFuncType(t *testing.T) {
	files, galaFile := samePackageModule(".", "package main\n",
		"package main\n\nimport \"net/http\"\n\n"+
			"func h() http.Handler = http.HandlerFunc((w, r) => w.WriteHeader(204))\n")
	out, err := transpileInModule(t, files, galaFile)
	require.NoError(t, err)
	assert.Contains(t, out, "func(w http.ResponseWriter, r *http.Request)")
}

// TestConversionToNamedFuncTypeTypesTheLambda covers `Handler((x) => x)` for
// `type Handler func(int) int`: the conversion's one argument has the
// function type, so the lambda is typed by it rather than left without a type.
func TestConversionToNamedFuncTypeTypesTheLambda(t *testing.T) {
	files, galaFile := samePackageModule(".", "package main\n",
		"package main\n\ntype Handler func(int) int\n\n"+
			"func h() Handler = Handler((x) => x + 1)\n")
	out, err := transpileInModule(t, files, galaFile)
	require.NoError(t, err)
	assert.Contains(t, out, "func(x int) int")
}
