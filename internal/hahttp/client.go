// Package hahttp configures HTTP clients for a selected Home Assistant target.
package hahttp

import (
	"net/http"
	"time"
)

// NewClient creates a client that does not follow redirects. Callers receive
// the redirect response and must check its status code.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
