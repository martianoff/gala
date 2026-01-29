package commands

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/graph"
	"martianoff/gala/internal/depman/mod"
)

var modGraphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Print the module dependency graph",
	Long: `Print the module dependency graph in text format.

Each line shows a module and its dependencies.

Examples:
  gala mod graph`,
	Run: runModGraph,
}

func runModGraph(cmd *cobra.Command, args []string) {
	// Load gala.mod
	galaMod, err := mod.ParseFile("gala.mod")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Run 'gala mod init' first.")
		os.Exit(1)
	}

	if len(galaMod.Require) == 0 {
		fmt.Println("No dependencies.")
		return
	}

	// Build the dependency graph
	config := fetch.DefaultConfig()
	cache := fetch.NewCache(config)
	fetcher := fetch.NewGitFetcher(cache)
	builder := graph.NewBuilder(cache, fetcher)

	g, err := builder.Build(galaMod)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building graph: %v\n", err)
		os.Exit(1)
	}

	// Check for cycles
	if cycleErr := g.DetectCycles(); cycleErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", cycleErr)
	}

	// Print the graph
	printGraph(g)
}

func printGraph(g *graph.Graph) {
	// Collect all edges
	type edge struct {
		from    string
		to      string
		version string
	}
	var edges []edge

	for _, node := range g.Nodes {
		for _, e := range node.Children {
			edges = append(edges, edge{
				from:    node.Path,
				to:      e.To.Path,
				version: e.To.Version.String(),
			})
		}
	}

	// Sort edges for consistent output
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].from != edges[j].from {
			return edges[i].from < edges[j].from
		}
		return edges[i].to < edges[j].to
	})

	// Print edges in "from to@version" format (like go mod graph)
	for _, e := range edges {
		fmt.Printf("%s %s@%s\n", e.from, e.to, e.version)
	}
}
