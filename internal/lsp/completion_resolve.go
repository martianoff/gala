package lsp

import (
	"context"
	"encoding/json"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
)

// Lazy completion documentation
// -----------------------------
// Documentation is not attached to completion items when the list is built. A
// dot-completion on a collection type offers on the order of a hundred methods,
// and the standard library documents nearly all of them, so attaching every doc
// comment eagerly would put tens of kilobytes on the wire for every keystroke —
// almost all of it for entries the user never looks at.
//
// Instead each item carries a small reference to the symbol it came from, and
// the client asks for documentation only for the entry it is about to display.
// That is what completionItem/resolve is for.

// Kinds a completion reference can point at.
const (
	refKindType    = "type"
	refKindFunc    = "func"
	refKindMember  = "member"  // a method or field of Key
	refKindVariant = "variant" // a sealed case of Key
)

// completionRef identifies the symbol behind a completion item well enough to
// find its documentation later. It travels to the client as the item's `data`
// and comes back untouched, so it must survive a JSON round-trip: a real client
// hands it back as generic JSON, never as this struct.
//
// Key is the RichAST map key the item was built from — not the label shown.
// That distinction matters: findType and findFunction fall back to matching a
// simple name across every package, in Go map order, while the lists are built
// by their own separate walk over the same maps. Resolving by name could
// therefore answer with a different package's symbol than the one the user is
// looking at, whenever two packages export the same name. Carrying the key the
// item actually came from makes the documentation provably the item's own.
type completionRef struct {
	URI  string `json:"uri"`
	Kind string `json:"kind"`
	Key  string `json:"key"`
	Name string `json:"name,omitempty"` // member or case name, when Key names its owner
}

// typeKey reconstructs the RichAST map key for a type, so a reference can name
// its owner exactly rather than by a display name that findType would have to
// search for again. The analyzer qualifies a type with its package except in
// main and test, where the key is bare — see the fullTypeName construction in
// the analyzer.
func typeKey(tm *transpiler.TypeMetadata) string {
	if tm == nil || tm.Name == "" {
		return ""
	}
	switch tm.Package {
	case "", "main", "test":
		return tm.Name
	}
	return tm.Package + "." + tm.Name
}

// withRef tags a completion item so its documentation can be resolved on demand.
// The URI is filled in once for the whole list by stampRefURI.
func withRef(item lsp.CompletionItem, ref completionRef) lsp.CompletionItem {
	if ref.Key == "" {
		return item
	}
	item.Data = ref
	return item
}

// stampRefURI records the document every tagged item in a list came from.
func stampRefURI(items []lsp.CompletionItem, uri string) {
	for i := range items {
		if ref, ok := items[i].Data.(completionRef); ok {
			ref.URI = uri
			items[i].Data = ref
		}
	}
}

// ResolveCompletionItem fills in the documentation for a single completion item.
//
// Items this server did not tag — keywords, builtins, anything a client echoes
// back from elsewhere — are returned unchanged rather than treated as an error.
// A resolve request is a display-time detail; failing it would put an error
// banner in front of the user for a popup that was about to render fine.
func (h *GalaHandler) ResolveCompletionItem(ctx context.Context, item *lsp.CompletionItem) (*lsp.CompletionItem, error) {
	if item == nil {
		return nil, nil
	}
	ref, ok := decodeCompletionRef(item.Data)
	if !ok {
		return item, nil
	}

	h.mu.Lock()
	richAST := h.richASTs[ref.URI]
	h.mu.Unlock()
	// The document may have been closed, or re-analyzed into a state where the
	// symbol no longer exists, between the list and this request. An item
	// without documentation is the correct outcome, not an error.
	if richAST == nil {
		return item, nil
	}

	if doc := resolveRefDoc(richAST, ref); doc != "" {
		item.Documentation = markdown(doc)
	}
	return item, nil
}

// decodeCompletionRef recovers a completionRef from an item's data field.
//
// The value arrives as whatever encoding/json produced for it — a
// map[string]any in the normal client round-trip, or the original struct when a
// caller passes an item straight back in-process. Re-marshalling handles both
// without assuming which, and yields `false` rather than panicking for a data
// value that is not an object at all.
func decodeCompletionRef(data any) (completionRef, bool) {
	if data == nil {
		return completionRef{}, false
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return completionRef{}, false
	}
	var ref completionRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		return completionRef{}, false
	}
	if ref.Key == "" || ref.Kind == "" {
		return completionRef{}, false
	}
	return ref, true
}

// resolveRefDoc looks up the documentation a reference points at.
func resolveRefDoc(richAST *transpiler.RichAST, ref completionRef) string {
	switch ref.Kind {
	case refKindType:
		if tm := richAST.Types[ref.Key]; tm != nil {
			return tm.Doc
		}
	case refKindFunc:
		if fm := richAST.Functions[ref.Key]; fm != nil {
			return fm.Doc
		}
	case refKindMember:
		if tm := richAST.Types[ref.Key]; tm != nil {
			if m, ok := tm.Methods[ref.Name]; ok {
				return m.Doc
			}
			return tm.FieldDocs[ref.Name]
		}
	case refKindVariant:
		if tm := richAST.Types[ref.Key]; tm != nil {
			for i := range tm.SealedVariants {
				if tm.SealedVariants[i].Name == ref.Name {
					return tm.SealedVariants[i].Doc
				}
			}
		}
	}
	return ""
}
