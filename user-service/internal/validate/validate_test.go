package validate

import (
	"testing"

	"user-service/internal/apperr"
)

func TestEmail(t *testing.T) {
	ok := map[string]string{
		" Alice@U.NUS.edu ": "alice@u.nus.edu",
		"a.b+c@x.co":        "a.b+c@x.co",
	}
	for in, want := range ok {
		f := apperr.Fields{}
		if got := Email(f, "email", in); got != want || len(f) != 0 {
			t.Errorf("%q -> %q %v", in, got, f)
		}
	}
	for _, in := range []string{"", "nope", "a@localhost", "Bob <b@x.com>"} {
		f := apperr.Fields{}
		Email(f, "email", in)
		if f["email"] == "" {
			t.Errorf("%q accepted", in)
		}
	}
	if EmailDomain("a@u.nus.edu") != "u.nus.edu" {
		t.Error("EmailDomain")
	}
}

func TestDomain(t *testing.T) {
	f := apperr.Fields{}
	if d := Domain(f, "d", " @U.NUS.edu "); d != "u.nus.edu" || len(f) != 0 {
		t.Errorf("got %q %v", d, f)
	}
	for _, in := range []string{"https://x.com", "nodot", "a b.com"} {
		f := apperr.Fields{}
		Domain(f, "d", in)
		if f["d"] == "" {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestReason(t *testing.T) {
	blank := "  "
	f := apperr.Fields{}
	if Reason(f, "r", &blank, false) != nil || len(f) != 0 {
		t.Error("optional blank")
	}
	Reason(f, "r", nil, true)
	if f["r"] == "" {
		t.Error("required missing")
	}
	ok := " spam "
	f = apperr.Fields{}
	if r := Reason(f, "r", &ok, true); r == nil || *r != "spam" {
		t.Error("trim")
	}
}

func TestFieldsErr(t *testing.T) {
	if (apperr.Fields{}).Err() != nil {
		t.Error("empty should be nil")
	}
	err := apperr.Invalid("x", "bad")
	if ae, ok := err.(*apperr.Error); !ok || ae.Status != 422 || ae.Fields["x"] != "bad" {
		t.Errorf("got %#v", err)
	}
}
