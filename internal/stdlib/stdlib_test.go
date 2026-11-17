package stdlib

import (
	goversion "go/version"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGoVersionCoversGenericAliases pins the floor of GoVersion: a GALA generic
// alias is emitted as a Go generic alias, which Go accepts from 1.24 on, and
// every go.mod GALA generates declares GoVersion — a stdlib package's included.
func TestGoVersionCoversGenericAliases(t *testing.T) {
	require.GreaterOrEqual(t, goversion.Compare("go"+GoVersion, "go1.24"), 0, GoVersion)
	require.Contains(t, generatePackageGoMod("std", PackageImportPaths["std"]), "\ngo "+GoVersion+"\n")
}

// TestGoVersionIsNoOlderThanSupportedGo: GALA supports no Go older than 1.25,
// and the go line GoVersion writes into the build workspace's go.mod picks the
// GODEBUG defaults of every program it builds. A go line older than the
// supported Go would run those programs with an older release's settings.
func TestGoVersionIsNoOlderThanSupportedGo(t *testing.T) {
	require.GreaterOrEqual(t, goversion.Compare("go"+GoVersion, "go1.25"), 0, GoVersion)
}
