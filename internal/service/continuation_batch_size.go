package service

// A zero/negative request asks for the previous effective size. Explicit
// valid sizes replace it; out-of-range sizes remain bounded to 10,000.
func effectiveContinuationBatchSize(requested, previous int) int {
	if requested <= 0 {
		requested = previous
	}
	if requested <= 0 || requested > 10000 {
		return 10000
	}
	return requested
}
