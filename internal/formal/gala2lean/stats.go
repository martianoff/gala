package gala2lean

import (
	"errors"

	"martianoff/gala/internal/formal/coreexport"
)

// Stats counts a program's functions and methods, and how many of them a
// translation covers. It measures how much translates, not agreement.
type Stats struct {
	// Funcs is every top-level function and method; Methods the methods
	// among them.
	Funcs, Methods int
	// Translated is how many of Funcs the translation covers: not a
	// generic function or method, not a method whose receiver is outside
	// the subset (Ptr[T], Box[T]), and not one err names.
	Translated int
}

// CountTranslated is the Stats of p, given err, the result of translating p
// (nil, or an *UnsupportedError).
func CountTranslated(p *coreexport.Program, err error) Stats {
	var s Stats
	names := map[string]bool{}
	opaque := 0
	for _, f := range p.Files {
		for _, d := range f.Decls {
			d, ok := d.(*coreexport.Func)
			if !ok {
				continue
			}
			s.Funcs++
			if d.Receiver != nil {
				s.Methods++
			}
			if len(d.TypeParams) > 0 {
				opaque++
				continue
			}
			key := d.Name
			if d.Receiver != nil {
				r, _ := d.Receiver.(*coreexport.Receiver)
				if r == nil || typeName(r.Type) == "" {
					opaque++
					continue
				}
				key = typeName(r.Type) + "." + d.Name
			}
			names[key] = true
		}
	}
	untranslated := map[string]bool{}
	var u *UnsupportedError
	if errors.As(err, &u) {
		for _, it := range u.Items {
			if names[it.Decl] {
				untranslated[it.Decl] = true
			}
		}
	}
	s.Translated = s.Funcs - opaque - len(untranslated)
	return s
}
