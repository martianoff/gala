// gc stores a map literal's constant entries first and every other one after, as an
// assignment its order pass may move: `{k: 2, "a": 1}` prints map[a:2]. GoCore runs
// literals of constant entries only.
package main

import "fmt"

func main() {
	k := "a"
	fmt.Println("before")
	fmt.Println(map[string]int{k: 2, "a": 1})
}
