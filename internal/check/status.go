// Package check holds stock checkers and their result types.
package check

// Status is an item's last known stock status.
type Status string

const (
	Unknown    Status = ""
	InStock    Status = "in_stock"
	OutOfStock Status = "out_of_stock"
)
