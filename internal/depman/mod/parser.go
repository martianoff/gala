package mod

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ParseError represents an error during gala.mod parsing.
type ParseError struct {
	Line    int
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("gala.mod:%d: %s", e.Line, e.Message)
}

// Parse parses a gala.mod file from a string.
func Parse(content string) (*File, error) {
	return parseLines(strings.Split(content, "\n"))
}

// ParseFile parses a gala.mod file from a filesystem path.
func ParseFile(path string) (*File, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read gala.mod: %w", err)
	}
	return Parse(string(content))
}

// parseLines parses gala.mod content from lines.
func parseLines(lines []string) (*File, error) {
	f := &File{
		Require: make([]Require, 0),
		Replace: make([]Replace, 0),
		Exclude: make([]Exclude, 0),
	}

	var inBlock string // "require", "replace", "exclude", or ""
	var blockIndirect bool
	var blockGo bool

	for lineNum, line := range lines {
		lineNum++ // 1-indexed for error messages

		// Track comment markers for current line
		lineIndirect := false
		lineGo := false

		// Remove comments but check for markers first
		if idx := strings.Index(line, "//"); idx >= 0 {
			comment := strings.TrimSpace(line[idx+2:])
			// Parse comment markers (can be "indirect", "go", or "indirect, go")
			for _, marker := range strings.Split(comment, ",") {
				marker = strings.TrimSpace(marker)
				if marker == "indirect" {
					if inBlock == "require" {
						blockIndirect = true
					}
					lineIndirect = true
				}
				if marker == "go" {
					if inBlock == "require" {
						blockGo = true
					}
					lineGo = true
				}
			}
			line = line[:idx]
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Handle block start/end
		if line == ")" {
			inBlock = ""
			blockIndirect = false
			blockGo = false
			continue
		}

		if strings.HasSuffix(line, "(") {
			directive := strings.TrimSuffix(line, "(")
			directive = strings.TrimSpace(directive)
			switch directive {
			case "require":
				inBlock = "require"
			case "replace":
				inBlock = "replace"
			case "exclude":
				inBlock = "exclude"
			default:
				return nil, &ParseError{Line: lineNum, Message: fmt.Sprintf("unknown block directive: %s", directive)}
			}
			continue
		}

		// Parse directives
		parts := splitFields(line)
		if len(parts) == 0 {
			continue
		}

		directive := parts[0]

		// Inside a block
		if inBlock != "" {
			switch inBlock {
			case "require":
				req, err := parseRequireLine(parts, blockIndirect || lineIndirect, blockGo || lineGo)
				if err != nil {
					return nil, &ParseError{Line: lineNum, Message: err.Error()}
				}
				f.Require = append(f.Require, req)
			case "replace":
				rep, err := parseReplaceLine(parts)
				if err != nil {
					return nil, &ParseError{Line: lineNum, Message: err.Error()}
				}
				f.Replace = append(f.Replace, rep)
			case "exclude":
				exc, err := parseExcludeLine(parts)
				if err != nil {
					return nil, &ParseError{Line: lineNum, Message: err.Error()}
				}
				f.Exclude = append(f.Exclude, exc)
			}
			continue
		}

		// Top-level directives
		switch directive {
		case "module":
			if len(parts) < 2 {
				return nil, &ParseError{Line: lineNum, Message: "module directive requires a path"}
			}
			f.Module.Path = parts[1]

		case "gala":
			if len(parts) < 2 {
				return nil, &ParseError{Line: lineNum, Message: "gala directive requires a version"}
			}
			f.Gala = parts[1]

		case "require":
			// Single-line require
			req, err := parseRequireLine(parts[1:], lineIndirect, lineGo)
			if err != nil {
				return nil, &ParseError{Line: lineNum, Message: err.Error()}
			}
			f.Require = append(f.Require, req)

		case "replace":
			// Single-line replace
			rep, err := parseReplaceLine(parts[1:])
			if err != nil {
				return nil, &ParseError{Line: lineNum, Message: err.Error()}
			}
			f.Replace = append(f.Replace, rep)

		case "exclude":
			// Single-line exclude
			exc, err := parseExcludeLine(parts[1:])
			if err != nil {
				return nil, &ParseError{Line: lineNum, Message: err.Error()}
			}
			f.Exclude = append(f.Exclude, exc)

		default:
			return nil, &ParseError{Line: lineNum, Message: fmt.Sprintf("unknown directive: %s", directive)}
		}
	}

	return f, nil
}

// parseRequireLine parses a require entry: "path version [// indirect] [// go]"
func parseRequireLine(parts []string, indirect bool, isGo bool) (Require, error) {
	if len(parts) < 2 {
		return Require{}, fmt.Errorf("require needs path and version")
	}

	return Require{
		Path:     parts[0],
		Version:  parts[1],
		Indirect: indirect,
		Go:       isGo,
	}, nil
}

// parseReplaceLine parses a replace entry: "old [version] => new [version]"
func parseReplaceLine(parts []string) (Replace, error) {
	// Find the => separator
	arrowIdx := -1
	for i, p := range parts {
		if p == "=>" {
			arrowIdx = i
			break
		}
	}

	if arrowIdx == -1 {
		return Replace{}, fmt.Errorf("replace requires '=>' separator")
	}

	if arrowIdx == 0 {
		return Replace{}, fmt.Errorf("replace requires old module path before '=>'")
	}

	if arrowIdx >= len(parts)-1 {
		return Replace{}, fmt.Errorf("replace requires new path after '=>'")
	}

	old := ModuleVersion{Path: parts[0]}
	if arrowIdx > 1 {
		old.Version = parts[1]
	}

	newParts := parts[arrowIdx+1:]
	new := ModuleVersion{Path: newParts[0]}
	if len(newParts) > 1 {
		new.Version = newParts[1]
	}

	return Replace{Old: old, New: new}, nil
}

// parseExcludeLine parses an exclude entry: "path version"
func parseExcludeLine(parts []string) (Exclude, error) {
	if len(parts) < 2 {
		return Exclude{}, fmt.Errorf("exclude needs path and version")
	}

	return Exclude{
		Path:    parts[0],
		Version: parts[1],
	}, nil
}

// splitFields splits a line into fields, respecting quoted strings.
func splitFields(line string) []string {
	var fields []string
	scanner := bufio.NewScanner(strings.NewReader(line))
	scanner.Split(bufio.ScanWords)
	for scanner.Scan() {
		fields = append(fields, scanner.Text())
	}
	return fields
}
