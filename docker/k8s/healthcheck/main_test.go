package main

import "testing"

func TestIsServingStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: "ok", want: true},
		{status: "SERVING", want: true},
		{status: "SERVING_STATUS_SERVING", want: true},
		{status: "NOT_SERVING", want: false},
		{status: "SERVING_STATUS_NOT_SERVING", want: false},
		{status: "", want: false},
	}

	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			if got := isServingStatus(test.status); got != test.want {
				t.Fatalf("isServingStatus(%q) = %t, want %t", test.status, got, test.want)
			}
		})
	}
}
