package analyzer

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// FuzzRecollect generates two versions of a file of package box and checks
// that collecting the second over the first's entries, as an editor session
// or watch mode does, gives the entries a first collection of the second
// gives: no type parameter, flag, field, method or case of a declaration's
// old form survives, and a method another file declared on a type does.
//
// The versions declare the same few names in changing forms (shorthand,
// block and generic structs, interfaces, sealed types whose cases come and
// go or are reused by another declaration, opaque types) with methods added
// and deleted, so every collection reuses entries the previous one made.
// Each pair is collected twice: as the analyzed file (TestRecollected-
// DeclarationDropsStaleMethods) and as a sibling of it (TestRecollected-
// SiblingDeclaration), and each version is also collected over itself.
//
// The exceptions are the ones those tests document: an interface records no
// DefinedIn, so the one recorded before stays; and a version that declares
// no doc comment keeps the doc recorded before (TestNativeSetDocKeeps-
// ExistingWhenAbsent), which the generator keeps out of reach by never
// dropping a doc it wrote: a name documented in the first version, as a
// declaration or as a case, is documented in the second whichever it is. Every name the first version declares at top
// level is declared again by the second: within one analysis, entries are
// reused only for declarations read again (each Analyze starts a fresh
// RichAST), so a type deleted outright is not a re-collection.
func FuzzRecollect(f *testing.F) {
	rng := rand.New(rand.NewSource(4))
	for range recollectSeeds {
		seed := make([]byte, 64)
		rng.Read(seed)
		f.Add(seed)
	}
	parseWithGalop(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		first, again := generateRecollectPair(data)
		collect := declCollectors["native"]
		recollectDeclaration(t, collect, first.src, again.src)
		if first.src != again.src {
			// Collecting again over itself; the pair already did when the
			// two versions are the same.
			recollectDeclaration(t, collect, again.src, again.src)
		}
		recollectSibling(t, first.src, again.src)
	})
}

// recollectSeeds is how many generated pairs the seed pass collects.
const recollectSeeds = 300

// The names the versions declare: tops are declared by every version, case
// names are a sealed type's cases or, when none takes them, now and then a
// declaration of their own.
var (
	recollectTops  = []string{"ID", "Cfg"}
	recollectCases = []string{"Small", "Large", "Big"}
)

// recollectGen turns fuzz bytes into choices; once they run out every choice
// is the first, so generation always terminates.
type recollectGen struct {
	data []byte
	pos  int
}

func (g *recollectGen) pick(n int) int {
	if g.pos >= len(g.data) {
		return 0
	}
	c := int(g.data[g.pos]) % n
	g.pos++
	return c
}

// recollectVersion is one generated version of the file.
type recollectVersion struct {
	src string
	// tops: the names it declares at top level; docs: those with a doc.
	tops, docs map[string]bool
}

// generateRecollectPair generates the two versions.
func generateRecollectPair(data []byte) (first, again recollectVersion) {
	g := &recollectGen{data: data}
	first = g.version(nil, nil)
	if g.pick(5) == 0 {
		// The same file again, as a save without an edit.
		return first, first
	}
	return first, g.version(first.tops, first.docs)
}

// version generates a version declaring every name in required at top level
// or as a case, and giving each name in docs a doc comment.
func (g *recollectGen) version(required, docs map[string]bool) recollectVersion {
	v := recollectVersion{tops: map[string]bool{}, docs: map[string]bool{}}
	var b strings.Builder
	b.WriteString("package box\n")
	free := slices.Clone(recollectCases)
	doc := func(name string) {
		if docs[name] || g.pick(4) == 0 {
			v.docs[name] = true
			fmt.Fprintf(&b, "// %s is version %d's.\n", name, g.pick(3))
		}
	}
	for _, name := range recollectTops {
		b.WriteString("\n")
		doc(name)
		v.tops[name] = true
		generic := g.pick(3) == 0
		recv := name
		switch g.pick(5) {
		case 0:
			if generic {
				fmt.Fprintf(&b, "struct %s[T any](N T, Tries int = %d)\n", name, g.pick(5))
				recv = name + "[T]"
			} else {
				fmt.Fprintf(&b, "struct %s(N int, Tries int = %d)\n", name, g.pick(5))
			}
		case 1:
			if generic {
				fmt.Fprintf(&b, "type %s[K comparable] struct {\n    V K\n}\n", name)
				recv = name + "[K]"
			} else {
				fmt.Fprintf(&b, "type %s struct {\n    var Name string\n    Size int\n}\n", name)
			}
		case 2:
			fmt.Fprintf(&b, "type %s interface {\n", name)
			specs := []string{"Raw() int64", "Name() string", "Tries(n int) int"}
			for i, s := range specs {
				if i == 0 || g.pick(2) == 0 {
					b.WriteString("    " + s + "\n")
				}
			}
			b.WriteString("}\n")
			continue // no methods on an interface
		case 3:
			if len(free) == 0 {
				fmt.Fprintf(&b, "opaque type %s int64\n", name)
				break
			}
			tp, field := "", "int"
			if generic {
				tp, field, recv = "[T any]", "T", name+"[T]"
			}
			fmt.Fprintf(&b, "sealed type %s%s {\n", name, tp)
			for n := 1 + g.pick(2); n > 0 && len(free) > 0; n-- {
				c := free[g.pick(len(free))]
				free = slices.DeleteFunc(free, func(s string) bool { return s == c })
				// A case is documented as a declaration is, so a doc a
				// stand-alone declaration of c had stays when c becomes a case.
				if docs[c] || g.pick(4) == 0 {
					v.docs[c] = true
					fmt.Fprintf(&b, "    // %s is version %d's.\n", c, g.pick(3))
				}
				if g.pick(3) == 0 {
					fmt.Fprintf(&b, "    case %s()\n", c)
				} else {
					fmt.Fprintf(&b, "    case %s(N %s)\n", c, field)
				}
			}
			b.WriteString("}\n")
		case 4:
			fmt.Fprintf(&b, "opaque type %s %s\n", name, []string{"int64", "string"}[g.pick(2)])
		}
		for _, m := range []string{"Old", "Keep"} {
			if g.pick(2) == 0 {
				fmt.Fprintf(&b, "\nfunc (i %s) %s() int = %d\n", recv, m, g.pick(9))
			}
		}
	}
	// A case name no sealed type took: its own declaration, as the
	// required ones must be.
	for _, c := range free {
		if !required[c] && g.pick(3) != 0 {
			continue
		}
		b.WriteString("\n")
		doc(c)
		v.tops[c] = true
		if g.pick(2) == 0 {
			fmt.Fprintf(&b, "opaque type %s int64\n", c)
		} else {
			fmt.Fprintf(&b, "struct %s(N int)\n", c)
		}
	}
	v.src = b.String()
	return v
}

