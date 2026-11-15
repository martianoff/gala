// Command go_ref is the differential oracle for the Lean interpolation port.
//
// In its default mode it reads case lines, calls the real Go
// `internal/interpolation`, and prints the same stable text format the Lean
// binary prints:
//
//	s <hex>                       Split(content, false), an s"..." body
//	f <hex>                       Split(content, true), an f"..." body
//	p <off> <line> <col> <hex>    Position(content, off, line, col)
//
// Each case prints its parts (or position) and then `--`:
//
//	L <offset> <start> <end> <text-hex>
//	E <offset> <start> <end> <spec-hex> <text-hex> <printed>
//	P <line> <column>
//	--
//
// With -gen N it emits deterministic curated, exhaustive, and fuzz cases;
// with -exhaustive it emits every raw byte string up to length 5 over a
// syntax-heavy alphabet.
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

	"martianoff/gala/internal/interpolation"
)

var curated = []string{
	"",
	"hello",
	"a $name b",
	"$$",
	"a$$b",
	"${x + 1}",
	"${a{b}c}",
	"${}",
	"${",
	"${a",
	"${a}",
	"${a}%04d",
	"$name%s",
	"$name%q",
	"${x}%i",
	"$1",
	`\$x`,
	`\`,
	`${a\"b}`,
	`${a\\\"b}`,
	`${a\{b\}}`,
	`${a\}b}`,
	`${a{b}c}%s`,
	`${a}%#x`,
	`${a}% d`,
	`${a}%+d`,
	`${a}%-5.2f`,
	`${a}%*d`,
	`${a}%.*f`,
	`${a}%`,
	`${a}%z`,
	`${a}%04`,
	`${a}%04d%s`,
	`$a$b`,
	"héllo ${π}",
	"π=$π",
	"tab\t$tab\n",
	"quote\"$q",
	"back\\slash$s",
	`\\$x`,
	`\${x}`,
	"$",
	"$$$",
	"${x}${y}",
	"${x}%s${y}%d",
}

var fuzzTokens = []string{
	"$", "$$", "${", "}", "{", `\`, `\"`, "%", "%04d", "%s", "%q", "%i",
	"a", "_x", "x1", "1", " ", `"`, "é", "π", "\n", "\t", "xy", "%#x", "%.2f",
}

func emitSplit(w *bufio.Writer, b []byte, formatSpecs bool) {
	kind := "s"
	if formatSpecs {
		kind = "f"
	}
	fmt.Fprintf(w, "%s %s\n", kind, hex.EncodeToString(b))
}

func emitPos(w *bufio.Writer, b []byte, off, line, col int) {
	fmt.Fprintf(w, "p %d %d %d %s\n", off, line, col, hex.EncodeToString(b))
}

func generateExhaustive(w *bufio.Writer) {
	alphabet := []byte(`${}\"%a0d_`)
	var walk func(prefix []byte, depth, max int)
	walk = func(prefix []byte, depth, max int) {
		emitSplit(w, prefix, false)
		emitSplit(w, prefix, true)
		if depth <= 3 {
			for off := 0; off <= len(prefix); off++ {
				emitPos(w, prefix, off, 3, 7)
			}
		}
		if depth == max {
			return
		}
		for _, b := range alphabet {
			walk(append(prefix, b), depth+1, max)
		}
	}
	walk(nil, 0, 5)
}

func generate(w *bufio.Writer, n int, seed int64) {
	for _, s := range curated {
		b := []byte(s)
		emitSplit(w, b, false)
		emitSplit(w, b, true)
		emitPos(w, b, 0, 3, 7)
		emitPos(w, b, len(b)/2, 3, 7)
		emitPos(w, b, len(b), 1, 0)
	}

	// Exhaustive token sequences of length 0..4.
	alphabet := []string{"$", "{", "}", `\`, `"`, "%", "a", "0", "d", " ", "é"}
	var walk func(prefix string, depth int)
	walk = func(prefix string, depth int) {
		emitSplit(w, []byte(prefix), false)
		emitSplit(w, []byte(prefix), true)
		if depth == 0 {
			return
		}
		for _, tok := range alphabet {
			walk(prefix+tok, depth-1)
		}
	}
	// The recursion above emits a duplicate at every node (once for the prefix,
	// once as the first leaf of each parent); duplicates are harmless and keep
	// the generator small.
	walk("", 4)

	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < n; i++ {
		var sb strings.Builder
		tokens := rng.Intn(13)
		for j := 0; j < tokens; j++ {
			sb.WriteString(fuzzTokens[rng.Intn(len(fuzzTokens))])
		}
		b := []byte(sb.String())
		emitSplit(w, b, false)
		emitSplit(w, b, true)
		emitPos(w, b, rng.Intn(max(1, len(b)+1)), 1+rng.Intn(4), rng.Intn(8))
	}

	// Raw-byte fuzz, including bytes that are not valid UTF-8.
	rawAlphabet := []byte(`$ { } \ " % a 1 0 d _ ` + "\n\t\x80\xff")
	for i := 0; i < n; i++ {
		length := rng.Intn(33)
		b := make([]byte, length)
		for j := range b {
			b[j] = rawAlphabet[rng.Intn(len(rawAlphabet))]
		}
		emitSplit(w, b, false)
		emitSplit(w, b, true)
		emitPos(w, b, rng.Intn(length+1), 1+rng.Intn(4), rng.Intn(8))
	}
}

func decodeHex(s string) ([]byte, error) {
	return hex.DecodeString(s)
}

func main() {
	gen := flag.Int("gen", 0, "emit N fuzz cases per family instead of splitting stdin")
	seed := flag.Int64("seed", 1, "fuzz seed")
	exhaustive := flag.Bool("exhaustive", false, "emit exhaustive raw-byte cases up to length 5")
	flag.Parse()

	if *exhaustive {
		w := bufio.NewWriter(os.Stdout)
		defer w.Flush()
		generateExhaustive(w)
		return
	}
	if *gen > 0 {
		w := bufio.NewWriter(os.Stdout)
		defer w.Flush()
		generate(w, *gen, *seed)
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, " ")
		switch fields[0] {
		case "s", "f":
			if len(fields) != 2 {
				fmt.Fprintf(os.Stderr, "go_ref: malformed case line: %q\n", line)
				os.Exit(1)
			}
			raw, err := decodeHex(fields[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "go_ref: bad hex: %v\n", err)
				os.Exit(1)
			}
			for _, p := range interpolation.Split(string(raw), fields[0] == "f") {
				printed := "0"
				if p.PrintedByString() {
					printed = "1"
				}
				if p.IsLiteral {
					fmt.Fprintf(out, "L %d %d %d %s\n", p.Offset, p.Start, p.End,
						hex.EncodeToString([]byte(p.Text)))
				} else {
					fmt.Fprintf(out, "E %d %d %d %s %s %s\n", p.Offset, p.Start, p.End,
						hex.EncodeToString([]byte(p.FormatSpec)),
						hex.EncodeToString([]byte(p.Text)), printed)
				}
			}
		case "p":
			if len(fields) != 5 {
				fmt.Fprintf(os.Stderr, "go_ref: malformed case line: %q\n", line)
				os.Exit(1)
			}
			off, err1 := strconv.Atoi(fields[1])
			lineNo, err2 := strconv.Atoi(fields[2])
			col, err3 := strconv.Atoi(fields[3])
			raw, err4 := decodeHex(fields[4])
			if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
				fmt.Fprintf(os.Stderr, "go_ref: malformed position case: %q\n", line)
				os.Exit(1)
			}
			l, c := interpolation.Position(string(raw), off, lineNo, col)
			fmt.Fprintf(out, "P %d %d\n", l, c)
		default:
			fmt.Fprintf(os.Stderr, "go_ref: malformed case line: %q\n", line)
			os.Exit(1)
		}
		fmt.Fprintln(out, "--")
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "go_ref: read: %v\n", err)
		os.Exit(1)
	}
}
