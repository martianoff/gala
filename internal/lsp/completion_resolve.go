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

// completion item kinds a reference can point at.
const (
	refKindType    = "type"
	refKindFunc    = "func"
	refKindMethod  = "method"
	refKindField   = "field"
	refKindVariant = "variant"
	refKindPkgType = "pkgtype"
	refKindPkgFunc = "pkgfunc"
)

// completionRef identifies the symbol behind a completion item well enough to
// find its documentation later. It travels to the client as the item's `data`
// and comes back untouched, so it must survive a JSON round-trip: the client
// hands it back as generic JSON, never as this struct.
type completionRef struct {
	URI   string `json:"uri"`
	Kind  string `json:"kind"`
	Owner string `json:"owner,omitempty"` // receiver type, or package name
	Name  string `json:"name"`
}

// withRef tags a completion item so its documentation can be resolved on demand.
func withRef(item lsp.CompletionItem, ref completionRef) lsp.CompletionItem {
	if ref.Name == "" || ref.URI == "" {
		return item
	}
	item.Data = ref
	return item
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
// without assuming which.
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
	if ref.Name == "" || ref.Kind == "" {
		return completionRef{}, false
	}
	return ref, true
}

// resolveRefDoc looks up the documentation a reference points at.
func resolveRefDoc(richAST *transpiler.RichAST, ref completionRef) string {
	switch ref.Kind {
	case refKindType:
		if tm := findType(richAST, ref.Name); tm != nil {
			return tm.Doc
		}
	case refKindFunc:
		if fm := findFunction(richAST, ref.Name); fm != nil {
			return fm.Doc
		}
	case refKindPkgType:
		if tm := findType(richAST, ref.Owner+"."+ref.Name); tm != nil && tm.Package == ref.Owner {
			return tm.Doc
		}
	case refKindPkgFunc:
		if fm := findFunction(richAST, ref.Owner+"."+ref.Name); fm != nil {
			return fm.Doc
		}
	case refKindMethod:
		if tm := findType(richAST, ref.Owner); tm != nil {
			if m, ok := tm.Methods[ref.Name]; ok {
				return m.Doc
			}
		}
	case refKindField:
		if tm := findType(richAST, ref.Owner); tm != nil {
			return tm.FieldDocs[ref.Name]
		}
	case refKindVariant:
		if tm := findType(richAST, ref.Owner); tm != nil {
			for i := range tm.SealedVariants {
				if tm.SealedVariants[i].Name == ref.Name {
					return tm.SealedVariants[i].Doc
				}
			}
		}
	}
	return ""
}
