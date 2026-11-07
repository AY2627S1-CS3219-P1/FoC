package load

import (
	"math"
	"sync/atomic"
)

// numBins exponential buckets in microseconds (1.06x each) cover ~1µs to far
// beyond any realistic latency with fine resolution near the median, so the
// hot path stays one fixed-size slice regardless of how many samples accrue.
const numBins = 1200

const binRatio = 1.06

// histogram is a global, lock-free-ish latency histogram. add performs one
// atomic increment per observed latency; percentiles and max read the bins.
type histogram struct {
	buckets []int64
}

// newHistogram allocates the fixed exponential-bucket slice.
func newHistogram() histogram {
	return histogram{buckets: make([]int64, numBins)}
}

// bucketIndex maps a latency in nanoseconds to an exponential-microsecond bin.
func bucketIndex(ns int64) int {
	if ns <= 0 {
		return 0
	}
	us := float64(ns) / 1000.0
	idx := int(math.Log(us) / math.Log(binRatio))
	if idx < 0 {
		idx = 0
	}
	if idx >= numBins {
		idx = numBins - 1
	}
	return idx
}

func (h *histogram) add(ns int64) {
	atomic.AddInt64(&h.buckets[bucketIndex(ns)], 1)
}

// percentile returns the value at quantile q in microseconds, read at the upper
// edge of the bin whose cumulative count first reaches q of the total.
func (h *histogram) percentile(q float64) float64 {
	total := int64(0)
	for _, b := range h.buckets {
		total += b
	}
	if total == 0 {
		return 0
	}
	var cum int64
	target := q * float64(total)
	for i, b := range h.buckets {
		cum += b
		if float64(cum) >= target {
			return math.Pow(binRatio, float64(i+1))
		}
	}
	return math.Pow(binRatio, float64(numBins-1))
}

// percentiles returns the values at each quantile q in microseconds, in order.
func (h *histogram) percentiles(qs []float64) []float64 {
	out := make([]float64, len(qs))
	for i, q := range qs {
		out[i] = h.percentile(q)
	}
	return out
}

// max returns the upper edge of the highest non-empty bin in microseconds.
func (h *histogram) max() float64 {
	for i := numBins - 1; i >= 0; i-- {
		if h.buckets[i] > 0 {
			return math.Pow(binRatio, float64(i+1))
		}
	}
	return 0
}
