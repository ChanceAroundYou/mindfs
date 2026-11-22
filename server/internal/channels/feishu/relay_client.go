package feishu

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

var errCrossHostRedirect = errors.New("feishu relay: cross-host redirect refused")

// NewRelayClient builds the HTTP client shared by the card relay and the answer
// relay. Go forwards custom headers such as X-MindFS-Relay-Token across
// redirects, so a redirect to another host (or scheme) must fail the call
// instead of handing the token to a server the node never configured.
func NewRelayClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 {
				return nil
			}
			first := via[0].URL
			if !strings.EqualFold(req.URL.Scheme, first.Scheme) ||
				!strings.EqualFold(req.URL.Host, first.Host) {
				return errCrossHostRedirect
			}
			return nil
		},
	}
}
