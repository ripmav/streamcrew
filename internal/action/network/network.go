// SPDX-License-Identifier: MIT

// Package network has the action types that reach other hosts (spec
// actions.md, B70 to B77): web_request sends an HTTP request and sets result
// values from the response. They belong to the category "network"
// (Code-ADR-0013) and need net:outbound (ADR-0013).
//
// The requests connect through a Dialer: in production netguard.Dialer,
// which keeps out internal networks in server mode (B77). Platform adapters
// do not use it; their targets are fixed (Code-ADR-0019, point 4).
package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/template"
)

// RequestTimeout is the time a web request has, with redirects and reading
// the response (actions.md B73).
const RequestTimeout = 10 * time.Second

// MaxRedirects is how many redirects a web request follows (B73).
const MaxRedirects = 10

// MaxResponse is the largest response body, 1 MiB (B73).
const MaxResponse = 1 << 20

// ErrTimeout is the error of a web request that took longer than
// RequestTimeout (B73).
var ErrTimeout = errors.New("the request took longer than its time limit")

// Dialer connects to the targets of web requests; netguard.Dialer in
// production.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Ports are what the network types need.
type Ports struct {
	// Templates renders the address, the headers and the body.
	Templates *template.Engine
	// Dialer connects to the targets, also of redirects (B77).
	Dialer Dialer
	// Logger records paths of a JSON response without a value (B76).
	Logger *slog.Logger
}

// ports are the ports of the network types with the HTTP client that all
// web requests share.
type ports struct {
	Ports
	client *http.Client
}

// Descriptors returns the network types with their ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Templates == nil:
		return nil, errors.New("network action types: no template engine")
	case p.Dialer == nil:
		return nil, errors.New("network action types: no dialer")
	case p.Logger == nil:
		return nil, errors.New("network action types: no logger")
	}
	ports := &ports{Ports: p, client: newClient(p.Dialer)}
	return []action.Descriptor{
		action.Descriptor{
			Type:         TypeWebRequest,
			Version:      1,
			Category:     action.CategoryNetwork,
			Capabilities: []capability.Capability{capability.NetOutbound},
			Results:      []string{ResultBody},
			Schema:       webRequestSchema(),
		}.WithKinds(MethodGet, func(m Method) (WebRequest, bool) { return newWebRequest(ports, m) }),
	}, nil
}

// newClient returns the HTTP client of web requests (B72, B73, B77):
//   - every connection goes through d, also those of redirects;
//   - no proxy, so that d sees the real target;
//   - no connection is used twice, so that every request asks d, also
//     after the allowlist changed (Code-ADR-0019, point 5);
//   - RequestTimeout for the whole request, at most MaxRedirects
//     redirects, and only to http and https.
func newClient(d Dialer) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         d.DialContext,
			DisableKeepAlives:   true,
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: RequestTimeout,
		},
		Timeout: RequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > MaxRedirects {
				return fmt.Errorf("more than %d redirects", MaxRedirects)
			}
			return checkScheme(req.URL.Scheme)
		},
	}
}

// checkScheme accepts only http and https (B72).
func checkScheme(scheme string) error {
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%w: scheme %q, only http and https", action.ErrInvalid, scheme)
	}
	return nil
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
