package mod

import (
	"fmt"
	"os"
	"strings"
)

// Format formats a File as a gala.mod string.
func Format(f *File) string {
	var sb strings.Builder

	// Module declaration
	sb.WriteString(fmt.Sprintf("module %s\n", f.Module.Path))
	sb.WriteString("\n")

	// GALA version
	if f.Gala != "" {
		sb.WriteString(fmt.Sprintf("gala %s\n", f.Gala))
		sb.WriteString("\n")
	}

	// Requires (all together, with appropriate comments)
	if len(f.Require) > 0 {
		if len(f.Require) == 1 {
			sb.WriteString(fmt.Sprintf("require %s\n", FormatRequireEntry(f.Require[0])))
		} else {
			sb.WriteString("require (\n")
			for _, r := range f.Require {
				sb.WriteString(fmt.Sprintf("\t%s\n", FormatRequireEntry(r)))
			}
			sb.WriteString(")\n")
		}
		sb.WriteString("\n")
	}

	// Replaces
	if len(f.Replace) > 0 {
		if len(f.Replace) == 1 {
			sb.WriteString(formatReplaceLine(f.Replace[0]))
		} else {
			sb.WriteString("replace (\n")
			for _, r := range f.Replace {
				sb.WriteString("\t")
				sb.WriteString(FormatReplaceEntry(r))
				sb.WriteString("\n")
			}
			sb.WriteString(")\n")
		}
		sb.WriteString("\n")
	}

	// Excludes
	if len(f.Exclude) > 0 {
		if len(f.Exclude) == 1 {
			sb.WriteString(fmt.Sprintf("exclude %s %s\n", f.Exclude[0].Path, f.Exclude[0].Version))
		} else {
			sb.WriteString("exclude (\n")
			for _, e := range f.Exclude {
				sb.WriteString(fmt.Sprintf("\t%s %s\n", e.Path, e.Version))
			}
			sb.WriteString(")\n")
		}
	}

	return strings.TrimRight(sb.String(), "\n") + "\n"
}

// FormatRequireEntry formats a require entry as gala.mod writes it, with its
// "// indirect" and "// go" markers.
func FormatRequireEntry(r Require) string {
	var sb strings.Builder
	sb.WriteString(r.Path)
	sb.WriteString(" ")
	sb.WriteString(r.Version)

	// Add comment markers
	var markers []string
	if r.Indirect {
		markers = append(markers, "indirect")
	}
	if r.Go {
		markers = append(markers, "go")
	}
	if len(markers) > 0 {
		sb.WriteString(" // ")
		sb.WriteString(strings.Join(markers, ", "))
	}

	return sb.String()
}

// formatReplaceLine formats a single replace directive.
func formatReplaceLine(r Replace) string {
	return fmt.Sprintf("replace %s\n", FormatReplaceEntry(r))
}

// FormatReplaceEntry formats a replace entry as gala.mod writes it, without
// the "replace" keyword.
func FormatReplaceEntry(r Replace) string {
	var sb strings.Builder

	sb.WriteString(r.Old.Path)
	if r.Old.Version != "" {
		sb.WriteString(" ")
		sb.WriteString(r.Old.Version)
	}

	sb.WriteString(" => ")

	sb.WriteString(r.New.Path)
	if r.New.Version != "" {
		sb.WriteString(" ")
		sb.WriteString(r.New.Version)
	}

	return sb.String()
}

// WriteFile writes a File to a filesystem path.
func WriteFile(f *File, path string) error {
	content := Format(f)
	return os.WriteFile(path, []byte(content), 0644)
}
