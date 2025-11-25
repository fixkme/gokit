package util

import "math"

func AddInt64(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		// 溢出
		return math.MaxInt64, false
	} else if b < 0 && a < math.MinInt64-b {
		// 溢出
		return math.MinInt64, false
	}
	// 未发生溢出
	return a + b, true
}
