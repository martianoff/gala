// Unbounded recursion: Go aborts with a stack overflow, GoCore runs out of
// fuel. Neither is compared; the fixture checks GoCore ends inconclusive.
package main

import "fmt"

func spin(n int) int { return spin(n+1) + 1 }

func main() {
	fmt.Println("start")
	fmt.Println(spin(0))
}
