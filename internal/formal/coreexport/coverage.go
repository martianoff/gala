package coreexport

import "encoding/json"

// UnsupportedCounts is how often each unsupported construct occurs in p, by
// its name; empty when p is entirely inside the subset. It reads p as JSON,
// the form a consumer sees.
func UnsupportedCounts(p *Program) (map[string]int, error) {
	return countFields(p, "unsupported")
}

// ConstructCounts is how often each construct occurs in p: the kind of every
// supported node and the name of every unsupported one.
func ConstructCounts(p *Program) (map[string]int, error) {
	return countFields(p, "kind", "unsupported")
}

// countFields counts the string values of the given fields over every
// object in p's JSON.
func countFields(p *Program, fields ...string) (map[string]int, error) {
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
			for _, field := range fields {
				if name, ok := v[field].(string); ok {
					counts[name]++
				}
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
