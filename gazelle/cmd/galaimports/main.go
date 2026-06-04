// Command galaimports is a lightweight stand-in for `gala imports --json`.
//
// It implements the helper contract the Gazelle extension depends on, scanning
// each .gala file for its package declaration and import block with simple
// line parsing (no grammar). It exists so the extension can be developed and
// dogfooded before the production `gala imports` subcommand ships; the real
// producer (owned by the transpiler) should emit the identical JSON shape.
//
// Usage:
//
//	galaimports imports --json <file.gala> [<file.gala> ...]
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type rawImport struct {
	Path  string `json:"path"`
	Alias string `json:"alias"`
	Dot   bool   `json:"dot"`
}

type rawFile struct {
	File    string      `json:"file"`
	Package string      `json:"package"`
	Imports []rawImport `json:"imports"`
	Error   string      `json:"error"`
}

var (
	packageRe = regexp.MustCompile(`^\s*package\s+([A-Za-z_][A-Za-z0-9_]*)`)
	// A single-line import: optional alias / dot, then a quoted path. The alias
	// or dot may be separated from the path by whitespace.
	importLineRe = regexp.MustCompile(`^\s*(?:(\.)|([A-Za-z_][A-Za-z0-9_]*))?\s*"([^"]+)"`)
)

func main() {
	args := os.Args[1:]
	if len(args) < 1 || args[0] != "imports" {
		fmt.Fprintln(os.Stderr, "usage: galaimports imports --json <files...>")
		os.Exit(2)
	}
	args = args[1:]
	// Tolerate the --json flag in any position.
	files := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--json" || a == "-json" {
			continue
		}
		files = append(files, a)
	}

	out := make([]rawFile, 0, len(files))
	for _, f := range files {
		out = append(out, scanFile(f))
	}
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func scanFile(path string) rawFile {
	rf := rawFile{File: path}
	file, err := os.Open(path)
	if err != nil {
		rf.Error = err.Error()
		return rf
	}
	defer file.Close()

	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inBlock := false
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if rf.Package == "" {
			if m := packageRe.FindStringSubmatch(line); m != nil {
				rf.Package = m[1]
				continue
			}
		}
		switch {
		case inBlock:
			if trimmed == ")" {
				inBlock = false
				continue
			}
			if imp, ok := parseImport(line); ok {
				rf.Imports = append(rf.Imports, imp)
			}
		case strings.HasPrefix(trimmed, "import ("):
			inBlock = true
		case strings.HasPrefix(trimmed, "import "):
			if imp, ok := parseImport(strings.TrimPrefix(trimmed, "import")); ok {
				rf.Imports = append(rf.Imports, imp)
			}
		}
	}
	if err := sc.Err(); err != nil {
		rf.Error = err.Error()
	}
	return rf
}

func parseImport(line string) (rawImport, bool) {
	m := importLineRe.FindStringSubmatch(line)
	if m == nil {
		return rawImport{}, false
	}
	return rawImport{
		Path:  m[3],
		Alias: m[2],
		Dot:   m[1] == ".",
	}, true
}
