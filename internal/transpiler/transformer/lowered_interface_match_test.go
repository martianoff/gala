package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestLoweredInterfacePattern pins GALA-E0109: a type pattern that tests for
// an interface a type has every method of only through a method generated as
// a free function (HashMap.Keys names another generic type) would never
// match in Go, so it is rejected instead of silently falling through. A
// pattern whose candidates keep the methods as methods is accepted, and a
// static use of such an interface is GALA-E0100.
func TestLoweredInterfacePattern(t *testing.T) {
	const header = `package main

import . "martianoff/gala/collection_immutable"

type HasKeys interface {
    Keys() HashSet[string]
}

type Sized interface {
    Size() int
}

struct Shelf[T any](Items Array[T])

func (s Shelf[T]) Size() int = s.Items.Size()

// Shelf's package declares HasKeys, so Shelf keeps Keys as a method.
func (s Shelf[T]) Keys() HashSet[string] = HashSetOf("shelf")

`
	t.Run("rejects an interface only lowered methods satisfy", func(t *testing.T) {
		_, err := transpileBareVariant(t, header+`func probe(x any) string = x match {
    case h: HasKeys => "keys"
    case _ => "none"
}

func main() = Println(probe(HashMapOf(("a", 1))))
`)
		require.Error(t, err)
		require.Contains(t, err.Error(), string(galaerr.CodeInterfaceViaLoweredMethod))
		require.Contains(t, err.Error(), "never matches `HasKeys`")
		require.Contains(t, err.Error(), "its Keys is generated as a free function")
	})

	t.Run("accepts an interface whose methods stay methods", func(t *testing.T) {
		src, err := transpileBareVariant(t, header+`func probe(x any) string = x match {
    case s: Sized => s"size ${s.Size()}"
    case _ => "none"
}

func main() = Println(probe(ArrayOf(1, 2)))
`)
		require.NoError(t, err)
		require.Contains(t, src, "Sized")
	})

	t.Run("accepts an interface no lowered method could implement", func(t *testing.T) {
		// HashMap.Keys returns a HashSet, so it never implemented this one.
		_, err := transpileBareVariant(t, header+`type KeyList interface {
    Keys() Array[string]
}

func probe(x any) string = x match {
    case k: KeyList => k.Keys().MkString(",")
    case _ => "none"
}

func main() = Println(probe(HashMapOf(("a", 1))))
`)
		require.NoError(t, err)
	})

	t.Run("rejects a static use as E0100", func(t *testing.T) {
		_, err := transpileBareVariant(t, header+`func keys(h HasKeys) HashSet[string] = h.Keys()

func main() = Println(keys(HashMapOf(("a", 1))))
`)
		require.Error(t, err)
		require.Contains(t, err.Error(), string(galaerr.CodeInterfaceNotImplemented))
		require.Contains(t, err.Error(), "its Keys is generated as a free function")
	})

	t.Run("accepts a static use through a kept method", func(t *testing.T) {
		_, err := transpileBareVariant(t, header+`func keys(h HasKeys) HashSet[string] = h.Keys()

func main() = Println(keys(Shelf(ArrayOf(1))))
`)
		require.NoError(t, err)
	})
}
