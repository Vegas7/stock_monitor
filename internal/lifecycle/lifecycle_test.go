package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSys struct {
	active      []bool // successive IsActive answers; last repeats
	calls       int
	restarts    int
	stopped     bool
	unitRemoved string
	reloaded    bool
	userDeleted bool
	order       []string
}

func (f *fakeSys) IsActive() bool {
	v := f.active[min(f.calls, len(f.active)-1)]
	f.calls++
	return v
}
func (f *fakeSys) Restart() error { f.restarts++; f.order = append(f.order, "restart"); return nil }
func (f *fakeSys) StopDisable() error {
	f.stopped = true
	f.order = append(f.order, "stop")
	return nil
}
func (f *fakeSys) RemoveUnit(p string) error { f.unitRemoved = p; os.Remove(p); return nil }
func (f *fakeSys) DaemonReload() error       { f.reloaded = true; return nil }
func (f *fakeSys) DeleteUser() error         { f.userDeleted = true; return nil }

type relServer struct {
	srv       *httptest.Server
	downloads atomic.Int32
}

func newRel(t *testing.T, tag string, bin []byte, sumOverride string) *relServer {
	r := &relServer{}
	sum := sha256.Sum256(bin)
	hexsum := hex.EncodeToString(sum[:])
	if sumOverride != "" {
		hexsum = sumOverride
	}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		base := "http://" + q.Host
		switch q.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":"stockmon_linux_amd64","browser_download_url":"%s/dl/bin"},{"name":"checksums.txt","browser_download_url":"%s/dl/sums"}]}`, tag, base, base)
		case "/dl/bin":
			r.downloads.Add(1)
			w.Write(bin)
		case "/dl/sums":
			fmt.Fprintf(w, "%s  stockmon_linux_amd64\nffff  stockmon_linux_arm64\n", hexsum)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func paths(t *testing.T) Paths {
	d := t.TempDir()
	p := Paths{Bin: filepath.Join(d, "bin", "stockmon"), Prev: filepath.Join(d, "bin", "stockmon.prev"),
		Unit: filepath.Join(d, "stockmon.service"), ConfigDir: filepath.Join(d, "etc"), StateDir: filepath.Join(d, "lib")}
	os.MkdirAll(filepath.Dir(p.Bin), 0o755)
	os.WriteFile(p.Bin, []byte("OLD"), 0o755)
	os.WriteFile(p.Unit, []byte("[Unit]"), 0o644)
	os.MkdirAll(p.ConfigDir, 0o750)
	os.WriteFile(filepath.Join(p.ConfigDir, "config.json"), []byte("{}"), 0o640)
	os.MkdirAll(p.StateDir, 0o750)
	return p
}

func upd(r *relServer, p Paths, sys System, notes *[]string) UpdateOptions {
	return UpdateOptions{Current: "v0.1.0", Paths: p, Sys: sys, HTTP: r.srv.Client(), APIURL: r.srv.URL + "/latest",
		Arch: "amd64", StartWait: time.Millisecond, Notify: func(s string) { *notes = append(*notes, s) }}
}

func read(p string) string { b, _ := os.ReadFile(p); return string(b) }

func onlyFiles(t *testing.T, dir string, want ...string) {
	ents, _ := os.ReadDir(dir)
	var got []string
	for _, e := range ents {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files in %s: %v want %v", dir, got, want)
	}
}

var ctx = context.Background()

func TestNewer(t *testing.T) {
	if !Newer("v0.1.0", "v0.1.1") || Newer("v0.2.0", "v0.1.9") || !Newer("dev", "v0.0.1") || Newer("v1.0.0", "v1.0.0") {
		t.Fatal()
	}
}

func TestUpdateAlreadyCurrent(t *testing.T) {
	r := newRel(t, "v0.1.0", []byte("NEW"), "")
	var notes []string
	msg, err := Update(ctx, upd(r, paths(t), &fakeSys{active: []bool{true}}, &notes))
	if err != nil || !strings.Contains(msg, "up to date") || r.downloads.Load() != 0 {
		t.Fatal(msg, err)
	}
}

func TestUpdateCheckOnly(t *testing.T) {
	r := newRel(t, "v0.2.0", []byte("NEW"), "")
	p := paths(t)
	var notes []string
	o := upd(r, p, &fakeSys{active: []bool{true}}, &notes)
	o.CheckOnly = true
	msg, err := Update(ctx, o)
	if err != nil || !strings.Contains(msg, "v0.1.0 → v0.2.0") || r.downloads.Load() != 0 || read(p.Bin) != "OLD" {
		t.Fatal(msg, err)
	}
}

func TestUpdateChecksumMismatchChangesNothing(t *testing.T) {
	r := newRel(t, "v0.2.0", []byte("NEW"), strings.Repeat("0", 64))
	p := paths(t)
	var notes []string
	_, err := Update(ctx, upd(r, p, &fakeSys{active: []bool{true}}, &notes))
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatal(err)
	}
	if read(p.Bin) != "OLD" {
		t.Fatal("binary changed")
	}
	onlyFiles(t, filepath.Dir(p.Bin), "stockmon")
}

func TestUpdateSwapsAndRestarts(t *testing.T) {
	r := newRel(t, "v0.2.0", []byte("NEW"), "")
	p := paths(t)
	sys := &fakeSys{active: []bool{true}}
	var notes []string
	msg, err := Update(ctx, upd(r, p, sys, &notes))
	if err != nil || !strings.Contains(msg, "v0.2.0") {
		t.Fatal(msg, err)
	}
	if read(p.Bin) != "NEW" || read(p.Prev) != "OLD" || sys.restarts != 1 {
		t.Fatal(read(p.Bin), read(p.Prev), sys.restarts)
	}
	if fi, _ := os.Stat(p.Bin); fi.Mode().Perm() != 0o755 {
		t.Fatal(fi.Mode())
	}
	onlyFiles(t, filepath.Dir(p.Bin), "stockmon", "stockmon.prev")
}

func TestUpdateRollsBackWhenNotActive(t *testing.T) {
	r := newRel(t, "v0.2.0", []byte("NEW"), "")
	p := paths(t)
	sys := &fakeSys{active: []bool{true, false, true}}
	var notes []string
	_, err := Update(ctx, upd(r, p, sys, &notes))
	if err == nil || read(p.Bin) != "OLD" || sys.restarts != 2 {
		t.Fatal(err, read(p.Bin), sys.restarts)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "rolled back") {
		t.Fatal(notes)
	}
}

func TestUpdateServiceStoppedNoRestart(t *testing.T) {
	r := newRel(t, "v0.2.0", []byte("NEW"), "")
	p := paths(t)
	sys := &fakeSys{active: []bool{false}}
	var notes []string
	if _, err := Update(ctx, upd(r, p, sys, &notes)); err != nil || sys.restarts != 0 || read(p.Bin) != "NEW" {
		t.Fatal(err, sys.restarts)
	}
}

func uninst(p Paths, sys System, answers map[string]bool, notes *[]string) UninstallOptions {
	return UninstallOptions{Paths: p, Sys: sys, Hostname: "vps1",
		Confirm: func(prompt string) bool { return answers[prompt] },
		Notify: func(s string) {
			// config must still exist when notifying
			*notes = append(*notes, s)
		}}
}

func TestUninstallDeclined(t *testing.T) {
	p := paths(t)
	sys := &fakeSys{active: []bool{true}}
	var notes []string
	_, err := Uninstall(uninst(p, sys, map[string]bool{}, &notes))
	if !errors.Is(err, ErrCancelled) || read(p.Bin) != "OLD" || sys.stopped || len(notes) != 0 {
		t.Fatal(err)
	}
}

func TestUninstallKeepConfig(t *testing.T) {
	p := paths(t)
	os.WriteFile(p.Prev, []byte("PREV"), 0o755)
	sys := &fakeSys{active: []bool{true}}
	var notes []string
	var cfgExistedAtNotify bool
	o := uninst(p, sys, map[string]bool{PromptConfirm: true, PromptDeleteData: false}, &notes)
	o.Notify = func(s string) {
		_, err := os.Stat(filepath.Join(p.ConfigDir, "config.json"))
		cfgExistedAtNotify = err == nil && !sys.stopped
		notes = append(notes, s)
	}
	rep, err := Uninstall(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{p.Bin, p.Prev, p.Unit} {
		if _, err := os.Stat(f); err == nil {
			t.Fatal("still exists:", f)
		}
	}
	if _, err := os.Stat(p.ConfigDir); err != nil {
		t.Fatal("config dir should be kept")
	}
	if !sys.stopped || !sys.reloaded || !sys.userDeleted || !cfgExistedAtNotify {
		t.Fatal(sys, cfgExistedAtNotify)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "uninstalled from vps1") {
		t.Fatal(notes)
	}
	if len(rep.Kept) != 2 || len(rep.Removed) < 3 {
		t.Fatal(rep)
	}
}

func TestUninstallDeleteAll(t *testing.T) {
	p := paths(t)
	sys := &fakeSys{active: []bool{true}}
	var notes []string
	if _, err := Uninstall(uninst(p, sys, map[string]bool{PromptConfirm: true, PromptDeleteData: true}, &notes)); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.ConfigDir, p.StateDir} {
		if _, err := os.Stat(d); err == nil {
			t.Fatal("not deleted:", d)
		}
	}
}

var _ = io.EOF
