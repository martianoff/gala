package stdlib

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"sync"
)

// Fingerprint returns a stable content hash of the embedded standard library
// snapshot compiled into this binary.
//
// Callers cache the extracted stdlib on disk. The CLI version string is not a
// sufficient key for that cache: an unstamped build reports the same version
// ("dev") for every revision, so one cached copy is shared by snapshots that
// have nothing in common. Since the cached stdlib is the type information the
// transpiler compiles against, an outdated copy silently changes compiler
// behaviour — a signature that has gained a marker type is still seen in its
// old, unmarked form, and the checks driven by that marker stop running.
//
// Comparing this fingerprint instead makes the cache self-invalidating: any
// change to an embedded file, a file added or removed, or a change to a
// generated go.mod produces a different digest and forces a re-extraction.
//
// The digest covers exactly what ExtractTo writes to disk: every package's
// files and the go.mod generated for it. It reads no files, and the embedded
// snapshot is immutable for the lifetime of the process, so it is computed at
// most once and only if something asks for it.
func Fingerprint() string {
	return embeddedFingerprint()
}

// embeddedFingerprint hashes roughly a megabyte of embedded sources, so it is
// deferred rather than run at package initialisation: most gala subcommands
// link this package without ever touching the stdlib cache.
var embeddedFingerprint = sync.OnceValue(func() string {
	return fingerprintPackages(EmbeddedPackages, PackageImportPaths)
})

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
