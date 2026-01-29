package sum

import (
	"fmt"
	"os"
	"strings"
)

// ParseError represents an error during gala.sum parsing.
type ParseError struct {
	Line    int
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("gala.sum:%d: %s", e.Line, e.Message)
}

// Parse parses a gala.sum file from a string.
func Parse(content string) (*File, error) {
	f := NewFile()
	lines := strings.Split(content, "\n")

	for lineNum, line := range lines {
		lineNum++ // 1-indexed for error messages

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		entry, err := parseLine(line)
		if err != nil {
			return nil, &ParseError{Line: lineNum, Message: err.Error()}
		}

		f.Entries = append(f.Entries, entry)
	}

	return f, nil
}

// ParseFile parses a gala.sum file from a filesystem path.
func ParseFile(path string) (*File, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Empty sum file is valid
			return NewFile(), nil
		}
		return nil, fmt.Errorf("failed to read gala.sum: %w", err)
	}
	return Parse(string(content))
}

// parseLine parses a single line of the form:
// "path version[/suffix] hash"
//
// Examples:
//   - github.com/example/utils v1.2.3 h1:abc123...
//   - github.com/example/utils v1.2.3/gala.mod h1:def456...
func parseLine(line string) (Entry, error) {
	// Split into at most 3 parts: path+version[suffix], hash
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return Entry{}, fmt.Errorf("invalid format: expected 'path version hash'")
	}

	// Last part is the hash
	hash := parts[len(parts)-1]
	if !strings.HasPrefix(hash, "h1:") {
		return Entry{}, fmt.Errorf("invalid hash format: expected 'h1:...'")
	}

	// First part is the path
	path := parts[0]

	// Middle part is version with optional suffix
	versionPart := parts[1]
	var version, suffix string

	// Check for suffix like /gala.mod
	if slashIdx := strings.Index(versionPart, "/"); slashIdx > 0 {
		version = versionPart[:slashIdx]
		suffix = versionPart[slashIdx:]
	} else {
		version = versionPart
	}

	return Entry{
		Path:    path,
		Version: version,
		Suffix:  suffix,
		Hash:    hash,
	}, nil
}
