package commands

import "martianoff/gala/internal/transpiler/analyzer"

// InitCompilerVersion sets the analyzer cache version from the CLI version info.
// This ensures stale cache entries from previous transpiler versions are automatically
// ignored when the binary is upgraded (BUG-057).
func InitCompilerVersion() {
	if GitCommit != "unknown" && GitCommit != "" {
		analyzer.CompilerVersion = Version + "-" + GitCommit
	} else {
		analyzer.CompilerVersion = Version
	}
}
