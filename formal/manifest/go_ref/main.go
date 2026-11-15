// Command go_ref is the differential oracle for the Lean gala.mod port.
//
// It reads `c|<content-hex>|<op>|...` cases, calls the real
// `internal/depman/mod` parser, formatter and file methods, and prints the
// same stable format the Lean binary prints:
//
//	P
//	module <hex path>
//	gala <hex version>
//	require <hex path> <hex version> <0|1 indirect> <0|1 go>
//	replace <hex old path> <hex old version> <hex new path> <hex new version>
//	exclude <hex path> <hex version>
//	O
//	<one line per operation that returns something>
//	F <hex of mod.Format>
//	--
//
// A parse error prints `E <hex of *mod.ParseError.Error()>` and `--` instead.
// Operations are `a:<path>:<version>:<indirect>` (AddRequire),
// `d:<path>` (RemoveRequire), `g:<path>` (GetRequire),
// `p:<old path>:<old version>:<new path>:<new version>` (AddReplace),
// `e:<path>:<version>` (AddExclude), `direct`, `indirect`, `gala`, `go`
// (the filtered views) and `format`; all string parameters are hex.
//
// With -gen N it emits curated and N random cases; with -exhaustive it emits
// every sequence of up to three lines from a fixed pool.
package main

import (
	"bufio"
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"unicode"

	"martianoff/gala/internal/depman/mod"
)

func bit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func hx(s string) string {
	return hex.EncodeToString([]byte(s))
}

func unhex(s string) (string, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("bad hex %q: %w", s, err)
	}
	return string(b), nil
}

func dump(f *mod.File) string {
	var b strings.Builder
	b.WriteString("P\n")
	fmt.Fprintf(&b, "module %s\n", hx(f.Module.Path))
	fmt.Fprintf(&b, "gala %s\n", hx(f.Gala))
	for _, r := range f.Require {
		fmt.Fprintf(&b, "require %s %s %s %s\n", hx(r.Path), hx(r.Version), bit(r.Indirect), bit(r.Go))
	}
	for _, r := range f.Replace {
		fmt.Fprintf(&b, "replace %s %s %s %s\n",
			hx(r.Old.Path), hx(r.Old.Version), hx(r.New.Path), hx(r.New.Version))
	}
	for _, e := range f.Exclude {
		fmt.Fprintf(&b, "exclude %s %s\n", hx(e.Path), hx(e.Version))
	}
	return b.String()
}

func pathsLine(name string, reqs []mod.Require) string {
	parts := []string{name, strconv.Itoa(len(reqs))}
	for _, r := range reqs {
		parts = append(parts, hx(r.Path))
	}
	return strings.Join(parts, " ")
}

func applyOp(f *mod.File, op string) (*mod.File, []string, error) {
	parts := strings.Split(op, ":")
	switch {
	case len(parts) == 4 && parts[0] == "a":
		path, err := unhex(parts[1])
		if err != nil {
			return nil, nil, err
		}
		version, err := unhex(parts[2])
		if err != nil {
			return nil, nil, err
		}
		switch parts[3] {
		case "0":
			f.AddRequire(path, version, false)
		case "1":
			f.AddRequire(path, version, true)
		default:
			return nil, nil, fmt.Errorf("bad indirect flag %q", parts[3])
		}
		return f, nil, nil
	case len(parts) == 2 && parts[0] == "d":
		path, err := unhex(parts[1])
		if err != nil {
			return nil, nil, err
		}
		found := f.RemoveRequire(path)
		return f, []string{"remove " + bit(found)}, nil
	case len(parts) == 2 && parts[0] == "g":
		path, err := unhex(parts[1])
		if err != nil {
			return nil, nil, err
		}
		r := f.GetRequire(path)
		if r == nil {
			return f, []string{"get 0"}, nil
		}
		return f, []string{fmt.Sprintf("get 1 %s %s %s", hx(r.Version), bit(r.Indirect), bit(r.Go))}, nil
	case len(parts) == 5 && parts[0] == "p":
		oldPath, err := unhex(parts[1])
		if err != nil {
			return nil, nil, err
		}
		oldVersion, err := unhex(parts[2])
		if err != nil {
			return nil, nil, err
		}
		newPath, err := unhex(parts[3])
		if err != nil {
			return nil, nil, err
		}
		newVersion, err := unhex(parts[4])
		if err != nil {
			return nil, nil, err
		}
		f.AddReplace(oldPath, oldVersion, newPath, newVersion)
		return f, nil, nil
	case len(parts) == 3 && parts[0] == "e":
		path, err := unhex(parts[1])
		if err != nil {
			return nil, nil, err
		}
		version, err := unhex(parts[2])
		if err != nil {
			return nil, nil, err
		}
		f.AddExclude(path, version)
		return f, nil, nil
	case len(parts) == 1 && parts[0] == "direct":
		return f, []string{pathsLine("direct", f.DirectRequires())}, nil
	case len(parts) == 1 && parts[0] == "indirect":
		return f, []string{pathsLine("indirect", f.IndirectRequires())}, nil
	case len(parts) == 1 && parts[0] == "gala":
		return f, []string{pathsLine("gala", f.GalaRequires())}, nil
	case len(parts) == 1 && parts[0] == "go":
		return f, []string{pathsLine("go", f.GoRequires())}, nil
	case len(parts) == 1 && parts[0] == "format":
		return f, []string{"format " + hx(mod.Format(f))}, nil
	}
	return nil, nil, fmt.Errorf("unknown operation %q", op)
}

func render(content string, ops []string) (string, error) {
	f, err := mod.Parse(content)
	if err != nil {
		return "E " + hx(err.Error()) + "\n--\n", nil
	}
	var opLines []string
	for _, op := range ops {
		var lines []string
		f, lines, err = applyOp(f, op)
		if err != nil {
			return "", fmt.Errorf("%s: %w", op, err)
		}
		opLines = append(opLines, lines...)
	}
	var b strings.Builder
	b.WriteString(dump(f))
	b.WriteString("O\n")
	for _, l := range opLines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "F %s\n--\n", hx(mod.Format(f)))
	return b.String(), nil
}

func parseCase(line string) (string, []string, error) {
	parts := strings.Split(line, "|")
	if len(parts) < 2 || parts[0] != "c" {
		return "", nil, fmt.Errorf("malformed case line %q", line)
	}
	content, err := unhex(parts[1])
	if err != nil {
		return "", nil, err
	}
	return content, parts[2:], nil
}

func run(w *bufio.Writer) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		content, ops, err := parseCase(line)
		if err != nil {
			return err
		}
		out, err := render(content, ops)
		if err != nil {
			return err
		}
		if _, err := w.WriteString(out); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func emitCase(w *bufio.Writer, content string, ops []string) {
	fmt.Fprintf(w, "c|%s", hx(content))
	for _, op := range ops {
		fmt.Fprintf(w, "|%s", op)
	}
	fmt.Fprintln(w)
}

func opAdd(path, version string, indirect bool) string {
	return "a:" + hx(path) + ":" + hx(version) + ":" + bit(indirect)
}

func opRemove(path string) string { return "d:" + hx(path) }
func opGet(path string) string    { return "g:" + hx(path) }
func opExclude(path, version string) string {
	return "e:" + hx(path) + ":" + hx(version)
}

func opReplace(oldPath, oldVersion, newPath, newVersion string) string {
	return "p:" + hx(oldPath) + ":" + hx(oldVersion) + ":" + hx(newPath) + ":" + hx(newVersion)
}

// curatedManifests are the shapes the parser has to get exactly right.
var curatedManifests = []string{
	"",
	"module example.com/app\n",
	"module example.com/app\n\ngala 1.0\n",
	"module m",
	"module m\n\ngala 9.9\n",
	"module example.com/app\n\ngala 1.0\n\nrequire github.com/example/utils v1.2.3\n",
	"module example.com/app\n\ngala 1.0\n\nrequire (\n\tgithub.com/example/utils v1.2.3\n\tgithub.com/example/math v2.0.0\n)\n",
	"module m\n\nrequire a v // indirect\n",
	"module m\n\nrequire a v // go\n",
	"module m\n\nrequire a v // indirect, go\n",
	"module m\n\nrequire a v // go, indirect\n",
	"module m\n\nrequire a v // indirect,go\n",
	"module m\n\nrequire a v // INDIRECT\n",
	"module m\n\nrequire a v // indirect, other\n",
	"module m\n\nrequire a v //\n",
	"module m\n\nrequire a v extra\n",
	"module m\n\nrequire (\n\ta v\n\tb w // indirect\n\tc x // go\n)\n",
	"module m\n\nrequire (\n// indirect\n\ta v\n\tb w\n)\n",
	"module m\n\nrequire ( // indirect\n\ta v\n)\n",
	"module m\n\nrequire (\n\ta v\n\n\t// go\n\tb w\n)\n",
	"module m\n\nrequire (\nrequire (\n)\n",
	"module m\n\nrequire (\nreplace a => b\n)\n",
	"module m\n\nrequire (\ngala 1.0\n)\n",
	"module m\n\nrequire (\n)\n",
	"module m\n\nrequire (\n",
	"module m\n\nrequire\n",
	"module m\n\nreplace a => ../local\n",
	"module m\n\nreplace a v1.0.0 => b v1.1.0\n",
	"module m\n\nreplace a => b v2\n",
	"module m\n\nreplace a v1 => b\n",
	"module m\n\nreplace (\n\ta => ../a\n\tb v1 => c v2\n)\n",
	"module m\n\nreplace (\n)\n",
	"module m\n\nreplace a => b\nreplace a => c\n",
	"module m\n\nreplace\n",
	"module m\n\nreplace a\n",
	"module m\n\nreplace => b\n",
	"module m\n\nreplace a =>\n",
	"module m\n\nexclude a v0.9.0\n",
	"module m\n\nexclude (\n\ta v1\n\tb v2\n)\n",
	"module m\n\nexclude (\n)\n",
	"module m\n\nexclude\n",
	"module m\n\nexclude a\n",
	"module m\n\nexclude a v\nexclude a v\n",
	"module m\n\nexclude a v\nexclude a w\n",
	"module m\n\nmodule n\ngala 2.0\n",
	"gala 1.0\nmodule m\n",
	"module m // trailing\n",
	"module m//x\n",
	"module\n",
	"gala\n",
	"module m\nunknown thing\n",
	"module m\nweird (\n",
	"module m\n(\n",
	"module m\n)\n",
	"module m\n)\nrequire a v\n",
	"\uFEFFmodule m\n",
	"\uFEFF\uFEFFmodule m\n",
	"module m\r\n\r\ngala 1.0\r\n",
	"module m\n\nrequire \u00A0a\u00A0v\u00A0\n",
	"module m\n\nrequire \u3000a\u3000v\n",
	"\vrequire\t a\v v\n",
	"module m\n\nrequire a//b v\n",
	"module m\n\nrequire \"a b\" v\n",
	"module m\n\nrequire a v\nrequire a w\n",
	"module m\n\nrequire a v\n\nrequire b w // indirect\n",
	"   \n\t\n",
	"\n\n\n",
	"// only a comment\n",
	"module m\n// trailing comment\n",
	"module m\n\nrequire (\n\ta v\n)\nrequire (\n\tb w\n)\n",
	"module m\n\nrequire a v // indirect // go\n",
	"module m\n\nrequire(\n\ta v\n)\n",
	"module m (\n",
	"module m\nreplace a=>b\n",
	" \uFEFFmodule m\n",
	"module m\n\nrequire a v // indirect\nrequire b w\n",
	"module m\n\nrequire a v // go , indirect\n",
	"module m\n\nreplace a b c => d\n",
	"module m\n\nreplace a => b c\n",
	"module m\n\nrequire ( \n\ta v\n) // done\n",
}

// curatedOpCases run the file methods on known parses.
var curatedOpCases = []struct {
	manifest string
	ops      []string
}{
	{"module m\n", []string{opAdd("a", "v1", false), opAdd("b", "v2", true)}},
	{"module m\n", []string{opAdd("a", "v1", false), opAdd("a", "v2", true)}},
	{"module m\n", []string{opAdd("a", "v1", true), opAdd("a", "v1", false)}},
	{"module m\n\nrequire a v // go\n", []string{opAdd("a", "w", true)}},
	{"module m\n\nrequire a v\nrequire a w\n", []string{opAdd("a", "z", false)}},
	{"module m\n\nrequire a v\nrequire b w\n", []string{opRemove("a"), opRemove("a")}},
	{"module m\n\nrequire a v\n", []string{opGet("a"), opGet("z")}},
	{"module m\n\nrequire a v // indirect\n", []string{opGet("a")}},
	{"module m\n", []string{opReplace("a", "", "b", ""), opReplace("a", "", "c", "v2")}},
	{"module m\n\nreplace a => b\n", []string{opReplace("a", "", "z", "v9")}},
	{"module m\n", []string{opExclude("a", "v1"), opExclude("a", "v1"), opExclude("a", "v2")}},
	{"module m\n\nrequire a v\nrequire b v // indirect\nrequire c v // go\nrequire d v // go, indirect\n",
		[]string{"direct", "indirect", "gala", "go", "format"}},
	{"module m\n", []string{opAdd("", "", false), opAdd(" ", "  ", true), opAdd("a//b", "v=>w", false)}},
	{"module m\n", []string{opAdd("é", "π", true), opAdd("a\nb", "c\nd", false)}},
	{"", []string{"format"}},
	{"", []string{opAdd("x", "1", false), "format"}},
	{"", []string{opAdd("a", "v", false), opAdd("a", "", false), "format"}},
	{"module m\n", []string{opAdd("a", "v", false), opRemove("a"), opGet("a"), "format"}},
	{"module m\n\nrequire a v\n\nreplace a => b\n", []string{"format"}},
	{"module m", []string{opAdd("a", "v", false), opAdd("b", "w", false), opRemove("a")}},
}

var randomPaths = []string{
	"a", "b", "c", "github.com/x/y", "../local", "./rel", ".", "/abs",
	"\\win", "x//y", "é/π", `"q"`, "a>b", "a=>b", "", "a b", "x:y",
}

var randomVersions = []string{"v1.2.3", "1.0", "", "^1.0.0", "v0.0.0-x", "latest", "π"}

func randPath(r *rand.Rand) string    { return randomPaths[r.Intn(len(randomPaths))] }
func randVersion(r *rand.Rand) string { return randomVersions[r.Intn(len(randomVersions))] }

func randMarker(r *rand.Rand) string {
	markers := []string{
		"", " // indirect", " // go", " // go, indirect", " // indirect, go",
		" // other", " //", " // indirect,INDIRECT", " //  go , indirect",
	}
	return markers[r.Intn(len(markers))]
}

func randMarkerBody(r *rand.Rand) string {
	bodies := []string{"indirect", "go", "go, indirect", "indirect, go", "x", "indirect , go", ""}
	return bodies[r.Intn(len(bodies))]
}

func randomLine(r *rand.Rand) string {
	switch r.Intn(24) {
	case 0:
		return ""
	case 1:
		return "module " + randPath(r)
	case 2:
		return "gala " + randVersion(r)
	case 3:
		return "require " + randPath(r) + " " + randVersion(r) + randMarker(r)
	case 4:
		return "require"
	case 5:
		return "require ("
	case 6:
		return ")"
	case 7:
		return "replace"
	case 8:
		oldVersion := ""
		if r.Intn(2) == 0 {
			oldVersion = " " + randVersion(r)
		}
		newVersion := ""
		if r.Intn(2) == 0 {
			newVersion = " " + randVersion(r)
		}
		return "replace " + randPath(r) + oldVersion + " => " + randPath(r) + newVersion
	case 9:
		return "exclude " + randPath(r) + " " + randVersion(r)
	case 10:
		return "// " + randMarkerBody(r)
	case 11:
		return "unknown " + randPath(r)
	case 12:
		return randPath(r) + " " + randVersion(r)
	case 13:
		return "\t" + randPath(r) + "\t" + randVersion(r) + randMarker(r)
	case 14:
		return "\u00A0require\u00A0" + randPath(r) + "\u00A0" + randVersion(r)
	case 15:
		return "module"
	case 16:
		return "gala"
	case 17:
		return "exclude"
	case 18:
		return "( // go"
	case 19:
		return "require ( // indirect"
	case 20:
		return "replace ("
	case 21:
		return "exclude ("
	case 22:
		return "  " + "  "
	default:
		return randPath(r) + " " + randVersion(r) + randMarker(r)
	}
}

func randomManifest(r *rand.Rand) string {
	var b strings.Builder
	if r.Intn(8) == 0 {
		b.WriteString("\uFEFF")
	}
	sep := "\n"
	if r.Intn(8) == 0 {
		sep = "\r\n"
	}
	n := r.Intn(9)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(randomLine(r))
	}
	if r.Intn(2) == 0 {
		b.WriteString(sep)
	}
	return b.String()
}

func randomOps(r *rand.Rand) []string {
	n := r.Intn(6)
	ops := make([]string, 0, n)
	for i := 0; i < n; i++ {
		switch r.Intn(11) {
		case 0, 1:
			ops = append(ops, opAdd(randPath(r), randVersion(r), r.Intn(2) == 0))
		case 2:
			ops = append(ops, opRemove(randPath(r)))
		case 3:
			ops = append(ops, opGet(randPath(r)))
		case 4, 5:
			ops = append(ops, opReplace(randPath(r), randVersion(r), randPath(r), randVersion(r)))
		case 6:
			ops = append(ops, opExclude(randPath(r), randVersion(r)))
		case 7:
			ops = append(ops, []string{"direct", "indirect", "gala", "go"}[r.Intn(4)])
		case 8:
			ops = append(ops, "format")
		case 9:
			ops = append(ops, opAdd(randPath(r)+"\n"+randPath(r), randVersion(r)+"\t", r.Intn(2) == 0))
		case 10:
			ops = append(ops, opReplace("", "", randPath(r), ""))
		}
	}
	return ops
}

var opBaseManifests = []string{
	"module m\n",
	"",
	"module m\n\nrequire a v\nrequire b v // indirect\nrequire c v // go\n",
	"module m\n\nrequire a v\n\nreplace a => b\n",
	"module m\n\nrequire (\n\ta v\n\tb w // go\n)\n\nexclude a v\n",
}

func emitCurated(w *bufio.Writer) {
	for _, content := range curatedManifests {
		emitCase(w, content, nil)
	}
	// Every rune Go's strings.TrimSpace and bufio.ScanWords treat as
	// whitespace, leading, between tokens and trailing. Lean's
	// Char.isWhitespace is only ASCII space, tab, CR and LF, so the port
	// spells Go's set out; this pins the whole set.
	for r := rune(0); r <= 0x3000; r++ {
		if !unicode.IsSpace(r) {
			continue
		}
		space := string(r)
		emitCase(w, "module"+space+"m"+space+"\n", nil)
		emitCase(w, space+"require a v"+space+"\n", nil)
		emitCase(w, "module m\n\nrequire"+space+"a"+space+"v"+space+"\n", nil)
		emitCase(w, "module m\n\nrequire a v //"+space+"indirect"+space+"\n", nil)
	}
	for _, c := range curatedOpCases {
		emitCase(w, c.manifest, c.ops)
	}
}

func emitRandom(w *bufio.Writer, n int, seed int64) {
	r := rand.New(rand.NewSource(seed))
	for i := 0; i < n; i++ {
		if i%4 == 3 {
			base := opBaseManifests[r.Intn(len(opBaseManifests))]
			emitCase(w, base, randomOps(r))
		} else {
			emitCase(w, randomManifest(r), nil)
		}
	}
}

var exhaustiveLines = []string{
	"module m",
	"gala 1.0",
	"require",
	"require a v",
	"require (",
	")",
	"replace",
	"replace a => b",
	"exclude",
	"exclude a v",
	"// indirect",
	"// require",
	"unknown",
	"",
	"a v",
}

func emitExhaustive(w *bufio.Writer) {
	emit := func(lines []string) {
		emitCase(w, strings.Join(lines, "\n"), nil)
	}
	emit(nil)
	for _, a := range exhaustiveLines {
		emit([]string{a})
	}
	for _, a := range exhaustiveLines {
		for _, b := range exhaustiveLines {
			emit([]string{a, b})
		}
	}
	for _, a := range exhaustiveLines {
		for _, b := range exhaustiveLines {
			for _, c := range exhaustiveLines {
				emit([]string{a, b, c})
			}
		}
	}
}

func main() {
	gen := flag.Int("gen", 0, "emit curated cases and N random cases")
	seed := flag.Int64("seed", 1, "seed for the random cases")
	exhaustive := flag.Bool("exhaustive", false, "emit exhaustive line sequences")
	flag.Parse()

	if *gen > 0 || *exhaustive {
		w := bufio.NewWriter(os.Stdout)
		emitCurated(w)
		if *gen > 0 {
			emitRandom(w, *gen, *seed)
		}
		if *exhaustive {
			emitExhaustive(w)
		}
		if err := w.Flush(); err != nil {
			fmt.Fprintln(os.Stderr, "go_ref:", err)
			os.Exit(1)
		}
		return
	}

	w := bufio.NewWriter(os.Stdout)
	if err := run(w); err != nil {
		fmt.Fprintln(os.Stderr, "go_ref:", err)
		os.Exit(1)
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "go_ref:", err)
		os.Exit(1)
	}
}
