package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

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
