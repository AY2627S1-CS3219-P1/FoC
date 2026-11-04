package main

import "testing"

func TestOriginOf(t *testing.T) {
	cases := map[string]string{
		"http://localhost:5174":       "http://localhost:5174",
		"http://localhost:5174/":      "http://localhost:5174",
		"https://foc.example/app/":    "https://foc.example",
		"http://127.0.0.1:5173?x=1#y": "http://127.0.0.1:5173",
	}
	for base, want := range cases {
		if got, err := originOf(base); err != nil || got != want {
			t.Errorf("originOf(%q) = %q, %v; want %q", base, got, err, want)
		}
	}
	for _, base := range []string{"localhost:5174", "ftp://localhost", "http://", "/app"} {
		if got, err := originOf(base); err == nil {
			t.Errorf("originOf(%q) = %q, want an error", base, got)
		}
	}
}
