package coreexport_test

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"

	"martianoff/gala/internal/formal/coreexport"
)

// exampleSources is every .gala file staged from //examples, by its path
// in the repository.
func exampleSources(t *testing.T) map[string]string {
	t.Helper()
	files, err := bazel.ListRunfiles()
	if err != nil {
		t.Fatalf("cannot list the runfiles: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		if !strings.HasPrefix(f.ShortPath, "examples/") || !strings.HasSuffix(f.ShortPath, ".gala") {
			continue
		}
		data, err := os.ReadFile(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		out[f.ShortPath] = string(data)
	}
	if len(out) < 200 {
		t.Fatalf("found %d examples in the runfiles; //examples:all_gala_sources is not staged", len(out))
	}
	return out
}

// unparsable is the examples the parser rejects, which no build compiles
// (the transformer's example tests skip them too), with why.
var unparsable = map[string]string{
	"examples/slice_type_param.gala": "uses []T as a type argument, which the grammar does not allow",
}

// TestExamples exports every example. Each one must export, wholly or with
// unsupported nodes, and pass checkDocument. The log reports the coverage:
// how many examples lie wholly inside the subset, and the most frequent
// unsupported constructs.
func TestExamples(t *testing.T) {
	srcs := exampleSources(t)
	paths := slices.Sorted(maps.Keys(srcs))
	total := map[string]int{}    // occurrences of each unsupported construct
	programs := map[string]int{} // examples using each unsupported construct
	var inside []string
	topLevel, topLevelInside := 0, 0
	for _, path := range paths {
		src := coreexport.Source{Path: path, Text: srcs[path]}
		counts, err := exportOne(src)
		if why, known := unparsable[path]; known {
			if err == nil {
				t.Errorf("%s exports now; remove it from unparsable (%s)", path, why)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		isTopLevel := filepath.Dir(path) == "examples"
		if isTopLevel {
			topLevel++
		}
		if len(counts) == 0 {
			inside = append(inside, path)
			if isTopLevel {
				topLevelInside++
			}
		}
		for what, n := range counts {
			total[what] += n
			programs[what]++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Core JSON coverage of examples/ (subset S1)\n")
	fmt.Fprintf(&b, "  all .gala files:       %d, wholly inside S1: %d, not parsable: %d\n",
		len(paths), len(inside), len(unparsable))
	fmt.Fprintf(&b, "  examples/*.gala:       %d, wholly inside S1: %d\n", topLevel, topLevelInside)
	fmt.Fprintf(&b, "  unsupported constructs, by the number of files using them (occurrences):\n")
	whats := slices.SortedFunc(maps.Keys(programs), func(a, b string) int {
		return cmp.Or(cmp.Compare(programs[b], programs[a]), cmp.Compare(a, b))
	})
	for _, what := range whats {
		fmt.Fprintf(&b, "    %-22s %4d files %6d\n", what, programs[what], total[what])
	}
	fmt.Fprintf(&b, "  wholly inside S1:\n")
	for _, path := range inside {
		fmt.Fprintf(&b, "    %s\n", path)
	}
	t.Log(b.String())
}

// exportOne exports src, turning a panic into an error, and checks the
// document.
func exportOne(src coreexport.Source) (counts map[string]int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("export panicked: %v", r)
		}
	}()
	p, err := coreexport.Export([]coreexport.Source{src}, hinted)
	if err != nil {
		return nil, err
	}
	data, err := coreexport.Marshal(p)
	if err != nil {
		return nil, err
	}
	if err := checkDocument(data, src); err != nil {
		return nil, err
	}
	return coreexport.UnsupportedCounts(p)
}
