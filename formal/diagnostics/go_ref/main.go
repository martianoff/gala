// Command go_ref is the differential oracle for the Lean diagnostics port.
//
// It reads `r|color|fallback-path-hex|source-hex|json-hint-hex|error...` lines,
// calls the real `galaerr` renderers, and prints the same stable format the
// Lean binary prints:
//
//	R <hex of RenderRich>
//	J <hex of RenderJSONWithHint>
//	--
//
// An error is either
//
//	d:<code>:<message>:<hint>:<file>:<line>:<column>:<endColumn>
//
// (a SemanticError; hex strings, decimal numbers, 0-based columns) or
//
//	x:<message>
//
// (an untyped error). Several errors make a MultiError, no errors the nil
// error. With -gen N it emits deterministic curated and fuzz cases; with
// -exhaustive it emits every caret position of a fixed set of sources.
package main

import (
	"bufio"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"

	"martianoff/gala/galaerr"
)

// fallbackPath names a file that does not exist, so the renderer uses the
// caller's fallback source. The generator's other path is different and also
// does not exist, so a diagnostic naming it gets no source at all.
const fallbackPath = "no-such-file-7a3f.gala"
const otherPath = "other-9c1e.gala"

var sources = []string{
	"",
	"package main\n",
	"package main\n\nfunc main() {\n  val n = len(s)\n}\n",
	"\uFEFFpackage main\n\nfunc main() {\n  val n = len(s)\n}\n",
	"\tA\tB\n\tC\n",
	"héllo ${π}\n",
	"line one\r\nline two\n",
	"a\n\nb\n",
	"func main() {\n\tval x = \"str\" + `raw`\n}\n",
}

var codes = []string{"", "GALA-E0001", "GALA-E0035", "GALA-E0107"}

var messages = []string{
	"",
	"bare `len` is forbidden",
	"a<b>&c",
	"quote \" and \\ backslash",
	"control \x01 and \x7f and \b and \f",
	"unicode é π 😀 \u2028 \u2029",
	"multi\nline",
}

var hints = []string{
	"",
	"use .Size() / .ByteSize() instead",
	"first clause (and more); detail. end",
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	"has\ttab and é",
}

func encode(errs []string, color bool, source, jsonHint string) string {
	flag := "0"
	if color {
		flag = "1"
	}
	parts := []string{"r", flag,
		hex.EncodeToString([]byte(fallbackPath)),
		hex.EncodeToString([]byte(source)),
		hex.EncodeToString([]byte(jsonHint))}
	parts = append(parts, errs...)
	return strings.Join(parts, "|")
}

func diagCase(code, message, hint, file string, line, column, endColumn int) string {
	return strings.Join([]string{
		"d",
		hex.EncodeToString([]byte(code)),
		hex.EncodeToString([]byte(message)),
		hex.EncodeToString([]byte(hint)),
		hex.EncodeToString([]byte(file)),
		strconv.Itoa(line), strconv.Itoa(column), strconv.Itoa(endColumn),
	}, ":")
}

func rawCase(message string) string {
	return "x:" + hex.EncodeToString([]byte(message))
}

func generate(w *bufio.Writer, n int, seed int64) {
	emit := func(errs []string, color bool, source, hint string) {
		fmt.Fprintln(w, encode(errs, color, source, hint))
	}

	// Curated: the shapes the renderer has to get exactly right.
	for i, src := range sources {
		for _, color := range []bool{false, true} {
			emit([]string{diagCase("GALA-E0035", "bare `len` is forbidden", "use .Size() instead", "", 4, 10, 13)}, color, src, "")
			emit([]string{diagCase("GALA-E0035", "bare `len` is forbidden", "use .Size() instead", fallbackPath, 1, 0, 0)}, color, src, "")
			emit([]string{diagCase("", "unexpected token", "", "", 3, 2, 0)}, color, src, "")
			emit([]string{diagCase("GALA-E0107", "no position", "a hint", "", 0, 0, 0)}, color, src, "")
			emit(nil, color, src, "")
			if i%2 == 0 {
				emit([]string{rawCase("go build: <bad> & \"stuff\"\nline2")}, color, src, "cli")
				emit([]string{
					diagCase("GALA-E0001", "one", "", "", 1, 0, 0),
					diagCase("", "two", "", "", 2, 1, 0),
				}, color, src, "cli")
			}
		}
	}

	// Every caret position on a few sources: line, column and end column all
	// vary, including out-of-range positions and spans clamped to the line.
	for _, src := range sources[:5] {
		lines := strings.Split(src, "\n")
		for line := 1; line <= len(lines)+1; line++ {
			for column := 0; column <= 12; column++ {
				for _, end := range []int{0, column + 1, column + 4, 60} {
					emit([]string{diagCase("GALA-E0036", "caret", "hint", "", line, column, end)}, false, src, "")
				}
			}
		}
	}

	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < n; i++ {
		src := sources[rng.Intn(len(sources))]
		line := rng.Intn(len(strings.Split(src, "\n")) + 3)
		column := rng.Intn(40)
		end := 0
		if rng.Intn(2) == 0 {
			end = column + rng.Intn(20)
		}
		file := []string{"", fallbackPath, otherPath}[rng.Intn(3)]
		errs := []string{diagCase(
			codes[rng.Intn(len(codes))],
			messages[rng.Intn(len(messages))],
			hints[rng.Intn(len(hints))],
			file, line, column, end)}
		if rng.Intn(10) == 0 {
			errs = append(errs, rawCase(messages[rng.Intn(len(messages))]))
		}
		if rng.Intn(20) == 0 {
			errs = append(errs, diagCase("", "sibling", "", "", rng.Intn(3), rng.Intn(9), 0))
		}
		jsonHint := ""
		if rng.Intn(3) == 0 {
			jsonHint = "cli hint"
		}
		emit(errs, rng.Intn(2) == 0, src, jsonHint)
	}
}

func generateExhaustive(w *bufio.Writer) {
	for _, src := range sources {
		lines := strings.Split(src, "\n")
		for line := 0; line <= len(lines)+1; line++ {
			for column := 0; column <= 24; column++ {
				for _, end := range []int{0, column + 1, column + 2, column + 8, column + 30} {
					errs := []string{diagCase("GALA-E0037", "span", "", "", line, column, end)}
					fmt.Fprintln(w, encode(errs, false, src, ""))
					fmt.Fprintln(w, encode(errs, true, src, "cli"))
				}
			}
		}
	}
}

func decode(s string) ([]byte, error) { return hex.DecodeString(s) }

func main() {
	gen := flag.Int("gen", 0, "emit N fuzz cases instead of rendering stdin")
	seed := flag.Int64("seed", 1, "fuzz seed")
	exhaustive := flag.Bool("exhaustive", false, "emit exhaustive caret positions")
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
		fields := strings.Split(line, "|")
		if len(fields) < 5 || fields[0] != "r" {
			fmt.Fprintf(os.Stderr, "go_ref: malformed case line: %q\n", line)
			os.Exit(1)
		}
		color := fields[1] == "1"
		if fields[1] != "0" && fields[1] != "1" {
			fmt.Fprintf(os.Stderr, "go_ref: bad color flag: %q\n", fields[1])
			os.Exit(1)
		}
		path, err := decode(fields[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "go_ref: bad path hex: %v\n", err)
			os.Exit(1)
		}
		source, err := decode(fields[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "go_ref: bad source hex: %v\n", err)
			os.Exit(1)
		}
		jsonHint, err := decode(fields[4])
		if err != nil {
			fmt.Fprintf(os.Stderr, "go_ref: bad hint hex: %v\n", err)
			os.Exit(1)
		}

		var errs []error
		for _, spec := range fields[5:] {
			parts := strings.Split(spec, ":")
			switch parts[0] {
			case "d":
				if len(parts) != 8 {
					fmt.Fprintf(os.Stderr, "go_ref: malformed diagnostic: %q\n", spec)
					os.Exit(1)
				}
				code, e1 := decode(parts[1])
				msg, e2 := decode(parts[2])
				hint, e3 := decode(parts[3])
				file, e4 := decode(parts[4])
				lineNo, e5 := strconv.Atoi(parts[5])
				column, e6 := strconv.Atoi(parts[6])
				endColumn, e7 := strconv.Atoi(parts[7])
				if e1 != nil || e2 != nil || e3 != nil || e4 != nil ||
					e5 != nil || e6 != nil || e7 != nil {
					fmt.Fprintf(os.Stderr, "go_ref: malformed diagnostic: %q\n", spec)
					os.Exit(1)
				}
				errs = append(errs, &galaerr.SemanticError{
					BaseError: galaerr.BaseError{Msg: string(msg), ErrType: galaerr.TypeSemantic},
					Line:      lineNo,
					Column:    column,
					EndColumn: endColumn,
					Code:      galaerr.ErrorCode(code),
					Hint:      string(hint),
					FilePath:  string(file),
				})
			case "x":
				if len(parts) != 2 {
					fmt.Fprintf(os.Stderr, "go_ref: malformed raw error: %q\n", spec)
					os.Exit(1)
				}
				msg, err := decode(parts[1])
				if err != nil {
					fmt.Fprintf(os.Stderr, "go_ref: bad message hex: %v\n", err)
					os.Exit(1)
				}
				errs = append(errs, errors.New(string(msg)))
			default:
				fmt.Fprintf(os.Stderr, "go_ref: unknown error spec: %q\n", spec)
				os.Exit(1)
			}
		}

		var renderErr error
		if len(errs) == 1 {
			renderErr = errs[0]
		} else if len(errs) > 1 {
			renderErr = &galaerr.MultiError{Errors: errs}
		}
		rich := galaerr.RenderRich(renderErr, galaerr.Options{
			FallbackPath:   string(path),
			FallbackSource: string(source),
			Color:          color,
		})
		json, jsonErr := galaerr.RenderJSONWithHint(renderErr, string(jsonHint))
		if jsonErr != nil {
			fmt.Fprintf(os.Stderr, "go_ref: RenderJSONWithHint: %v\n", jsonErr)
			os.Exit(1)
		}
		fmt.Fprintf(out, "R %s\nJ %s\n--\n",
			hex.EncodeToString([]byte(rich)), hex.EncodeToString([]byte(json)))
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "go_ref: read: %v\n", err)
		os.Exit(1)
	}
}
