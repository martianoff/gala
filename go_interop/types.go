// Package go_interop provides Go interoperability functions for GALA.
// This package contains helper functions for working with Go's native
// slice and map types, which are useful when interacting with Go libraries.
//
// This package is NOT auto-imported and must be explicitly imported:
//
//	import "martianoff/gala/go_interop"
//
// For type-safe collections, prefer collection_immutable or collection_mutable packages.
package go_interop

import (
	"sync"
	"time"
)

// === Slice Helper Functions for efficient operations ===

// SliceAppendAll appends all elements from src to dst. O(m) where m = len(src).
func SliceAppendAll[T any](dst []T, src []T) []T {
	return append(dst, src...)
}

// SlicePrepend inserts a value at the front of a slice. O(n).
// Uses in-place shift for efficiency.
func SlicePrepend[T any](s []T, value T) []T {
	s = append(s, value)
	copy(s[1:], s[:len(s)-1])
	s[0] = value
	return s
}

// SlicePrependAll prepends all elements from values to s. O(n+m).
func SlicePrependAll[T any](s []T, values []T) []T {
	if len(values) == 0 {
		return s
	}
	result := make([]T, len(s)+len(values))
	copy(result, values)
	copy(result[len(values):], s)
	return result
}

// SliceInsert inserts a value at the given index. O(n).
func SliceInsert[T any](s []T, index int, value T) []T {
	var zero T
	s = append(s, zero)
	copy(s[index+1:], s[index:len(s)-1])
	s[index] = value
	return s
}

// SliceRemoveAt removes the element at the given index. O(n).
func SliceRemoveAt[T any](s []T, index int) []T {
	copy(s[index:], s[index+1:])
	return s[:len(s)-1]
}

// SliceDrop returns a slice with the first n elements removed. O(1).
func SliceDrop[T any](s []T, n int) []T {
	if n >= len(s) {
		return nil
	}
	return s[n:]
}

// SliceTake returns a slice with only the first n elements. O(1).
func SliceTake[T any](s []T, n int) []T {
	if n >= len(s) {
		return s
	}
	return s[:n]
}

// === Slice Creation Functions ===

// SliceEmpty creates an empty slice of type T.
func SliceEmpty[T any]() []T {
	return nil
}

// SliceOf creates a slice from variadic arguments.
func SliceOf[T any](elements ...T) []T {
	return elements
}

// SliceWithCapacity creates an empty slice with pre-allocated capacity.
func SliceWithCapacity[T any](capacity int) []T {
	return make([]T, 0, capacity)
}

// SliceWithSize creates a slice with specified length (zero-initialized).
func SliceWithSize[T any](size int) []T {
	return make([]T, size)
}

// SliceWithSizeAndCapacity creates a slice with specified length and capacity.
func SliceWithSizeAndCapacity[T any](size int, capacity int) []T {
	return make([]T, size, capacity)
}

// SliceCopy creates a copy of an existing slice.
func SliceCopy[T any](elements []T) []T {
	if elements == nil {
		return nil
	}
	result := make([]T, len(elements))
	copy(result, elements)
	return result
}

// === Map Creation Functions ===

// MapEmpty creates an empty map of type map[K]V.
func MapEmpty[K comparable, V any]() map[K]V {
	return make(map[K]V)
}

// MapWithCapacity creates an empty map with pre-allocated capacity.
func MapWithCapacity[K comparable, V any](capacity int) map[K]V {
	return make(map[K]V, capacity)
}

// === Map Mutation Functions ===

// MapPut adds or updates a key-value pair. Returns the map for chaining.
func MapPut[K comparable, V any](m map[K]V, k K, v V) map[K]V {
	m[k] = v
	return m
}

// MapDelete removes a key. Returns the map for chaining.
func MapDelete[K comparable, V any](m map[K]V, k K) map[K]V {
	delete(m, k)
	return m
}

// === Map Query Functions ===

// MapGet returns the value for a key and whether it exists.
func MapGet[K comparable, V any](m map[K]V, k K) (V, bool) {
	v, ok := m[k]
	return v, ok
}

// MapContains checks if a key exists.
func MapContains[K comparable, V any](m map[K]V, k K) bool {
	_, ok := m[k]
	return ok
}

// MapLen returns the number of entries.
func MapLen[K comparable, V any](m map[K]V) int {
	return len(m)
}

// === Map Iteration Functions ===

// MapForEach applies a function to each key-value pair.
func MapForEach[K comparable, V any](m map[K]V, f func(K, V)) {
	for k, v := range m {
		f(k, v)
	}
}

// MapKeys returns a slice of all keys.
func MapKeys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// MapValues returns a slice of all values.
func MapValues[K comparable, V any](m map[K]V) []V {
	values := make([]V, 0, len(m))
	for _, v := range m {
		values = append(values, v)
	}
	return values
}

// === Map Copy Function ===

// MapCopy creates a shallow copy of a map.
func MapCopy[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return nil
	}
	result := make(map[K]V, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

// === Concurrency Primitives ===

// Signal is an empty channel used for signaling completion.
type Signal = chan struct{}

// NewSignal creates a new signal channel.
func NewSignal() Signal {
	return make(chan struct{})
}

// CloseSignal closes a signal channel to broadcast completion.
func CloseSignal(s Signal) {
	close(s)
}

// WaitSignal blocks until the signal is closed.
func WaitSignal(s Signal) {
	<-s
}

// WaitSignalTimeout waits for a signal with timeout.
// Returns true if signal was received, false if timeout occurred.
func WaitSignalTimeout(s Signal, timeout time.Duration) bool {
	select {
	case <-s:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Mutex wraps sync.Mutex for GALA compatibility.
type Mutex struct {
	mu sync.Mutex
}

// NewMutex creates a new Mutex.
func NewMutex() *Mutex {
	return &Mutex{}
}

// Lock acquires the mutex.
func (m *Mutex) Lock() {
	m.mu.Lock()
}

// Unlock releases the mutex.
func (m *Mutex) Unlock() {
	m.mu.Unlock()
}

// RWMutex wraps sync.RWMutex for GALA compatibility.
type RWMutex struct {
	mu sync.RWMutex
}

// NewRWMutex creates a new RWMutex.
func NewRWMutex() *RWMutex {
	return &RWMutex{}
}

// Lock acquires the write lock.
func (m *RWMutex) Lock() {
	m.mu.Lock()
}

// Unlock releases the write lock.
func (m *RWMutex) Unlock() {
	m.mu.Unlock()
}

// RLock acquires the read lock.
func (m *RWMutex) RLock() {
	m.mu.RLock()
}

// RUnlock releases the read lock.
func (m *RWMutex) RUnlock() {
	m.mu.RUnlock()
}

// Once wraps sync.Once for GALA compatibility.
type Once struct {
	once sync.Once
	done bool
}

// NewOnce creates a new Once.
func NewOnce() *Once {
	return &Once{}
}

// Do executes the function only once. Returns true if this call executed the function.
// Accepts func() any to be compatible with GALA's lambda generation.
func (o *Once) Do(f func() any) bool {
	executed := false
	o.once.Do(func() {
		f()
		executed = true
		o.done = true
	})
	return executed
}

// IsDone returns true if Do has been called.
func (o *Once) IsDone() bool {
	return o.done
}

// WaitGroup wraps sync.WaitGroup for GALA compatibility.
type WaitGroup struct {
	wg sync.WaitGroup
}

// NewWaitGroup creates a new WaitGroup.
func NewWaitGroup() *WaitGroup {
	return &WaitGroup{}
}

// Add adds delta to the WaitGroup counter.
func (w *WaitGroup) Add(delta int) {
	w.wg.Add(delta)
}

// Done decrements the WaitGroup counter by one.
func (w *WaitGroup) Done() {
	w.wg.Done()
}

// Wait blocks until the WaitGroup counter is zero.
func (w *WaitGroup) Wait() {
	w.wg.Wait()
}

// Go launches a goroutine. This is a helper to work around GALA's go statement limitations.
// Accepts func() any to be compatible with GALA's lambda generation.
func Go(f func() any) {
	go func() { f() }()
}

// GoWithRecover launches a goroutine with panic recovery.
// If the function panics, the recovery function is called with the panic value.
// Accepts func() any and func(any) any to be compatible with GALA's lambda generation.
func GoWithRecover(f func() any, onPanic func(any) any) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				onPanic(r)
			}
		}()
		f()
	}()
}

// Sleep pauses the current goroutine for the specified duration.
func Sleep(d time.Duration) {
	time.Sleep(d)
}

// After returns a channel that receives the current time after the duration.
func After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// === Error Handling ===

// PanicError wraps a panic value as an error.
type PanicError struct {
	Message string
}

func (e PanicError) Error() string {
	return e.Message
}

// PanicToError converts a panic value to an error.
// If the value is already an error, it returns it directly.
// If it's a string, it wraps it in a PanicError.
// Otherwise, it creates a PanicError with "unknown panic".
func PanicToError(r any) error {
	if e, ok := r.(error); ok {
		return e
	}
	if s, ok := r.(string); ok {
		return PanicError{Message: s}
	}
	return PanicError{Message: "unknown panic"}
}
