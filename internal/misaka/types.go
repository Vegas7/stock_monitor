// Package misaka talks to the Misaka web console's internal JSON API.
// It must not import other stockmon packages (leaf package).
package misaka

import "errors"

// Tokens are the console session tokens.
type Tokens struct {
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
	Sliding string `json:"sliding"`
}

// TokenStore persists session tokens between runs.
type TokenStore interface {
	LoadTokens() Tokens
	SaveTokens(Tokens) error
}

// ErrFormatChanged means the response did not look like what stockmon expects.
var ErrFormatChanged = errors.New("response format changed")
