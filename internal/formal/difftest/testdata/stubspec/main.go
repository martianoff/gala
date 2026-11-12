// Command stubspec stands in for galaspec in the harness's own tests. It
// implements galaspec's command-line contract for a tiny language: a main
// whose statements are `val name = expr`, `Println(expr)`/`Print(expr)` and
// Go's builtin `println(expr)` (to stderr),
// where an expression is an int, bool or string literal, a name, or + - * /
// on ints. Any other construct is unsupported, as `stub-<kind>`.
//
// It lies on purpose about one string: Println("stub:lie") prints "truth",
// so the tests can see a disagreement end to end.
//
// Usage: stubspec run <file.json> [--fuel N]. Each statement costs one step.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"martianoff/gala/internal/formal/coreexport"
)

// Exit statuses and outcomes, as galaspec has them.
const (
	exitOK = iota
	exitPanic
	exitFuel
	exitUnsupported
	exitMalformed
)

func main() {
	status, outcome := run(os.Args[1:])
	fmt.Fprintf(os.Stderr, "galaspec: outcome=%s\n", outcome)
	os.Exit(status)
}

type node map[string]any

func (n node) kind() string { s, _ := n["kind"].(string); return s }

func (n node) child(f string) node { c, _ := n[f].(map[string]any); return c }

func (n node) list(f string) []node {
	raw, _ := n[f].([]any)
	out := make([]node, 0, len(raw))
	for _, r := range raw {
		c, _ := r.(map[string]any)
		out = append(out, c)
	}
	return out
}

// stop ends evaluation with an exit status and outcome.
type stop struct {
	status  int
	outcome string
}

// unsupported stops at a construct the stub does not model: an exporter
// Unsupported node by its own name, as galaspec reports it, anything else as
// stub-<what>.
func unsupported(n node, what string) stop {
	if name, ok := n["unsupported"].(string); ok {
		return stop{exitUnsupported, "unsupported:" + name}
	}
	return stop{exitUnsupported, "unsupported:stub-" + what}
}

func run(args []string) (status int, outcome string) {
	fuel := 1 << 20
	var path string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "run":
		case args[i] == "--fuel" && i+1 < len(args):
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return exitMalformed, "malformed"
			}
			fuel = n
			i++
		default:
			path = args[i]
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return exitMalformed, "malformed"
	}
	var doc node
	if json.Unmarshal(data, &doc) != nil || doc["core"] != float64(coreexport.Version) {
		return exitMalformed, "malformed"
	}
	defer func() {
		if r := recover(); r != nil {
			s := r.(stop)
			status, outcome = s.status, s.outcome
		}
	}()
	var body node
	for _, f := range doc.list("files") {
		for _, d := range f.list("decls") {
			if d.kind() != "func" || d["name"] != "main" {
				panic(unsupported(d, d.kind()+"-decl"))
			}
			body = d.child("body")
		}
	}
	if body.kind() != "block" {
		panic(unsupported(body, "main"))
	}
	env := map[string]any{}
	for _, st := range body.list("stmts") {
		if fuel--; fuel < 0 {
			return exitFuel, "fuel"
		}
		switch st.kind() {
		case "val":
			names, values := st["names"].([]any), st.list("values")
			if len(names) != 1 || len(values) != 1 {
				panic(unsupported(st, "val"))
			}
			env[names[0].(string)] = eval(values[0], env)
		case "call":
			callee := st.child("callee")
			args := st.list("args")
			if callee.kind() != "name" || len(args) != 1 {
				panic(unsupported(st, "call"))
			}
			v := eval(args[0], env)
			if v == "stub:lie" {
				v = "truth"
			}
			switch callee["name"] {
			case "Println":
				fmt.Println(v)
			case "Print":
				fmt.Print(v)
			case "println": // Go's builtin, which writes to stderr
				fmt.Fprintln(os.Stderr, v)
			default:
				panic(unsupported(st, "call"))
			}
		default:
			panic(unsupported(st, st.kind()))
		}
	}
	return exitOK, "ok"
}

func eval(e node, env map[string]any) any {
	switch e.kind() {
	case "int":
		n, err := strconv.Atoi(e["text"].(string))
		if err != nil {
			panic(unsupported(e, "int"))
		}
		return n
	case "bool":
		return e["value"]
	case "string":
		return e["value"]
	case "name":
		v, ok := env[e["name"].(string)]
		if !ok {
			panic(unsupported(e, "name"))
		}
		return v
	case "binary":
		l, lok := eval(e.child("left"), env).(int)
		r, rok := eval(e.child("right"), env).(int)
		if !lok || !rok {
			panic(unsupported(e, "binary"))
		}
		switch e["op"] {
		case "+":
			return l + r
		case "-":
			return l - r
		case "*":
			return l * r
		case "/":
			if r == 0 {
				panic(stop{exitPanic, "panic:divide"})
			}
			return l / r
		}
	}
	panic(unsupported(e, e.kind()))
}
