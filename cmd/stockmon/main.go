// Command stockmon monitors stock and auto-orders Misaka VPS plans.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Vegas7/stock_monitor/internal/menu"
	"github.com/Vegas7/stock_monitor/internal/service"
)

var version = "dev"

const usage = `usage: stockmon [--config PATH] [--state PATH] [command]

commands:
  menu       interactive setup menu (default)
  run        run the monitor (used by systemd)
  update     update to the latest release (--check: only report)
  uninstall  remove stockmon from this machine
  version    print the version
`

func main() {
	fs := flag.NewFlagSet("stockmon", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	cfgPath := fs.String("config", "/etc/stockmon/config.json", "config file")
	statePath := fs.String("state", "/var/lib/stockmon/state.json", "state file")
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
			ConfigPath: *cfgPath, StatePath: *statePath, Version: version,
			In: os.Stdin, Out: os.Stdout, ReadSecret: readSecret, ServiceStatus: serviceStatus,
		})
	case "run":
		err = service.Run(ctx, service.Options{ConfigPath: *cfgPath, StatePath: *statePath, Version: version})
	default:
		fs.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "stockmon:", err)
		os.Exit(1)
	}
}
