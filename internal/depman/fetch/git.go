package fetch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"

	"martianoff/gala/internal/depman/sum"
	"martianoff/gala/internal/depman/version"
)

// GitFetcher fetches GALA packages from Git repositories.
type GitFetcher struct {
	cache *Cache
}

// NewGitFetcher creates a new GitFetcher.
func NewGitFetcher(cache *Cache) *GitFetcher {
	return &GitFetcher{cache: cache}
}

// Fetch downloads a module from a Git repository and stores it in the cache.
// Returns the cached module path and computed hash.
func (f *GitFetcher) Fetch(modulePath, ver string) (string, string, error) {
	// Check if already cached
	if f.cache.config.IsCached(modulePath, ver) {
		modPath := f.cache.config.ModulePath(modulePath, ver)
		hash, err := sum.HashDir(modPath)
		if err != nil {
			return "", "", err
		}
		return modPath, hash, nil
	}

	// Ensure cache directories exist
	if err := f.cache.config.EnsureDirs(); err != nil {
		return "", "", fmt.Errorf("failed to create cache directories: %w", err)
	}

	// Convert module path to Git URL
	gitURL := modulePathToGitURL(modulePath)

	// Create temporary directory for clone
	tempDir, err := os.MkdirTemp("", "gala-fetch-*")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Clone repository
	repo, err := git.PlainClone(tempDir, false, &git.CloneOptions{
		URL:      gitURL,
		Progress: nil, // TODO: Add progress reporting
		Depth:    1,   // Shallow clone for efficiency
		Tags:     git.AllTags,
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to clone repository %s: %w", gitURL, err)
	}

	// Checkout the specific version
	if err := f.checkoutVersion(repo, tempDir, ver); err != nil {
		return "", "", fmt.Errorf("failed to checkout version %s: %w", ver, err)
	}

	// Store in cache
	if err := f.cache.Store(modulePath, ver, tempDir); err != nil {
		return "", "", fmt.Errorf("failed to store in cache: %w", err)
	}

	// Compute hash
	modPath := f.cache.config.ModulePath(modulePath, ver)
	hash, err := sum.HashDir(modPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to compute hash: %w", err)
	}

	return modPath, hash, nil
}

// FetchLatest fetches the latest version of a module.
// Returns the version string, cached path, and hash.
func (f *GitFetcher) FetchLatest(modulePath string) (string, string, string, error) {
	// Get available versions
	versions, err := f.ListVersions(modulePath)
	if err != nil {
		return "", "", "", err
	}

	if len(versions) == 0 {
		return "", "", "", fmt.Errorf("no versions found for %s", modulePath)
	}

	// Get the latest (last) version
	latest := versions[len(versions)-1]
	path, hash, err := f.Fetch(modulePath, latest.String())
	return latest.String(), path, hash, err
}

// ListVersions lists available versions for a module from the remote repository.
func (f *GitFetcher) ListVersions(modulePath string) ([]version.Version, error) {
	gitURL := modulePathToGitURL(modulePath)

	// List remote references
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{gitURL},
	})

	refs, err := remote.List(&git.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list remote refs: %w", err)
	}

	var versions []version.Version
	for _, ref := range refs {
		name := ref.Name()
		if name.IsTag() {
			tagName := name.Short()
			v, err := version.Parse(tagName)
			if err != nil {
				continue // Skip non-semver tags
			}
			versions = append(versions, v)
		}
	}

	// Sort versions
	version.Sort(versions)

	return versions, nil
}

// checkoutVersion checks out a specific version in the repository.
func (f *GitFetcher) checkoutVersion(repo *git.Repository, repoDir, ver string) error {
	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}

	// Try tag first
	tagRef := plumbing.NewTagReferenceName(ver)
	hash, err := repo.ResolveRevision(plumbing.Revision(tagRef))
	if err == nil {
		return worktree.Checkout(&git.CheckoutOptions{
			Hash: *hash,
		})
	}

	// Try branch
	branchRef := plumbing.NewBranchReferenceName(ver)
	hash, err = repo.ResolveRevision(plumbing.Revision(branchRef))
	if err == nil {
		return worktree.Checkout(&git.CheckoutOptions{
			Hash: *hash,
		})
	}

	// Try as commit hash
	hash, err = repo.ResolveRevision(plumbing.Revision(ver))
	if err == nil {
		return worktree.Checkout(&git.CheckoutOptions{
			Hash: *hash,
		})
	}

	return fmt.Errorf("version not found: %s", ver)
}

// modulePathToGitURL converts a module path to a Git URL.
// Supports common hosting services.
func modulePathToGitURL(modulePath string) string {
	// Handle common hosting services
	parts := strings.Split(modulePath, "/")
	if len(parts) < 2 {
		return "https://" + modulePath + ".git"
	}

	host := parts[0]
	switch host {
	case "github.com", "gitlab.com", "bitbucket.org":
		// For these services, the repo is usually the first two path components
		if len(parts) >= 3 {
			return fmt.Sprintf("https://%s/%s/%s.git", host, parts[1], parts[2])
		}
		return "https://" + modulePath + ".git"
	default:
		// Generic handling
		return "https://" + modulePath + ".git"
	}
}

// FetchResult contains the result of a fetch operation.
type FetchResult struct {
	ModulePath  string
	Version     string
	CachePath   string
	Hash        string
	GalaModHash string // Hash of just the gala.mod file, if present
}

// FetchWithInfo fetches a module and returns detailed information.
func (f *GitFetcher) FetchWithInfo(modulePath, ver string) (*FetchResult, error) {
	cachePath, hash, err := f.Fetch(modulePath, ver)
	if err != nil {
		return nil, err
	}

	result := &FetchResult{
		ModulePath: modulePath,
		Version:    ver,
		CachePath:  cachePath,
		Hash:       hash,
	}

	// Try to compute gala.mod hash
	galaModPath := filepath.Join(cachePath, "gala.mod")
	if _, err := os.Stat(galaModPath); err == nil {
		galaModHash, err := sum.HashFile(galaModPath)
		if err == nil {
			result.GalaModHash = galaModHash
		}
	}

	return result, nil
}
