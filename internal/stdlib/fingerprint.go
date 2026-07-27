package stdlib

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
)

// Fingerprint returns a stable content hash of the embedded standard library
// snapshot compiled into this binary.
//
// Callers cache the extracted stdlib on disk keyed by the CLI version string.
// That key alone is not sufficient: an unstamped build reports the same version
// ("dev") for every revision, and even a tagged build can find a cache
// directory written by an older binary. Because the on-disk copy was previously
// considered valid whenever a marker file merely existed, an outdated snapshot
// could survive indefinitely and be used as the authoritative source of stdlib
// type information — silently changing compiler behaviour (for example, a
// signature that has since gained a marker type would still be seen in its old,
// unmarked form, disabling the checks that depend on that marker).
//
// Storing this fingerprint in the marker file and comparing contents makes the
// cache self-invalidating: any change to an embedded file, a file being added
// or removed, or a change to a generated go.mod produces a different digest and
// forces a re-extraction.
//
// The digest covers exactly what ExtractTo writes to disk: every package's
// files and the go.mod generated for it. It is computed once, from memory, with
// no filesystem access.
func Fingerprint() string {
	return embeddedFingerprint
}

// embeddedFingerprint is computed once at package initialisation. The embedded
// snapshot is immutable for the lifetime of the process, so re-hashing it per
// call would be pure overhead.
var embeddedFingerprint = fingerprintPackages(EmbeddedPackages, PackageImportPaths)

// fingerprintPackages hashes a package set in a deterministic order: packages
// by name, then files by name, then the generated go.mod for the package. Every
// chunk is length-prefixed so that no rearrangement of names and contents can
// produce the same digest.
func fingerprintPackages(packages map[string]map[string]string, importPaths map[string]string) string {
	h := sha256.New()

	pkgNames := make([]string, 0, len(packages))
	for pkg := range packages {
		pkgNames = append(pkgNames, pkg)
	}
	sort.Strings(pkgNames)

	writeChunk := func(s string) {
		h.Write([]byte(strconv.Itoa(len(s))))
		h.Write([]byte{':'})
		h.Write([]byte(s))
	}

	for _, pkg := range pkgNames {
		writeChunk(pkg)

		files := packages[pkg]
		fileNames := make([]string, 0, len(files))
		for name := range files {
			fileNames = append(fileNames, name)
		}
		sort.Strings(fileNames)

		writeChunk(strconv.Itoa(len(fileNames)))
		for _, name := range fileNames {
			writeChunk(name)
			writeChunk(files[name])
		}

		// ExtractTo also writes a generated go.mod per package, so changes to
		// it must invalidate the cache just like a source change would.
		writeChunk("go.mod")
		writeChunk(generatePackageGoMod(pkg, importPaths[pkg]))
	}

	return hex.EncodeToString(h.Sum(nil))
}
