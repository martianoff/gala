// echoecho is a tiny fixture binary used by subprocess_test.go.
//
// It reads lines from stdin until EOF and, for each one, emits two responses:
//   - "out: <line>" on stdout
//   - "err: <line>" on stderr
//
// Special inputs:
//   - "BYE"   → exit 0 immediately
//   - "FAIL"  → exit 7 immediately
//   - any other → echoed as above
//
// This shape covers every code path subprocess.go has: stdout reads, stderr
// reads, multi-line writes, clean exit, non-zero exit, and EOF detection.
package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch line {
		case "BYE":
			return
		case "FAIL":
			os.Exit(7)
		default:
			fmt.Fprintln(os.Stdout, "out: "+line)
			fmt.Fprintln(os.Stderr, "err: "+line)
		}
	}
}
