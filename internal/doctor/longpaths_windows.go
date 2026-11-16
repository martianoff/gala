//go:build windows

package doctor

import "golang.org/x/sys/windows/registry"

// LongPathsEnabled reads the system setting that lifts Windows' 260-character
// path limit. A missing value means the setting was never turned on.
var LongPathsEnabled = func() (bool, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\FileSystem`, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer key.Close()
	v, _, err := key.GetIntegerValue("LongPathsEnabled")
	if err == registry.ErrNotExist {
		return false, nil
	}
	return v == 1, err
}
