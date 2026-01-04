package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Printf("Usage: gala_test_runner <binary> <expected_file> (got %d args: %v)\n", len(os.Args), os.Args)
		os.Exit(1)
	}

	binaryPath := os.Args[len(os.Args)-2]
	expectedPath := os.Args[len(os.Args)-1]

	// fmt.Printf("Running binary: %s\n", binaryPath)
	// fmt.Printf("Expected file: %s\n", expectedPath)

	cmd := exec.Command(binaryPath)
	// CombinedOutput captures both stdout and stderr, which is important
	// because GALA's println currently maps to Go's built-in println which prints to stderr.
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Execution failed: %v\n", err)
		fmt.Printf("Output: %s\n", string(out))
		os.Exit(1)
	}

	actual := string(out)
	expectedBytes, err := os.ReadFile(expectedPath)
	if err != nil {
		fmt.Printf("Failed to read expected file: %v\n", err)
		os.Exit(1)
	}
	expected := string(expectedBytes)

	// Normalize
	actualNormalized := normalize(actual)
	expectedNormalized := normalize(expected)

	if actualNormalized != expectedNormalized {
		fmt.Printf("Output mismatch!\n")
		fmt.Printf("Expected:\n%s\n", expectedNormalized)
		fmt.Printf("Actual:\n%s\n", actualNormalized)
		os.Exit(1)
	}

	fmt.Println("Test passed!")
}

func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(s)
}
