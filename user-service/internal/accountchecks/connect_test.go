package accountchecks

import "testing"

func TestWorkResultFailsClosedForUnknownAndContradictoryReasons(t *testing.T) {
	known := map[int]string{1: "Account has an active delivery"}
	for _, tc := range []struct {
		has     bool
		reasons []int
	}{
		{false, []int{1}}, {true, []int{99}}, {true, []int{0}},
	} {
		if _, err := workResult(tc.has, tc.reasons, known); err == nil {
			t.Fatalf("accepted unknown/contradictory work: %+v", tc)
		}
	}
	if out, err := workResult(false, []int{}, known); err != nil || out.HasOutstandingWork {
		t.Fatalf("clean response: %+v %v", out, err)
	}
	if out, err := workResult(
		true,
		[]int{1},
		known,
	); err != nil || !out.HasOutstandingWork ||
		out.Reasons[0] != known[1] {
		t.Fatalf("busy response: %+v %v", out, err)
	}
}
