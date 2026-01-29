// Package sum provides parsing, writing, and verification of gala.sum files.
package sum

// File represents a parsed gala.sum file.
type File struct {
	Entries []Entry
}

// Entry represents a single checksum entry in gala.sum.
// Each module version may have multiple entries (one for the module content,
// one for gala.mod, etc.).
type Entry struct {
	Path    string // Module path (e.g., "github.com/example/utils")
	Version string // Version (e.g., "v1.2.3")
	Suffix  string // Optional suffix (e.g., "/gala.mod")
	Hash    string // Hash value (e.g., "h1:abc123...")
}

// Key returns a unique key for this entry (path + version + suffix).
func (e Entry) Key() string {
	key := e.Path + " " + e.Version
	if e.Suffix != "" {
		key += e.Suffix
	}
	return key
}

// NewFile creates a new empty gala.sum file.
func NewFile() *File {
	return &File{
		Entries: make([]Entry, 0),
	}
}

// Add adds or updates an entry in the file.
func (f *File) Add(path, version, suffix, hash string) {
	key := path + " " + version + suffix

	// Check if entry already exists
	for i := range f.Entries {
		if f.Entries[i].Key() == key {
			f.Entries[i].Hash = hash
			return
		}
	}

	f.Entries = append(f.Entries, Entry{
		Path:    path,
		Version: version,
		Suffix:  suffix,
		Hash:    hash,
	})
}

// Get retrieves an entry by path, version, and suffix.
func (f *File) Get(path, version, suffix string) *Entry {
	key := path + " " + version + suffix
	for i := range f.Entries {
		if f.Entries[i].Key() == key {
			return &f.Entries[i]
		}
	}
	return nil
}

// GetModuleEntries returns all entries for a given module path and version.
func (f *File) GetModuleEntries(path, version string) []Entry {
	entries := make([]Entry, 0)
	for _, e := range f.Entries {
		if e.Path == path && e.Version == version {
			entries = append(entries, e)
		}
	}
	return entries
}

// Remove removes all entries for a given path and version.
func (f *File) Remove(path, version string) bool {
	removed := false
	newEntries := make([]Entry, 0)
	for _, e := range f.Entries {
		if e.Path == path && e.Version == version {
			removed = true
		} else {
			newEntries = append(newEntries, e)
		}
	}
	f.Entries = newEntries
	return removed
}

// Contains checks if the file has any entry for the given path and version.
func (f *File) Contains(path, version string) bool {
	for _, e := range f.Entries {
		if e.Path == path && e.Version == version {
			return true
		}
	}
	return false
}
