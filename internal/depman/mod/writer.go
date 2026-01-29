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

	// Direct requires
	direct := f.DirectRequires()
	if len(direct) > 0 {
		if len(direct) == 1 {
			sb.WriteString(fmt.Sprintf("require %s %s\n", direct[0].Path, direct[0].Version))
		} else {
			sb.WriteString("require (\n")
			for _, r := range direct {
				sb.WriteString(fmt.Sprintf("\t%s %s\n", r.Path, r.Version))
			}
			sb.WriteString(")\n")
		}
		sb.WriteString("\n")
	}

	// Indirect requires
	indirect := f.IndirectRequires()
	if len(indirect) > 0 {
		if len(indirect) == 1 {
			sb.WriteString(fmt.Sprintf("require %s %s // indirect\n", indirect[0].Path, indirect[0].Version))
		} else {
			sb.WriteString("require (\n")
			for _, r := range indirect {
				sb.WriteString(fmt.Sprintf("\t%s %s // indirect\n", r.Path, r.Version))
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
				sb.WriteString(formatReplaceEntry(r))
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

// formatReplaceLine formats a single replace directive.
func formatReplaceLine(r Replace) string {
	return fmt.Sprintf("replace %s\n", formatReplaceEntry(r))
}

// formatReplaceEntry formats the content of a replace entry (without "replace" prefix).
func formatReplaceEntry(r Replace) string {
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
