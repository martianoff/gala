// Command gala-bench is the Go half of the formal/bench harness. It drives
// the same pure component calls as the Lean benchmark binaries so the two
// sides can be timed on identical input, and it generates the deterministic
// corpora both use.
//
// Subcommands:
//
//	gala-bench interp <s|f> <reps> <cases-file>
//	gala-bench mod <reps> <manifest-file> [both|parse|format]
//	gala-bench gen <out-dir>
//
// The interp and mod subcommands print one `iter=<n> ns=<elapsed>` line per
// repetition, then a checksum line, then a `mem load_peak_kib=<n>
// peak_kib=<n>` line read from procfs (Linux). formal/bench/run.sh compares
// the checksum lines before reporting any timing; formal/bench/mem.sh reads
// the mem line and requires it to be non-zero.
package main

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/interpolation"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "interp":
		err = cmdInterp(os.Args[2:])
	case "mod":
		err = cmdMod(os.Args[2:])
	case "gen":
		err = cmdGen(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gala-bench:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  gala-bench interp <s|f> <reps> <cases-file>
  gala-bench mod <reps> <manifest-file> [both|parse|format]
  gala-bench gen <out-dir>`)
	os.Exit(2)
}

func parseReps(s string) (int, error) {
	reps, err := strconv.Atoi(s)
	if err != nil || reps < 1 {
		return 0, fmt.Errorf("bad repeat count %q", s)
	}
	return reps, nil
}

// peakRSSKiB returns this process's peak resident set size in KiB, read from
// VmHWM in procfs. It returns 0 when procfs is unavailable (for example on
// macOS); formal/bench/mem.sh treats zero as an error rather than reporting a
// made-up number. VmHWM is the high-water mark, so reading it costs nothing
// and does not disturb the measured loop.
func peakRSSKiB() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "VmHWM:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return 0
		}
		kib, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0
		}
		return kib
	}
	return 0
}

func cmdInterp(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: gala-bench interp <s|f> <reps> <cases-file>")
	}
	mode := args[0]
	if mode != "s" && mode != "f" {
		return fmt.Errorf("interp mode must be s or f, got %q", mode)
	}
	reps, err := parseReps(args[1])
	if err != nil {
		return err
	}
	data, err := os.ReadFile(args[2])
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	formatSpecs := mode == "f"

	// Corpus load is complete here, so this is the high-water mark before the
	// measured loop; mem.sh reports it next to the final peak.
	loadPeak := peakRSSKiB()

	var parts, bytes int64
	var total int64
	for r := 0; r < reps; r++ {
		start := time.Now()
		for _, line := range lines {
			ps := interpolation.Split(line, formatSpecs)
			parts += int64(len(ps))
			for _, p := range ps {
				bytes += int64(len(p.Text) + len(p.FormatSpec))
			}
		}
		elapsed := time.Since(start)
		total += elapsed.Nanoseconds()
		fmt.Printf("iter=%d ns=%d\n", r, elapsed.Nanoseconds())
	}
	fmt.Printf("parts=%d bytes=%d ns=%d\n", parts, bytes, total)
	fmt.Printf("mem load_peak_kib=%d peak_kib=%d\n", loadPeak, peakRSSKiB())
	return nil
}

func cmdMod(args []string) error {
	if len(args) != 2 && len(args) != 3 {
		return fmt.Errorf("usage: gala-bench mod <reps> <manifest-file> [both|parse|format]")
	}
	reps, err := parseReps(args[0])
	if err != nil {
		return err
	}
	mode := "both"
	if len(args) == 3 {
		mode = args[2]
	}
	if mode != "both" && mode != "parse" && mode != "format" {
		return fmt.Errorf("mod mode must be both, parse or format, got %q", mode)
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	text := string(data)
	// Read the load mark before the setup parse below: for the manifest driver
	// that parse is part of the work both sides do, not part of loading.
	loadPeak := peakRSSKiB()
	base, err := mod.Parse(text)
	if err != nil {
		return err
	}
	var reqs, reps2, excs, bytes int64
	for r := 0; r < reps; r++ {
		start := time.Now()
		f := base
		if mode != "format" {
			f, err = mod.Parse(text)
			if err != nil {
				return err
			}
		}
		if mode != "parse" {
			bytes += int64(len(mod.Format(f)))
		}
		elapsed := time.Since(start)
		reqs += int64(len(f.Require))
		reps2 += int64(len(f.Replace))
		excs += int64(len(f.Exclude))
		fmt.Printf("iter=%d ns=%d\n", r, elapsed.Nanoseconds())
	}
	fmt.Printf("require=%d replace=%d exclude=%d bytes=%d\n", reqs, reps2, excs, bytes)
	fmt.Printf("mem load_peak_kib=%d peak_kib=%d\n", loadPeak, peakRSSKiB())
	return nil
}

// cmdGen writes deterministic corpora with the same shapes used by the
// benchmark: realistic interpolation bodies, format-string bodies, and
// gala.mod manifests of a few sizes.
func cmdGen(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gala-bench gen <out-dir>")
	}
	dir := args[0]
	if err := os.MkdirAll(args[0], 0o755); err != nil {
		return err
	}
	if err := genInterp(filepath.Join(args[0], "s-cases.txt"), 200000, false); err != nil {
		return err
	}
	if err := genInterp(filepath.Join(args[0], "f-cases.txt"), 100000, true); err != nil {
		return err
	}
	for _, n := range []int{40, 400, 1000, 4000} {
		if err := genManifest(filepath.Join(dir, fmt.Sprintf("manifest-%d.mod", n)), n); err != nil {
			return err
		}
	}
	return nil
}

// genInterp writes count interpolation bodies. Templates cycle so every
// template appears equally often; random identifiers and a little length
// variation keep the byte-level shape from being degenerate.
func genInterp(path string, count int, formatSpecs bool) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	defer w.Flush()

	rng := rand.New(rand.NewSource(20261116))
	idents := []string{
		"order", "batch", "item", "node", "session", "account", "job",
		"customer", "email", "path", "count", "status", "value", "msg",
	}
	pick := func() string { return idents[rng.Intn(len(idents))] }
	tails := func() string { return strings.Repeat("x", rng.Intn(50)) }

	if formatSpecs {
		templates := []func() string{
			func() string { return fmt.Sprintf("${%s}%%d ${%s}%%s", pick(), pick()) },
			func() string { return "${value}%04d ${ratio}%.2f" },
			func() string { return "${msg}%q ${path}%s" },
			func() string { return "${x}%#v ${y}%v" },
			func() string { return fmt.Sprintf("literal %% not a spec ${%s}", pick()) },
			func() string { return fmt.Sprintf("${name}%%z ${path}%%") },
			func() string { return "$item%-5.2f ${line}%+d ${end}%t" },
			func() string { return "${a}%s-${b}%d-${c}%q " + tails() },
			func() string { return "${node}%d ${count}%s" },
			func() string { return "${value}%04d ${ratio}%.2f" },
		}
		for i := 0; i < count; i++ {
			fmt.Fprintln(w, templates[i%len(templates)]())
		}
		return nil
	}

	templates := []func() string{
		func() string { return fmt.Sprintf("Hello, $%s! You have ${%s}$ new messages.", pick(), pick()) },
		func() string {
			base := pick()
			return fmt.Sprintf("${%s.firstName} ${%s.lastName} <${%s.email}>", base, base, base)
		},
		func() string { return fmt.Sprintf("Rendered ${%s.id} items in ${%s}ms", pick(), pick()) },
		func() string { return "path=${path}, line=${line}, col=${col}" },
		func() string { return "Result: ${result} (${status})" },
		func() string { return fmt.Sprintf("Cache hit ratio: ${hits}/${total} for $%s", pick()) },
		func() string { return "Error: ${err.message} at ${file}:${line}" },
		func() string { return fmt.Sprintf("$$literal $dollar and ${%s} $$%s", pick(), pick()) },
		func() string { return fmt.Sprintf(`escaped \" quote and $%s \\ tail`, pick()) },
		func() string { return fmt.Sprintf("nested ${a{b}c} braces and $%s %s", pick(), tails()) },
	}
	for i := 0; i < count; i++ {
		fmt.Fprintln(w, templates[i%len(templates)]())
	}
	return nil
}

// genManifest writes a gala.mod with n require lines, about one in five
// carrying the `// indirect` marker.
func genManifest(path string, n int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<16)
	defer w.Flush()

	rng := rand.New(rand.NewSource(int64(9000 + n)))
	fmt.Fprintln(w, "module example.com/bench")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "gala 0.101.0")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "require (")
	for i := 0; i < n; i++ {
		fmt.Fprintf(w, "\tgithub.com/pkg/errors/pkg%d v0.9.%d", i, i%10)
		if rng.Intn(100) < 22 {
			fmt.Fprint(w, " // indirect")
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, ")")
	return nil
}
