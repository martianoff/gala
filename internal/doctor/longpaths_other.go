//go:build !windows

package doctor

// LongPathsCheck returns nil outside Windows, which has no path length
// setting to check.
func LongPathsCheck() func() (bool, error) { return nil }
