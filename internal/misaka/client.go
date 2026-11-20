package misaka

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the web console's API base (verify live; spec §11).
const DefaultBaseURL = "https://app.misaka.io/api/"

var (
	// ErrLogin means Misaka rejected the login (or it could not be attempted).
	ErrLogin = errors.New("misaka login failed")
	// ErrLoginBackoff means a login was skipped because the last one failed recently.
	ErrLoginBackoff = fmt.Errorf("%w: waiting before retrying after a failed login", ErrLogin)
)

var loginBackoff = []time.Duration{5 * time.Minute, 15 * time.Minute, 60 * time.Minute}

// APIError is a non-2xx response.
type APIError struct {
	Status       int
	Code         string
	InternalCode string
	Detail       string
}

func (e *APIError) Error() string {
	s := fmt.Sprintf("misaka: HTTP %d", e.Status)
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	if e.InternalCode != "" {
		s += " (" + e.InternalCode + ")"
	}
	return s
}

// Unclear reports whether a request may or may not have taken effect
// (network error, timeout, 5xx, unreadable response). 4xx and login errors are clear.
func Unclear(err error) bool {
	if err == nil || errors.Is(err, ErrLogin) {
		return false
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status >= 500
	}
	return true
}

// Client talks to the console API as a logged-in user.
type Client struct {
	BaseURL  string
	HTTP     *http.Client
	Now      func() time.Time
	username string
	password string
	store    TokenStore

	mu        sync.Mutex // serializes login/refresh
	loginFail int
	lastFail  time.Time
}

func New(baseURL, username, password string, ts TokenStore, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	return &Client{BaseURL: baseURL, HTTP: hc, Now: time.Now, username: username, password: password, store: ts}
}

func (c *Client) scrub(s string) string {
	if len(c.password) >= 4 {
		s = strings.ReplaceAll(s, c.password, "[REDACTED]")
	}
	return s
}

func (c *Client) authHeader(path string) string {
	if strings.HasPrefix(path, "session/") {
		return ""
	}
	t := c.store.LoadTokens()
	tok := t.Access
	if tok == "" {
		tok = t.Sliding
	}
	if tok == "" {
		return ""
	}
	return "Bearer " + tok
}

// raw performs one request. path is relative to BaseURL, or an absolute URL.
func (c *Client) raw(ctx context.Context, method, path string, body, out any) error {
	u := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		u = c.BaseURL + path
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Misaka-User-Agent", "stockmon")
	req.Header.Set("X-Misaka-User-Time", c.Now().UTC().Format(time.RFC3339))
	req.Header.Set("X-Misaka-User-TZ", "Asia/Tokyo")
	req.Header.Set("X-Misaka-User-Locale", "en-US")
	if a := c.authHeader(strings.TrimPrefix(u, c.BaseURL)); a != "" {
		req.Header.Set("Authorization", a)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("misaka %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("misaka %s %s: reading response: %w", method, path, err)
	}
	trim := bytes.TrimSpace(data)
	isJSON := len(trim) > 0 && (trim[0] == '{' || trim[0] == '[')
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &APIError{Status: resp.StatusCode}
		if isJSON {
			var e struct {
				Detail       any    `json:"detail"`
				Code         string `json:"code"`
				InternalCode string `json:"internal_code"`
			}
			if json.Unmarshal(trim, &e) == nil {
				ae.Code, ae.InternalCode = e.Code, e.InternalCode
				if e.Detail != nil {
					ae.Detail = fmt.Sprint(e.Detail)
				}
			} else {
				var arr []any
				if json.Unmarshal(trim, &arr) == nil && len(arr) > 0 {
					ae.Detail = fmt.Sprint(arr[0])
				}
			}
		}
		if ae.Detail == "" {
			ae.Detail = http.StatusText(resp.StatusCode)
		}
		ae.Detail = c.scrub(ae.Detail)
		return ae
	}
	if out == nil {
		return nil
	}
	if !isJSON {
		return fmt.Errorf("misaka %s %s: expected JSON, got %q: %w", method, path, ctype(resp), ErrFormatChanged)
	}
	if err := json.Unmarshal(trim, out); err != nil {
		return fmt.Errorf("misaka %s %s: %v: %w", method, path, err, ErrFormatChanged)
	}
	return nil
}

func ctype(r *http.Response) string {
	if s := r.Header.Get("Content-Type"); s != "" {
		return s
	}
	return "no content type"
}

// needsRenew reports an auth failure that a fresh session may fix
// (token_not_valid, or any 401 on an authenticated path).
func needsRenew(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Code == "token_not_valid" || ae.Status == http.StatusUnauthorized)
}

// do runs an authenticated request, renewing the session once if needed.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	if err := c.ensureSession(ctx); err != nil {
		return err
	}
	err := c.raw(ctx, method, path, body, out)
	if !needsRenew(err) {
		return err
	}
	if rerr := c.renew(ctx); rerr != nil {
		return rerr
	}
	return c.raw(ctx, method, path, body, out)
}

func (c *Client) ensureSession(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.store.LoadTokens()
	if t.Access != "" || t.Sliding != "" {
		return nil
	}
	return c.loginLocked(ctx)
}

func (c *Client) renew(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.store.LoadTokens()
	if t.Refresh != "" {
		var r struct {
			Access  string `json:"access"`
			Refresh string `json:"refresh"`
		}
		if err := c.raw(ctx, http.MethodPost, "session/refresh", map[string]string{"refresh": t.Refresh}, &r); err == nil && r.Access != "" {
			t.Access = r.Access
			if r.Refresh != "" {
				t.Refresh = r.Refresh
			}
			return c.store.SaveTokens(t)
		}
	}
	return c.loginLocked(ctx)
}

// Login logs in with username and password and stores the session tokens.
func (c *Client) Login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loginLocked(ctx)
}

func (c *Client) loginLocked(ctx context.Context) error {
	now := c.Now()
	if c.loginFail > 0 {
		wait := loginBackoff[min(c.loginFail, len(loginBackoff))-1]
		if now.Before(c.lastFail.Add(wait)) {
			return ErrLoginBackoff
		}
	}
	if c.username == "" || c.password == "" {
		return fmt.Errorf("%w: Misaka email/password not set (use the menu)", ErrLogin)
	}
	var r struct {
		User         json.RawMessage `json:"user"`
		AccessToken  string          `json:"access_token"`
		RefreshToken string          `json:"refresh_token"`
		SlidingToken string          `json:"sliding_token"`
		Detail       any             `json:"detail"`
	}
	err := c.raw(ctx, http.MethodPost, "session/login", map[string]string{"username": c.username, "password": c.password}, &r)
	if err == nil && (len(r.User) == 0 || string(r.User) == "null") {
		d := "no user in response"
		if r.Detail != nil {
			d = fmt.Sprint(r.Detail)
		}
		err = errors.New(c.scrub(d))
	}
	// The console keeps the tokens on the user object (Vt(e.user)); top level is a fallback.
	if err == nil {
		var u struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			SlidingToken string `json:"sliding_token"`
		}
		if json.Unmarshal(r.User, &u) == nil && (u.AccessToken != "" || u.SlidingToken != "") {
			r.AccessToken, r.RefreshToken, r.SlidingToken = u.AccessToken, u.RefreshToken, u.SlidingToken
		}
	}
	if err == nil && r.AccessToken == "" && r.SlidingToken == "" {
		err = fmt.Errorf("login response has no token: %w", ErrFormatChanged)
	}
	if err != nil {
		c.loginFail++
		c.lastFail = now
		return fmt.Errorf("%w: %v", ErrLogin, err)
	}
	c.loginFail = 0
	return c.store.SaveTokens(Tokens{Access: r.AccessToken, Refresh: r.RefreshToken, Sliding: r.SlidingToken})
}
