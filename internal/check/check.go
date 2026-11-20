package check

import (
	"context"
	"errors"

	"github.com/Vegas7/stock_monitor/internal/misaka"
)

// Result is one successful check.
type Result struct {
	Status Status
	Detail string
}

// Checker checks one item.
type Checker interface {
	Check(ctx context.Context) (Result, error)
}

// ErrFormatChanged is the same value as misaka.ErrFormatChanged.
var ErrFormatChanged = misaka.ErrFormatChanged

// Urgent reports errors that warn immediately instead of after N failures.
func Urgent(err error) bool {
	return err != nil && (errors.Is(err, ErrFormatChanged) || errors.Is(err, misaka.ErrLogin))
}
