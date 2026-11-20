package redact

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactReplacesAll(t *testing.T) {
	r := New("hunter22", "123:ABC")
	out := r.String("pw=hunter22 tok=123:ABC again hunter22")
	if strings.Contains(out, "hunter22") || strings.Contains(out, "123:ABC") || !strings.Contains(out, "[REDACTED]") {
		t.Fatal(out)
	}
}

func TestRedactIgnoresShort(t *testing.T) {
	if got := New("ab", "").String("abc"); got != "abc" {
		t.Fatal(got)
	}
}

func TestRedactErr(t *testing.T) {
	if got := New("secret1").Err(errors.New("bad secret1")); got != "bad [REDACTED]" {
		t.Fatal(got)
	}
	if New().Err(nil) != "" {
		t.Fatal("nil err")
	}
}

func TestNilRedactorSafe(t *testing.T) {
	var r *Redactor
	if r.String("x") != "x" {
		t.Fatal()
	}
}
