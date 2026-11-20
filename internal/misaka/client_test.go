package misaka

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

var ctx = context.Background()

func TestLoginStoresTokens(t *testing.T) {
	f := newFake(t)
	st := &memStore{}
	if err := f.client(st).Login(ctx); err != nil {
		t.Fatal(err)
	}
	r := f.requests("POST session/login")[0]
	if r.Body["username"] != "me@example.com" || r.Body["password"] != "pw-secret" {
		t.Fatal(r.Body)
	}
	if got := st.LoadTokens(); got.Access != "A1" || got.Refresh != "R1" {
		t.Fatal(got)
	}
}

func TestSlidingTokenLogin(t *testing.T) {
	f := newFake(t)
	f.set("POST session/login", jsonH(200, `{"user":{"id":1},"sliding_token":"S1"}`))
	f.set("GET mc2/regions", jsonH(200, regionsJSON))
	st := &memStore{}
	if _, err := f.client(st).Regions(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.requests("GET mc2/regions")[0].Auth; got != "Bearer S1" {
		t.Fatal(got)
	}
}

func TestFirstCallLogsIn(t *testing.T) {
	f := newFake(t)
	f.set("GET mc2/regions", jsonH(200, regionsJSON))
	if _, err := f.client(nil).Regions(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.requests("POST session/login")) != 1 {
		t.Fatal("no login")
	}
	if got := f.requests("GET mc2/regions")[0].Auth; got != "Bearer A1" {
		t.Fatal(got)
	}
}

func TestTokenNotValidRefreshesAndRetries(t *testing.T) {
	f := newFake(t)
	st := &memStore{t: Tokens{Access: "OLD", Refresh: "R0"}}
	f.set("GET mc2/regions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer OLD" {
			jsonH(401, `{"code":"token_not_valid","detail":"expired"}`)(w, r)
			return
		}
		jsonH(200, regionsJSON)(w, r)
	})
	f.set("POST session/refresh", jsonH(200, `{"access":"NEW","refresh":"R2"}`))
	if _, err := f.client(st).Regions(ctx); err != nil {
		t.Fatal(err)
	}
	if f.requests("POST session/refresh")[0].Body["refresh"] != "R0" {
		t.Fatal("refresh body")
	}
	if got := st.LoadTokens(); got.Access != "NEW" || got.Refresh != "R2" {
		t.Fatal(got)
	}
	if len(f.requests("POST session/login")) != 0 {
		t.Fatal("should not log in")
	}
}

func TestRefreshFailFallsBackToLogin(t *testing.T) {
	f := newFake(t)
	st := &memStore{t: Tokens{Access: "OLD", Refresh: "R0"}}
	f.set("GET mc2/regions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer OLD" {
			jsonH(401, `{"code":"token_not_valid"}`)(w, r)
			return
		}
		jsonH(200, regionsJSON)(w, r)
	})
	f.set("POST session/refresh", jsonH(401, `{"code":"token_not_valid","detail":"refresh expired"}`))
	if _, err := f.client(st).Regions(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.requests("POST session/login")) != 1 || st.LoadTokens().Access != "A1" {
		t.Fatal("expected re-login")
	}
}

func TestLoginFailureWrapsErrLoginAndBacksOff(t *testing.T) {
	f := newFake(t)
	f.set("POST session/login", jsonH(400, `{"detail":"Invalid credentials"}`))
	c := f.client(nil)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }
	err := c.Login(ctx)
	if !errors.Is(err, ErrLogin) || errors.Is(err, ErrLoginBackoff) {
		t.Fatal(err)
	}
	attempts := func() int { return len(f.requests("POST session/login")) }
	step := func(d time.Duration, wantAttempts int) {
		t.Helper()
		now = now.Add(d)
		c.Login(ctx)
		if attempts() != wantAttempts {
			t.Fatalf("after +%v attempts=%d want %d", d, attempts(), wantAttempts)
		}
	}
	if err := c.Login(ctx); !errors.Is(err, ErrLoginBackoff) || !errors.Is(err, ErrLogin) {
		t.Fatal(err)
	}
	step(4*time.Minute, 1)
	step(1*time.Minute, 2)  // 5m after 1st failure
	step(14*time.Minute, 2) // 15m backoff now
	step(1*time.Minute, 3)
	step(59*time.Minute, 3) // 60m backoff
	step(1*time.Minute, 4)
	step(60*time.Minute, 5) // stays 60m
}

func TestSessionPathsHaveNoAuthHeader(t *testing.T) {
	f := newFake(t)
	st := &memStore{t: Tokens{Access: "X"}}
	f.client(st).Login(ctx)
	if a := f.requests("POST session/login")[0].Auth; a != "" {
		t.Fatal(a)
	}
}

func TestMisakaHeadersSent(t *testing.T) {
	f := newFake(t)
	f.set("GET mc2/regions", jsonH(200, regionsJSON))
	f.client(nil).Regions(ctx)
	h := f.requests("GET mc2/regions")[0].Header
	if _, err := time.Parse(time.RFC3339, h.Get("X-Misaka-User-Time")); err != nil {
		t.Fatal(err)
	}
	if h.Get("X-Misaka-User-TZ") != "Asia/Tokyo" || h.Get("X-Misaka-User-Locale") != "en-US" ||
		h.Get("X-Misaka-User-Agent") != "stockmon" || h.Get("Accept") != "application/json" {
		t.Fatal(h)
	}
}

func TestUnclear(t *testing.T) {
	if !Unclear(&APIError{Status: 502}) || Unclear(&APIError{Status: 400, Detail: "x"}) {
		t.Fatal("status")
	}
	if !Unclear(context.DeadlineExceeded) || !Unclear(ErrFormatChanged) {
		t.Fatal("timeout/format")
	}
	if Unclear(ErrLogin) || Unclear(nil) {
		t.Fatal("login/nil")
	}
}

func TestErrorsDoNotLeakPassword(t *testing.T) {
	f := newFake(t)
	f.set("POST session/login", jsonH(400, `{"detail":"bad password pw-secret"}`))
	err := f.client(nil).Login(ctx)
	if err == nil || contains(err.Error(), "pw-secret") {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
