// String comparison is byte order (Go spec, "Comparison operators": strings are
// ordered lexically byte-wise). On valid UTF-8 that is code-point order: multi-byte
// strings, mixed with ASCII, differing in a lead or a continuation byte, prefixes,
// against untyped constants, of a defined string type, and an insertion sort by `<`.
package main

import "fmt"

type Name string

func compare(a, b string) {
	fmt.Println(a, b, a < b, a <= b, a > b, a >= b, a == b, a != b)
}

func main() {
	pairs := [][2]string{
		{"é", "z"},
		{"€", "é"},
		{"aÿ", "aĀ"},
		{"\U0001F600", "￿"},
		{"é", "è"},
		{"€", "₭"},
		{"é", "éa"},
		{"", "a"},
		{"abc", "abd"},
		{"Z", "a"},
		{"日本", "日本語"},
	}
	for _, p := range pairs {
		compare(p[0], p[1])
		compare(p[1], p[0])
	}
	compare("日本", "日本")
	z := "z"
	fmt.Println(z < "é", z >= "é", "€" < "é", "\U0001F600" > "￿", "è" < "é")
	var n, m Name = "é", "€"
	fmt.Println(n < m, n > m, n == "é", m > "￿")
	words := []string{"zebra", "é", "\U0001F600", "apple", "€", "Äpfel", "￿", "éa", "aĀ", "aÿ", "", "è"}
	for i := 1; i < len(words); i++ {
		for j := i; j > 0 && words[j] < words[j-1]; j-- {
			words[j], words[j-1] = words[j-1], words[j]
		}
	}
	for _, w := range words {
		fmt.Println(len(w), w)
	}
}
