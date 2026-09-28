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
	// gitURL maps a module path to the repository to clone. Tests point it
	// at a local repository.
	gitURL func(modulePath string) string
}

// NewGitFetcher creates a new GitFetcher.
func NewGitFetcher(cache *Cache) *GitFetcher {
	return &GitFetcher{cache: cache, gitURL: modulePathToGitURL}
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
	gitURL := f.gitURL(modulePath)

	// Create temporary directory for clone
	tempDir, err := os.MkdirTemp("", "gala-fetch-*")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Clone repository.
	// Try a fast shallow clone targeting the exact tag first. If the tag name
	// doesn't match or the server rejects the request, fall back to a full
	// clone that fetches all history and tags.
	repo, err := cloneForVersion(tempDir, gitURL, ver)
	if err != nil {
		return "", "", fmt.Errorf("failed to clone repository %s: %w", gitURL, err)
	}

	// Checkout the specific version
	if err := checkoutVersion(repo, ver); err != nil {
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
	gitURL := f.gitURL(modulePath)

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

// checkoutVersion checks out a specific version in a clone the fetcher owns.
// It discards anything in the clone's worktree, so repo must be a clone
// made for this fetch (see cloneForVersion), never a checkout someone edits.
func checkoutVersion(repo *git.Repository, ver string) error {
	hash, err := resolveVersion(repo, ver)
	if err != nil {
		return err
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}
	// Force: there are no local changes to protect, so the worktree is not
	// compared against the index at all. Without it go-git refuses to check
	// out over any "unstaged changes" it sees, and on Windows it sees one in
	// every checked-out file the repository records as executable (mode
	// 100755), since the filesystem has no exec bit to match. File modes do
	// not matter here: the cache stores every file as 0644 (see
	// copyModuleFiles).
	return worktree.Checkout(&git.CheckoutOptions{Hash: hash, Force: true})
}

// versionTagNames returns the tag names ver may be published under: as
// written, and without its v prefix.
func versionTagNames(ver string) []string {
	if trimmed, ok := strings.CutPrefix(ver, "v"); ok {
		return []string{ver, trimmed}
	}
	return []string{ver}
}

// resolveVersion resolves ver as a tag, then a branch, then a commit hash.
func resolveVersion(repo *git.Repository, ver string) (plumbing.Hash, error) {
	var candidates []plumbing.Revision
	for _, tagName := range versionTagNames(ver) {
		candidates = append(candidates, plumbing.Revision(plumbing.NewTagReferenceName(tagName)))
	}
	candidates = append(candidates,
		plumbing.Revision(plumbing.NewBranchReferenceName(ver)),
		plumbing.Revision(ver),
	)
	for _, rev := range candidates {
		if hash, err := repo.ResolveRevision(rev); err == nil {
			return *hash, nil
		}
	}
	return plumbing.ZeroHash, fmt.Errorf("version not found: %s", ver)
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

// cloneForVersion clones a repository so that the given version tag is
// available locally. It first attempts a shallow clone referencing the tag
// directly (fast: fetches only one commit). If that fails — because the tag
// name doesn't match any remote ref, or the server doesn't support the
// upload-pack request — it falls back to a full clone with all tags.
//
// The clone checks nothing out, so checkoutVersion writes the version's files
// once rather than over a checkout of the clone's HEAD.
func cloneForVersion(dir, gitURL, ver string) (*git.Repository, error) {
	// Try each candidate tag name as a direct shallow clone. This is the fast
	// path: depth=1 with a specific ref.
	for _, tagName := range versionTagNames(ver) {
		repo, err := git.PlainClone(dir, false, &git.CloneOptions{
			URL:           gitURL,
			ReferenceName: plumbing.NewTagReferenceName(tagName),
			Depth:         1,
			Tags:          git.NoTags,
			NoCheckout:    true,
		})
		if err == nil {
			return repo, nil
		}
		// Clean up the failed attempt so the next try starts fresh.
		os.RemoveAll(dir)
		os.MkdirAll(dir, 0755)
	}

	// Fallback: full clone with all tags — always correct.
	return git.PlainClone(dir, false, &git.CloneOptions{
		URL:        gitURL,
		Tags:       git.AllTags,
		NoCheckout: true,
	})
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
