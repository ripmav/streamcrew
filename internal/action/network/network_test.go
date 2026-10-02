// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/network"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/netguard"
	"github.com/ripmav/streamcrew/internal/template"
)

// update reports whether golden files are written first (Code-ADR-0006).
func update() bool {
	return os.Getenv("STREAMCREW_UPDATE_GOLDEN") != ""
}

// logs is a log that tests read.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// dialFunc is a network.Dialer of a function.
type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

func (f dialFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}

// seen is what the server of the tests received.
type seen struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
}

func (s *seen) add(r *http.Request, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r)
	s.bodies = append(s.bodies, body)
}

func (s *seen) last() (*http.Request, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[len(s.requests)-1], s.bodies[len(s.bodies)-1]
}

// fixture is a running engine with the network types and a test server on
// the in-memory network, whose handler routes are set by the tests.
type fixture struct {
	t         *testing.T
	reg       *action.Registry
	harness   *actiontest.Harness
	templates *template.Engine
	mux       *http.ServeMux
	seen      *seen
	logs      *logs
	lines     []string
}

// newFixture returns a fixture whose core has the capabilities granted
// (net:outbound if none are given). Create it inside synctest.Test.
func newFixture(t *testing.T, granted ...capability.Capability) *fixture {
	t.Helper()
	if granted == nil {
		granted = []capability.Capability{capability.NetOutbound}
	}
	f := &fixture{t: t, mux: http.NewServeMux(), seen: &seen{}, logs: &logs{}}
	srv := httptest.NewTestServer(t, f.mux)
	transport, ok := srv.Client().Transport.(*http.Transport)
	require.True(t, ok)
	f.reg, f.templates = registry(t, dialFunc(transport.DialContext), f.logs, granted...)
	f.harness = actiontest.NewHarness(t, f.reg)
	return f
}

// registry returns the network types with a template engine that knows the
// arguments and the values of the run.
func registry(t *testing.T, d network.Dialer, log *logs, granted ...capability.Capability) (*action.Registry, *template.Engine) {
	t.Helper()
	identifiers, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily())
	require.NoError(t, err)
	templates := template.New(identifiers)
	ds, err := network.Descriptors(network.Ports{Templates: templates, Dialer: d, Logger: slog.New(slog.NewTextHandler(log, nil))})
	require.NoError(t, err)
	set, err := capability.NewSet(granted...)
	require.NoError(t, err)
	reg, err := action.NewRegistry(set, ds...)
	require.NoError(t, err)
	return reg, templates
}

// echo handles path by recording the request and answering with status,
// content type and body.
func (f *fixture) echo(path string, status int, contentType, body string) {
	f.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		f.seen.add(r, string(data))
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

// request returns a web request of the document doc, without "type".
func (f *fixture) request(doc string) network.WebRequest {
	f.t.Helper()
	d, ok := f.reg.Descriptor(network.TypeWebRequest)
	require.True(f.t, ok)
	a, err := d.Decode([]byte(doc), json.DefaultOptionsV2())
	require.NoError(f.t, err, doc)
	require.NoError(f.t, a.Validate(), doc)
	w, ok := a.(network.WebRequest)
	require.True(f.t, ok)
	return w
}

// show returns an action that renders text and records it.
func (f *fixture) show(text string) command.Action {
	return probe{fn: func(ctx context.Context, run *engine.Run) error {
		out, err := f.templates.Render(ctx, template.Parse(text), run.Scope(), template.Text)
		f.lines = append(f.lines, out)
		return err
	}}
}

// start runs a command with the actions and the arguments.
func (f *fixture) start(args []string, actions ...command.Action) engine.Instance {
	return f.harness.Start(f.harness.Command("x", actions...), engine.Params{Args: args, ArgsText: strings.Join(args, " ")})
}

// probe is an action that runs fn.
type probe struct {
	fn func(ctx context.Context, run *engine.Run) error
}

func (probe) DocType() string                                      { return "probe" }
func (probe) Validate() error                                      { return nil }
func (probe) Enabled() bool                                        { return true }
func (p probe) Perform(ctx context.Context, run *engine.Run) error { return p.fn(ctx, run) }

func TestConformance(t *testing.T) {
	t.Parallel()
	reg, _ := registry(t, &net.Dialer{}, &logs{})
	d, ok := reg.Descriptor(network.TypeWebRequest)
	require.True(t, ok)
	assert.Equal(t, []capability.Capability{capability.NetOutbound}, d.Capabilities)
	assert.Equal(t, []string{network.ResultBody}, d.Results)
	actiontest.Suite{Descriptor: d, Update: update(), Examples: []actiontest.Example{
		{Name: "post json", Doc: `{"type":"web_request","kind":"post","url":"https://api.example.invalid/users/$arg1text","headers":[{"name":"Content-Type","value":"application/json"},{"name":"Authorization","value":"Bearer $token"}],"body":"{\"name\":\"$username\"}","response":{"as":"json","values":[{"path":"job\\title","name":"title"},{"path":"items\\0\\id","name":"first"}]}}`, Valid: true},
		{Name: "get with the defaults", Doc: `{"type":"web_request","kind":"get","url":"https://example.invalid/"}`, Valid: true},
		{Name: "get text", Doc: `{"type":"web_request","kind":"get","url":"http://example.invalid/$arg1text","headers":[],"response":{"as":"text"}}`, Valid: true},
		{Name: "put", Doc: `{"type":"web_request","enabled":false,"kind":"put","url":"https://example.invalid/x","body":"","response":{"as":"ignore"}}`, Valid: true},
		{Name: "delete", Doc: `{"type":"web_request","kind":"delete","url":"https://example.invalid/x"}`, Valid: true},
		{Name: "kind missing", Doc: `{"type":"web_request","url":"https://example.invalid/"}`},
		{Name: "unknown kind", Doc: `{"type":"web_request","kind":"patch","url":"https://example.invalid/"}`},
		{Name: "url missing", Doc: `{"type":"web_request","kind":"get"}`},
		{Name: "empty url", Doc: `{"type":"web_request","kind":"get","url":""}`},
		{Name: "get with a body", Doc: `{"type":"web_request","kind":"get","url":"https://example.invalid/","body":"x"}`},
		{Name: "header name with a space", Doc: `{"type":"web_request","kind":"get","url":"https://example.invalid/","headers":[{"name":"X Token","value":"x"}]}`},
		{Name: "header without value", Doc: `{"type":"web_request","kind":"get","url":"https://example.invalid/","headers":[{"name":"X-Empty"}]}`, Valid: true},
		{Name: "header without name", Doc: `{"type":"web_request","kind":"get","url":"https://example.invalid/","headers":[{"value":"x"}]}`},
		{Name: "unknown use", Doc: `{"type":"web_request","kind":"get","url":"https://example.invalid/","response":{"as":"xml"}}`},
		{Name: "text with values", Doc: `{"type":"web_request","kind":"get","url":"https://example.invalid/","response":{"as":"text","values":[{"path":"a","name":"a"}]}}`},
		{Name: "json without values", Doc: `{"type":"web_request","kind":"get","url":"https://example.invalid/","response":{"as":"json","values":[]}}`},
		{Name: "empty path part", Doc: `{"type":"web_request","kind":"get","url":"https://example.invalid/","response":{"as":"json","values":[{"path":"a\\\\b","name":"a"}]}}`},
		{Name: "invalid value name", Doc: `{"type":"web_request","kind":"get","url":"https://example.invalid/","response":{"as":"json","values":[{"path":"a","name":"A B"}]}}`},
	}}.Run(t)

	// Fixed addresses and header values are checked when saving (B72).
	for doc, want := range map[string]string{
		`{"kind":"get","url":"ftp://example.invalid/"}`:                                           `scheme "ftp"`,
		`{"kind":"get","url":"example.invalid/x"}`:                                                `scheme ""`,
		`{"kind":"get","url":"https:///x"}`:                                                       "has no host",
		`{"kind":"get","url":"https://example.invalid/","headers":[{"name":"X","value":"a\nb"}]}`: "line break",
	} {
		a, err := d.Decode([]byte(doc), json.DefaultOptionsV2())
		require.NoError(t, err, doc)
		require.ErrorContains(t, a.Validate(), want, doc)
	}
}

// TestRequest covers actions.md B70, B71 and B213: method, headers and
// body; values in the address are encoded for URLs and change neither path
// nor query; the body follows the header Content-Type.
func TestRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.echo("/", http.StatusOK, "", "ok")
		params := []string{"a/../b&x=1", `say "hi"`}
		in := f.start(params,
			f.request(`{"kind":"get","url":"http://example.invalid/users/$arg1text?q=$arg2text","headers":[{"name":"X-Arg","value":"$arg2text"}]}`),
		)
		require.Empty(t, in.Errors)
		r, body := f.seen.last()
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/users/a%2F..%2Fb%26x%3D1", r.URL.EscapedPath())
		assert.Equal(t, "q=say%20%22hi%22", r.URL.RawQuery)
		assert.Equal(t, []string{"say \"hi\""}, r.Header.Values("X-Arg"))
		assert.Equal(t, "streamcrew", r.Header.Get("User-Agent"))
		assert.Empty(t, body)

		for _, tc := range []struct {
			kind, contentType, body, want string
		}{
			{"post", "application/json; charset=utf-8", `{"text":"$arg2text"}`, `{"text":"say \"hi\""}`},
			{"put", "application/x-www-form-urlencoded", "a=$arg1text", "a=a%2F..%2Fb%26x%3D1"},
			{"post", "text/plain", "raw $arg2text", `raw say "hi"`},
			{"put", "application/vnd.api+json", `"$arg2text"`, `"say \"hi\""`},
		} {
			doc := `{"kind":"` + tc.kind + `","url":"http://example.invalid/","headers":[{"name":"Content-Type","value":` +
				strconv.Quote(tc.contentType) + `},{"name":"User-Agent","value":"bot/1"}],"body":` + strconv.Quote(tc.body) + `}`
			in := f.start(params, f.request(doc))
			require.Empty(t, in.Errors, doc)
			r, body := f.seen.last()
			assert.Equal(t, strings.ToUpper(tc.kind), r.Method)
			assert.Equal(t, tc.want, body, tc.contentType)
			assert.Equal(t, "bot/1", r.Header.Get("User-Agent"), "a header of the action replaces the default")
		}
		f.start(nil, f.request(`{"kind":"delete","url":"http://example.invalid/"}`))
		r, _ = f.seen.last()
		assert.Equal(t, http.MethodDelete, r.Method)
	})
}

// TestResponseText covers actions.md B74 and B75: the body becomes
// $webrequestresult; a status outside 2xx lets the action fail without a
// value.
func TestResponseText(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.echo("/ok", http.StatusCreated, "text/plain", "Hello\xff")
		f.echo("/missing", http.StatusNotFound, "text/plain", "not here")
		text := `,"response":{"as":"text"}}`
		in := f.start(nil,
			f.request(`{"kind":"get","url":"http://example.invalid/ok"`+text), f.show("$webrequestresult"),
			f.request(`{"kind":"get","url":"http://example.invalid/missing"`+text), f.show("$webrequestresult"),
		)
		require.Len(t, in.Errors, 1)
		assert.Equal(t, "response: status 404 Not Found", in.Errors[0].Message)
		assert.Equal(t, []string{"Hello�", "Hello�"}, f.lines, "a failed request sets no value")
	})
}

// TestResponseJSON covers actions.md B76 and B214: paths with "\" and list
// indexes from 0; text unchanged, numbers and truth values as written, null
// as empty text, objects and lists as compact JSON; a path without a value
// sets nothing and is logged; invalid JSON lets the action fail.
func TestResponseJSON(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.echo("/user", http.StatusOK, "application/json", `{
			"name": "Ann é",
			"job": {"title": "Streamer", "since": 2021},
			"score": 1.50, "live": true, "note": null,
			"items": [{"id": 7}, {"id": 8}],
			"tags": [ "a", "b" ],
			"0": "key zero"
		}`)
		f.echo("/broken", http.StatusOK, "application/json", `{"a": 1,`)
		values := `[{"path":"name","name":"name"},{"path":"job\\title","name":"title"},{"path":"score","name":"score"},` +
			`{"path":"live","name":"live"},{"path":"note","name":"note"},{"path":"items\\1\\id","name":"second"},` +
			`{"path":"tags","name":"tags"},{"path":"job","name":"job"},{"path":"0","name":"zero"},` +
			`{"path":"items\\5\\id","name":"missing"},{"path":"name\\x","name":"deeper"},` +
			`{"path":"items\\01\\id","name":"leading"},{"path":"items\\-1\\id","name":"negative"}]`
		in := f.start(nil,
			f.request(`{"kind":"get","url":"http://example.invalid/user","response":{"as":"json","values":`+values+`}}`),
			f.show("$name|$title|$score|$live|$note|$second|$tags|$job|$zero|$missing|$deeper|$leading|$negative"),
			f.request(`{"kind":"get","url":"http://example.invalid/broken","response":{"as":"json","values":[{"path":"a","name":"a"}]}}`),
		)
		require.Len(t, in.Errors, 1)
		assert.Equal(t, "response: invalid action: the response is not valid JSON", in.Errors[0].Message)
		assert.Equal(t, []string{`Ann é|Streamer|1.50|true||8|["a","b"]|{"title":"Streamer","since":2021}|key zero|$missing|$deeper|$leading|$negative`}, f.lines)
		assert.Equal(t, 4, strings.Count(f.logs.String(), "no value at the JSON path"), "an index is digits without a leading zero")
	})
}

// TestLimits covers actions.md B73: at most 1 MiB of response, 10 seconds
// for the whole request and at most 10 redirects.
func TestLimits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.echo("/exact", http.StatusOK, "", strings.Repeat("x", network.MaxResponse))
		f.echo("/big", http.StatusOK, "", strings.Repeat("x", network.MaxResponse+1))
		f.mux.HandleFunc("/slow", func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Minute):
			}
		})
		f.mux.HandleFunc("/hop/{n}", func(w http.ResponseWriter, r *http.Request) {
			n, _ := strconv.Atoi(r.PathValue("n"))
			if n > 0 {
				http.Redirect(w, r, "/hop/"+strconv.Itoa(n-1), http.StatusFound)
				return
			}
			_, _ = io.WriteString(w, "arrived")
		})
		f.mux.HandleFunc("/ftp", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "ftp://example.invalid/x", http.StatusFound)
		})
		text := `,"response":{"as":"text"}}`
		in := f.start(nil,
			f.request(`{"kind":"get","url":"http://example.invalid/exact"`+text),
			f.request(`{"kind":"get","url":"http://example.invalid/big"`+text),
			f.request(`{"kind":"get","url":"http://example.invalid/slow"`+text),
			f.request(`{"kind":"get","url":"http://example.invalid/hop/10"`+text), f.show("$webrequestresult"),
			f.request(`{"kind":"get","url":"http://example.invalid/hop/11"`+text),
			f.request(`{"kind":"get","url":"http://example.invalid/ftp"`+text),
		)
		// The slow request waits for its timeout; let the time pass.
		time.Sleep(network.RequestTimeout)
		synctest.Wait()
		in = f.harness.Instance(in.ID)
		require.Equal(t, engine.StateCompleted, in.State)
		require.Len(t, in.Errors, 4)
		assert.Equal(t, []int{2}, in.Errors[0].Path)
		assert.Contains(t, in.Errors[0].Message, "more than 1048576 bytes")
		assert.Equal(t, "url: the request took longer than its time limit of 10s", in.Errors[1].Message)
		assert.Contains(t, in.Errors[2].Message, "more than 10 redirects")
		assert.Contains(t, in.Errors[3].Message, `scheme "ftp", only http and https`)
		assert.Equal(t, []string{"arrived"}, f.lines)
		assert.Equal(t, network.RequestTimeout, in.EndedAt.Sub(in.StartedAt), "only the slow request took time")
	})
}

// TestInvalidInput covers actions.md B72: an address that is not absolute
// or not http or https after rendering, and a header value with a line
// break, let the action fail without a request.
func TestInvalidInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.echo("/", http.StatusOK, "", "ok")
		in := f.harness.Start(f.harness.Command("x",
			f.request(`{"kind":"get","url":"$target"}`),
			f.request(`{"kind":"get","url":"$scheme://example.invalid/"}`),
			f.request(`{"kind":"get","url":"http://example.invalid/","headers":[{"name":"X","value":"$evil"}]}`),
		), engine.Params{Values: map[string]template.Value{
			"target": template.TextValue("example.invalid/x"), "scheme": template.TextValue("file"),
			"evil": template.TextValue("a\r\nX-Injected: 1"),
		}})
		require.Len(t, in.Errors, 3)
		assert.Contains(t, in.Errors[0].Message, "url: invalid action:")
		assert.Contains(t, in.Errors[1].Message, "url: invalid action:")
		assert.Equal(t, "headers: invalid action: header X: a line break in the value", in.Errors[2].Message)
		assert.Empty(t, f.seen.requests)
	})
}

// TestCapability covers actions.md B7: without net:outbound no request is
// sent.
func TestCapability(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, capability.HostFS)
		f.echo("/", http.StatusOK, "", "ok")
		in := f.start(nil, f.request(`{"kind":"get","url":"http://example.invalid/"}`))
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "net:outbound")
		assert.Empty(t, f.seen.requests)
	})
}

// TestServerMode covers actions.md B77 with netguard.Dialer: in server
// mode, a request to an internal address fails unless the allowlist opens
// it; it asks the allowlist at every request.
func TestServerMode(t *testing.T) {
	t.Parallel()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "inside")
	}))
	srv.Start()
	t.Cleanup(srv.Close)

	var mu sync.Mutex
	allow := netguard.Allowlist{}
	dialer := netguard.Dialer{Protect: true, Allowlist: func() netguard.Allowlist {
		mu.Lock()
		defer mu.Unlock()
		return allow
	}}
	synctest.Test(t, func(t *testing.T) {
		reg, templates := registry(t, dialer, &logs{}, capability.NetOutbound)
		h := actiontest.NewHarness(t, reg)
		d, _ := reg.Descriptor(network.TypeWebRequest)
		a, err := d.Decode([]byte(`{"kind":"get","url":"`+srv.URL+`/","response":{"as":"text"}}`), json.DefaultOptionsV2())
		require.NoError(t, err)
		var got []string
		show := probe{fn: func(ctx context.Context, run *engine.Run) error {
			out, err := templates.Render(ctx, template.Parse("$webrequestresult"), run.Scope(), template.Text)
			got = append(got, out)
			return err
		}}

		in := h.Start(h.Command("x", a), engine.Params{})
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "target in an internal network")

		mu.Lock()
		allow, err = netguard.ParseAllowlist([]string{"127.0.0.0/8"})
		mu.Unlock()
		require.NoError(t, err)
		in = h.Start(h.Command("y", a, show), engine.Params{})
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"inside"}, got)

		mu.Lock()
		allow = netguard.Allowlist{}
		mu.Unlock()
		in = h.Start(h.Command("z", a), engine.Params{})
		require.Len(t, in.Errors, 1, "a removed entry applies to the next request")
	})
}

func TestPorts(t *testing.T) {
	t.Parallel()
	full := network.Ports{Templates: template.New(nil), Dialer: &net.Dialer{}, Logger: slog.New(slog.DiscardHandler)}
	_, err := network.Descriptors(full)
	require.NoError(t, err)
	for name, edit := range map[string]func(*network.Ports){
		"templates": func(p *network.Ports) { p.Templates = nil },
		"dialer":    func(p *network.Ports) { p.Dialer = nil },
		"logger":    func(p *network.Ports) { p.Logger = nil },
	} {
		p := full
		edit(&p)
		_, err := network.Descriptors(p)
		require.Error(t, err, name)
	}
	assert.True(t, errors.Is(action.ErrInvalid, action.ErrInvalid))
}
