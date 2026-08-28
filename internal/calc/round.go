// Package calc computes the monthly cost positions (Strom, Heizung/
// Warmwasser, Wasser) from a period's Verbrauch. See Issue #8 for the
// rounding decision this package implements.
package calc

import "math"

// Round2 rounds to 2 decimal places (Cent), kaufmännisch (0,5 Cent always
// rounds up) - math.Round already rounds half-away-from-zero, which for
// the non-negative EUR amounts here is exactly that rule.
func Round2(x float64) float64 {
	return math.Round(x*100) / 100
}
