// Package digestgolden holds a test's results to a golden file of digests:
// one line per case, `key<TAB>input<TAB>result`, the case's name, a digest of
// what it reads and a digest of what it gives. A case whose input changed
// since the golden was written is not compared: its line is stale, and
// refreshing the golden brings it back. A golden is refreshed by running its
// test with an environment variable set to the golden's path in the source
// tree; the test then writes the golden instead of comparing.
package digestgolden

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
)

// Golden is a golden file being checked, or being written.
type Golden struct {
	tb        testing.TB
	want, got map[string][2]string
	// update is the path to write the golden to, "" to compare.
	update string
	// Compared counts the cases held to the golden.
	Compared int
}

// Open reads the golden staged at runfile, the path of the golden in the
// source tree; when updateEnv names a variable that is set, the golden is
// written to its value by Finish instead of compared.
func Open(tb testing.TB, runfile, updateEnv string) *Golden {
	tb.Helper()
	g := &Golden{tb: tb, want: map[string][2]string{}, got: map[string][2]string{}, update: os.Getenv(updateEnv)}
	path, err := bazel.Runfile(runfile)
	if err != nil {
		tb.Fatalf("the golden %s is not staged: %v", runfile, err)
	}
	f, err := os.Open(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r") // a checkout may end lines with CRLF
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			tb.Fatalf("%s: malformed line %q", runfile, line)
		}
		g.want[parts[0]] = [2]string{parts[1], parts[2]}
	}
	if err := sc.Err(); err != nil {
		tb.Fatal(err)
	}
	return g
}

// Digest is the digest of s the golden records. Line ends are read as LF,
// as written and as escaped in JSON (`\r\n`), so that a checkout that turns
// them into CRLF digests as one that does not.
func Digest(s string) string {
	s = NormalizeNewlines(s)
	s = strings.ReplaceAll(s, `\r\n`, `\n`)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// NormalizeNewlines is s with each CRLF line end turned into LF.
func NormalizeNewlines(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// Check records the case key, whose input digests to inputDigest, as giving a
// result that digests to resultDigest (see Digest), and fails the test when
// the golden's line for the same input records another; describe, when not
// nil, explains the difference.
func (g *Golden) Check(key, inputDigest, resultDigest string, describe func() string) {
	g.tb.Helper()
	if _, dup := g.got[key]; dup {
		g.tb.Fatalf("two cases are keyed %q", key)
	}
	entry := [2]string{inputDigest, resultDigest}
	g.got[key] = entry
	if g.update != "" {
		return
	}
	if w, ok := g.want[key]; ok && w[0] == entry[0] {
		g.Compared++
		if w[1] != entry[1] {
			msg := ""
			if describe != nil {
				msg = ":\n" + describe()
			}
			g.tb.Errorf("%s: the result differs from the golden's%s", key, msg)
		}
	}
}

// Updating reports whether the golden is being written.
func (g *Golden) Updating() bool { return g.update != "" }

// Finish writes the golden, headed by header, when it is being written.
func (g *Golden) Finish(header string) {
	g.tb.Helper()
	if g.update == "" {
		return
	}
	keys := make([]string, 0, len(g.got))
	for k := range g.got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(header, "\n"), "\n") {
		b.WriteString("# " + line + "\n")
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", k, g.got[k][0], g.got[k][1])
	}
	if err := os.WriteFile(g.update, []byte(b.String()), 0o644); err != nil {
		g.tb.Fatal(err)
	}
	g.tb.Logf("wrote %d lines to %s", len(keys), g.update)
}
