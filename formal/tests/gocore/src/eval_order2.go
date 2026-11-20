package main

import "fmt"

var a = 1

func bump() int {
	a = 10
	return 100
}

func loud() int {
	fmt.Println("loud ran")
	return 1
}

func main() {
	fmt.Println(a, bump() > 0 && true)
	a = 1
	fmt.Println(int64(a) + int64(bump()))
	a = 1
	if a+bump() > 105 {
		fmt.Println("if saw 110")
	}
	x, z := 7, 0
	fmt.Println(x/z + loud())
}
