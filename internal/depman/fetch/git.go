package fetch

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"
	"golang.org/x/mod/module"

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
	if module.IsPseudoVersion(ver) {
		if err := checkPseudoVersion(modulePath, ver); err != nil {
			return "", "", err
		}
	}
	if path, hash, ok, err := f.cached(modulePath, ver); ok || err != nil {
		return path, hash, err
	}
	// Try a fast shallow clone targeting the exact tag first. If the tag name
	// doesn't match or the server rejects the request, fall back to a full
	// clone that fetches all history and tags.
	_, path, hash, err := f.fetchClone(modulePath, func(dir, gitURL string) (*git.Repository, string, error) {
		repo, err := cloneForVersion(dir, gitURL, ver)
		return repo, ver, err
	})
	return path, hash, err
}

// cached returns the cache directory and hash of modulePath at ver, and
// whether the cache holds it.
func (f *GitFetcher) cached(modulePath, ver string) (string, string, bool, error) {
	if !f.cache.config.IsCached(modulePath, ver) {
		return "", "", false, nil
	}
	modPath := f.cache.config.ModulePath(modulePath, ver)
	hash, err := hashModule(modPath)
	if err != nil {
		return "", "", false, err
	}
	return modPath, hash, true, nil
}

// fetchClone clones modulePath's repository into a temporary directory with
// clone, which returns the clone and the version of the module to take from
// it, then checks that version out and stores it in the cache. It returns the
// version, its cache directory and its hash.
func (f *GitFetcher) fetchClone(modulePath string, clone func(dir, gitURL string) (*git.Repository, string, error)) (string, string, string, error) {
	if err := f.cache.config.EnsureDirs(); err != nil {
		return "", "", "", fmt.Errorf("failed to create cache directories: %w", err)
	}
	tempDir, err := os.MkdirTemp("", "gala-fetch-*")
	if err != nil {
		return "", "", "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	gitURL := f.gitURL(modulePath)
	repo, ver, err := clone(tempDir, gitURL)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to clone repository %s: %w", gitURL, err)
	}
	if path, hash, ok, err := f.cached(modulePath, ver); ok || err != nil {
		return ver, path, hash, err
	}
	if err := checkoutVersion(repo, ver); err != nil {
		return "", "", "", fmt.Errorf("failed to checkout version %s: %w", ver, err)
	}
	if err := f.cache.Store(modulePath, ver, tempDir); err != nil {
		return "", "", "", fmt.Errorf("failed to store in cache: %w", err)
	}
	modPath := f.cache.config.ModulePath(modulePath, ver)
	hash, err := hashModule(modPath)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to compute hash: %w", err)
	}
	return ver, modPath, hash, nil
}

// FetchLatest fetches the latest version of a module: its highest semver tag
// of the major version its path names (v0 or v1 without a suffix, vN for /vN
// or gopkg.in's .vN) or, when the repository has no such tag, the
// pseudo-version of its default branch's head.
// Returns the version string, cached path, and hash.
func (f *GitFetcher) FetchLatest(modulePath string) (string, string, string, error) {
	versions, err := f.ListVersions(modulePath)
	if err != nil {
		return "", "", "", err
	}
	_, pathMajor, _ := module.SplitPathVersion(modulePath)
	versions = slices.DeleteFunc(versions, func(v version.Version) bool {
		return module.CheckPathMajor(v.String(), pathMajor) != nil
	})
	if len(versions) > 0 {
		latest := versions[len(versions)-1].String()
		path, hash, err := f.Fetch(modulePath, latest)
		return latest, path, hash, err
	}
	return f.fetchClone(modulePath, func(dir, gitURL string) (*git.Repository, string, error) {
		return cloneHead(dir, gitURL, modulePath)
	})
}

// cloneHead clones only the head commit of the default branch of the
// repository at gitURL, and returns the clone and the pseudo-version Go gives
// that commit.
func cloneHead(dir, gitURL, modulePath string) (*git.Repository, string, error) {
	repo, err := git.PlainClone(dir, false, &git.CloneOptions{
		URL:        gitURL,
		Depth:      1,
		Tags:       git.NoTags,
		NoCheckout: true,
	})
	if err != nil {
		return nil, "", fmt.Errorf("no versions found for %s: cannot read its default branch: %w", modulePath, err)
	}
	head, err := repo.Head()
	if err != nil {
		return nil, "", fmt.Errorf("no versions found for %s: %w", modulePath, err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, "", fmt.Errorf("no versions found for %s: %w", modulePath, err)
	}
	return repo, pseudoVersion(modulePath, commit.Committer.When, commit.Hash.String()), nil
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
// A Go pseudo-version resolves to the commit it names.
func resolveVersion(repo *git.Repository, ver string) (plumbing.Hash, error) {
	if module.IsPseudoVersion(ver) {
		return resolvePseudoVersion(repo, ver)
	}
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

// checkPseudoVersion rejects a pseudo-version of modulePath in a form Go never
// gives one: a revision that is not 12 lowercase hex digits, or a major
// version other than the one the module path names. It does not check, as Go
// does, that the tag the pseudo-version builds on is an ancestor of the
// commit.
func checkPseudoVersion(modulePath, ver string) error {
	rev, err := module.PseudoVersionRev(ver)
	if err != nil {
		return err
	}
	if len(rev) != 12 || strings.Trim(rev, "0123456789abcdef") != "" {
		return fmt.Errorf("invalid pseudo-version %s: revision %s is not a 12-character commit hash prefix", ver, rev)
	}
	_, pathMajor, _ := module.SplitPathVersion(modulePath)
	if err := module.CheckPathMajor(ver, pathMajor); err != nil {
		return fmt.Errorf("invalid pseudo-version %s for %s: %w", ver, modulePath, err)
	}
	return nil
}

// resolvePseudoVersion resolves a pseudo-version to the commit its revision
// names. As Go does, it rejects one whose timestamp is not that commit's time:
// the version would not be the one Go gives the commit.
func resolvePseudoVersion(repo *git.Repository, ver string) (plumbing.Hash, error) {
	when, err := module.PseudoVersionTime(ver)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	rev, err := module.PseudoVersionRev(ver)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	hash, err := repo.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("version not found: %s: no commit %s", ver, rev)
	}
	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("version not found: %s: %w", ver, err)
	}
	if got := commit.Committer.When; !got.Equal(when) {
		return plumbing.ZeroHash, fmt.Errorf("invalid pseudo-version %s: commit %s was made at %s, not %s",
			ver, rev, got.UTC().Format(time.RFC3339), when.Format(time.RFC3339))
	}
	return *hash, nil
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
// A pseudo-version names a commit, not a tag, so it goes straight to the full
// clone, without tags.
//
// The clone checks nothing out, so checkoutVersion writes the version's files
// once rather than over a checkout of the clone's HEAD.
func cloneForVersion(dir, gitURL, ver string) (*git.Repository, error) {
	if module.IsPseudoVersion(ver) {
		return git.PlainClone(dir, false, &git.CloneOptions{
			URL:        gitURL,
			Tags:       git.NoTags,
			NoCheckout: true,
		})
	}
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

	// Hash the module's gala.mod, when it has one.
	galaModPath := filepath.Join(cachePath, "gala.mod")
	if _, err := os.Stat(galaModPath); err == nil {
		galaModHash, err := sum.HashFile(galaModPath)
		if err != nil {
			return nil, fmt.Errorf("failed to hash %s: %w", galaModPath, err)
		}
		result.GalaModHash = galaModHash
	}

	return result, nil
}
