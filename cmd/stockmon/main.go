// Command stockmon monitors stock and auto-orders Misaka VPS plans.
package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "version" {
		fmt.Println(version)
		return
	}
	fmt.Fprintln(os.Stderr, "usage: stockmon [menu|run|update|uninstall|version]")
	os.Exit(2)
}
