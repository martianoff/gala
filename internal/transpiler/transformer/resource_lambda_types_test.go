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

// TestLambdaArgOfFuncValuedCallee covers a lambda passed to a callee that is a
// value of function type other than a parameter, a val read through its
// `.Get()` unwrap included: the lambda is typed by the value's function type,
// whatever holds it.
func TestLambdaArgOfFuncValuedCallee(t *testing.T) {
	cases := []struct {
		name, gala, want string
		lib              string // an imported package lib, when the case needs one
		goSrc            string // the package's own .go file, when the case needs one
	}{
		{
			name: "a val bound to a lambda",
			gala: "func run() string {\n    val apply = (h func(string) string) => h(\"x\")\n    apply((s) => s + \"!\")\n}\n",
			want: "apply.Get()(func(s string) string {",
		},
		{
			name: "a val with a declared function type",
			gala: "func run() string {\n    val apply func(func(string) string) string = (h) => h(\"x\")\n    apply((s) => s + \"!\")\n}\n",
			want: "apply.Get()(func(s string) string {",
		},
		{
			name: "a placeholder lambda",
			gala: "func run() string {\n    val apply = (h func(string) string) => h(\"x\")\n    apply(_ + \"!\")\n}\n",
			want: "apply.Get()(func(__p0 string) string {",
		},
		{
			name: "a val shadowing a package function of the same name",
			gala: "func apply(n int) int = n + 1\n\n" +
				"func run() string {\n    val apply = (h func(string) string) => h(\"x\")\n    apply((s) => s + \"!\")\n}\n",
			want: "apply.Get()(func(s string) string {",
		},
		{
			name: "a val typed by a generic alias",
			gala: "type Visitor[T any] func(func(T))\n\n" +
				"func run(v Visitor[int]) {\n    val visit Visitor[int] = v\n    visit((n) => Println(n + 1))\n}\n",
			want: "visit.Get()(func(n int) {",
		},
		{
			name: "a generic call's result over the enclosing declaration's same-named type parameter",
			gala: "func wrap[T any](x T) func(func(T) string) string = (h) => h(x)\n\n" +
				"func run[T any](x T) string = wrap(x)((v) => \"a\")\n",
			want: "(func(v T) string {",
		},
		{
			name:  "a parameter named like a Go function of the package",
			gala:  "func run(apply func(func(string) string) string) string = apply((s) => s + \"!\")\n",
			want:  "apply(func(s string) string {",
			goSrc: "package main\n\nfunc apply(h func(int) int) int { return h(1) }\n",
		},
		{
			name:  "a parameter typed by a Go named function type of the package",
			gala:  "func run(v Visitor) = v((n) => Println(n + 1))\n",
			want:  "v(func(n int) {",
			goSrc: "package main\n\ntype Visitor func(func(int))\n",
		},
		{
			name:  "a Go named function type over a Go type named like a type parameter",
			gala:  "func run(v Visitor) = v((x) => Println(x))\n",
			want:  "v(func(x T) {",
			goSrc: "package main\n\ntype T struct{ N int }\n\ntype Visitor func(func(T))\n",
		},
		{
			name: "a var bound to a lambda",
			gala: "func run() string {\n    var apply = (h func(string) string) => h(\"x\")\n    apply((s) => s + \"!\")\n}\n",
			want: "apply(func(s string) string {",
		},
		{
			name: "a val holding a function a call returned",
			gala: "func mk() func(func(int) int) int = (h) => h(2)\n\n" +
				"func run() int {\n    val apply = mk()\n    apply((n) => n * 10)\n}\n",
			want: "apply.Get()(func(n int) int {",
		},
		{
			name: "the result of a call, called directly",
			gala: "func mk() func(func(int) int) int = (h) => h(2)\n\n" +
				"func run() int = mk()((n) => n * 10)\n",
			want: "mk()(func(n int) int {",
		},
		{
			name: "a package-level val",
			gala: "val top = (h func(string) string) => h(\"x\")\n\n" +
				"func run() string = top((s) => s + \"!\")\n",
			want: "top.Get()(func(s string) string {",
		},
		{
			name: "a val over the enclosing declaration's type parameter",
			gala: "func run[T any](x T, g func(T) string) string {\n" +
				"    val apply = (h func(T) string) => h(x)\n    apply((v) => g(v))\n}\n",
			want: "apply.Get()(func(v T) string {",
		},
		{
			name: "a parenthesized val",
			gala: "func run() string {\n    val apply = (h func(string) string) => h(\"x\")\n    val r = (apply)((s) => s + \"!\")\n    r\n}\n",
			want: "(apply.Get())(func(s string) string {",
		},
		{
			name: "an imported package-level val",
			gala: "import \"example.com/sibs/lib\"\n\nfunc run() string = lib.Shout((s) => s + \"!\")\n",
			want: "lib.Shout.Get()(func(s string) string {",
			lib:  "package lib\n\nval Shout func(func(string) string) string = (h) => h(\"x\")\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goSrc := tc.goSrc
			if goSrc == "" {
				goSrc = "package main\n"
			}
			files, galaFile := samePackageModule(".", goSrc, "package main\n\n"+tc.gala)
			if tc.lib != "" {
				files["lib/lib.gala"] = tc.lib
			}
			out, err := transpileInModule(t, files, galaFile)
			require.NoError(t, err)
			assert.Contains(t, out, tc.want)
		})
	}

	// The result of a generic function or method whose type arguments the
	// call leaves undetermined still names its own type parameter, however
	// deep the call; the lambda is not lowered against it (`func(s B)`,
	// undefined in the caller). A free function's type parameter only its
	// result mentions is reported at the call that leaves it open
	// (GALA-E0067), before the lambda is reached; a method's, at the lambda.
	for _, tc := range []struct{ src, code string }{
		{"func mk[A any, B any](a A) func(func(B) A) A = (h) => a\n\n" +
			"func run() int = mk(1)((s) => 2)\n", "GALA-E0067"},
		{"func mk[A any, B any](a A) func(int) func(func(B) A) A = (n) => (h) => a\n\n" +
			"func run() int = mk(1)(2)((s) => 2)\n", "GALA-E0067"},
		{"struct Box(N int)\n\nfunc (b Box) Maker[U any]() func(func(U) int) int = (h) => b.N\n\n" +
			"func run() int = Box(1).Maker()((s) => 2)\n", "GALA-E0033"},
		// The caller declares a type named like the callee's type parameter.
		{"struct B(X int)\n\nfunc mk[A any, B any](a A) func(func(B) A) A = (h) => a\n\n" +
			"func run() int = mk(1)((s) => 2)\n", "GALA-E0067"},
	} {
		files, galaFile := samePackageModule(".", "package main\n", "package main\n\n"+tc.src)
		_, err := transpileInModule(t, files, galaFile)
		require.Error(t, err, tc.src)
		assert.Contains(t, err.Error(), tc.code, tc.src)
	}

	// A declared default is lowered at its use site. A top-level val the
	// default calls types its lambda there. A use-site local of that name was
	// not in scope where the default was written: the lowered default would
	// read the local, so its lambda is refused (GALA-E0033) rather than typed
	// against the local.
	const defaultDecls = "package main\n\nval apply = (h func(string) string) => h(\"x\")\n\n" +
		"func greet(msg string = apply((s) => s + \"!\")) string = msg\n\n"
	files, galaFile := samePackageModule(".", "package main\n",
		defaultDecls+"func run() string = greet()\n")
	out, err := transpileInModule(t, files, galaFile)
	require.NoError(t, err)
	assert.Contains(t, out, "greet(apply.Get()(func(s string) string {")

	files, galaFile = samePackageModule(".", "package main\n",
		defaultDecls+"func run() string {\n    val apply = (h func(int) int) => h(1)\n    Println(apply((n) => n))\n    greet()\n}\n")
	out, err = transpileInModule(t, files, galaFile)
	assert.NotContains(t, out, "func(s int)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0033")
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

// TestGoNamedFuncTypeSlots covers a slot typed by a Go named function type —
// imported (`fs.WalkDirFunc`, `http.HandlerFunc`, `context.CancelFunc`) or of
// the package's own .go files (`Visitor`, `Stop`) — beyond calling a value of
// that type: a GALA function's parameter, a val annotation, a result, and a
// struct field. A lambda there takes the type's underlying signature, never
// GALA-E0033; a value of the type, or nil, is passed as it is, not as a thunk;
// and a call of such a value has the type's result.
func TestGoNamedFuncTypeSlots(t *testing.T) {
	const goSrc = "package main\n\ntype Visitor func(int) bool\n\ntype Stop func()\n\ntype Provider func() string\n\ntype Mapper[T any] func(T) T\n"
	cases := []struct {
		name string
		gala string
		want string
	}{
		{
			name: "a GALA function's parameter",
			gala: "func check(v Visitor) bool = v(3)\n\nfunc run() bool = check((n) => n > 2)\n",
			want: "check(func(n int) bool {",
		},
		{
			name: "a val annotation",
			gala: "import \"io/fs\"\n\nfunc run() error {\n    val f fs.WalkDirFunc = (path, d, err) => err\n    f(\".\", nil, nil)\n}\n",
			want: "func(path string, d fs.DirEntry, err error) error {",
		},
		{
			name: "a result",
			gala: "import \"io/fs\"\n\nfunc run() fs.WalkDirFunc = (path, d, err) => err\n",
			want: "func(path string, d fs.DirEntry, err error) error {",
		},
		{
			name: "a struct field",
			gala: "import \"io/fs\"\n\nstruct Walker(Fn fs.WalkDirFunc)\n\nfunc run() Walker = Walker(Fn = (path, d, err) => err)\n",
			want: "NewImmutable[fs.WalkDirFunc](func(path string, d fs.DirEntry, err error) error {",
		},
		{
			name: "a struct field given a declared function",
			gala: "import \"io/fs\"\n\nstruct Walker(Fn fs.WalkDirFunc)\n\n" +
				"func keep(path string, d fs.DirEntry, err error) error = err\n\nfunc run() Walker = Walker(Fn = keep)\n",
			want: "NewImmutable[fs.WalkDirFunc](keep)",
		},
		{
			name: "an imported handler type",
			gala: "import \"net/http\"\n\nfunc run() http.HandlerFunc = (w, r) => w.WriteHeader(204)\n",
			want: "func(w http.ResponseWriter, r *http.Request) {",
		},
		{
			name: "a value of a zero-parameter type is not a thunk",
			gala: "import \"context\"\n\nfunc stop(c context.CancelFunc) = c()\n\n" +
				"func run() {\n    val ctx, cancel = context.WithCancel(context.Background())\n    Println(ctx.Err())\n    stop(cancel)\n}\n",
			want: "stop(cancel.Get())",
		},
		{
			name: "nil for a zero-parameter type is not a thunk",
			gala: "func halt(s Stop) bool = s == nil\n\nfunc run() bool = halt(nil)\n",
			want: "halt(nil)",
		},
		{
			name: "nil for a type with a result is not a thunk",
			gala: "func supply(p Provider) bool = p == nil\n\nfunc run() bool = supply(nil)\n",
			want: "supply(nil)",
		},
		{
			name: "the result of calling a value of the type",
			gala: "import \"io/fs\"\n\nfunc run(f fs.WalkDirFunc) string {\n    val err = f(\".\", nil, nil)\n    err.Error()\n}\n",
			want: "err.Get().Error()",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, galaFile := samePackageModule(".", goSrc, "package main\n\n"+tc.gala)
			out, err := transpileInModule(t, files, galaFile)
			require.NoError(t, err)
			assert.Contains(t, out, tc.want)
		})
	}

	// A generic Go named function type is not instantiated here, so a lambda
	// in its slot still has no type to take.
	t.Run("a generic Go named function type", func(t *testing.T) {
		files, galaFile := samePackageModule(".", goSrc,
			"package main\n\nfunc apply(m Mapper[int]) int = m(1)\n\nfunc run() int = apply((x) => x)\n")
		_, err := transpileInModule(t, files, galaFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GALA-E0033")
	})
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

// TestConversionToGenericGoNamedFuncType covers a conversion to a generic Go
// named function type declared in the package's own .go file. Its signature
// is not instantiated, so the lambda gets no type from it (GALA-E0033) and is
// never typed by the type's own parameter names (`func(x A) B`), with or
// without type arguments.
func TestConversionToGenericGoNamedFuncType(t *testing.T) {
	for _, call := range []string{"Conv((x) => x)", "Conv[int, int]((x) => x)"} {
		t.Run(call, func(t *testing.T) {
			files, galaFile := samePackageModule(".",
				"package main\n\ntype Conv[A any, B any] func(A) B\n",
				"package main\n\nfunc main() {\n    val c = "+call+"\n    Println(c != nil)\n}\n")
			out, err := transpileInModule(t, files, galaFile)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0033")
			assert.NotContains(t, out, "x A")
		})
	}
}
