package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"
)

var stdinReader = bufio.NewReader(os.Stdin)

// readSecret reads a line without echo when stdin is a terminal.
func readSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Println()
		return string(b), err
	}
	line, err := stdinReader.ReadString('\n')
	return strings.TrimSpace(line), err
}

// exitOnInterrupt makes Ctrl+C (or SIGTERM) quit the interactive menu at once,
// restoring the terminal first in case a hidden password prompt had turned echo off.
// Every config write is atomic, so quitting at any point is safe.
func exitOnInterrupt() {
	fd := int(os.Stdin.Fd())
	var st *term.State
	if term.IsTerminal(fd) {
		st, _ = term.GetState(fd)
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		if st != nil {
			_ = term.Restore(fd, st)
		}
		fmt.Println("\nExited.")
		os.Exit(130)
	}()
}

func serviceStatus() string {
	out, err := exec.Command("systemctl", "is-active", "stockmon").Output()
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	if err != nil {
		return "not installed"
	}
	return "unknown"
}
