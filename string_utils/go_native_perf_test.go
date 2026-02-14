package main

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Performance test utilities
func measureNs(iterations int, f func()) int64 {
	start := time.Now()
	for i := 0; i < iterations; i++ {
		f()
	}
	elapsed := time.Since(start)
	return elapsed.Nanoseconds() / int64(iterations)
}

func printResult(name string, nsPerOp int64) {
	fmt.Printf("%-55s %12d ns/op\n", name, nsPerOp)
}

// ============ TEST DATA ============

var shortStr = "Hello, World!"                                            // 13 chars
var mediumStr = strings.Repeat("abcdefghij", 100)                        // 1,000 chars
var longStr = strings.Repeat("abcdefghij", 10000)                        // 100,000 chars
var unicodeStr = strings.Repeat("\u00e9\u00e0\u00fc\u00f1\u00f6", 200)   // 1,000 multi-byte chars
var wordsStr = "hello world foo bar baz qux quux corge grault garply"    // 10 words

// ============ CREATION ============

func benchCreation() {
	ns := measureNs(100000, func() {
		_ = []rune(mediumStr)
	})
	printResult("GoString.ToRunes(1000)", ns)
}

func benchCreationLong() {
	ns := measureNs(1000, func() {
		_ = []rune(longStr)
	})
	printResult("GoString.ToRunes(100000)", ns)
}

// ============ LENGTH ============

func benchLength() {
	ns := measureNs(10000000, func() {
		_ = len(mediumStr)
	})
	printResult("GoString.Len(1000) [bytes]", ns)
}

func benchRuneCount() {
	runes := []rune(mediumStr)
	ns := measureNs(10000000, func() {
		_ = len(runes)
	})
	printResult("GoRunes.Len(1000) [runes]", ns)
}

// ============ CASE TRANSFORMATIONS ============

func benchToUpper() {
	ns := measureNs(10000, func() {
		_ = strings.ToUpper(mediumStr)
	})
	printResult("GoString.ToUpper(1000)", ns)
}

func benchToLower() {
	ns := measureNs(10000, func() {
		_ = strings.ToLower(mediumStr)
	})
	printResult("GoString.ToLower(1000)", ns)
}

// ============ TRIMMING ============

func benchTrim() {
	padded := "   " + mediumStr + "   "
	ns := measureNs(100000, func() {
		_ = strings.TrimSpace(padded)
	})
	printResult("GoString.TrimSpace(1000)", ns)
}

// ============ REPLACE ============

func benchReplaceAll() {
	ns := measureNs(10000, func() {
		_ = strings.ReplaceAll(mediumStr, "abc", "xyz")
	})
	printResult("GoString.ReplaceAll(1000)", ns)
}

// ============ CONTAINS / SEARCH ============

func benchContains() {
	ns := measureNs(1000000, func() {
		_ = strings.Contains(mediumStr, "fghij")
	})
	printResult("GoString.Contains(1000)", ns)
}

func benchIndex() {
	ns := measureNs(1000000, func() {
		_ = strings.Index(mediumStr, "fghij")
	})
	printResult("GoString.Index(1000)", ns)
}

// ============ SPLIT ============

func benchSplit() {
	ns := measureNs(100000, func() {
		_ = strings.Split(wordsStr, " ")
	})
	printResult("GoString.Split(10 words)", ns)
}

func benchSplitLong() {
	longWords := strings.Repeat("word ", 1000)
	ns := measureNs(1000, func() {
		_ = strings.Split(longWords, " ")
	})
	printResult("GoString.Split(1000 words)", ns)
}

// ============ REVERSE ============

func benchReverse() {
	runes := []rune(mediumStr)
	ns := measureNs(10000, func() {
		result := make([]rune, len(runes))
		for i, j := 0, len(runes)-1; j >= 0; i, j = i+1, j-1 {
			result[i] = runes[j]
		}
		_ = string(result)
	})
	printResult("GoString.Reverse(1000)", ns)
}

// ============ FUNCTIONAL OPS (manual loops) ============

func benchMapRunes() {
	runes := []rune(mediumStr)
	ns := measureNs(10000, func() {
		result := make([]rune, len(runes))
		for i, r := range runes {
			result[i] = unicode.ToUpper(r)
		}
		_ = string(result)
	})
	printResult("GoString.MapRunes(1000)", ns)
}

func benchFilterRunes() {
	runes := []rune(mediumStr)
	ns := measureNs(10000, func() {
		result := make([]rune, 0, len(runes))
		for _, r := range runes {
			if unicode.IsLetter(r) {
				result = append(result, r)
			}
		}
		_ = string(result)
	})
	printResult("GoString.FilterRunes(1000)", ns)
}

func benchForEachCount() {
	runes := []rune(mediumStr)
	ns := measureNs(100000, func() {
		count := 0
		for _, r := range runes {
			if unicode.IsLetter(r) {
				count++
			}
		}
		_ = count
	})
	printResult("GoString.ForEach+Count(1000)", ns)
}

// ============ COMPARISON ============

func benchEquals() {
	other := strings.Clone(mediumStr)
	ns := measureNs(1000000, func() {
		_ = mediumStr == other
	})
	printResult("GoString.Equals(1000)", ns)
}

// ============ CONCAT ============

func benchConcat() {
	a := mediumStr[:500]
	b := mediumStr[500:]
	ns := measureNs(100000, func() {
		_ = a + b
	})
	printResult("GoString.Concat(500+500)", ns)
}

// ============ PREDICATES ============

func benchIsAlpha() {
	alpha := strings.Repeat("abcdefghij", 100)
	runes := []rune(alpha)
	ns := measureNs(10000, func() {
		for _, r := range runes {
			if !unicode.IsLetter(r) {
				break
			}
		}
	})
	printResult("GoString.IsAlpha(1000)", ns)
}

// ============ SUBSTRING ============

func benchSubstring() {
	runes := []rune(mediumStr)
	ns := measureNs(100000, func() {
		_ = string(runes[100:500])
	})
	printResult("GoString.Substring(100,500)", ns)
}

// ============ STRINGS.BUILDER ============

func benchBuilderAppend100() {
	ns := measureNs(100000, func() {
		var b strings.Builder
		for i := 0; i < 100; i++ {
			b.WriteString("hello")
		}
		_ = b.String()
	})
	printResult("GoBuilder.Append(100x\"hello\")", ns)
}

func benchBuilderAppend10000() {
	ns := measureNs(1000, func() {
		var b strings.Builder
		for i := 0; i < 10000; i++ {
			b.WriteString("hello")
		}
		_ = b.String()
	})
	printResult("GoBuilder.Append(10000x\"hello\")", ns)
}

func benchBuilderAppendRune() {
	ns := measureNs(1000, func() {
		var b strings.Builder
		for i := 0; i < 10000; i++ {
			b.WriteRune('x')
		}
		_ = b.String()
	})
	printResult("GoBuilder.AppendRune(10000x'x')", ns)
}

func main() {
	fmt.Println("=== Go Native String Performance ===")
	fmt.Println("")

	fmt.Println("--- Creation / Conversion ---")
	benchCreation()
	benchCreationLong()
	fmt.Println("")

	fmt.Println("--- Length ---")
	benchLength()
	benchRuneCount()
	fmt.Println("")

	fmt.Println("--- Case Transformations ---")
	benchToUpper()
	benchToLower()
	fmt.Println("")

	fmt.Println("--- Trimming ---")
	benchTrim()
	fmt.Println("")

	fmt.Println("--- Replace ---")
	benchReplaceAll()
	fmt.Println("")

	fmt.Println("--- Contains / Search ---")
	benchContains()
	benchIndex()
	fmt.Println("")

	fmt.Println("--- Split ---")
	benchSplit()
	benchSplitLong()
	fmt.Println("")

	fmt.Println("--- Reverse ---")
	benchReverse()
	fmt.Println("")

	fmt.Println("--- Functional Operations ---")
	benchMapRunes()
	benchFilterRunes()
	benchForEachCount()
	fmt.Println("")

	fmt.Println("--- Comparison ---")
	benchEquals()
	fmt.Println("")

	fmt.Println("--- Concat ---")
	benchConcat()
	fmt.Println("")

	fmt.Println("--- Predicates ---")
	benchIsAlpha()
	fmt.Println("")

	fmt.Println("--- Substring ---")
	benchSubstring()
	fmt.Println("")

	fmt.Println("--- strings.Builder ---")
	benchBuilderAppend100()
	benchBuilderAppend10000()
	benchBuilderAppendRune()
}
