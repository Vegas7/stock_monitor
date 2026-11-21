package main

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Ctrl+C in the menu must quit it. Before, the signal was swallowed: the menu kept
// running with a cancelled context and every later login or check failed with
// "interrupt signal received".
func TestMenuExitsOnInterrupt(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "stockmon")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "--config", filepath.Join(dir, "config.json"),
		"--state", filepath.Join(dir, "state.json"), "menu")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	// Wait for the main menu prompt.
	r := bufio.NewReader(stdout)
	deadline := time.Now().Add(10 * time.Second)
	var seen strings.Builder
	for !strings.Contains(seen.String(), "0) Exit") {
		if time.Now().After(deadline) {
			t.Fatalf("menu never showed; output: %q", seen.String())
		}
		b, err := r.ReadByte()
		if err != nil {
			t.Fatalf("reading menu: %v; output: %q", err, seen.String())
		}
		seen.WriteByte(b)
	}
	go func() { _, _ = r.WriteTo(discard{}) }()

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("menu exited with %v, want exit code 130", err)
		}
		if ws, ok := ee.Sys().(syscall.WaitStatus); !ok || ws.ExitStatus() != 130 {
			t.Fatalf("menu exited with %v, want exit code 130", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("menu kept running after Ctrl+C")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
