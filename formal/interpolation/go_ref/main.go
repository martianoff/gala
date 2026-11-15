// Command go_ref is the differential oracle for the Lean interpolation port.
//
// In its default mode it reads `c <hex>` lines, calls the real Go
// `internal/interpolation.Split`, and prints the same stable text format the
// Lean binary prints:
//
//	L <offset> <text-hex>
//	E <offset> <format-spec-hex> <text-hex>
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

func emitCase(w *bufio.Writer, b []byte) {
	w.WriteString("c ")
	w.WriteString(hex.EncodeToString(b))
	w.WriteByte('\n')
}

func generateExhaustive(w *bufio.Writer) {
	alphabet := []byte(`${}\"%a0d_`)
	var walk func(prefix []byte, depth int)
	walk = func(prefix []byte, depth int) {
		emitCase(w, prefix)
		if depth == 0 {
			return
		}
		for _, b := range alphabet {
			walk(append(prefix, b), depth-1)
		}
	}
	walk(nil, 5)
}

func generate(w *bufio.Writer, n int, seed int64) {
	for _, s := range curated {
		emitCase(w, []byte(s))
	}

	// Exhaustive token sequences of length 0..4.
	alphabet := []string{"$", "{", "}", `\`, `"`, "%", "a", "0", "d", " ", "é"}
	var walk func(prefix string, depth int)
	walk = func(prefix string, depth int) {
		emitCase(w, []byte(prefix))
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
		emitCase(w, []byte(sb.String()))
	}

	// Raw-byte fuzz, including bytes that are not valid UTF-8.
	rawAlphabet := []byte(`$ { } \ " % a 1 0 d _ ` + "\n\t\x80\xff")
	for i := 0; i < n; i++ {
		length := rng.Intn(33)
		b := make([]byte, length)
		for j := range b {
			b[j] = rawAlphabet[rng.Intn(len(rawAlphabet))]
		}
		emitCase(w, b)
	}
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
		if !strings.HasPrefix(line, "c ") {
			fmt.Fprintf(os.Stderr, "go_ref: malformed case line: %q\n", line)
			os.Exit(1)
		}
		raw, err := hex.DecodeString(line[2:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "go_ref: bad hex: %v\n", err)
			os.Exit(1)
		}
		parts := interpolation.Split(string(raw))
		for _, p := range parts {
			if p.IsLiteral {
				fmt.Fprintf(out, "L %d %s\n", p.Offset, hex.EncodeToString([]byte(p.Text)))
			} else {
				fmt.Fprintf(out, "E %d %s %s\n", p.Offset,
					hex.EncodeToString([]byte(p.FormatSpec)),
					hex.EncodeToString([]byte(p.Text)))
			}
		}
		fmt.Fprintln(out, "--")
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "go_ref: read: %v\n", err)
		os.Exit(1)
	}
}
