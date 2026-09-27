// Package box is hand-written Go inside a GALA module, the way a project
// keeps a thin layer over a Go API next to the GALA code that uses it.
package box

type Box struct{ Size int }

func New(size int) *Box { return &Box{Size: size} }
