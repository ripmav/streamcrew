// SPDX-License-Identifier: Apache-2.0

package network

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeWebRequest is the type ID of the web request action (Code-ADR-0013,
// point 1).
const TypeWebRequest = "web_request"

// ResultBody is the fixed name of the result value with the body of the
// response (actions.md B5, B75). [Interop] It follows the original and may
// be replaced after the legal assessment (roadmap Gate O, O.1).
const ResultBody = "webrequestresult"

// userAgent is sent unless the action sets the header User-Agent.
const userAgent = "streamcrew"

// The patterns of header names and JSON paths; they use only what Go RE2
// and ECMA-262 read alike (Code-ADR-0013, point 6).
const (
	// PatternHeaderName is a token of RFC 9110.
	PatternHeaderName = "^[!#$%&'*+.^_`|~0-9A-Za-z-]+$"
	// PatternJSONPath are parts that are not empty, separated by "\" (B76).
	PatternJSONPath = `^[^\\]+(\\[^\\]+)*$`
)

// headerName and jsonPath check the patterns in Go.
var (
	headerName = regexp.MustCompile(PatternHeaderName)
	jsonPath   = regexp.MustCompile(PatternJSONPath)
)

// Method is the HTTP method of a web request, the kind of the action
// (actions.md B70).
type Method string

// The kinds of the web request action.
const (
	// MethodGet is GET. New web requests do this.
	MethodGet    Method = "get"
	MethodPost   Method = "post"
	MethodPut    Method = "put"
	MethodDelete Method = "delete"
)

// Methods returns the kinds, in the order editors show them.
func Methods() []Method {
	return []Method{MethodGet, MethodPost, MethodPut, MethodDelete}
}

// Valid reports whether m is a known kind.
func (m Method) Valid() bool {
	return slices.Contains(Methods(), m)
}

// hasBody reports whether requests of kind m send a body (B70).
func (m Method) hasBody() bool {
	return m == MethodPost || m == MethodPut
}

// http returns the HTTP method.
func (m Method) http() string {
	return strings.ToUpper(string(m))
}

// Use says what the action does with the response (B70, B75, B76).
type Use string

// What the action does with the response.
const (
	// UseIgnore sets nothing; the body is not read. New web requests do
	// this.
	UseIgnore Use = "ignore"
	// UseText sets the body as $webrequestresult (B75).
	UseText Use = "text"
	// UseJSON reads the body as JSON and sets the values of its paths
	// (B76).
	UseJSON Use = "json"
)

// Uses returns the uses, in the order editors show them.
func Uses() []Use {
	return []Use{UseIgnore, UseText, UseJSON}
}

// Valid reports whether u is a known use.
func (u Use) Valid() bool {
	return slices.Contains(Uses(), u)
}

// Header is a header of the request: a fixed name and a value as a
// template (B70). A header without a value has an empty value.
type Header struct {
	Name  string          `json:"name"`
	Value action.Template `json:"value"`
}

// Response says what the action does with the response.
type Response struct {
	// As is the use; a new action ignores the response.
	As Use `json:"as"`
	// Values are the paths of a JSON response and the names of their result
	// values; only with UseJSON, then at least one.
	Values []JSONValue `json:"values,omitzero"`
}

// JSONValue maps a path of a JSON response to a result value (B76).
type JSONValue struct {
	// Path names the value; "\" separates its parts, and a part of digits
	// selects an entry of a list, from 0, e.g. `job\title` or `items\0\id`.
	Path string `json:"path"`
	// Name is the name of the result value (B5).
	Name action.ResultName `json:"name"`
}

// webRequestSchema returns the schema of the web request action: the
// method decides whether it has a body, the use of the response whether it
// has JSON values.
func webRequestSchema() *schema.Schema {
	header := schema.Object(
		schema.Property{Name: "name", Schema: &schema.Schema{Type: "string", Pattern: PatternHeaderName, UI: schema.UIText}, Required: true},
		schema.Property{Name: "value", Schema: schema.Template()},
	)
	value := schema.Object(
		schema.Property{Name: "path", Schema: &schema.Schema{Type: "string", Pattern: PatternJSONPath, UI: schema.UIText}, Required: true},
		schema.Property{Name: "name", Schema: schema.ResultName(), Required: true},
	)
	response := schema.Pick("as", nil,
		schema.Alternative{Values: []string{string(UseIgnore)}},
		schema.Alternative{Values: []string{string(UseText)}},
		schema.Alternative{Values: []string{string(UseJSON)}, Props: []schema.Property{
			{Name: "values", Schema: schema.List(value, 1), Required: true},
		}},
	)
	body := []schema.Property{{Name: "body", Schema: schema.Template()}}
	return schema.Kinds(
		[]schema.Property{
			{Name: "url", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
			{Name: "headers", Schema: schema.List(header, 0)},
			{Name: "response", Schema: response},
		},
		schema.Variant{Kind: string(MethodGet)},
		schema.Variant{Kind: string(MethodPost), Props: body},
		schema.Variant{Kind: string(MethodPut), Props: body},
		schema.Variant{Kind: string(MethodDelete)},
	)
}

// WebRequest is the web request action (actions.md B70 to B77).
type WebRequest struct {
	action.Common `json:",embed"`
	Kind          Method `json:"kind"`
	// URL is the address as a template; inserted values are encoded for
	// URLs (B71). A new action has none.
	URL action.Template `json:"url,omitzero"`
	// Headers are sent with the request, values encoded as text.
	Headers []Header `json:"headers"`
	// Body is the body of post and put, as a template, encoded after the
	// header Content-Type (B71); nil for get and delete.
	Body *action.Template `json:"body,omitzero"`
	// Response says what the action does with the response.
	Response Response `json:"response"`
	ports    *ports
}

// newWebRequest returns a new web request of kind m; ok is false for an
// unknown kind.
func newWebRequest(p *ports, m Method) (WebRequest, bool) {
	w := WebRequest{Common: action.On(), Kind: m, Headers: []Header{}, Response: Response{As: UseIgnore}, ports: p}
	if m.hasBody() {
		w.Body = new(action.Template)
	}
	return w, m.Valid()
}

// DocType implements command.Action.
func (WebRequest) DocType() string { return TypeWebRequest }

// Validate implements command.Action: the members fit the kind and the use
// of the response; an address without identifiers is absolute with http or
// https (B72), header names are tokens without line breaks in fixed values.
func (w WebRequest) Validate() error {
	switch {
	case !w.Kind.Valid():
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, w.Kind))
	case w.URL == "":
		return field("url", fmt.Errorf("%w: empty address", action.ErrInvalid))
	case w.Kind.hasBody() != (w.Body != nil):
		return field("body", fmt.Errorf("%w: only post and put have a body", action.ErrInvalid))
	case w.Headers == nil:
		return field("headers", fmt.Errorf("%w: no list of headers", action.ErrInvalid))
	}
	if !strings.Contains(string(w.URL), "$") {
		if _, err := parseURL(string(w.URL)); err != nil {
			return field("url", err)
		}
	}
	for i, h := range w.Headers {
		if !headerName.MatchString(h.Name) {
			return field("headers", fmt.Errorf("%w: header %d: %q is not a header name", action.ErrInvalid, i+1, h.Name))
		}
		if strings.ContainsAny(string(h.Value), "\r\n\x00") {
			return field("headers", fmt.Errorf("%w: header %s: a line break in the value", action.ErrInvalid, h.Name))
		}
	}
	return field("response", w.Response.validate())
}

// validate checks the use and its values.
func (r Response) validate() error {
	switch {
	case !r.As.Valid():
		return fmt.Errorf("%w: unknown use %q", action.ErrInvalid, r.As)
	case r.As != UseJSON && r.Values != nil:
		return fmt.Errorf("%w: only json has values", action.ErrInvalid)
	case r.As == UseJSON && len(r.Values) == 0:
		return fmt.Errorf("%w: json needs at least one value", action.ErrInvalid)
	}
	for _, v := range r.Values {
		if !jsonPath.MatchString(v.Path) {
			return fmt.Errorf("%w: path %q: parts that are not empty, separated by \\", action.ErrInvalid, v.Path)
		}
		if err := v.Name.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ResultNames implements command.ResultSetter: the names of the JSON
// values (B5).
func (w WebRequest) ResultNames() []string {
	names := make([]string, 0, len(w.Response.Values))
	for _, v := range w.Response.Values {
		names = append(names, string(v.Name))
	}
	return names
}

// parseURL returns the absolute address with http or https (B72).
func parseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", action.ErrInvalid, err)
	}
	if err := checkScheme(u.Scheme); err != nil {
		return nil, err
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%w: %q has no host", action.ErrInvalid, raw)
	}
	return u, nil
}

// bodyEncoding returns the encoding of the body after the header
// Content-Type as written in the action (B71): JSON for application/json
// and +json types, URL for forms, otherwise text.
func (w WebRequest) bodyEncoding() template.Encoding {
	for _, h := range w.Headers {
		if !strings.EqualFold(h.Name, "Content-Type") {
			continue
		}
		mediaType, _, err := mime.ParseMediaType(string(h.Value))
		switch {
		case err != nil:
			return template.Text
		case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
			return template.JSON
		case mediaType == "application/x-www-form-urlencoded":
			return template.URL
		}
		return template.Text
	}
	return template.Text
}

// Perform implements engine.Performer. The address, the header values and
// the body come from one render, each with its encoding (B3, B71).
func (w WebRequest) Perform(ctx context.Context, run *engine.Run) error {
	parts := make([]template.Part, 0, 2+len(w.Headers))
	parts = append(parts, template.Part{Template: w.URL.Parse(), Encoding: template.URL})
	for _, h := range w.Headers {
		parts = append(parts, template.Part{Template: h.Value.Parse(), Encoding: template.Text})
	}
	if w.Body != nil {
		parts = append(parts, template.Part{Template: w.Body.Parse(), Encoding: w.bodyEncoding()})
	}
	rendered, err := w.ports.Templates.RenderParts(ctx, parts, run.Scope())
	if err != nil {
		return err
	}
	u, err := parseURL(rendered[0].Text)
	if err != nil {
		return field("url", err)
	}
	var body io.Reader
	if w.Body != nil {
		body = strings.NewReader(rendered[len(rendered)-1].Text)
	}
	req, err := http.NewRequestWithContext(ctx, w.Kind.http(), u.String(), body)
	if err != nil {
		return field("url", err)
	}
	req.Header.Set("User-Agent", userAgent)
	for i, h := range w.Headers {
		value := rendered[1+i].Text
		if strings.ContainsAny(value, "\r\n\x00") {
			return field("headers", fmt.Errorf("%w: header %s: a line break in the value", action.ErrInvalid, h.Name))
		}
		if i == slices.IndexFunc(w.Headers, func(o Header) bool { return strings.EqualFold(o.Name, h.Name) }) {
			req.Header.Del(h.Name) // the first header of a name replaces the default
		}
		req.Header.Add(h.Name, value)
	}

	resp, err := w.ports.client.Do(req)
	switch {
	case err != nil && ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, context.DeadlineExceeded):
		return field("url", fmt.Errorf("%w of %s", ErrTimeout, RequestTimeout))
	case err != nil:
		return field("url", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return field("response", fmt.Errorf("status %s", resp.Status)) // B74
	}
	if w.Response.As == UseIgnore {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponse+1))
	if err != nil {
		return field("response", err)
	}
	if len(data) > MaxResponse {
		return field("response", fmt.Errorf("%w: more than %d bytes", action.ErrInvalid, MaxResponse))
	}
	if w.Response.As == UseText {
		run.Scope().SetValue(ResultBody, template.TextValue(strings.ToValidUTF8(string(data), "�")))
		return nil
	}
	return w.setJSON(ctx, run, data)
}

// setJSON sets the values of the paths of the JSON response (B76). A path
// without a value sets nothing and is logged; invalid JSON lets the action
// fail.
func (w WebRequest) setJSON(ctx context.Context, run *engine.Run, data []byte) error {
	doc := jsontext.Value(data)
	if !doc.IsValid() {
		return field("response", fmt.Errorf("%w: the response is not valid JSON", action.ErrInvalid))
	}
	for _, v := range w.Response.Values {
		text, ok := lookup(doc, v.Path)
		if !ok {
			w.ports.Logger.WarnContext(ctx, "web request: no value at the JSON path",
				"instance_id", run.InstanceID(), "path", v.Path, "name", v.Name)
			continue
		}
		run.Scope().SetValue(string(v.Name), template.TextValue(text))
	}
	return nil
}

// lookup returns the value at path in doc as text (B76): text unchanged,
// numbers, true and false as in the JSON, null as empty text, objects and
// lists as compact JSON. ok is false if the path has no value.
func lookup(doc jsontext.Value, path string) (text string, ok bool) {
	cur := doc
	for part := range strings.SplitSeq(path, `\`) {
		switch cur.Kind() {
		case '{':
			var members map[string]jsontext.Value
			if json.Unmarshal(cur, &members) != nil {
				return "", false
			}
			if cur, ok = members[part]; !ok {
				return "", false
			}
		case '[':
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || strconv.Itoa(i) != part {
				return "", false
			}
			var items []jsontext.Value
			if json.Unmarshal(cur, &items) != nil || i >= len(items) {
				return "", false
			}
			cur = items[i]
		default:
			return "", false
		}
	}
	switch cur.Kind() {
	case '"':
		var s string
		if json.Unmarshal(cur, &s) != nil {
			return "", false
		}
		return s, true
	case 'n':
		return "", true
	case '{', '[':
		compact := bytes.Clone(cur)
		v := jsontext.Value(compact)
		if v.Compact() != nil {
			return "", false
		}
		return string(v), true
	default: // numbers, true and false as written
		return string(bytes.TrimSpace(cur)), true
	}
}
