package lifecycle

import (
	"errors"
	"fmt"
	"os"
)

const (
	PromptConfirm    = "This stops stockmon and removes the service and program. Type yes to continue"
	PromptDeleteData = "Also delete config and state? They contain your Misaka password and Telegram token (recommended)"
)

var ErrCancelled = errors.New("uninstall cancelled")

type UninstallOptions struct {
	Paths    Paths
	Sys      System
	Confirm  func(prompt string) bool
	Notify   func(string)
	Hostname string
}

type Report struct {
	Removed, Kept []string
}

// Uninstall removes stockmon. Nothing is touched unless Confirm(PromptConfirm) is true.
func Uninstall(o UninstallOptions) (Report, error) {
	var rep Report
	if !o.Confirm(PromptConfirm) {
		return rep, ErrCancelled
	}
	deleteData := o.Confirm(PromptDeleteData)
	if o.Notify != nil {
		o.Notify(fmt.Sprintf("stockmon uninstalled from %s", o.Hostname))
	}
	var errs []error
	if err := o.Sys.StopDisable(); err != nil {
		errs = append(errs, err)
	}
	if err := o.Sys.RemoveUnit(o.Paths.Unit); err != nil {
		errs = append(errs, err)
	} else {
		rep.Removed = append(rep.Removed, o.Paths.Unit)
	}
	if err := o.Sys.DaemonReload(); err != nil {
		errs = append(errs, err)
	}
	for _, f := range []string{o.Paths.Bin, o.Paths.Prev} {
		if err := os.Remove(f); err == nil {
			rep.Removed = append(rep.Removed, f)
		} else if !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	for _, d := range []string{o.Paths.ConfigDir, o.Paths.StateDir} {
		if !deleteData {
			rep.Kept = append(rep.Kept, d)
			continue
		}
		if err := os.RemoveAll(d); err != nil {
			errs = append(errs, err)
		} else {
			rep.Removed = append(rep.Removed, d)
		}
	}
	if err := o.Sys.DeleteUser(); err != nil {
		errs = append(errs, err)
	} else {
		rep.Removed = append(rep.Removed, "user stockmon")
	}
	return rep, errors.Join(errs...)
}
