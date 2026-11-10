package reservation_test

import (
	"math"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
)

func TestPayable(t *testing.T) {
	const T = time.Hour
	tests := []struct {
		name    string
		reward  int64
		elapsed time.Duration
		want    int64
	}{
		{"instant", 100, 0, 100},
		{"on time", 100, T, 100},
		{"just late", 100, T + time.Nanosecond, 99},
		{"half late", 100, T + T/2, 50},
		{"rounds down", 7, T + T/3, 4},
		{"just before twice", 100, 2*T - time.Nanosecond, 0},
		{"twice", 100, 2 * T, 0},
		{"past twice", 100, 3 * T, 0},
		{"huge reward", math.MaxInt64, T + T/2, math.MaxInt64 / 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reservation.Payable(tt.reward, tt.elapsed, T); got != tt.want {
				t.Fatalf("Payable(%d, %v, %v) = %d, want %d", tt.reward, tt.elapsed, T, got, tt.want)
			}
		})
	}
}
