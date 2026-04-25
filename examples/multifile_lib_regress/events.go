package multifile_lib_regress

// Hand-written Go file in the same package as GALA sources. Exercises the
// cross-language type inference path where a generic-method lambda's element
// type comes from a .go declaration (Event) and is fed in via a Go factory
// (MakeEvents). Without correct local-package Go-function lookup, callers
// like ArrayFromSlice(MakeEvents()) fall back to Array[T] and downstream
// methods (FoldLeft / Map / Filter / Find / Exists / ForAll) emit the
// un-substituted type-parameter name as the lambda parameter type.

type Event struct {
	Method string
	Status int
}

func MakeEvents() []Event {
	return []Event{
		{"GET", 200},
		{"POST", 201},
		{"GET", 500},
	}
}
