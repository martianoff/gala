//go:build windows

package doctor

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// LongPathsCheck returns the check of the system setting that lifts Windows'
// 260-character path limit.
func LongPathsCheck() func() (bool, error) { return longPathsEnabled }

// longPathsEnabled reads the setting. A missing value means it was never
// turned on.
func longPathsEnabled() (bool, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\FileSystem`, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer key.Close()
	v, _, err := key.GetIntegerValue("LongPathsEnabled")
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	return v == 1, err
}
