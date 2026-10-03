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
	require.True(t, goversion.IsValid("go"+GoVersion), GoVersion)
	require.GreaterOrEqual(t, goversion.Compare("go"+GoVersion, "go1.24"), 0, GoVersion)
	require.Contains(t, generatePackageGoMod("std", PackageImportPaths["std"]), "\ngo "+GoVersion+"\n")
}
