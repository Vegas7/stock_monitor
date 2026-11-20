package misaka

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type memStore struct {
	mu sync.Mutex
	t  Tokens
}

func (m *memStore) LoadTokens() Tokens { m.mu.Lock(); defer m.mu.Unlock(); return m.t }
func (m *memStore) SaveTokens(t Tokens) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.t = t
	return nil
}

type rec struct {
	Method, Path, Auth string
	Header         http.Header
	Body           map[string]any
	RawBody        string
}

// fake is a scriptable Misaka API. Handlers are keyed by "METHOD path" (path without /api/).
type fake struct {
	mu       sync.Mutex
	reqs     []rec
	handlers map[string]http.HandlerFunc
	srv      *httptest.Server
}

func newFake(t *testing.T) *fake {
	f := &fake{handlers: map[string]http.HandlerFunc{}}
	f.handlers["POST session/login"] = jsonH(200, `{"user":{"id":1},"access_token":"A1","refresh_token":"R1"}`)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		p := strings.TrimPrefix(r.URL.Path, "/api/")
		f.mu.Lock()
		f.reqs = append(f.reqs, rec{r.Method, p, r.Header.Get("Authorization"), r.Header.Clone(), m, string(b)})
		h := f.handlers[r.Method+" "+p]
		f.mu.Unlock()
		if h == nil {
			w.WriteHeader(404)
			io.WriteString(w, `{"detail":"not found"}`)
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) base() string { return f.srv.URL + "/api/" }

func (f *fake) set(key string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[key] = h
}

func (f *fake) requests(key string) []rec {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rec
	for _, r := range f.reqs {
		if r.Method+" "+r.Path == key {
			out = append(out, r)
		}
	}
	return out
}

func (f *fake) client(st TokenStore) *Client {
	if st == nil {
		st = &memStore{}
	}
	return New(f.base(), "me@example.com", "pw-secret", st, f.srv.Client())
}

func jsonH(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
}

const regionsJSON = `[{"id":11,"slug":"nrt04","name":"Tokyo","available":true}]`
const plansJSON = `[{"id":42,"slug":"s3n-1c1g","name":"S3N 1C1G","available":false,"unavailable_reason":"Out of stock","price_monthly":10,"price_annual":"100.00"}]`
