package menu

import (
	"strings"
	"testing"
)

const clearSeq = "\033[H\033[2J"

func TestClearScreenBeforeEachMenu(t *testing.T) {
	e := newEnv(t)
	e.opts.ClearScreen = true
	e.save(misakaCfg())
	// 1 = show items (prints a result), Enter to continue, 0 = exit
	out := e.run("1", "", "0")
	if n := strings.Count(out, clearSeq); n != 2 {
		t.Fatalf("want the screen cleared before each of the 2 menus, got %d:\n%q", n, out)
	}
	if n := strings.Count(out, "Press Enter to continue"); n != 1 {
		t.Fatalf("want one pause so the result can be read, got %d:\n%q", n, out)
	}
	// The result is shown before the pause, and the next menu comes after the clear.
	res := strings.Index(out, "- Tokyo")
	pause := strings.Index(out, "Press Enter to continue")
	last := strings.LastIndex(out, clearSeq)
	if !(res >= 0 && res < pause && pause < last) {
		t.Fatalf("order wrong (result %d, pause %d, clear %d):\n%q", res, pause, last, out)
	}
}

func TestNoPauseWhenGoingBack(t *testing.T) {
	e := newEnv(t)
	e.opts.ClearScreen = true
	e.save(misakaCfg())
	// 7 = auto-order → item 1 → 0 back (sub-menu) → 0 exit
	out := e.run("7", "1", "0", "0")
	if strings.Contains(out, "Press Enter to continue") {
		t.Fatalf("going back printed nothing new, so it must not pause:\n%q", out)
	}
	if n := strings.Count(out, clearSeq); n != 3 {
		t.Fatalf("want main, auto-order and main again each on a clean screen, got %d:\n%q", n, out)
	}
}

func TestInvalidChoicePausesToShowError(t *testing.T) {
	e := newEnv(t)
	e.opts.ClearScreen = true
	out := e.run("42", "", "0")
	if !strings.Contains(out, "Please enter a number from 0 to 9") || !strings.Contains(out, "Press Enter to continue") {
		t.Fatalf("%q", out)
	}
}

func TestNoClearWhenNotATerminal(t *testing.T) {
	e := newEnv(t)
	e.save(misakaCfg())
	out := e.run("1", "0")
	if strings.Contains(out, "\033[") || strings.Contains(out, "Press Enter to continue") {
		t.Fatalf("piped output must stay plain:\n%q", out)
	}
}
