package graph

import (
	"fmt"
	"sort"

	"martianoff/gala/internal/depman/version"
)

// MVS implements Minimal Version Selection algorithm.
// MVS selects the minimum version of each module that satisfies all requirements.
// In practice, this means selecting the maximum of all minimum version requirements.
type MVS struct {
	// requirements maps module path to list of required versions
	requirements map[string][]version.Version
	// selected maps module path to selected version
	selected map[string]version.Version
}

// NewMVS creates a new MVS resolver.
func NewMVS() *MVS {
	return &MVS{
		requirements: make(map[string][]version.Version),
		selected:     make(map[string]version.Version),
	}
}

// AddRequirement adds a version requirement for a module.
func (m *MVS) AddRequirement(modulePath string, ver version.Version) {
	m.requirements[modulePath] = append(m.requirements[modulePath], ver)
}

// AddRequirements adds multiple requirements from a graph.
func (m *MVS) AddRequirements(g *Graph) {
	for _, node := range g.Nodes {
		if node == g.Root {
			continue
		}
		m.AddRequirement(node.Path, node.Version)
	}
}

// Resolve applies MVS to select versions for all modules.
// Returns a map of module path to selected version.
func (m *MVS) Resolve() map[string]version.Version {
	for path, versions := range m.requirements {
		if len(versions) == 0 {
			continue
		}

		// MVS: select the maximum of all required versions
		// This is the minimum version that satisfies all requirements
		maxVer := versions[0]
		for _, v := range versions[1:] {
			if v.GreaterThan(maxVer) {
				maxVer = v
			}
		}
		m.selected[path] = maxVer
	}

	return m.selected
}

// Selected returns the selected version for a module, or false if not selected.
func (m *MVS) Selected(modulePath string) (version.Version, bool) {
	v, ok := m.selected[modulePath]
	return v, ok
}

// BuildList returns a sorted list of selected modules with their versions.
func (m *MVS) BuildList() []ModuleVersion {
	var list []ModuleVersion
	for path, ver := range m.selected {
		list = append(list, ModuleVersion{Path: path, Version: ver})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Path < list[j].Path
	})
	return list
}

// ModuleVersion represents a module at a specific version.
type ModuleVersion struct {
	Path    string
	Version version.Version
}

func (mv ModuleVersion) String() string {
	return fmt.Sprintf("%s@%s", mv.Path, mv.Version.String())
}

// Upgrade upgrades a module to a new version, recalculating dependencies.
// Returns the new build list after the upgrade.
func (m *MVS) Upgrade(modulePath string, newVersion version.Version) map[string]version.Version {
	// Add the upgrade requirement
	m.requirements[modulePath] = append(m.requirements[modulePath], newVersion)

	// Re-resolve
	return m.Resolve()
}

// Downgrade attempts to downgrade a module to a lower version.
// This may fail if other modules require a higher version.
// Returns error if downgrade is not possible.
func (m *MVS) Downgrade(modulePath string, targetVersion version.Version) error {
	versions := m.requirements[modulePath]
	for _, v := range versions {
		if v.GreaterThan(targetVersion) {
			return fmt.Errorf("cannot downgrade %s to %s: version %s is required",
				modulePath, targetVersion.String(), v.String())
		}
	}

	// Replace all requirements with the target version
	m.requirements[modulePath] = []version.Version{targetVersion}
	m.Resolve()
	return nil
}

// Conflicts checks if there are any version conflicts that cannot be resolved.
// Returns a list of conflict descriptions.
func (m *MVS) Conflicts() []string {
	// In MVS, there are no conflicts since we always select the maximum version.
	// However, we can report when multiple versions were required.
	var conflicts []string
	for path, versions := range m.requirements {
		if len(versions) > 1 {
			// Check if versions differ significantly
			unique := make(map[string]bool)
			for _, v := range versions {
				unique[v.String()] = true
			}
			if len(unique) > 1 {
				selected := m.selected[path]
				conflicts = append(conflicts, fmt.Sprintf(
					"%s: multiple versions required, selected %s",
					path, selected.String()))
			}
		}
	}
	return conflicts
}
