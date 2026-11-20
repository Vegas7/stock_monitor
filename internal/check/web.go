package check

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/Vegas7/stock_monitor/internal/config"
)

// MaxBody caps how much of a page is read (memory bound).
const MaxBody = 2 << 20

// BrowserUA is sent so ordinary shops serve their normal page.
const BrowserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

type web struct {
	it config.WebItem
	hc *http.Client
}

// NewWeb returns a checker for a plain HTML page.
func NewWeb(it config.WebItem, hc *http.Client) Checker { return &web{it, hc} }

func (w *web) Check(ctx context.Context) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.it.URL, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", BrowserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := w.hc.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return Result{}, fmt.Errorf("reading page: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Result{}, fmt.Errorf("HTTP %d from %s", resp.StatusCode, w.it.URL)
	}

	var found bool
	var what string
	switch w.it.Match {
	case config.MatchSelector:
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
		if err != nil {
			return Result{}, fmt.Errorf("parsing page: %w", err)
		}
		sel, err := safeFind(doc, w.it.Value)
		if err != nil {
			return Result{}, err
		}
		if w.it.ValueText != "" {
			want := strings.ToLower(w.it.ValueText)
			sel = sel.FilterFunction(func(_ int, s *goquery.Selection) bool {
				return strings.Contains(strings.ToLower(s.Text()), want)
			})
		}
		found = sel.Length() > 0
		what = fmt.Sprintf("selector %q", w.it.Value)
	default:
		found = bytes.Contains(bytes.ToLower(body), []byte(strings.ToLower(w.it.Value)))
		what = fmt.Sprintf("keyword %q", w.it.Value)
	}

	inStock := found == (w.it.PresentMeans == config.MeansInStock)
	st := OutOfStock
	if inStock {
		st = InStock
	}
	verb := "not found"
	if found {
		verb = "found"
	}
	detail := fmt.Sprintf("HTTP %d, %d KB, %s %s", resp.StatusCode, (len(body)+1023)/1024, what, verb)
	return Result{Status: st, Detail: detail}, nil
}

// safeFind turns cascadia's panic on a bad selector into an error.
func safeFind(doc *goquery.Document, sel string) (s *goquery.Selection, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("invalid CSS selector %q: %v", sel, r)
		}
	}()
	return doc.Find(sel), nil
}
