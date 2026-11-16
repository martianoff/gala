package gala2lean

import (
	"fmt"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Collections. A program that dot-imports martianoff/gala/collection_immutable
// may use its Array and List: the prelude's GArray and GList, which hold the
// elements as a Lean List, with the functions below modelled in the prelude
// (formal/Gala/Gala2Lean/Prelude.lean), each after the std code it cites.
// A function a call passes runs as std runs it: in the same order, with the
// same early exits. Where std panics (Get out of range, Head of an empty
// Array, Reduce of an empty List, ...) the model throws Panic.std, class
// `other`, as Go's run of it is classed.
//
// HashMap and HashSet are not translated (hash-order): their printing,
// iteration and folding follow the order of their hash trie, which the
// prelude does not model. Nor is any other name of the package, or a
// function of Array or List not below (collection-method).

// collectionsPath is the import path of the immutable collections.
const collectionsPath = "martianoff/gala/collection_immutable"

// hashTypes are the collection types whose order the prelude does not model,
// and the functions that build them.
var hashTypes = map[string]bool{
	"HashMap": true, "HashSet": true,
	"EmptyHashMap": true, "HashMapOf": true, "HashMapFromSlice": true,
	"EmptyHashSet": true, "HashSetOf": true, "HashSetFromSlice": true,
}

// seqCtors are the functions that build an Array or a List.
var seqCtors = map[string]string{"ArrayOf": "GArray", "EmptyArray": "GArray", "ListOf": "GList", "EmptyList": "GList"}

// hashType stops the translation at a HashMap or HashSet type (hash-order).
func (g *gen) hashType(name string, pos coreexport.Pos) {
	if g.collections && hashTypes[name] && !g.declaredType(name) {
		unsup(pos, "hash-order")
	}
}

// seqOf reads Lean type t as an Array or a List: its prelude type and its
// element's Lean type.
func seqOf(t string) (kind, elem string, ok bool) {
	p := parseLean(t)
	if (p.head != "GArray" && p.head != "GList") || len(p.args) != 1 {
		return "", "", false
	}
	return p.head, p.args[0].String(), true
}

// seqCtor builds an Array or a List from its elements, in the order written.
func (g *gen) seqCtor(x string, e *coreexport.Call, sc *scope) term {
	return g.seqCtorAt(seqCtors[x]+".mk", x, e, sc)
}

// seqCtorAt is seqCtor with the constructor applied as head: `GArray.mk`, or
// with its element type given, `GArray.mk (α := Int64)`.
func (g *gen) seqCtorAt(head, x string, e *coreexport.Call, sc *scope) term {
	if strings.HasPrefix(x, "Empty") && len(e.Args) > 0 {
		unsup(e.Pos, "argument-count")
	}
	parts := make([]string, len(e.Args))
	subs := make([]term, len(e.Args))
	for i, a := range e.Args {
		positional(a)
		subs[i] = g.expr(a, sc)
		parts[i] = subs[i].val()
	}
	return wrap("("+head+" ["+strings.Join(parts, ", ")+"])", subs...)
}

// seqCtorType is exprType of a call of an Array or List builder.
func (g *gen) seqCtorType(x string, e *coreexport.Call, sc *scope, seen map[string]bool) string {
	if len(e.Args) == 0 {
		return ""
	}
	if t := g.exprType(argNode(e.Args[0]), sc, seen); t != "" {
		return "(" + seqCtors[x] + " " + t + ")"
	}
	return ""
}

// inferTypeArg stands for a type Lean infers: a function's result where the
// context does not say it (Map's).
const inferTypeArg = "_gala_infer"

// seqMethod is one function of Array or List the prelude models.
type seqMethod struct {
	// params are the parameter types, over T (the element type), S (the
	// collection's own type), U (a type the call fixes: FoldLeft's and
	// FoldRight's initial value), B, I, Str (bool, int, string), S_ (a
	// collection of the same kind, of elements Lean infers), O_ (an Option
	// of one), and function types of those, `T,U->U`, `T->` without a
	// result, `T->_` with a result Lean infers.
	params []string
	// code is the Lean code of the call, over {r} (the receiver's
	// elements, a List), {k} (the collection's constructor) and {0}, {1}
	// (the arguments).
	code string
	// pure is true when the code is a value, not an action.
	pure bool
	// result is the result's type over T, S, U, B, I, Str, O (GOption T),
	// M (the collection of Map's result), F (the function argument's
	// result, a collection), C (the collection of what the Option the
	// function argument returns holds), L, A (the other collection), P (a
	// pair of S), Z (S of each T with its index), Y (S of each T with the
	// argument's element), G (an Array of S), or "" for Unit.
	result string
	// compares and prints say that the call compares or prints elements.
	compares, prints bool
	// only is the collection kind that alone has the function (GArray or
	// GList), "" when both have it.
	only string
}

// seqMethods are the functions of Array and List the prelude models, by
// name. Both types have each, except those marked only.
var seqMethods = map[string]seqMethod{
	"Size":       {code: "(seqSize {r})", pure: true, result: "I"},
	"Length":     {code: "(seqSize {r})", pure: true, result: "I"},
	"IsEmpty":    {code: "{r}.isEmpty", pure: true, result: "B"},
	"NonEmpty":   {code: "(!{r}.isEmpty)", pure: true, result: "B"},
	"Get":        {params: []string{"I"}, code: "seqGet {r} {0}", result: "T"},
	"GetOption":  {params: []string{"I"}, code: "(seqGetOption {r} {0})", pure: true, result: "O"},
	"Head":       {code: "seqHead {r}", result: "T"},
	"HeadOption": {code: "(toGOption {r}.head?)", pure: true, result: "O"},
	"Last":       {code: "seqLast {r}", result: "T"},
	"LastOption": {code: "(toGOption {r}.getLast?)", pure: true, result: "O"},
	"Updated":    {params: []string{"I", "T"}, code: "{k} <$> seqUpdated {r} {0} {1}", result: "S"},
	"Append":     {params: []string{"T"}, code: "({k} ({r} ++ [{0}]))", pure: true, result: "S"},
	"Prepend":    {params: []string{"T"}, code: "({k} (seqPrepend {r} {0}))", pure: true, result: "S"},
	"AppendAll":  {params: []string{"S"}, code: "({k} ({r} ++ ({0}).data))", pure: true, result: "S"},
	"Concat":     {params: []string{"S"}, code: "({k} ({r} ++ ({0}).data))", pure: true, result: "S"},
	"Take":       {params: []string{"I"}, code: "({k} (seqTake {r} {0}))", pure: true, result: "S"},
	"Drop":       {params: []string{"I"}, code: "({k} (seqDrop {r} {0}))", pure: true, result: "S"},
	"Reverse":    {code: "({k} {r}.reverse)", pure: true, result: "S"},
	"Contains":   {params: []string{"T"}, code: "({r}.contains {0})", pure: true, result: "B", compares: true},
	"IndexOf":    {params: []string{"T"}, code: "(seqIndexOf {r} {0})", pure: true, result: "I", compares: true},
	"MkString":   {params: []string{"Str"}, code: "(seqMkString {r} {0})", pure: true, result: "Str", prints: true},
	"Map":        {params: []string{"T->_"}, code: "{k} <$> seqMap {r} {0}", result: "M"},
	"Filter":     {params: []string{"T->B"}, code: "{k} <$> seqFilter {r} {0}", result: "S"},
	"ForEach":    {params: []string{"T->"}, code: "seqForEach {r} {0}"},
	"Exists":     {params: []string{"T->B"}, code: "seqExists {r} {0}", result: "B"},
	"ForAll":     {params: []string{"T->B"}, code: "seqForAll {r} {0}", result: "B"},
	"Find":       {params: []string{"T->B"}, code: "seqFind {r} {0}", result: "O"},
	"Count":      {params: []string{"T->B"}, code: "seqCount {r} {0}", result: "I"},
	"FoldLeft":   {params: []string{"U", "U,T->U"}, code: "seqFoldLeft {r} {0} {1}", result: "U"},
	"FoldRight":  {params: []string{"U", "T,U->U"}, code: "seqFoldRight {r} {0} {1}", result: "U"},
	"Reduce":     {params: []string{"T,T->T"}, code: "seqReduce {r} {0}", result: "T"},
	"Partition": {params: []string{"T->B"}, code: "(fun (yes, no) => T2.mk ({k} yes) ({k} no)) <$> seqPartition {r} {0}",
		result: "P"},
	"ZipWithIndex": {code: "({k} (seqZipWithIndex {r}))", pure: true, result: "Z"},
	"ToList":       {code: "(GList.mk {r})", pure: true, result: "L", only: "GArray"},
	"ToArray":      {code: "(GArray.mk {r})", pure: true, result: "A", only: "GList"},
	"FlatMap":      {params: []string{"T->S_"}, code: "{k} <$> seqFlatMap {r} {0} (·.data)", result: "F"},
	"Collect":      {params: []string{"T->O_"}, code: "{k} <$> seqCollect {r} {0}", result: "C"},
	"Zip":          {params: []string{"S_"}, code: "({k} (seqZip {r} ({0}).data))", pure: true, result: "Y"},
	"TakeWhile":    {params: []string{"T->B"}, code: "{k} <$> seqTakeWhile {r} {0}", result: "S"},
	"DropWhile":    {params: []string{"T->B"}, code: "{k} <$> seqDropWhile {r} {0}", result: "S"},
	"Distinct":     {code: "({k} (seqDistinct {r}))", pure: true, result: "S", compares: true},
	"FindLast":     {params: []string{"T->B"}, code: "seqFindLast {r} {0}", result: "O", only: "GArray"},
	"Grouped":      {params: []string{"I"}, code: "GArray.mk <$> seqGrouped {r} {0}", result: "G", only: "GArray"},
}

// seqMethodOf is function name of a collection of kind, when modelled.
func seqMethodOf(kind, name string) (seqMethod, bool) {
	m, ok := seqMethods[name]
	if !ok || m.only != "" && m.only != kind {
		return seqMethod{}, false
	}
	return m, true
}

// seqCall translates a call of a function of an Array or a List: the
// receiver, then the arguments in the order written, then the function.
// It reports false when the receiver is not plainly a collection.
func (g *gen) seqCall(e *coreexport.Call, s *coreexport.Select, sc *scope) (term, bool) {
	if !g.collections {
		return term{}, false
	}
	kind, elem, ok := seqOf(g.typeOf(s.Target, sc))
	if !ok {
		return term{}, false
	}
	m, ok := seqMethodOf(kind, s.Name)
	if !ok {
		unsup(s.Pos, "collection-method")
	}
	if len(e.Args) != len(m.params) {
		unsup(e.Pos, "argument-count")
	}
	if m.compares {
		g.comparableType(elem, s.Pos)
	}
	if m.prints {
		g.printableType(elem, s.Pos)
	}
	recv := g.expr(s.Target, sc)
	subs := []term{recv}
	repl := []string{"{r}", "(" + recv.val() + ").data", "{k}", kind + ".mk"}
	var u coreexport.Node
	for i, a := range e.Args {
		positional(a)
		t := g.exprExpect(a, sc, g.seqParamType(m.params[i], elem, kind, u))
		if m.params[i] == "U" {
			u = g.galaType(g.typeOf(a, sc))
		}
		subs = append(subs, t)
		repl = append(repl, fmt.Sprintf("{%d}", i), t.val())
	}
	code := strings.NewReplacer(repl...).Replace(m.code)
	if m.pure {
		return wrap(code, subs...), true
	}
	return actUnit(code, m.result == ""), true
}

// seqParamType is the GALA type a parameter of kind p wants: elem's GALA
// type for T, the collection's for S, u for U, and function types of
// those; nil where it is not known.
func (g *gen) seqParamType(p, elem, kind string, u coreexport.Node) coreexport.Node {
	t := g.galaType(elem)
	if t == nil {
		t = &coreexport.TypeName{Name: unknownTypeArg}
	}
	if u == nil {
		u = &coreexport.TypeName{Name: unknownTypeArg}
	}
	one := func(x string) coreexport.Node {
		switch x {
		case "T":
			return t
		case "U":
			return u
		case "B":
			return &coreexport.TypeName{Name: "bool"}
		case "I":
			return &coreexport.TypeName{Name: "int"}
		case "Str":
			return &coreexport.TypeName{Name: "string"}
		case "S":
			if s := g.galaType("(" + kind + " " + elem + ")"); s != nil {
				return s
			}
			return &coreexport.TypeName{Name: unknownTypeArg}
		case "_":
			return &coreexport.TypeName{Name: inferTypeArg}
		case "S_":
			return &coreexport.TypeName{Name: seqGalaName[kind], Args: []coreexport.Node{&coreexport.TypeName{Name: inferTypeArg}}}
		case "O_":
			return optionOfInferred()
		}
		return nil
	}
	ps, res, isFunc := strings.Cut(p, "->")
	if !isFunc {
		return one(p)
	}
	f := &coreexport.TypeFunc{}
	for _, x := range strings.Split(ps, ",") {
		f.Params = append(f.Params, one(x))
	}
	if res != "" {
		f.Result = one(res)
	}
	return f
}

// seqGalaName is the GALA name of each collection kind.
var seqGalaName = map[string]string{"GArray": "Array", "GList": "List"}

// seqCallType is exprType of a call of a function of an Array or a List
// whose receiver has Lean type rt.
func (g *gen) seqCallType(e *coreexport.Call, rt string, s *coreexport.Select, sc *scope, seen map[string]bool) string {
	if !g.collections {
		return ""
	}
	kind, elem, ok := seqOf(rt)
	if !ok {
		return ""
	}
	m, ok := seqMethodOf(kind, s.Name)
	if !ok || len(e.Args) != len(m.params) {
		return ""
	}
	switch m.result {
	case "T":
		return elem
	case "S":
		return "(" + kind + " " + elem + ")"
	case "U":
		return g.exprType(argNode(e.Args[0]), sc, seen)
	case "B":
		return "Bool"
	case "I":
		return "Int64"
	case "Str":
		return "String"
	case "O":
		return "(GOption " + elem + ")"
	case "L":
		return "(GList " + elem + ")"
	case "A":
		return "(GArray " + elem + ")"
	case "P":
		return "(T2 (" + kind + " " + elem + ") (" + kind + " " + elem + "))"
	case "Z":
		return "(" + kind + " (T2 " + elem + " Int64))"
	case "G":
		return "(GArray (" + kind + " " + elem + "))"
	case "Y":
		if _, u, ok := seqOf(g.exprType(argNode(e.Args[0]), sc, seen)); ok {
			return "(" + kind + " (T2 " + elem + " " + u + "))"
		}
	case "M", "F", "C":
		r := g.funcArgResult(e.Args[0], g.seqParamType(m.params[0], elem, kind, nil), sc, seen)
		if r == "" {
			return ""
		}
		switch m.result {
		case "M":
			return "(" + kind + " " + r + ")"
		case "F":
			return r
		}
		if p := parseLean(r); p.head == "GOption" && len(p.args) == 1 {
			return "(" + kind + " " + p.args[0].String() + ")"
		}
	}
	return ""
}
