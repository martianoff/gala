package lsp

import (
	"strings"

	"github.com/owenrumney/go-lsp/lsp"
)

// Parameter documentation
// -----------------------
// GALA's standard library documents parameters with a `name: description` line
// following the summary:
//
//	// GetOrElse returns the option's value if the option is Some, otherwise
//	// returns the result of evaluating defaultValue.
//	// defaultValue: the default value to return if the option is empty.
//
// splitDoc separates those lines from the prose so signature help can attach
// each one to the parameter it describes, instead of repeating the whole
// comment under every parameter.
//
// A line is only treated as parameter documentation when its label matches a
// declared parameter name. Matching any `word:` prefix instead would swallow
// ordinary prose — "Note: ..." or "Example: ..." would silently vanish from the
// summary and reappear as documentation for a parameter that does not exist.

// splitDoc returns the summary prose of a doc comment and the per-parameter
// documentation found in it, keyed by parameter name.
func splitDoc(doc string, paramNames []string) (summary string, params map[string]string) {
	if doc == "" || len(paramNames) == 0 {
		return doc, nil
	}
	declared := make(map[string]bool, len(paramNames))
	for _, n := range paramNames {
		declared[n] = true
	}

	var prose []string
	var current string // parameter whose description we are still accumulating
	for _, line := range strings.Split(doc, "\n") {
		if name, rest, ok := strings.Cut(line, ":"); ok && declared[strings.TrimSpace(name)] {
			current = strings.TrimSpace(name)
			if params == nil {
				params = make(map[string]string)
			}
			params[current] = strings.TrimSpace(rest)
			continue
		}
		// A parameter's description may wrap onto the following line; a blank
		// line ends it and returns to prose.
		if current != "" {
			if strings.TrimSpace(line) == "" {
				current = ""
				continue
			}
			params[current] = strings.TrimSpace(params[current] + " " + strings.TrimSpace(line))
			continue
		}
		prose = append(prose, line)
	}

	for len(prose) > 0 && strings.TrimSpace(prose[len(prose)-1]) == "" {
		prose = prose[:len(prose)-1]
	}
	return strings.Join(prose, "\n"), params
}

// markdown wraps text as Markdown MarkupContent, or returns nil for empty text
// so the field is omitted from the payload entirely rather than sent blank.
func markdown(text string) *lsp.MarkupContent {
	if text == "" {
		return nil
	}
	return &lsp.MarkupContent{Kind: lsp.Markdown, Value: text}
}
