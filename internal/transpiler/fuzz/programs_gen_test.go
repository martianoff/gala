package fuzz_test

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// programs_gen_test.go generates the programs FuzzPrograms runs, together
// with the output each must print: the generator models every value it
// builds and evaluates every pattern it writes, so the expected output is
// known without running any GALA.
//
// A program declares a small domain — a sealed type, a generic sealed type, a
// struct, a generic struct, a package-level value, a function, a method and a
// generic function — either in package main or in a library package that main
// imports qualified, under an alias, or with a dot import. std is used
// implicitly, dot-imported or imported qualified, and collection_immutable
// dot-imported, qualified or aliased. The program then matches generated
// values against generated patterns in several positions — a val, a
// function, a lambda, an if-expression branch, a partial function — and calls
// into the domain.

// importStyle is how main reaches a package.
type importStyle int

const (
	samePackage importStyle = iota // the domain only: declared in main itself
	qualified                      // names spelled pkg.X
	aliased                        // names spelled alias.X
	dotImported                    // names spelled X
	importStyles
)

// valueKind is the type of a generated value.
type valueKind int

const (
	kInt valueKind = iota
	kShape
	kPoint
	kOpt      // Option[int]
	kBoxInt   // Box[int]
	kBoxShape // Box[Shape]
	kBoxOpt   // Box[Option[int]]
	kTuple    // (Shape, int)
	kArr      // Array[int]
	kPair     // Pair[int, int]
	valueKinds
)

// value is the generator's model of a generated value.
type value struct {
	kind   valueKind
	tag    string  // the variant: Circle, Rect, Empty, Some, None, Full, Nothing
	n      int     // an int's value
	fields []value // a variant's, struct's or tuple's fields, an array's elements
}

// area is the domain's Area function.
func area(s value) int {
	switch s.tag {
	case "Circle":
		return s.fields[0].n * 3
	case "Rect":
		return s.fields[0].n * s.fields[1].n
	}
	return 0
}

// sum adds up the int fields of a struct value.
func sum(v value) int { return v.fields[0].n + v.fields[1].n }

// limit is the domain's package-level Limit.
const limit = 3

// program is a generated program: its files and what it must do.
type program struct {
	main string
	// lib is the library package's source and import path; "" when the
	// domain is declared in main.
	lib, libPath string
	// rejected says the program must be rejected with a diagnostic;
	// otherwise it must print want.
	rejected bool
	want     string
}

// progGen generates a program from fuzz bytes; once they run out every
// choice is the first option, so generation always terminates.
type progGen struct {
	data []byte
	pos  int
	q    string // the qualifier the domain's names are spelled with in main
	sp   string // the qualifier std's names are spelled with
	cp   string // the qualifier collection_immutable's names are spelled with
	// usesArrays: the program uses collection_immutable, which is then
	// imported.
	usesArrays bool
	next       int
	funcs      []string // main's top-level functions besides main
	body       []string // main's statements
	out        []string // the lines main must print
	// rejected: a statement the transpiler must reject was emitted.
	rejected bool
}

func (g *progGen) pick(n int) int {
	if g.pos >= len(g.data) {
		return 0
	}
	c := int(g.data[g.pos]) % n
	g.pos++
	return c
}

func (g *progGen) name(prefix string) string {
	g.next++
	return fmt.Sprintf("%s%d", prefix, g.next)
}

// domain is the domain's declarations, with std's names spelled with the
// qualifier sp.
func domain(sp string) string {
	return fmt.Sprintf(`sealed type Shape {
    case Circle(R int)
    case Rect(W int, H int)
    case Empty()
}

sealed type Box[T any] {
    case Full(V T)
    case Nothing()
}

struct Point(X int, Y int)

struct Pair[A any, B any](First A, Second B)

val Limit = %d

func Area(s Shape) int = s match {
    case Circle(r) => r * 3
    case Rect(w, h) => w * h
    case Empty() => 0
}

func (p Point) Sum() int = p.X + p.Y

func Pick[T any](b Box[T], d T) T = b match {
    case Full(v) => v
    case Nothing() => d
}

func Half(n int) %[2]sOption[int] = if (n %% 2 == 0) %[2]sSome(n / 2) else %[2]sNone[int]()
`, limit, sp)
}

// stdImport is the import of std in style s ("" for the implicit one), and
// the qualifier its names are then spelled with.
func stdImport(s importStyle) (line, qual string) {
	switch s {
	case qualified:
		return "import \"martianoff/gala/std\"\n", "std."
	case dotImported:
		return "import . \"martianoff/gala/std\"\n", ""
	}
	return "", ""
}

func generateProgram(data []byte) program {
	g := &progGen{data: data}
	style := importStyle(g.pick(int(importStyles)))
	// std: implicit (the samePackage slot), qualified or dot-imported; an
	// alias of std is not generated.
	std := importStyle(g.pick(int(importStyles)))
	if std == aliased {
		std = samePackage
	}
	stdLine, sp := stdImport(std)
	g.sp = sp
	coll := importStyle(1 + g.pick(3))
	collLine := ""
	switch coll {
	case qualified:
		g.cp = "collection_immutable."
		collLine = "import \"martianoff/gala/collection_immutable\"\n"
	case aliased:
		g.cp = "ci."
		collLine = "import ci \"martianoff/gala/collection_immutable\"\n"
	case dotImported:
		collLine = "import . \"martianoff/gala/collection_immutable\"\n"
	}

	var p program
	lib := "package lib\n\n" + stdLine + "\n" + domain(sp)
	libImport := ""
	if style != samePackage {
		h := fnv.New64a()
		h.Write([]byte(lib))
		p.lib = lib
		p.libPath = fmt.Sprintf("martianoff/gala/fuzzgen/p%016x/lib", h.Sum64())
		switch style {
		case qualified:
			g.q = "lib."
			libImport = fmt.Sprintf("import %q\n", p.libPath)
		case aliased:
			g.q = "l."
			libImport = fmt.Sprintf("import l %q\n", p.libPath)
		case dotImported:
			libImport = fmt.Sprintf("import . %q\n", p.libPath)
		}
	}

	for range 1 + g.pick(5) {
		g.statement()
	}

	var b strings.Builder
	b.WriteString("package main\n\n")
	b.WriteString(stdLine)
	if g.usesArrays {
		b.WriteString(collLine)
	}
	b.WriteString(libImport)
	b.WriteString("\n")
	if style == samePackage {
		b.WriteString(domain(sp))
		b.WriteString("\n")
	}
	for _, f := range g.funcs {
		b.WriteString(f + "\n\n")
	}
	b.WriteString("func main() {\n")
	for _, s := range g.body {
		b.WriteString("    " + strings.ReplaceAll(s, "\n", "\n    ") + "\n")
	}
	b.WriteString("}\n")
	p.main = b.String()
	p.rejected = g.rejected
	if len(g.out) > 0 {
		p.want = strings.Join(g.out, "\n") + "\n"
	}
	return p
}

// statement adds a statement that prints one line to main.
func (g *progGen) statement() {
	switch g.pick(10) {
	case 0:
		s := g.value(kShape, 2)
		g.print(fmt.Sprintf("%sArea(%s)", g.q, g.expr(s)), area(s))
	case 1:
		p := g.value(kPoint, 1)
		g.print(fmt.Sprintf("%s.Sum()", g.expr(p)), sum(p))
	case 2:
		// A generic function of the domain, called across the package
		// boundary when the domain is a library.
		b := g.value(kBoxInt, 2)
		d := g.pick(9)
		want := d
		if b.tag == "Full" {
			want = b.fields[0].n
		}
		g.print(fmt.Sprintf("%sPick(%s, %d)", g.q, g.expr(b), d), want)
	case 3:
		// Tuple destructuring.
		t := g.value(kTuple, 2)
		a, n := g.name("a"), g.name("n")
		g.body = append(g.body, fmt.Sprintf("val (%s, %s) = %s", a, n, g.expr(t)))
		g.print(fmt.Sprintf("%sArea(%s) + %s", g.q, a, n), area(t.fields[0])+t.fields[1].n)
	case 4:
		// A std value returned across the package boundary.
		n := g.pick(9)
		want := -1
		if n%2 == 0 {
			want = n / 2
		}
		g.print(fmt.Sprintf("%sHalf(%d).GetOrElse(-1)", g.q, n), want)
	default:
		g.match()
	}
}

// print adds `Println(expr)` to main, which must print want.
func (g *progGen) print(expr string, want int) {
	g.body = append(g.body, "Println("+expr+")")
	g.out = append(g.out, fmt.Sprint(want))
}

// typeName spells the type of a value of kind k.
func (g *progGen) typeName(k valueKind) string {
	switch k {
	case kInt:
		return "int"
	case kShape:
		return g.q + "Shape"
	case kPoint:
		return g.q + "Point"
	case kOpt:
		return g.sp + "Option[int]"
	case kBoxInt:
		return g.q + "Box[int]"
	case kBoxShape:
		return g.q + "Box[" + g.q + "Shape]"
	case kBoxOpt:
		return g.q + "Box[" + g.sp + "Option[int]]"
	case kTuple:
		return g.sp + "Tuple[" + g.q + "Shape, int]"
	case kArr:
		g.usesArrays = true
		return g.cp + "Array[int]"
	case kPair:
		return g.q + "Pair[int, int]"
	}
	panic("unknown kind")
}

func boxElem(k valueKind) valueKind {
	switch k {
	case kBoxShape:
		return kShape
	case kBoxOpt:
		return kOpt
	}
	return kInt
}

// value generates a model value of kind k; depth bounds nesting.
func (g *progGen) value(k valueKind, depth int) value {
	small := func() value { return value{kind: kInt, n: g.pick(6)} }
	switch k {
	case kInt:
		return small()
	case kShape:
		switch g.pick(3) {
		case 0:
			return value{kind: k, tag: "Circle", fields: []value{small()}}
		case 1:
			return value{kind: k, tag: "Rect", fields: []value{small(), small()}}
		}
		return value{kind: k, tag: "Empty"}
	case kPoint, kPair:
		return value{kind: k, fields: []value{small(), small()}}
	case kOpt:
		if g.pick(3) == 0 {
			return value{kind: k, tag: "None"}
		}
		return value{kind: k, tag: "Some", fields: []value{small()}}
	case kBoxInt, kBoxShape, kBoxOpt:
		if g.pick(3) == 0 || depth == 0 {
			return value{kind: k, tag: "Nothing"}
		}
		return value{kind: k, tag: "Full", fields: []value{g.value(boxElem(k), depth-1)}}
	case kTuple:
		return value{kind: k, fields: []value{g.value(kShape, depth), small()}}
	case kArr:
		v := value{kind: k}
		for range g.pick(5) {
			v.fields = append(v.fields, small())
		}
		return v
	}
	panic("unknown kind")
}

// expr spells a GALA expression that builds v, choosing among equivalent
// spellings.
func (g *progGen) expr(v value) string {
	switch v.kind {
	case kInt:
		return fmt.Sprint(v.n)
	case kShape:
		switch v.tag {
		case "Circle":
			switch g.pick(4) {
			case 0:
				return fmt.Sprintf("%sCircle(R = %d)", g.q, v.fields[0].n)
			case 1:
				// Copy of a sealed value, with an override.
				return fmt.Sprintf("%sCircle(%d).Copy(R = %d)", g.q, v.fields[0].n+1, v.fields[0].n)
			}
			return fmt.Sprintf("%sCircle(%d)", g.q, v.fields[0].n)
		case "Rect":
			if g.pick(3) == 0 {
				return fmt.Sprintf("%sRect(H = %d, W = %d)", g.q, v.fields[1].n, v.fields[0].n)
			}
			return fmt.Sprintf("%sRect(%d, %d)", g.q, v.fields[0].n, v.fields[1].n)
		}
		return g.q + "Empty()"
	case kPoint, kPair:
		name, first, second := "Point", "X", "Y"
		if v.kind == kPair {
			name, first, second = "Pair", "First", "Second"
			if g.pick(3) == 0 {
				name = "Pair[int, int]"
			}
		}
		a, b := v.fields[0].n, v.fields[1].n
		switch g.pick(3) {
		case 0:
			return fmt.Sprintf("%s%s(%s = %d, %s = %d)", g.q, name, second, b, first, a)
		case 1:
			// Copy with an override keeps the other field.
			return fmt.Sprintf("%s%s(%d, %d).Copy(%s = %d)", g.q, name, a, b+1, second, b)
		}
		return fmt.Sprintf("%s%s(%d, %d)", g.q, name, a, b)
	case kOpt:
		if v.tag == "None" {
			return g.sp + "None[int]()"
		}
		return fmt.Sprintf("%sSome(%d)", g.sp, v.fields[0].n)
	case kBoxInt, kBoxShape, kBoxOpt:
		if v.tag == "Nothing" {
			return fmt.Sprintf("%sNothing[%s]()", g.q, g.typeName(boxElem(v.kind)))
		}
		return fmt.Sprintf("%sFull(%s)", g.q, g.expr(v.fields[0]))
	case kTuple:
		return fmt.Sprintf("(%s, %d)", g.expr(v.fields[0]), v.fields[1].n)
	case kArr:
		g.usesArrays = true
		if len(v.fields) == 0 {
			return g.cp + "ArrayOf[int]()"
		}
		elems := make([]string, len(v.fields))
		for i, e := range v.fields {
			elems[i] = fmt.Sprint(e.n)
		}
		return g.cp + "ArrayOf(" + strings.Join(elems, ", ") + ")"
	}
	panic("unknown kind")
}

// binder is a name a pattern binds, as the int expression the arm's body
// reads it through.
type binder struct {
	use string
}

// pattern is a generated pattern: its source, whether it matches a value
// (and the values its binders read then), and what it covers for
// exhaustiveness.
type pattern struct {
	src     string
	binders []binder
	match   func(value) (ok bool, bound []int)
	// irrefutable: matches every value of its type.
	irrefutable bool
	// covers: the variants it matches every value of, for a sealed or
	// Option subject.
	covers map[string]bool
}

func always(ok bool) func(value) (bool, []int) {
	return func(value) (bool, []int) { return ok, nil }
}

// pattern generates a pattern for values of kind k. noBind keeps it free of
// binders (inside an alternative). depth bounds nesting.
func (g *progGen) pattern(k valueKind, depth int, noBind bool) pattern {
	wildcard := pattern{src: "_", match: always(true), irrefutable: true}
	bind := func(use func(string) string, eval func(value) int) pattern {
		name := g.name("b")
		return pattern{
			src:         name,
			binders:     []binder{{use(name)}},
			match:       func(v value) (bool, []int) { return true, []int{eval(v)} },
			irrefutable: true,
		}
	}
	choice := g.pick(8)
	if depth == 0 {
		choice = 0
	}
	if choice == 0 {
		if noBind || g.pick(2) == 0 {
			return wildcard
		}
		switch k {
		case kInt:
			return bind(func(n string) string { return n }, func(v value) int { return v.n })
		case kShape:
			return bind(func(n string) string { return g.q + "Area(" + n + ")" }, area)
		case kPoint:
			return bind(func(n string) string { return n + ".Sum()" }, sum)
		case kPair:
			return bind(func(n string) string { return n + ".First + " + n + ".Second" }, sum)
		case kArr:
			return bind(func(n string) string { return n + ".Size()" }, func(v value) int { return len(v.fields) })
		case kOpt:
			return bind(func(n string) string { return n + ".GetOrElse(7)" }, func(v value) int {
				if v.tag == "None" {
					return 7
				}
				return v.fields[0].n
			})
		}
		return wildcard
	}
	switch k {
	case kInt:
		switch choice % 3 {
		case 0:
			n := g.pick(6)
			return pattern{src: fmt.Sprint(n), match: func(v value) (bool, []int) { return v.n == n, nil }}
		case 1:
			a, b := g.pick(6), g.pick(6)
			if a == b {
				b = a + 1
			}
			return pattern{src: fmt.Sprintf("%d | %d", a, b), match: func(v value) (bool, []int) { return v.n == a || v.n == b, nil }}
		}
		// A stable identifier: compared, never bound.
		return pattern{src: g.q + "Limit", match: func(v value) (bool, []int) { return v.n == limit, nil }}
	case kShape:
		switch choice % 5 {
		case 0:
			return g.variant("Circle", g.q, depth, noBind, kInt)
		case 1:
			return g.variant("Rect", g.q, depth, noBind, kInt, kInt)
		case 2:
			// A named sub-pattern on one field.
			sub := g.pattern(kInt, depth-1, noBind)
			p := pattern{
				src:     fmt.Sprintf("%sRect(H = %s)", g.q, sub.src),
				binders: sub.binders,
				match: func(v value) (bool, []int) {
					if v.tag != "Rect" {
						return false, nil
					}
					return sub.match(v.fields[1])
				},
			}
			if sub.irrefutable {
				p.covers = map[string]bool{"Rect": true}
			}
			return p
		case 3:
			return g.variant("Empty", g.q, depth, noBind)
		}
		// An alternative of two binder-free patterns.
		a, b := g.pattern(kShape, depth-1, true), g.pattern(kShape, depth-1, true)
		if a.src == "_" || b.src == "_" {
			return g.variant("Empty", g.q, depth, noBind)
		}
		p := pattern{
			src: a.src + " | " + b.src,
			match: func(v value) (bool, []int) {
				ok1, _ := a.match(v)
				ok2, _ := b.match(v)
				return ok1 || ok2, nil
			},
			covers: map[string]bool{},
		}
		for c := range a.covers {
			p.covers[c] = true
		}
		for c := range b.covers {
			p.covers[c] = true
		}
		return p
	case kPoint, kPair:
		name, second := "Point", "Y"
		if k == kPair {
			name, second = "Pair", "Second"
		}
		if choice%2 == 0 {
			x, y := g.pattern(kInt, depth-1, noBind), g.pattern(kInt, depth-1, noBind)
			return pattern{
				src:     fmt.Sprintf("%s%s(%s, %s)", g.q, name, x.src, y.src),
				binders: append(append([]binder{}, x.binders...), y.binders...),
				match: func(v value) (bool, []int) {
					ok1, b1 := x.match(v.fields[0])
					ok2, b2 := y.match(v.fields[1])
					return ok1 && ok2, append(b1, b2...)
				},
			}
		}
		y := g.pattern(kInt, depth-1, noBind)
		return pattern{
			src:     fmt.Sprintf("%s%s(%s = %s)", g.q, name, second, y.src),
			binders: y.binders,
			match:   func(v value) (bool, []int) { return y.match(v.fields[1]) },
		}
	case kOpt:
		if choice%2 == 0 {
			return g.variant("Some", g.sp, depth, noBind, kInt)
		}
		return g.variant("None", g.sp, depth, noBind)
	case kBoxInt, kBoxShape, kBoxOpt:
		if choice%2 == 0 {
			return g.variant("Full", g.q, depth, noBind, boxElem(k))
		}
		return g.variant("Nothing", g.q, depth, noBind)
	case kTuple:
		s, n := g.pattern(kShape, depth-1, noBind), g.pattern(kInt, depth-1, noBind)
		return pattern{
			src:     fmt.Sprintf("(%s, %s)", s.src, n.src),
			binders: append(append([]binder{}, s.binders...), n.binders...),
			match: func(v value) (bool, []int) {
				ok1, b1 := s.match(v.fields[0])
				ok2, b2 := n.match(v.fields[1])
				return ok1 && ok2, append(b1, b2...)
			},
			irrefutable: s.irrefutable && n.irrefutable,
		}
	case kArr:
		// A sequence pattern: leading element sub-patterns, and a rest
		// that matches any remaining elements, discarded or bound — or none,
		// and the pattern matches exactly as many elements.
		g.usesArrays = true
		n := g.pick(3)
		subs := make([]pattern, n)
		srcs := make([]string, 0, n+1)
		var binders []binder
		for i := range subs {
			subs[i] = g.pattern(kInt, depth-1, noBind)
			srcs = append(srcs, subs[i].src)
			binders = append(binders, subs[i].binders...)
		}
		// The rest: none (exactly n elements), discarded, or bound.
		rest, bindsRest := "", false
		switch g.pick(3) {
		case 1:
			rest = "_..."
		case 2:
			if noBind {
				rest = "_..."
				break
			}
			name := g.name("rest")
			rest, bindsRest = name+"...", true
			binders = append(binders, binder{name + ".Size()"})
		}
		if rest != "" {
			srcs = append(srcs, rest)
		}
		exact := rest == ""
		return pattern{
			src:     g.cp + "Array(" + strings.Join(srcs, ", ") + ")",
			binders: binders,
			match: func(v value) (bool, []int) {
				if len(v.fields) < n || exact && len(v.fields) != n {
					return false, nil
				}
				var bound []int
				for i, s := range subs {
					ok, b := s.match(v.fields[i])
					if !ok {
						return false, nil
					}
					bound = append(bound, b...)
				}
				if bindsRest {
					bound = append(bound, len(v.fields)-n)
				}
				return true, bound
			},
		}
	}
	return wildcard
}

// variant generates the extractor pattern tag(sub...) spelled with qualifier
// q, a sub-pattern for each field kind.
func (g *progGen) variant(tag, q string, depth int, noBind bool, fields ...valueKind) pattern {
	subs := make([]pattern, len(fields))
	srcs := make([]string, len(fields))
	all := true
	var binders []binder
	for i, k := range fields {
		subs[i] = g.pattern(k, depth-1, noBind)
		srcs[i] = subs[i].src
		all = all && subs[i].irrefutable
		binders = append(binders, subs[i].binders...)
	}
	p := pattern{
		src:     fmt.Sprintf("%s%s(%s)", q, tag, strings.Join(srcs, ", ")),
		binders: binders,
		match: func(v value) (bool, []int) {
			if v.tag != tag {
				return false, nil
			}
			var bound []int
			for i, s := range subs {
				ok, b := s.match(v.fields[i])
				if !ok {
					return false, nil
				}
				bound = append(bound, b...)
			}
			return true, bound
		},
	}
	if all {
		p.covers = map[string]bool{tag: true}
	}
	return p
}

// variantsOf lists the variants of a sealed or Option kind, nil for others.
func variantsOf(k valueKind) []string {
	switch k {
	case kShape:
		return []string{"Circle", "Rect", "Empty"}
	case kOpt:
		return []string{"Some", "None"}
	case kBoxInt, kBoxShape, kBoxOpt:
		return []string{"Full", "Nothing"}
	}
	return nil
}

// arm is one generated case of a match.
type arm struct {
	src  string
	eval func(value) (ok bool, result int)
}

// match adds a match over a generated value, in one of several positions, to
// main, printing its result.
func (g *progGen) match() {
	k := valueKind(g.pick(int(valueKinds)))
	subject := g.value(k, 2)
	position := g.pick(5)
	partial := position == 4

	var arms []arm
	covered := map[string]bool{}
	catchAll := false
	for i := range 1 + g.pick(4) {
		p := g.pattern(k, 3, false)
		guard, guarded := "", false
		var guardIdx, guardMin int
		if len(p.binders) > 0 && g.pick(3) == 0 {
			guardIdx, guardMin = g.pick(len(p.binders)), g.pick(5)
			guard, guarded = fmt.Sprintf(" if %s > %d", p.binders[guardIdx].use, guardMin), true
		}
		if p.irrefutable && !guarded && !partial {
			// A catch-all arm goes last, after at least one other.
			if i == 0 {
				continue
			}
			catchAll = true
		}
		weight := (i + 1) * 100
		uses := []string{fmt.Sprint(weight)}
		for _, b := range p.binders {
			uses = append(uses, b.use)
		}
		pm, gi, gm := p.match, guardIdx, guardMin
		arms = append(arms, arm{
			src: fmt.Sprintf("case %s%s => %s", p.src, guard, strings.Join(uses, " + ")),
			eval: func(v value) (bool, int) {
				ok, bound := pm(v)
				if !ok || (guarded && bound[gi] <= gm) {
					return false, 0
				}
				r := weight
				for _, n := range bound {
					r += n
				}
				return true, r
			},
		})
		if !guarded {
			for c := range p.covers {
				covered[c] = true
			}
		}
		if catchAll {
			break
		}
	}
	if len(arms) == 0 {
		arms = append(arms, arm{src: "case _ => 1", eval: func(value) (bool, int) { return true, 1 }})
		catchAll = true
	}

	exhaustive := catchAll
	if vs := variantsOf(k); vs != nil && !exhaustive {
		exhaustive = true
		for _, v := range vs {
			exhaustive = exhaustive && covered[v]
		}
	}
	// A match with no default must be exhaustive. Leave the default out when
	// the arms are; now and then also when a variant is not mentioned at all,
	// which must be rejected.
	if !exhaustive && !partial {
		missing := false
		if vs := variantsOf(k); vs != nil && g.pick(10) == 0 {
			all := strings.Join(armSources(arms), " ")
			for _, v := range vs {
				missing = missing || !strings.Contains(all, v+"(")
			}
		}
		if missing {
			g.rejected = true
		} else {
			arms = append(arms, arm{src: "case _ => -1", eval: func(value) (bool, int) { return true, -1 }})
		}
	}

	want := -7 // a partial function's result when no case matches
	for _, a := range arms {
		if ok, r := a.eval(subject); ok {
			want = r
			break
		}
	}

	body := strings.Join(armSources(arms), "\n    ")
	typ := g.typeName(k)
	subj := g.expr(subject)
	switch position {
	case 0:
		name := g.name("r")
		g.body = append(g.body, fmt.Sprintf("val %s = %s match {\n    %s\n}", name, subj, body))
		g.print(name, want)
	case 1:
		name := g.name("m")
		g.funcs = append(g.funcs, fmt.Sprintf("func %s(x %s) int = x match {\n    %s\n}", name, typ, body))
		g.print(fmt.Sprintf("%s(%s)", name, subj), want)
	case 2:
		name := g.name("f")
		g.body = append(g.body, fmt.Sprintf("val %s = (x %s) => x match {\n    %s\n}", name, typ, body))
		g.print(fmt.Sprintf("%s(%s)", name, subj), want)
	case 3:
		name := g.name("r")
		g.body = append(g.body, fmt.Sprintf("val %s = if (%d > 0) {\n    %s match {\n        %s\n    }\n} else { 0 }",
			name, len(arms), subj, strings.Join(armSources(arms), "\n        ")))
		g.print(name, want)
	case 4:
		name := g.name("p")
		g.body = append(g.body, fmt.Sprintf("val %s func(%s) %sOption[int] = {\n    %s\n}", name, typ, g.sp, body))
		g.print(fmt.Sprintf("%s(%s).GetOrElse(-7)", name, subj), want)
	}
}

func armSources(arms []arm) []string {
	out := make([]string, len(arms))
	for i, a := range arms {
		out[i] = a.src
	}
	return out
}
