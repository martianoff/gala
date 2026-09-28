// Package core is hand-written Go inside a GALA module. Its signatures name
// types from net, whose Go source uses cgo.
package core

import (
	"net"
	"time"
)

// Server hands one in-memory connection to a handler.
type Server struct{ name string }

// NewServer returns a Server; the error result makes GALA see a Try.
func NewServer(name string) (*Server, error) { return &Server{name: name}, nil }

// Serve calls handler with one end of an in-memory pipe.
func (s *Server) Serve(handler func(net.Conn), shutdownTimeout time.Duration) error {
	_ = shutdownTimeout
	local, remote := net.Pipe()
	defer remote.Close()
	handler(local)
	return local.Close()
}
