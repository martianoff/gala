package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
)

// undefinedInputs are packages, as files, whose compiled file (the first) the
// undefined-symbol check finds each kind of error in, or, for clean, none:
// every decision of the value walk and the type pass, every binder the check
// adds to the walk's, and every message and hint it writes. Each file holds
// many errors; the check reports them all, and the analysis the first.
var undefinedInputs = []struct {
	name  string
	clean bool
	files [][2]string
}{
	{"values", false, [][2]string{{"main.gala", `package main

import "martianoff/gala/collection_immutable"

struct Point(x int = origin, y int = x)

val (left, right) = (1, two)

var counter = left + right + missingVar

func useImportedByName() int = ArrayOf(1, 2).Size()

func useUndeclared() string = s"héllo ${nowhere + 1} $alsoNowhere"

func useUnimported() int = EmptyArray[int]().Size()

func (p Point) sum() int = p.x + p.y + offset

func shadow(p int) int {
    val q = (r int) => r + p + missing
    func local[U any](u U) int = 1 + localMissing
    return q(local(p))
}

func repeated() int = missing + missing

func localTypeOutOfBlock(ok bool) int {
    if ok {
        type Inner struct {
            N int
        }
        Println(Inner(1).N)
    }
    return Inner(2).N
}

func qualifiable(xs Array[int]) int = xs.Size()

func main() {
    Println(collection_immutable.ArrayOf(1))
}
`}}},
	{"types", false, [][2]string{{"main.gala", `package main

import "time"

type Coord Pointt

type Pair[K Numbr, V any] struct {
    Key K
    Value V
}

func f(sb strings.Builder) int = 1

func g(x Widgt) int = 1

func h(cb func(Wdg, int) int) int = 1

func notType(x helper) int = 1

func helper() int = 1

func dur(d Duration) int = 1

func movedEarlier(x Later) int = 1

val laterUse = Later

func generic[T any](x T, y _) T = x

struct Holder[T any](items []T)

func (h Holder[T]) first(m map[string]T) T = h.items[0]

func localType() int {
    type Inner struct {
        N int
    }
    val i Inner = Inner{N: 1}
    return i.N
}

sealed type Shape {
    case Circle(r float64)
    case Square(side Sidee)
}

func usesCase(c Circle) float64 = c.r

func pointer(p *Missing, q []other.T) int = 1

func main() {
    Println(time.Now())
}
`}}},
	{"method signature allowance", false, [][2]string{
		{"b.gala", `package main

func (b Box) total(xs Array[int]) int = xs.Size() + EmptyArray[int]().Size()

func (b *Box) ptr(xs Array[int]) Array[int] = xs
`},
		{"a.gala", `package main

import . "martianoff/gala/collection_immutable"

struct Box(v int)

func main() {
    Println(ArrayOf(1).Size())
}
`},
	}},
	{"binders", true, [][2]string{{"main.gala", `package main

import . "martianoff/gala/go_interop"

struct Node[T any](value T, next Option[Node[T]])

val (a, b) = (1, 2)

var (c, d) = (a, b)

func id[T any](x T) T = x

func (n Node[T]) get() T = n.value

func (n *Node[T]) set(v T) T = v

func pairUp[K comparable, V any](k K, v V) (K, V) = (k, v)

func walk(xs []int) int {
    var total = 0
    for i, x := range xs {
        total = total + i + x
    }
    val f = (y int) => y + total
    func inner[U any](u U) U = u
    type Cell struct {
        V int
    }
    return f(inner(Cell(c + d).V))
}

func pick(o Option[int]) int = o match {
    case Some(v) => v
    case _ => 0
}

func main() {
    Println(walk(SliceOf(1, 2)))
}
`}}},
}

// TestUndefinedCheckAgrees holds the undefined-symbol check on the native
// tree (undefined_symbol_native.go) to the check on ANTLR's: each GALA source
// staged as data (those TestEnginesAgree parses), the examples of the error
// reference pages, and undefinedInputs, analyzed as the CLI analyzes a file.
// Wherever the check runs, it is run again on the file's ANTLR tree in the
// same state, and both must report the same errors, in the same order, with
// the same code, message, hint, position and span. The check must have read
// the native tree exactly where the file's declarations were collected on
// it, and every input of undefinedInputs must be checked natively, and the
// whole analysis of it must agree with an analysis on ANTLR's tree alone, the
// file of its error included.
func TestUndefinedCheckAgrees(t *testing.T) {
	parseWithGalop(t)
	paths, searchPaths := collectionCorpus(t)
	p := galaparser.NewAntlrGalaParser()
	nativeA := newCollectionAnalyzer(t, p, searchPaths)
	antlrA := newCollectionAnalyzer(t, antlrOnly{p}, searchPaths)

	type input struct {
		name  string
		files [][2]string
		// handwritten says the input is one of undefinedInputs; clean, that
		// it must report no error.
		handwritten, clean bool
	}
	var inputs []input
	for _, path := range paths {
		text, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// A corpus file is analyzed in place: its siblings are its own.
		inputs = append(inputs, input{name: path, files: [][2]string{{path, string(text)}}})
	}
	for i, files := range docExamples(t) {
		inputs = append(inputs, input{name: fmt.Sprintf("error reference example %d (%s)", i, files[0][0]), files: files})
	}
	for _, in := range undefinedInputs {
		inputs = append(inputs, input{in.name, in.files, true, in.clean})
	}

	checked, native, errors := 0, 0, 0
	for _, in := range inputs {
		// A corpus file is analyzed in place, with its own siblings; the
		// files of any other input are written out and analyzed each with
		// the others as its package, the first as the input's subject.
		paths := []string{in.files[0][0]}
		if !filepath.IsAbs(paths[0]) {
			paths = nil
			dir := t.TempDir()
			for _, f := range in.files {
				path := filepath.Join(dir, f[0])
				if err := os.WriteFile(path, []byte(f[1]), 0o644); err != nil {
					t.Fatal(err)
				}
				paths = append(paths, path)
			}
		}
		for i, path := range paths {
			var packageFiles []string
			if len(paths) > 1 {
				packageFiles = append(append(packageFiles, paths[:i]...), paths[i+1:]...)
			}
			name := in.name + ": " + filepath.Base(path)
			c, n, e := checkUndefinedAgree(t, nativeA, antlrA, name, path, in.files[i][1], packageFiles, in.handwritten && i == 0, in.clean)
			checked, native, errors = checked+c, native+n, errors+e
		}
	}
	// Nearly every source loads its imports, so the check runs on it, and on
	// its native tree; floors catch the sources not being staged or the
	// native path going unused.
	if checked < 300 || native < checked*3/4 || errors < 40 {
		t.Fatalf("checked %d files, %d of them on the native tree, %d errors", checked, native, errors)
	}
	t.Logf("checked %d files, %d of them on the native tree, %d errors", checked, native, errors)
}

// checkUndefinedAgree analyzes path, whose source is text, with nativeA, in a
// package of packageFiles (see analyzeForCollection), and holds its
// undefined-symbol check to the check on ANTLR's tree (see
// TestUndefinedCheckAgrees). It returns whether the check ran (1 or 0), on
// the native tree, and how many errors it reported. A subject is one of
// undefinedInputs, which must be checked natively, with errors unless clean,
// and analyze as antlrA analyzes it.
func checkUndefinedAgree(t *testing.T, nativeA, antlrA *galaAnalyzer, name, path, text string, packageFiles []string, subject, clean bool) (checked, native, errors int) {
	t.Helper()
	parsed := nativeA.parser.ParseFile(text)
	if parsed.Err() != nil {
		if subject {
			t.Errorf("%s: does not parse: %v", name, parsed.Err())
		}
		return 0, 0, 0
	}
	var runs []undefinedCheck
	nativeA.onUndefinedCheck = func(run undefinedCheck) {
		runs = append(runs, run)
		if want := run.antlr(); !reflect.DeepEqual(run.errs, want) {
			t.Errorf("%s: the check on the native tree (native %v) reports %s, on ANTLR's %s", name, run.native, errorList(run.errs), errorList(want))
		}
	}
	an := analyzeParsedForCollection(t, nativeA, path, parsed, packageFiles)
	nativeA.onUndefinedCheck = nil
	if !checkTrace(t, name, an, true) {
		return 0, 0, 0
	}
	_, failed := an.result.(error)
	switch {
	case len(runs) > 1:
		t.Errorf("%s: the check ran %d times", name, len(runs))
		return 0, 0, 0
	case len(runs) == 0:
		// The check stands down, or the analysis stopped before it.
		if an.trace.checksUndefined && !failed {
			t.Errorf("%s: the check was due but did not run", name)
		}
		if subject {
			t.Errorf("%s: the check did not run: %v", name, an.result)
		}
		return 0, 0, 0
	}
	run := runs[0]
	if run.native != an.trace.native {
		t.Errorf("%s: the check read the native tree: %v; the declarations were collected on it: %v", name, run.native, an.trace.native)
	}
	if run.native {
		native = 1
	}
	if subject {
		if !run.native {
			t.Errorf("%s: not checked on the native tree", name)
		}
		if clean != (len(run.errs) == 0) {
			t.Errorf("%s: the check reports %s", name, errorList(run.errs))
		}
		t.Logf("%s: %s", name, errorList(run.errs))
		a, _ := analyzeForCollection(t, antlrA, path, text, packageFiles)
		if diff := firstDiff(reflect.ValueOf(an.result), reflect.ValueOf(a.result), "", map[visit]bool{}); diff != "" {
			t.Errorf("%s: the analysis on the native tree differs from ANTLR's at %s", name, diff)
		}
	}
	return 1, native, len(run.errs)
}

// errorList prints errs for a failure message.
func errorList(errs []*galaerr.SemanticError) string {
	s := fmt.Sprintf("%d errors", len(errs))
	for _, e := range errs {
		s += fmt.Sprintf("\n  %d:%d %+v", e.Line, e.Column, *e)
	}
	return s
}
