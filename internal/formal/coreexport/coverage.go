package coreexport

import "encoding/json"

// UnsupportedCounts is how often each unsupported construct occurs in p, by
// its name; empty when p is entirely inside the subset. It reads p as JSON,
// the form a consumer sees.
func UnsupportedCounts(p *Program) (map[string]int, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if what, ok := v["unsupported"].(string); ok {
				counts[what]++
			}
			for _, c := range v {
				walk(c)
			}
		case []any:
			for _, c := range v {
				walk(c)
			}
		}
	}
	walk(doc)
	return counts, nil
}
