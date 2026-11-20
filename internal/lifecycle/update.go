package lifecycle

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type UpdateOptions struct {
	Current   string
	CheckOnly bool
	Paths     Paths
	Sys       System
	HTTP      *http.Client
	APIURL    string
	Arch      string
	Notify    func(string)
	StartWait time.Duration
}

// Update installs the latest release, keeping the old binary as Paths.Prev and
// rolling back if the service does not come up.
func Update(ctx context.Context, o UpdateOptions) (string, error) {
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	if o.APIURL == "" {
		o.APIURL = DefaultAPIURL
	}
	if o.Arch == "" {
		o.Arch = runtime.GOARCH
	}
	if o.StartWait == 0 {
		o.StartWait = 10 * time.Second
	}
	if o.Notify == nil {
		o.Notify = func(string) {}
	}
	rel, err := Latest(ctx, o.HTTP, o.APIURL)
	if err != nil {
		return "", err
	}
	if !Newer(o.Current, rel.Tag) {
		return fmt.Sprintf("Already up to date (%s).", o.Current), nil
	}
	if o.CheckOnly {
		return fmt.Sprintf("Update available: %s → %s. Run: sudo stockmon update", o.Current, rel.Tag), nil
	}
	asset := "stockmon_linux_" + o.Arch
	binURL, sumURL := rel.Assets[asset], rel.Assets["checksums.txt"]
	if binURL == "" || sumURL == "" {
		return "", fmt.Errorf("release %s has no %s or checksums.txt", rel.Tag, asset)
	}
	want, err := fetchChecksum(ctx, o.HTTP, sumURL, asset)
	if err != nil {
		return "", err
	}
	tmp, err := download(ctx, o.HTTP, binURL, filepath.Dir(o.Paths.Bin), want)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp) // no-op after a successful rename

	wasActive := o.Sys.IsActive()
	if err := copyFile(o.Paths.Bin, o.Paths.Prev); err != nil {
		return "", fmt.Errorf("keeping the current version: %w", err)
	}
	if err := os.Rename(tmp, o.Paths.Bin); err != nil {
		return "", fmt.Errorf("installing: %w", err)
	}
	if !wasActive {
		return fmt.Sprintf("Updated %s → %s (service not running; start it with: sudo systemctl start stockmon).", o.Current, rel.Tag), nil
	}
	if err := o.Sys.Restart(); err == nil {
		time.Sleep(o.StartWait)
		if o.Sys.IsActive() {
			return fmt.Sprintf("Updated %s → %s and restarted the service.", o.Current, rel.Tag), nil
		}
	}
	// Roll back.
	if err := copyFile(o.Paths.Prev, o.Paths.Bin); err != nil {
		return "", fmt.Errorf("%s did not start and rollback failed: %w", rel.Tag, err)
	}
	o.Sys.Restart()
	msg := fmt.Sprintf("⚠️ Warning: stockmon %s did not start, rolled back to %s", rel.Tag, o.Current)
	o.Notify(msg)
	return "", fmt.Errorf("%s", msg)
}

func get(ctx context.Context, hc *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "stockmon")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	return resp, nil
}

func fetchChecksum(ctx context.Context, hc *http.Client, url, asset string) (string, error) {
	resp, err := get(ctx, hc, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 64<<10))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", asset)
}

// download writes url into a temp file in dir and verifies its SHA-256.
func download(ctx context.Context, hc *http.Client, url, dir, wantHex string) (string, error) {
	resp, err := get(ctx, hc, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	f, err := os.CreateTemp(dir, ".stockmon-new-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 200<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != wantHex {
		err = fmt.Errorf("checksum mismatch for downloaded binary; nothing was changed")
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o755)
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// copyFile copies src to dst atomically (temp + rename), mode 0755.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), ".stockmon-copy-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(out.Name(), 0o755)
	}
	if err == nil {
		err = os.Rename(out.Name(), dst)
	}
	if err != nil {
		os.Remove(out.Name())
	}
	return err
}
