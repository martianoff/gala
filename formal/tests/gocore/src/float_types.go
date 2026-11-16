// Float types without float operations: type assertions and switches on them,
// float variables stored and moved. GoCore runs this much; any operation on a
// float value is reported (unsupported_float.go).
package main

import "fmt"

type celsius float64

func kind(x any) string {
	switch x.(type) {
	case float64:
		return "float64"
	case float32:
		return "float32"
	case celsius:
		return "celsius"
	case int:
		return "int"
	}
	return "other"
}

func asFloat(x any) (float64, bool) {
	f, ok := x.(float64)
	return f, ok
}

type pair struct {
	name  string
	value float64
}

func main() {
	var z float64
	var y float32
	var c celsius
	fmt.Println(kind(z), kind(y), kind(c), kind(3), kind("s"))
	_, ok := asFloat(z)
	_, ok2 := asFloat(7)
	fmt.Println(ok, ok2)
	p := pair{name: "p"}
	q := p
	q.value = z
	xs := []float64{z, z}
	xs = append(xs, z)
	fmt.Println(q.name, len(xs))
	// Between a defined float type and its underlying type: a retype.
	fmt.Println(kind(celsius(z)), kind(float64(c)))
}
