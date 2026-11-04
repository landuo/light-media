package sourcecharset

import "math"

// Fixed CPython3.12.14 builtin_sum_impl, exact homogeneous float path after
// its initial integer0 + first float transition. Input is only CN's float
// ratios, not arbitrary Python objects or integer/mixed-value sum semantics.
func cnFloatSum(values []float64) float64 {
	result := 0.0
	if len(values) == 0 {
		return result
	}
	result += values[0]
	compensation := 0.0
	for _, x := range values[1:] {
		t := result + x
		if math.Abs(result) >= math.Abs(x) {
			compensation += (result - t) + x
		} else {
			compensation += (x - t) + result
		}
		result = t
	}
	if compensation != 0 && !math.IsNaN(compensation) && !math.IsInf(compensation, 0) {
		result += compensation
	}
	return result
}
