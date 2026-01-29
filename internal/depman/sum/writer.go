package sum

import (
	"os"
	"sort"
	"strings"
)

// Format formats a File as a gala.sum string.
func Format(f *File) string {
	if len(f.Entries) == 0 {
		return ""
	}

	// Sort entries for deterministic output
	entries := make([]Entry, len(f.Entries))
	copy(entries, f.Entries)
	sort.Slice(entries, func(i, j int) bool {
		// Sort by path, then version, then suffix
		if entries[i].Path != entries[j].Path {
			return entries[i].Path < entries[j].Path
		}
		if entries[i].Version != entries[j].Version {
			return entries[i].Version < entries[j].Version
		}
		return entries[i].Suffix < entries[j].Suffix
	})

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.Path)
		sb.WriteString(" ")
		sb.WriteString(e.Version)
		if e.Suffix != "" {
			sb.WriteString(e.Suffix)
		}
		sb.WriteString(" ")
		sb.WriteString(e.Hash)
		sb.WriteString("\n")
	}

	return sb.String()
}

// WriteFile writes a File to a filesystem path.
func WriteFile(f *File, path string) error {
	content := Format(f)
	return os.WriteFile(path, []byte(content), 0644)
}
