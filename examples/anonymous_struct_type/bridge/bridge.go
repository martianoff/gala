// Package bridge is Go code that takes and returns signal channels typed by
// the anonymous struct type struct{}, as Go APIs such as context do.
package bridge

// Fire closes ch, a signal channel this Go API takes as a chan struct{}.
func Fire(ch chan struct{}) {
	close(ch)
}

// Await blocks until ch, a signal channel this Go API takes receive-only, is
// closed.
func Await(ch <-chan struct{}) string {
	<-ch
	return "closed"
}

// Fired returns a signal channel that is already closed.
func Fired() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
