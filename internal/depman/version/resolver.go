package version

// MVS implements Minimal Version Selection for dependency resolution.
// MVS always selects the minimum version that satisfies all requirements,
// which makes builds reproducible and avoids version churn.

// Requirement represents a dependency requirement.
type Requirement struct {
	Path       string
	Constraint Constraint
}

// Selected represents a selected module version.
type Selected struct {
	Path    string
	Version Version
}

// MVSResult contains the result of MVS resolution.
type MVSResult struct {
	Selected []Selected
	Errors   []error
}

// VersionProvider is an interface for fetching available versions of a module.
type VersionProvider interface {
	// AvailableVersions returns all available versions for a module path.
	AvailableVersions(path string) ([]Version, error)

	// Requirements returns the requirements for a specific module version.
	Requirements(path string, version Version) ([]Requirement, error)
}

// Resolver resolves dependencies using Minimal Version Selection.
type Resolver struct {
	provider VersionProvider
}

// NewResolver creates a new MVS resolver.
func NewResolver(provider VersionProvider) *Resolver {
	return &Resolver{provider: provider}
}

// Resolve resolves dependencies starting from the given requirements.
// It returns the minimum set of versions that satisfy all requirements.
func (r *Resolver) Resolve(requirements []Requirement) (*MVSResult, error) {
	result := &MVSResult{
		Selected: make([]Selected, 0),
		Errors:   make([]error, 0),
	}

	// Track selected versions by path
	selected := make(map[string]Version)
	// Track which paths we've processed
	processed := make(map[string]bool)
	// Queue of paths to process
	queue := make([]string, 0)

	// Initialize with direct requirements
	for _, req := range requirements {
		versions, err := r.provider.AvailableVersions(req.Path)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}

		// Find minimum version satisfying constraint
		version, found := selectMinimum(req.Constraint, versions)
		if !found {
			result.Errors = append(result.Errors, &NoVersionError{Path: req.Path, Constraint: req.Constraint})
			continue
		}

		if existing, ok := selected[req.Path]; ok {
			// Take the maximum of existing and new
			if version.GreaterThan(existing) {
				selected[req.Path] = version
			}
		} else {
			selected[req.Path] = version
			queue = append(queue, req.Path)
		}
	}

	// Process transitive dependencies
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]

		if processed[path] {
			continue
		}
		processed[path] = true

		version := selected[path]
		transitive, err := r.provider.Requirements(path, version)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}

		for _, req := range transitive {
			versions, err := r.provider.AvailableVersions(req.Path)
			if err != nil {
				result.Errors = append(result.Errors, err)
				continue
			}

			// Find minimum version satisfying constraint
			version, found := selectMinimum(req.Constraint, versions)
			if !found {
				result.Errors = append(result.Errors, &NoVersionError{Path: req.Path, Constraint: req.Constraint})
				continue
			}

			if existing, ok := selected[req.Path]; ok {
				// MVS: take the maximum of existing and new
				if version.GreaterThan(existing) {
					selected[req.Path] = version
					// Need to reprocess since version changed
					processed[req.Path] = false
					queue = append(queue, req.Path)
				}
			} else {
				selected[req.Path] = version
				queue = append(queue, req.Path)
			}
		}
	}

	// Convert map to slice
	for path, version := range selected {
		result.Selected = append(result.Selected, Selected{Path: path, Version: version})
	}

	return result, nil
}

// selectMinimum finds the minimum version that satisfies the constraint.
func selectMinimum(constraint Constraint, versions []Version) (Version, bool) {
	// Sort versions in ascending order
	sorted := make([]Version, len(versions))
	copy(sorted, versions)
	Sort(sorted)

	// Find first version that satisfies
	for _, v := range sorted {
		if constraint.Satisfies(v) {
			return v, true
		}
	}

	return Version{}, false
}

// NoVersionError is returned when no version satisfies a constraint.
type NoVersionError struct {
	Path       string
	Constraint Constraint
}

func (e *NoVersionError) Error() string {
	return "no version found for " + e.Path + " satisfying " + e.Constraint.String()
}
