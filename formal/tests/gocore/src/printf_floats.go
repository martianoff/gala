// fmt.Printf's float verbs, %e %E %f %F %g %G and %v, with flags, width and
// precision (fmt package documentation, "Printing"; strconv.FormatFloat).
package main

import "fmt"

func main() {
	// Rounding is exact, half to even on an exact tie.
	fmt.Printf("%.0f %.0f %.0f %.0f %.1f %.1f\n", 0.5, 1.5, 2.5, 3.5, 0.25, 0.35)
	fmt.Printf("%.2e %.2e %.3e %e %E\n", 9.995, 9.9951, 0.0, 123456.789, 1e-7)
	fmt.Printf("%f %F %.3f %.0f %f\n", 3.14159, 2.0, 1e-4, 999.5, 1e21)
	fmt.Printf("%g %g %g %g %g %G\n", 1e-5, 123456789.0, 100000.0, 1e6, 0.0001, 1e-10)
	fmt.Printf("%.3g %.3g %.3g %.0g %.1g %.10g %.3g\n", 1234.5, 0.0001234, 100.0, 7.5, 0.95, 1.0/3, 1e21)
	// Flags, width and the sign.
	fmt.Printf("[%10.3f] [%-10.2e] [%+.1f] [% .1f] [%08.3f] [%+08.2f] [%-8.1f]\n",
		3.14159, 1234.5, 2.0, 2.0, -1.5, 1.5, -2.25)
	fmt.Printf("[%8v] [%-8v] [%08v] [% v] [%8g]\n", 1.5, 2.5, -0.5, 0.1, 1e-9)
	// Infinities and NaN are padded with spaces, never zeros.
	zero := 0.0
	inf, nan := 1/zero, zero/zero
	fmt.Printf("[%f] [%010f] [%e] [%g] [%-6f] [%+f] [% f] [%f] [%5.1f]\n",
		inf, inf, -inf, inf, -inf, nan, nan, nan, nan)
	// A negative value keeps its sign when it rounds to zero, and so does -0.
	negz := -zero
	fmt.Printf("%.1f %f %e %g %v\n", -0.01, negz, negz, negz, negz)
	// A float32 prints its own value, exactly.
	var f32 float32 = 0.1
	fmt.Printf("%.10f %g %e %v\n", f32, f32, f32, f32)
	// Long exact expansions.
	fmt.Printf("%.20e %.20f\n", 0.1, 0.1)
	fmt.Printf("%.30e\n", 5e-324)
	// 5e-324 is exactly 751 significant digits ending in 5: rounding it to 750 is an
	// exact tie, to even, which strconv's decimal sees whole (no truncation).
	fmt.Printf("%.749e\n", 5e-324)
	fmt.Printf("[%.2v] [%8.3v] [%.3E] [%G] [%E]\n", 3.14159, 2.0/3, 12345.678, 1e-7, inf)
	fmt.Printf("%.0f\n", 1e308)
	s := fmt.Sprintf("%6.2f|%-7.3g|%G", 2.0/3, 2.0/3, 1e100)
	fmt.Println(s, len(s))
}
