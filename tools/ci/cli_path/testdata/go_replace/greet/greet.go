// Package greet is a Go module the fixture's go.mod replaces with this
// directory; no module of its path can be downloaded.
package greet

// Hello returns a greeting from the replacement.
func Hello(name string) string {
	return "hello, " + name + ", from the local replacement"
}
