package lsp

import "testing"

func TestSplitDoc(t *testing.T) {
	tests := []struct {
		name        string
		doc         string
		params      []string
		wantSummary string
		wantParams  map[string]string
	}{
		{
			name: "trailing Returns line stays prose",
			// Verbatim shape from std/option.gala's Map. Treating an unlabelled
			// line as a continuation of the preceding parameter moved this
			// sentence into f's documentation and out of the summary.
			doc: "Map builds a new option by applying a function to all values of this option.\n" +
				"f: the function to apply.\n" +
				"Returns a new Option containing the result of applying f to this option's value if it is nonempty.",
			params: []string{"f"},
			wantSummary: "Map builds a new option by applying a function to all values of this option.\n" +
				"Returns a new Option containing the result of applying f to this option's value if it is nonempty.",
			wantParams: map[string]string{"f": "the function to apply."},
		},
		{
			name:        "parameter line after a two-line summary",
			doc:         "GetOrElse returns the option's value if the option is Some,\notherwise the default.\ndefaultValue: the default value to return if the option is empty.",
			params:      []string{"defaultValue"},
			wantSummary: "GetOrElse returns the option's value if the option is Some,\notherwise the default.",
			wantParams:  map[string]string{"defaultValue": "the default value to return if the option is empty."},
		},
		{
			name:        "prose with a colon is not mistaken for a parameter",
			doc:         "Encode writes v as JSON.\nNote: the encoder is not reentrant.\nv: the value to encode.",
			params:      []string{"v"},
			wantSummary: "Encode writes v as JSON.\nNote: the encoder is not reentrant.",
			wantParams:  map[string]string{"v": "the value to encode."},
		},
		{
			name:        "undeclared label stays prose",
			doc:         "Run starts the job.\ntimeout: how long to wait.",
			params:      []string{"other"},
			wantSummary: "Run starts the job.\ntimeout: how long to wait.",
			wantParams:  nil,
		},
		{
			name:        "no parameters leaves the doc whole",
			doc:         "Size reports the element count.",
			params:      nil,
			wantSummary: "Size reports the element count.",
			wantParams:  nil,
		},
		{
			name:        "empty doc",
			doc:         "",
			params:      []string{"x"},
			wantSummary: "",
			wantParams:  nil,
		},
		{
			name:        "doc that is only a parameter line",
			doc:         "x: the input.",
			params:      []string{"x"},
			wantSummary: "",
			wantParams:  map[string]string{"x": "the input."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary, params := splitDoc(tt.doc, tt.params)
			if summary != tt.wantSummary {
				t.Errorf("summary\n got: %q\nwant: %q", summary, tt.wantSummary)
			}
			if len(params) != len(tt.wantParams) {
				t.Fatalf("params = %v, want %v", params, tt.wantParams)
			}
			for k, want := range tt.wantParams {
				if params[k] != want {
					t.Errorf("params[%q]\n got: %q\nwant: %q", k, params[k], want)
				}
			}
		})
	}
}

// A doc comment for a parameter that shares its name with a word used in prose
// must still be attributed by the label, not by appearance in the text.
func TestSplitDocMatchesLabelNotMention(t *testing.T) {
	doc := "Filter keeps elements where p returns true.\np: the predicate used for testing."
	summary, params := splitDoc(doc, []string{"p"})
	if summary != "Filter keeps elements where p returns true." {
		t.Errorf("summary = %q", summary)
	}
	if params["p"] != "the predicate used for testing." {
		t.Errorf("params[p] = %q", params["p"])
	}
}
