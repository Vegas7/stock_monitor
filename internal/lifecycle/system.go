package lifecycle

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// Paths are the files stockmon installs.
type Paths struct {
	Bin, Prev, Unit, ConfigDir, StateDir string
}

func DefaultPaths() Paths {
	return Paths{
		Bin:       "/usr/local/bin/stockmon",
		Prev:      "/usr/local/bin/stockmon.prev",
		Unit:      "/etc/systemd/system/stockmon.service",
		ConfigDir: "/etc/stockmon",
		StateDir:  "/var/lib/stockmon",
	}
}

// System is the OS service manager and user database.
type System interface {
	IsActive() bool
	Restart() error
	StopDisable() error
	RemoveUnit(path string) error
	DaemonReload() error
	DeleteUser() error
}

// Systemd is the real System.
type Systemd struct{}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return errors.New(strings.TrimSpace(name + " " + strings.Join(args, " ") + ": " + string(out)))
	}
	return nil
}

func (Systemd) IsActive() bool {
	return exec.Command("systemctl", "is-active", "--quiet", "stockmon").Run() == nil
}
func (Systemd) Restart() error      { return run("systemctl", "restart", "stockmon") }
func (Systemd) DaemonReload() error { return run("systemctl", "daemon-reload") }
func (Systemd) StopDisable() error  { return run("systemctl", "disable", "--now", "stockmon") }
func (Systemd) RemoveUnit(p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
func (Systemd) DeleteUser() error {
	if exec.Command("id", "-u", "stockmon").Run() != nil {
		return nil // already gone
	}
	return run("userdel", "stockmon")
}
