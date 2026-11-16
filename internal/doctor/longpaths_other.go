//go:build !windows

package doctor

// LongPathsEnabled is nil outside Windows, which has no path length setting
// to check.
var LongPathsEnabled func() (bool, error)
