package main

import (
	"encoding/json"
	"sort"

	"example.com/budget/textutil"
)

// HelperBanner is hand-written Go next to the GALA files: the analyzer
// type-checks it, and what it imports, with the package.
func HelperBanner() string {
	keys := []string{"b", "a"}
	sort.Strings(keys)
	out, _ := json.Marshal(keys)
	return textutil.Reverse(string(out))
}
