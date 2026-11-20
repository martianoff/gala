// String comparison is byte order (Go spec, "Comparison operators": strings are
// ordered lexically byte-wise). On valid UTF-8 that is code-point order: multi-byte
// strings, mixed with ASCII, prefixes, and an insertion sort by `<`.
package main

import "fmt"

func compare(a, b string) {
	fmt.Println(a, b, a < b, a <= b, a > b, a >= b, a == b, a != b)
}

func main() {
	pairs := [][2]string{
		{"é", "z"},
		{"€", "é"},
		{"aÿ", "aĀ"},
		{"😀", "￿"},
		{"é", "éa"},
		{"", "a"},
		{"abc", "abd"},
		{"Z", "a"},
		{"日本", "日本語"},
		{"x", "x"},
	}
	for _, p := range pairs {
		compare(p[0], p[1])
		compare(p[1], p[0])
	}
	words := []string{"zebra", "é", "😀", "apple", "€", "Äpfel", "￿", "éa", "aĀ", "aÿ", ""}
	for i := 1; i < len(words); i++ {
		for j := i; j > 0 && words[j] < words[j-1]; j-- {
			words[j], words[j-1] = words[j-1], words[j]
		}
	}
	for _, w := range words {
		fmt.Println(len(w), w)
	}
}
