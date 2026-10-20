package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

func newUnknownMethodTranspiler() *checkedTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return newCheckedTranspiler(p, a, tr, g)
}

// TestUnknownMethodOnKnownTypeIsRejected pins B3: calling a method a GALA type
// does not have must be a GALA error naming a real method, instead of reaching
// `go build` — which reported it against the GENERATED expression, including
// auto-unwrap `.Get()` calls the user never wrote:
//
//	xs.Get().Filter(func(x int) bool {…}).Sum undefined
//	  (type collection_immutable.Array[int] has no field or method Sum)
func TestUnknownMethodOnKnownTypeIsRejected(t *testing.T) {
	trans := newUnknownMethodTranspiler()

	cases := []struct {
		name       string
		input      string
		wantMethod string
		wantInHint string
	}{
		{
			name: "no_such_method_on_stdlib_type",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2, 3)
    Println(xs.Sum())
}`,
			wantMethod: "Sum",
		},
		{
			name: "near_miss_suggests_the_real_one",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2, 3)
    Println(xs.Sise())
}`,
			wantMethod: "Sise",
			wantInHint: "Size",
		},
		{
			name: "user_defined_struct",
			input: `package main

struct Point(X int, Y int)

func main() {
    val p = Point(1, 2)
    Println(p.Norm())
}`,
			wantMethod: "Norm",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "main.gala")
			require.Error(t, err, "an unknown method must be rejected by GALA")
			require.Contains(t, err.Error(), string(galaerr.CodeUnknownMethod))
			require.Contains(t, err.Error(), tc.wantMethod)
			if tc.wantInHint != "" {
				require.Contains(t, err.Error(), tc.wantInHint,
					"the hint must suggest the nearest real method")
			}
		})
	}
}

// TestSynthesizedMethodsAreNotUnknown is the false-positive guard for the
// methods the transformer generates rather than the user declaring them. None
// of them appear in TypeMetadata.Methods, so a naive "not in Methods" check
// would reject every one.
func TestSynthesizedMethodsAreNotUnknown(t *testing.T) {
	trans := newUnknownMethodTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name: "copy_equal_on_struct",
			input: `package main

struct Point(X int, Y int)

func main() {
    val p = Point(1, 2)
    val q = p.Copy(X = 5)
    Println(p.Equal(q))
}`,
		},
		{
			name: "string_on_sealed",
			input: `package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func main() {
    val c = Circle(1.0)
    Println(c.String())
}`,
		},
		{
			// sealed.go generates `is`+variant on the parent for every case,
			// whatever the case of the variant's first letter.
			name: "is_predicates_on_sealed",
			input: `package main

sealed type frame {
    case lineFrame(Text string)
    case endFrame()
    case Idle()
}

func main() {
    val f frame = endFrame()
    Println(f.isendFrame(), f.islineFrame(), f.isIdle())
}`,
		},
		{
			name: "real_methods_still_work",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2, 3)
    Println(xs.Filter((x) => x > 1).Size())
}`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "main.gala")
			require.NoError(t, err, "this method call is legitimate and must transpile")
		})
	}
}

// TestHintWhenTypeDeclaresNoMethods pins the review finding that the hint
// degenerated to a dangling "Point declares: " with nothing after the colon.
// A type in the package being compiled is judged even with an empty method set,
// so the empty list is reachable and has to read as a sentence.
func TestHintWhenTypeDeclaresNoMethods(t *testing.T) {
	trans := newUnknownMethodTranspiler()

	_, err := trans.Transpile(`package main

struct Point(X int, Y int)

func main() {
    val p = Point(1, 2)
    Println(p.Norm())
}`, "main.gala")

	require.Error(t, err)
	require.Contains(t, err.Error(), "Point declares no methods")
	require.NotContains(t, err.Error(), "declares: ",
		"an empty method set must not render as a dangling list")
}

// genericNodeProgram is a program around decls whose generic struct Node has
// a value method, a pointer-receiver method and a function-typed field, so a
// hint has methods to list and Ptr[Node[T]] has a method of its own to call.
func genericNodeProgram(imports, decls string) string {
	return `package main

` + imports + `

struct Node[T any](var value T, var next func() T)

func (n Node[T]) Value() T = n.value

func (n *Node[T]) Reset(v T) {
    n.value = v
}
` + decls + `

func main() {}
`
}

// TestUnknownMethodInGenericCodeIsRejected covers a receiver whose type
// arguments are type parameters of the enclosing declaration (#807). The
// method set of Node[T] is the method set of Node whatever T is, so a method
// Node does not declare is GALA-E0044 there exactly as it is on Node[int],
// instead of reaching `go build` as "type Node[T] has no field or method".
func TestUnknownMethodInGenericCodeIsRejected(t *testing.T) {
	trans := newUnknownMethodTranspiler()

	cases := []struct {
		name       string
		decls      string
		wantMethod string
		wantInHint string
	}{
		{
			name: "zero_argument_call_in_generic_function",
			decls: `
func walk[T any](start Node[T]) {
    Println(start.IsDefined())
}`,
			wantMethod: "IsDefined",
			wantInHint: "Node declares: Reset, Value",
		},
		{
			name: "call_with_arguments_in_generic_function",
			decls: `
func walk[T any](start Node[T], v T) {
    start.Push(v)
}`,
			wantMethod: "Push",
		},
		{
			name: "near_miss_in_generic_function",
			decls: `
func walk[T any](start Node[T]) T = start.Valu()`,
			wantMethod: "Valu",
			wantInHint: "did you mean `Value`?",
		},
		{
			name: "ptr_receiver_in_generic_function",
			decls: `
func walk[T any](start Ptr[Node[T]]) {
    Println(start.IsDefined())
}`,
			wantMethod: "IsDefined",
		},
		{
			name: "own_receiver_in_generic_method",
			decls: `
func (n Node[T]) Twice() T = n.Missing()`,
			wantMethod: "Missing",
		},
		{
			name: "receiver_captured_by_a_lambda",
			decls: `
func walk[T any](start Node[T]) {
    val f = () => start.Missing()
    f()
}`,
			wantMethod: "Missing",
		},
		{
			name: "partially_bound_receiver",
			decls: `
struct Pair[A any, B any](a A, b B)

func (p Pair[A, B]) First() A = p.a

func second[B any](p Pair[int, B]) B = p.Second()`,
			wantMethod: "Second",
			wantInHint: "Pair declares: First",
		},
		{
			name: "ptr_to_a_two_parameter_type",
			decls: `
struct Pair[A any, B any](a A, b B)

func swap[A any, B any](p Ptr[Pair[A, B]]) B = p.Second()`,
			wantMethod: "Second",
			wantInHint: "Pair declares no methods",
		},
		{
			// The receiver's type is a generic call's result, typed by the
			// callee's signature; whether the callee's parameter shares the
			// enclosing one's name must not matter.
			name: "receiver_typed_by_a_generic_call",
			decls: `
func head[T any](n Node[T]) Node[T] = n

func walkT[T any](n Node[T]) T = head(n).Missing()

func walkA[A any](n Node[A]) A = head(n).Missing()`,
			wantMethod: "Missing",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(genericNodeProgram("", tc.decls), "main.gala")
			require.Error(t, err, "an unknown method in generic code must be rejected by GALA")
			require.Contains(t, err.Error(), string(galaerr.CodeUnknownMethod))
			require.Contains(t, err.Error(), "has no method "+tc.wantMethod)
			if tc.wantInHint != "" {
				require.Contains(t, err.Error(), tc.wantInHint)
			}
		})
	}
}

// TestFieldCalledInGenericCodeIsJudged covers the field half of the same
// check in generic code: a field whose type is not callable is GALA-E0054.
// A function-typed field called there is covered by
// TestKnownMethodsInGenericCodeAreNotUnknown.
func TestFieldCalledInGenericCodeIsJudged(t *testing.T) {
	trans := newUnknownMethodTranspiler()
	_, err := trans.Transpile(genericNodeProgram("", `
struct Counter[T any](var count int, item T)

func total[T any](c Counter[T]) int = c.count()`), "main.gala")
	require.Error(t, err)
	require.Contains(t, err.Error(), string(galaerr.CodeValueCalledAsFunction))
}

// TestKnownMethodsInGenericCodeAreNotUnknown is the false-positive guard for
// TestUnknownMethodInGenericCodeIsRejected: every way a call in generic code
// legitimately reaches a method or a callable member still transpiles, and the
// oracle compiles each result.
func TestKnownMethodsInGenericCodeAreNotUnknown(t *testing.T) {
	trans := newUnknownMethodTranspiler()

	cases := []struct {
		name    string
		imports string
		decls   string
	}{
		{
			name: "declared_methods_and_function_field",
			decls: `
func walk[T any](start Node[T]) T {
    Println(start.next())
    start.Value()
}`,
		},
		{
			name: "pointer_receiver_method_through_ptr",
			decls: `
func reset[T any](start Ptr[Node[T]], v T) T {
    start.Reset(v)
    start.Value()
}`,
		},
		{
			name: "synthesized_methods",
			decls: `
func same[T any](a Node[T], b Node[T]) bool = a.Equal(b.Copy())`,
		},
		{
			name: "own_receiver_in_generic_method",
			decls: `
func (n Node[T]) Again() T = n.Value()`,
		},
		{
			name: "generic_method_with_its_own_type_parameter",
			decls: `
func (n Node[T]) Fold[U any](f func(T) U) U = f(n.value)

func show[T any](n Node[T]) string = n.Fold((v) => s"$v")`,
		},
		{
			name:    "std_generic_types",
			imports: `import . "martianoff/gala/collection_immutable"`,
			decls: `
func count[T any](xs Array[T], o Option[T]) int = xs.Size() + o.Map((v) => 1).GetOrElse(0)`,
		},
		{
			name: "constraint_interface_method_on_type_parameter",
			decls: `
type Shower interface {
    Show() string
}

func render[T Shower](x T) string = x.Show()`,
		},
		{
			// The type parameter shadows the struct of the same name, whose
			// method set says nothing about it.
			name: "type_parameter_shadowing_a_struct",
			decls: `
type Shower interface {
    Show() string
}

struct Item(name string)

func render[Item Shower](x Item) string = x.Show()`,
		},
		{
			name: "generic_interface_receiver",
			decls: `
type Source[T any] interface {
    Next() T
}

func pull[T any](s Source[T]) T = s.Next()`,
		},
		{
			name: "synthesized_methods_on_a_generic_opaque_type",
			decls: `
opaque type Id[T any] int64

func order[T any](a Id[T], b Id[T]) int = a.Compare(b) + int(a.Hash() % 2)`,
		},
		{
			name:    "go_generic_type",
			imports: `import "sync/atomic"`,
			decls: `
func swap[T any](p Ptr[atomic.Pointer[T]], v Ptr[T]) {
    p.Store(v)
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(genericNodeProgram(tc.imports, tc.decls), "main.gala")
			require.NoError(t, err, "this method call is legitimate and must transpile")
		})
	}
}

// TestGenericMethodsFromOtherFilesAreKnown covers a generic type whose methods
// are declared outside the declaration of the type, called in generic code:
// in a hand-written Go file of its package, and in another GALA file of an
// imported package.
func TestGenericMethodsFromOtherFilesAreKnown(t *testing.T) {
	cases := []struct {
		name     string
		files    func(call string) (map[string]string, string) // call: the method `walk` calls on n
		method   string
		misspelt string
	}{
		{
			name: "go_file_of_the_package",
			files: func(call string) (map[string]string, string) {
				return samePackageModule(".",
					"package main\n\nfunc (n Node[T]) Label() string { return \"node\" }\n",
					"package main\n\nstruct Node[T any](value T)\n\nfunc walk[T any](n Node[T]) string = n."+call+"()\n")
			},
			method:   "Label",
			misspelt: "Lable",
		},
		{
			name: "other_gala_file_of_an_imported_package",
			files: func(call string) (map[string]string, string) {
				return map[string]string{
					"go.mod":        "module example.com/sibs\n\ngo 1.25\n",
					"gala.mod":      "module example.com/sibs\n",
					"lib/node.gala": "package lib\n\nstruct Node[T any](Value T)\n",
					"lib/more.gala": "package lib\n\nfunc (n Node[T]) Name() string = \"n\"\n",
					"main.gala": "package main\n\nimport \"example.com/sibs/lib\"\n\n" +
						"func walk[T any](n lib.Node[T]) string = n." + call + "()\n\nfunc main() {}\n",
				}, "main.gala"
			},
			method:   "Name",
			misspelt: "Nme",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, galaFile := tc.files(tc.method)
			_, err := transpileInModule(t, files, galaFile)
			require.NoError(t, err)

			files, galaFile = tc.files(tc.misspelt)
			_, err = transpileInModule(t, files, galaFile)
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeUnknownMethod))
			require.Contains(t, err.Error(), "did you mean `"+tc.method+"`?")
		})
	}
}
