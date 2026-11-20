// Command stockmon monitors stock and auto-orders Misaka VPS plans.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/lifecycle"
	"github.com/Vegas7/stock_monitor/internal/menu"
	"github.com/Vegas7/stock_monitor/internal/service"
	"github.com/Vegas7/stock_monitor/internal/telegram"
)

var version = "dev"

const usage = `usage: stockmon [--config PATH] [--state PATH] [command]

commands:
  menu              interactive setup menu (default)
  run               run the monitor (used by systemd)
  update [--check]  update to the latest release (--check: only report)
  uninstall         remove stockmon from this machine
  version           print the version
`

type app struct {
	cfgPath, statePath string
}

func main() {
	fs := flag.NewFlagSet("stockmon", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	a := &app{}
	fs.StringVar(&a.cfgPath, "config", "/etc/stockmon/config.json", "config file")
	fs.StringVar(&a.statePath, "state", "/var/lib/stockmon/state.json", "state file")
	fs.Parse(os.Args[1:])
	args := fs.Args()
	cmd := "menu"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "version":
		fmt.Println(version)
	case "menu":
		err = menu.Run(ctx, menu.Options{
			ConfigPath: a.cfgPath, StatePath: a.statePath, Version: version,
			In: os.Stdin, Out: os.Stdout, ReadSecret: readSecret, ServiceStatus: serviceStatus,
			Update:    func(ctx context.Context) error { return a.update(ctx, false) },
			Uninstall: a.uninstall,
		})
	case "run":
		err = service.Run(ctx, service.Options{ConfigPath: a.cfgPath, StatePath: a.statePath, Version: version,
			Latest: func(ctx context.Context) (string, error) {
				rel, err := lifecycle.Latest(ctx, &http.Client{Timeout: 30 * time.Second}, lifecycle.DefaultAPIURL)
				return rel.Tag, err
			}})
	case "update":
		err = a.update(ctx, len(args) > 0 && args[0] == "--check")
	case "uninstall":
		err = a.uninstall(ctx)
		if errors.Is(err, lifecycle.ErrCancelled) {
			fmt.Println("Cancelled; nothing was changed.")
			err = nil
		}
	default:
		fs.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "stockmon:", err)
		os.Exit(1)
	}
}

func needRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("this needs root: run it with sudo")
	}
	return nil
}

// notify sends a Telegram message if Telegram is configured; failures are printed, not fatal.
func (a *app) notify(text string) {
	c, err := config.Load(a.cfgPath)
	if err != nil || c.Telegram.BotToken == "" || c.Telegram.ChatID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := telegram.New(c.Telegram.BotToken).Send(ctx, c.Telegram.ChatID, text); err != nil {
		fmt.Fprintln(os.Stderr, "telegram:", err)
	}
}

func (a *app) update(ctx context.Context, checkOnly bool) error {
	if !checkOnly {
		if err := needRoot(); err != nil {
			return err
		}
	}
	msg, err := lifecycle.Update(ctx, lifecycle.UpdateOptions{
		Current: version, CheckOnly: checkOnly, Paths: lifecycle.DefaultPaths(), Sys: lifecycle.Systemd{}, Notify: a.notify,
	})
	if msg != "" {
		fmt.Println(msg)
	}
	return err
}

func (a *app) uninstall(ctx context.Context) error {
	if err := needRoot(); err != nil {
		return err
	}
	host, _ := os.Hostname()
	rep, err := lifecycle.Uninstall(lifecycle.UninstallOptions{
		Paths: lifecycle.DefaultPaths(), Sys: lifecycle.Systemd{}, Hostname: host, Notify: a.notify,
		Confirm: func(prompt string) bool {
			if prompt == lifecycle.PromptConfirm {
				return ask(prompt+": ") == "yes"
			}
			return !strings.EqualFold(ask(prompt+" [Y/n]: "), "n")
		},
	})
	for _, r := range rep.Removed {
		fmt.Println("removed:", r)
	}
	for _, k := range rep.Kept {
		fmt.Println("kept:   ", k)
	}
	return err
}

func ask(prompt string) string {
	fmt.Print(prompt)
	line, _ := stdinReader.ReadString('\n')
	return strings.TrimSpace(line)
}
