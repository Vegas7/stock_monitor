// Package redact strips secrets from text before it is logged or sent.
package redact

import "strings"

const mask = "[REDACTED]"

// Redactor replaces known secret values with [REDACTED].
type Redactor struct{ secrets []string }

// New builds a Redactor; secrets shorter than 4 characters are ignored.
func New(secrets ...string) *Redactor {
	r := &Redactor{}
	for _, s := range secrets {
		if len(s) >= 4 {
			r.secrets = append(r.secrets, s)
		}
	}
	return r
}

// String returns s with every secret replaced. A nil Redactor is a no-op.
func (r *Redactor) String(s string) string {
	if r == nil {
		return s
	}
	for _, sec := range r.secrets {
		s = strings.ReplaceAll(s, sec, mask)
	}
	return s
}

// Err returns the redacted error text ("" for nil).
func (r *Redactor) Err(err error) string {
	if err == nil {
		return ""
	}
	return r.String(err.Error())
}
