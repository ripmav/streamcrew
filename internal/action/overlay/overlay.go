// SPDX-License-Identifier: MIT

// Package overlay has the overlay action (roadmap 7.2, spec overlays.md,
// B38, B39): one action type with the kinds item and widget. The kind
// item shows an item on an endpoint (B13, B9, B10, B18); the kind widget
// runs an operation on a widget of the profile (B21 to B28).
//
// The action is visual_audio (B39); it waits for the item to leave the
// active set (B11) and reports the end of an audio or video playback to
// the engine with PlaybackEnds (B14, B46).
package overlay

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeOverlay is the type ID of the overlay action (Code-ADR-0013,
// point 1).
const TypeOverlay = "overlay"

// Kind is what an overlay action does (B38).
type Kind string

// The kinds of the overlay action.
const (
	// KindItem shows an item on an endpoint. New overlay actions do this.
	KindItem Kind = "item"
	// KindWidget runs an operation on a widget of the profile.
	KindWidget Kind = "widget"
)

// Kinds returns the kinds of the overlay action, in the order editors
// show them.
func Kinds() []Kind {
	return []Kind{KindItem, KindWidget}
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	return slices.Contains(Kinds(), k)
}

// WidgetOp is the operation of the kind widget (B38).
type WidgetOp string

// The operations of the kind widget of 7.2; the operations of the widgets
// of 7.5 are a clear error (A4).
const (
	// OpShow switches the widget on (B21). New widget operations do this.
	OpShow WidgetOp = "show"
	// OpHide switches the widget off (B21).
	OpHide WidgetOp = "hide"
	// OpUpdate changes the state data of the widget (B22); for the timer
	// widget a new duration or a reset (B26).
	OpUpdate WidgetOp = "update"
	// OpAddToGoal adds the amount, which may be negative (B25, B50), to
	// the goal widget.
	OpAddToGoal WidgetOp = "add_to_goal"
	// OpAddToTimer adds the amount of seconds, which may be negative
	// (B26, B38), to the timer widget.
	OpAddToTimer WidgetOp = "add_to_timer"
	// OpPauseTimer pauses the timer widget, or resumes it (B26, B38).
	OpPauseTimer WidgetOp = "pause_timer"
	// OpAddToEventList adds a dynamic entry of the user of the call to
	// the event_list widget (B27, B38).
	OpAddToEventList WidgetOp = "add_to_event_list"
)

// Ops returns the operations of the kind widget of 7.2, in the order
// editors show them.
func Ops() []WidgetOp {
	return []WidgetOp{
		OpShow, OpHide, OpUpdate, OpAddToGoal, OpAddToTimer,
		OpPauseTimer, OpAddToEventList,
	}
}

// Valid reports whether o is a known operation.
func (o WidgetOp) Valid() bool {
	return slices.Contains(Ops(), o)
}

// ItemKinds are the item kinds the kind item shows (B13); emote_effect is
// 7.5 and a clear error here.
//
//nolint:gochecknoglobals // static table of item kinds
var ItemKinds = []string{
	"text", "image", "video", "audio", "html", "youtube", "clip", "timer",
}

// ValidItemKind reports whether k is an item kind of 7.2.
func ValidItemKind(k string) bool { return slices.Contains(ItemKinds, k) }

// Clips resolves the latest clip of a user for the item kind clip
// (B13, B16).
type Clips interface {
	// LatestClip returns the latest clip of the channel with the login.
	LatestClip(ctx context.Context, login string) (Clip, error)
}

// Clip is a clip as the item shows it (B13, B16).
type Clip struct {
	// URL is the page of the clip, as the embed loads it.
	URL string
	// Duration is the length of the clip (B16); 0 if unknown.
	Duration time.Duration
}

// Widgets are the operations of the kind widget on the widgets of the
// profile (B25, B26, B27).
type Widgets interface {
	// AddToGoal adds the amount, which may be negative (B25, B50), to
	// the goal widget, as the effect of the platform and user.
	AddToGoal(ctx context.Context, widgetID string, amount int64, p platform.Name, u *user.User) error
	// UpdateTimer sets a new duration, or resets the timer widget to its
	// configured duration (B26).
	UpdateTimer(ctx context.Context, widgetID string, d time.Duration, reset bool) error
	// AddToTimer adds the duration, which may be negative (B26, B38),
	// to the remaining time of the timer widget.
	AddToTimer(ctx context.Context, widgetID string, d time.Duration) error
	// PauseTimer pauses the timer widget, or resumes it (B26, B38).
	PauseTimer(ctx context.Context, widgetID string) error
	// AddToEventList adds a dynamic entry of the user with the name and
	// the text to the event_list widget (B27, B38).
	AddToEventList(ctx context.Context, widgetID, user, text string) error
}

// Service is the overlay service the action runs on (B18, B21, B22).
type Service interface {
	// ShowItem shows the item on the endpoint and returns its ID and a
	// channel that is closed when the item leaves the active set (B11).
	ShowItem(ctx context.Context, endpoint string, item overlay.Item) (string, <-chan struct{}, error)
	// SetVisible switches the widget on or off (B21).
	SetVisible(ctx context.Context, widgetID string, visible bool) error
	// Widget returns the definition of the widget from the profile; ok is
	// false if the profile does not know it.
	Widget(widgetID string) (settings.OverlayWidget, bool)
	// UpdateWidget pushes the state data to the runtimes (B22).
	UpdateWidget(ctx context.Context, widgetID string, state any) error
}

// Ports are what the overlay types need.
type Ports struct {
	// Templates renders the operation members.
	Templates *template.Engine
	// Overlay shows the items and carries the widget visibility
	// (B18, B21, B22).
	Overlay Service
	// Widgets run the operations of the kind widget (B22, B25 to B28).
	Widgets Widgets
	// Clips resolve the latest clip of the item kind clip (B13, B16).
	Clips Clips
	// Logger records the operations and their results.
	Logger *slog.Logger
}

// ports are the ports of the overlay types.
type ports struct {
	Ports
}

// Descriptors returns the overlay type with its ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Templates == nil:
		return nil, errors.New("overlay action types: no template engine")
	case p.Overlay == nil:
		return nil, errors.New("overlay action types: no overlay service")
	case p.Widgets == nil:
		return nil, errors.New("overlay action types: no widget operations")
	case p.Clips == nil:
		return nil, errors.New("overlay action types: no clip resolver")
	case p.Logger == nil:
		return nil, errors.New("overlay action types: no logger")
	}
	return descriptors(&ports{Ports: p}), nil
}

// Catalog returns the overlay type without ports, for the type catalog and
// commands as code (Code-ADR-0013, point 3): its actions decode, validate
// and encode, but must not run.
func Catalog() []action.Descriptor {
	return descriptors(nil)
}

// descriptors returns the overlay type with its ports, which are nil in
// the catalog.
func descriptors(ports *ports) []action.Descriptor {
	return []action.Descriptor{
		action.Descriptor{
			Type:        TypeOverlay,
			Version:     1,
			Category:    action.CategoryOverlay,
			VisualAudio: true,
			Schema:      overlaySchema(),
		}.WithKinds(KindItem, newOverlay(ports)),
	}
}

// overlaySchema returns the schema of the overlay action: the kind item
// carries the item configuration (B13, B9, B10), the kind widget the
// operation (B38).
func overlaySchema() *schema.Schema {
	ops := make([]string, len(Ops()))
	for i, o := range Ops() {
		ops[i] = string(o)
	}
	return schema.Kinds(
		nil,
		schema.Variant{Kind: string(KindItem), Props: []schema.Property{
			{Name: "endpoint", Schema: schema.Text(schema.UIText)},
			{Name: "position", Schema: positionSchema()},
			{Name: "layer", Schema: &schema.Schema{Type: "integer"}},
			{Name: "animation", Schema: schema.Choice(overlay.Animations...)},
			{Name: "animationDuration", Schema: schema.Duration()},
			{Name: "duration", Schema: schema.Duration()},
			{Name: "content", Schema: contentSchema(), Required: true},
		}},
		schema.Variant{Kind: string(KindWidget), Props: []schema.Property{
			{Name: "widget", Schema: schema.NonEmpty(schema.UIText), Required: true},
			{Name: "op", Schema: schema.Choice(ops...), Required: true},
			{Name: "data", Schema: &schema.Schema{Type: "object"}},
			{Name: "amount", Schema: action.Between(-1_000_000, 1_000_000).Schema()},
			{Name: "text", Schema: schema.Template()},
		}},
	)
}

// positionSchema returns the schema of a position (B9): the model picks
// which of the members exist, as Position.Validate checks it.
func positionSchema() *schema.Schema {
	points := make([]string, len(settings.SimplePoints))
	copy(points, settings.SimplePoints)
	return schema.Object(
		schema.Property{Name: "model", Schema: schema.Choice(settings.PositionSimple, settings.PositionPercent, settings.PositionPixels, settings.PositionRandom), Required: true},
		schema.Property{Name: "point", Schema: schema.Choice(points...)},
		schema.Property{Name: "x", Schema: &schema.Schema{Type: "number", Minimum: new(0.0)}},
		schema.Property{Name: "y", Schema: &schema.Schema{Type: "number", Minimum: new(0.0)}},
	)
}

// contentSchema returns the schema of the content of an item (B13): the
// member kind picks the members of its kind.
func contentSchema() *schema.Schema {
	number := func(minimum float64) *schema.Schema {
		return &schema.Schema{Type: "number", Minimum: new(minimum)}
	}
	volume := &schema.Schema{Type: "number", Minimum: new(0.0), Maximum: new(100.0)}
	return schema.Pick("kind", nil,
		schema.Alternative{Values: []string{"text"}, Props: []schema.Property{
			{Name: "text", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
			{Name: "maxWidth", Schema: number(1)},
		}},
		schema.Alternative{Values: []string{"image"}, Props: []schema.Property{
			{Name: "src", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
			{Name: "width", Schema: number(1)},
			{Name: "height", Schema: number(1)},
		}},
		schema.Alternative{Values: []string{"video"}, Props: []schema.Property{
			{Name: "src", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
			{Name: "start", Schema: number(0)},
			{Name: "volume", Schema: volume},
			{Name: "loop", Schema: schema.Switch()},
		}},
		schema.Alternative{Values: []string{"audio"}, Props: []schema.Property{
			{Name: "src", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
			{Name: "volume", Schema: volume},
		}},
		schema.Alternative{Values: []string{"html"}, Props: []schema.Property{
			{Name: "html", Schema: schema.NonEmpty(schema.UIMultiline), Required: true},
		}},
		schema.Alternative{Values: []string{"youtube"}, Props: []schema.Property{
			{Name: "url", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
			{Name: "width", Schema: number(1), Required: true},
			{Name: "height", Schema: number(1), Required: true},
			{Name: "start", Schema: number(0)},
			{Name: "volume", Schema: volume},
		}},
		schema.Alternative{Values: []string{"clip"}, Props: []schema.Property{
			{Name: "url", Schema: schema.NonEmpty(schema.UITemplate)},
			{Name: "user", Schema: schema.NonEmpty(schema.UITemplate)},
		}},
		schema.Alternative{Values: []string{"timer"}, Props: []schema.Property{
			{Name: "duration", Schema: schema.Duration(), Required: true},
			{Name: "direction", Schema: schema.Choice("down", "up")},
			{Name: "format", Schema: schema.Text(schema.UIText)},
		}},
	)
}

// Overlay is the overlay action (roadmap 7.2). Which members it has
// depends on its kind.
type Overlay struct {
	action.Common `json:",embed"`
	Kind          Kind `json:"kind"`
	// Item is the configuration of the kind item; nil for the other kinds.
	Item *ItemOptions `json:",embed"`
	// Widget is the operation of the kind widget; nil for the other kinds.
	Widget *WidgetOptions `json:",embed"`
	ports  *ports
}

// ItemOptions are the options of the kind item (B13, B9, B10, B18).
type ItemOptions struct {
	// Endpoint is the endpoint the item shows on; empty for the default
	// (B18).
	Endpoint string `json:"endpoint"`
	// Position is where the item shows (B9).
	Position settings.Position `json:"position"`
	// Layer is the integer layer; a higher layer shows above (B9).
	Layer int `json:"layer"`
	// Animation is the show and hide animation, one of the overlay
	// animations; empty for none (B10).
	Animation string `json:"animation"`
	// AnimationDuration is the time of the animation; 0 for the default
	// of 300 ms (B10).
	AnimationDuration polydoc.Duration `json:"animationDuration"`
	// Duration is the show time; 0 keeps the item until the playback ends
	// (B11, B14), at most until the time limit of the action (B46).
	Duration polydoc.Duration `json:"duration"`
	// Content is the kind-specific content (B13); the kind decides which
	// members it has.
	Content any `json:"content"`
}

// WidgetOptions are the options of the kind widget (B38).
type WidgetOptions struct {
	// Widget is the ID of the target widget (B19).
	Widget string `json:"widget"`
	// Op is the operation.
	Op WidgetOp `json:"op"`
	// Data is the state data of the operation update (B22); a JSON
	// document. For the timer widget it carries a new duration or a
	// reset (B26).
	Data any `json:"data"`
	// Amount is the amount of the operations add_to_goal and
	// add_to_timer; it may be negative (B25, B26, B50).
	Amount action.Amount `json:"amount"`
	// Text is the details text of the operation add_to_event_list (B27);
	// the user is the user of the call.
	Text action.Template `json:"text"`
}

// newOverlay makes an overlay action of kind k with the defaults of that
// kind and the ports of the type; ok is false for an unknown kind.
func newOverlay(ports *ports) func(Kind) (Overlay, bool) {
	return func(k Kind) (Overlay, bool) {
		o := Overlay{Common: action.On(), Kind: k, ports: ports}
		switch k {
		case KindItem:
			o.Item = &ItemOptions{
				Position: settings.Position{Model: settings.PositionSimple, Point: "top-left"},
				Content:  map[string]any{"kind": "text"},
			}
		case KindWidget:
			o.Widget = &WidgetOptions{Op: OpShow}
		default:
			return Overlay{}, false
		}
		return o, true
	}
}

// DocType implements command.Action.
func (Overlay) DocType() string { return TypeOverlay }

// Validate implements command.Action.
func (t Overlay) Validate() error {
	switch {
	case !t.Kind.Valid():
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, t.Kind))
	case t.Kind == KindItem && t.Widget != nil:
		return field("widget", fmt.Errorf("%w: only the kind widget has its options", action.ErrInvalid))
	case t.Kind == KindItem:
		if t.Item == nil {
			return field("endpoint", fmt.Errorf("%w: the kind item needs its options", action.ErrInvalid))
		}
		return t.itemInvalid()
	case t.Item != nil:
		return field("endpoint", fmt.Errorf("%w: only the kind item has its options", action.ErrInvalid))
	case t.Widget == nil:
		return field("widget", fmt.Errorf("%w: the kind widget needs its options", action.ErrInvalid))
	default:
		return t.widgetInvalid()
	}
}

// itemContent is the content of the kind item (B13); the member kind
// picks which of the members exist, and itemContentInvalid names a member
// that belongs to another kind.
type itemContent struct {
	// Kind is the kind of the item, one of ItemKinds.
	Kind string `json:"kind"`
	// Text is the text of the kind text.
	Text string `json:"text"`
	// MaxWidth is the maximum width of the kind text in pixels; 0 for
	// none.
	MaxWidth float64 `json:"maxWidth"`
	// Src is the path or URL of the kinds image, video and audio.
	Src string `json:"src"`
	// Width and Height are the size in pixels of the kinds image and
	// youtube.
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	// Start is the start time in seconds of the kinds video and youtube.
	Start float64 `json:"start"`
	// Volume is the volume from 0 to 100 of the kinds video, audio and
	// youtube.
	Volume float64 `json:"volume"`
	// Loop repeats the kind video until the item hides.
	Loop bool `json:"loop"`
	// HTML is the own HTML of the kind html.
	HTML string `json:"html"`
	// URL is the URL of the kinds youtube and clip.
	URL string `json:"url"`
	// User is the login whose latest clip the kind clip shows; a URL and
	// a user are mutually exclusive.
	User string `json:"user"`
	// Duration is the counted duration of the kind timer, as text.
	Duration string `json:"duration"`
	// Direction counts the kind timer down (default) or up.
	Direction string `json:"direction"`
	// Format is the display format of the kind timer (B17); empty for
	// MM:SS.
	Format string `json:"format"`
}

// contentMembers are the members of each item kind (B13); a member the
// kind does not name is an error.
//
//nolint:gochecknoglobals // static table of content members
var contentMembers = map[string][]string{
	"text":    {"text", "maxWidth"},
	"image":   {"src", "width", "height"},
	"video":   {"src", "start", "volume", "loop"},
	"audio":   {"src", "volume"},
	"html":    {"html"},
	"youtube": {"url", "width", "height", "start", "volume"},
	"clip":    {"url", "user"},
	"timer":   {"duration", "direction", "format"},
}

// itemInvalid names the invalid member of the kind item; nil if it is
// valid.
func (t Overlay) itemInvalid() error {
	o := t.Item
	switch {
	case o.Position.Model == "":
		return field("position.model", fmt.Errorf("%w: the position needs a model", action.ErrInvalid))
	case o.Position.Model != settings.PositionSimple && o.Position.Model != settings.PositionPercent && o.Position.Model != settings.PositionPixels && o.Position.Model != settings.PositionRandom:
		return field("position.model", fmt.Errorf("%w: unknown position model %q", action.ErrInvalid, o.Position.Model))
	case o.Position.Model == settings.PositionSimple && !slices.Contains(settings.SimplePoints, o.Position.Point):
		return field("position.point", fmt.Errorf("%w: unknown point %q", action.ErrInvalid, o.Position.Point))
	case o.Position.Model == settings.PositionPercent && (o.Position.X < 0 || o.Position.X > 100 || o.Position.Y < 0 || o.Position.Y > 100):
		return field("position.x", fmt.Errorf("%w: the percent coordinates are not between 0 and 100", action.ErrInvalid))
	case o.Position.Model == settings.PositionPixels && (o.Position.X < 0 || o.Position.Y < 0):
		return field("position.x", fmt.Errorf("%w: the pixel coordinates must be 0 or more", action.ErrInvalid))
	case o.Animation != "" && !slices.Contains(overlay.Animations, o.Animation):
		return field("animation", fmt.Errorf("%w: unknown animation %q", action.ErrInvalid, o.Animation))
	case o.AnimationDuration.Std() < 0:
		return field("animationDuration", fmt.Errorf("%w: the animation duration %s must be 0 or more", action.ErrInvalid, o.AnimationDuration.Std()))
	case o.Duration.Std() < 0:
		return field("duration", fmt.Errorf("%w: the duration %s must be 0 or more", action.ErrInvalid, o.Duration.Std()))
	default:
		return t.contentInvalid()
	}
}

// contentInvalid decodes the content of the kind item and names its
// invalid member; nil if it is valid.
func (t Overlay) contentInvalid() error {
	data, err := json.Marshal(t.Item.Content)
	if err != nil {
		return field("content", fmt.Errorf("%w: the content is not a JSON document", action.ErrInvalid))
	}
	var doc map[string]jsontext.Value
	if err := json.Unmarshal(data, &doc); err != nil {
		return field("content", fmt.Errorf("%w: the content must be an object", action.ErrInvalid))
	}
	var c itemContent
	if err := json.Unmarshal(data, &c, json.RejectUnknownMembers(true)); err != nil {
		return field("content", fmt.Errorf("%w: the content: %w", action.ErrInvalid, err))
	}
	if !ValidItemKind(c.Kind) {
		return field("content.kind", fmt.Errorf("%w: unknown item kind %q", action.ErrInvalid, c.Kind))
	}
	allowed := contentMembers[c.Kind]
	for key := range doc {
		if key != "kind" && !slices.Contains(allowed, key) {
			return field("content."+key, fmt.Errorf("%w: the kind %s has no member %q", action.ErrInvalid, c.Kind, key))
		}
	}
	return contentValuesInvalid(&c)
}

// decodeContent decodes the content document of the kind item; it must be
// an object.
func (t Overlay) decodeContent() (*itemContent, error) {
	data, err := json.Marshal(t.Item.Content)
	if err != nil {
		return nil, fmt.Errorf("%w: the content is not a JSON document", action.ErrInvalid)
	}
	var c itemContent
	if err := json.Unmarshal(data, &c, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("%w: the content: %w", action.ErrInvalid, err)
	}
	return &c, nil
}

// contentValuesInvalid names the invalid value of the content of kind
// c.Kind; nil if it is valid. A member that belongs to another kind
// contentInvalid already named.
func contentValuesInvalid(c *itemContent) error {
	switch c.Kind {
	case "text":
		switch {
		case c.Text == "":
			return field("content.text", fmt.Errorf("%w: an empty text", action.ErrInvalid))
		case c.MaxWidth < 0:
			return field("content.maxWidth", fmt.Errorf("%w: the maximum width must be 0 or more", action.ErrInvalid))
		}
	case "image", "video", "audio":
		switch {
		case c.Src == "":
			return field("content.src", fmt.Errorf("%w: an empty source", action.ErrInvalid))
		case c.Width < 0 || c.Height < 0:
			return field("content.width", fmt.Errorf("%w: the size must be 0 or more", action.ErrInvalid))
		case c.Start < 0:
			return field("content.start", fmt.Errorf("%w: the start time must be 0 or more", action.ErrInvalid))
		case c.Volume < 0 || c.Volume > 100:
			return field("content.volume", fmt.Errorf("%w: the volume is 0 to 100", action.ErrInvalid))
		}
	case "html":
		if c.HTML == "" {
			return field("content.html", fmt.Errorf("%w: an empty HTML", action.ErrInvalid))
		}
	case "youtube":
		switch {
		case c.URL == "":
			return field("content.url", fmt.Errorf("%w: an empty YouTube URL", action.ErrInvalid))
		case c.Width < 1:
			return field("content.width", fmt.Errorf("%w: the YouTube item needs a width", action.ErrInvalid))
		case c.Height < 1:
			return field("content.height", fmt.Errorf("%w: the YouTube item needs a height", action.ErrInvalid))
		case c.Start < 0:
			return field("content.start", fmt.Errorf("%w: the start time must be 0 or more", action.ErrInvalid))
		case c.Volume < 0 || c.Volume > 100:
			return field("content.volume", fmt.Errorf("%w: the volume is 0 to 100", action.ErrInvalid))
		}
	case "clip":
		if (c.URL == "") == (c.User == "") {
			return field("content.url", fmt.Errorf("%w: the clip takes a URL or a user, not both or neither", action.ErrInvalid))
		}
	case "timer":
		if c.Duration == "" {
			return field("content.duration", fmt.Errorf("%w: the timer needs a duration", action.ErrInvalid))
		}
		d, err := time.ParseDuration(c.Duration)
		switch {
		case err != nil:
			return field("content.duration", fmt.Errorf("%w: the duration %q is unknown", action.ErrInvalid, c.Duration))
		case d <= 0:
			return field("content.duration", fmt.Errorf("%w: the duration %s must be positive", action.ErrInvalid, d))
		case c.Direction != "" && c.Direction != "down" && c.Direction != "up":
			return field("content.direction", fmt.Errorf("%w: unknown direction %q", action.ErrInvalid, c.Direction))
		}
	}
	return nil
}

// widgetInvalid names the invalid member of the kind widget; nil if it is
// valid.
func (t Overlay) widgetInvalid() error {
	o := t.Widget
	switch {
	case o.Widget == "":
		return field("widget", fmt.Errorf("%w: an empty widget ID", action.ErrInvalid))
	case !o.Op.Valid():
		return field("op", fmt.Errorf("%w: unknown operation %q", action.ErrInvalid, o.Op))
	case o.Op == OpAddToGoal || o.Op == OpAddToTimer:
		if o.Amount.IsZero() {
			return field("amount", fmt.Errorf("%w: the operation %s needs an amount", action.ErrInvalid, o.Op))
		}
		return field("amount", o.Amount.Validate(amountRange()))
	case !o.Amount.IsZero():
		return field("amount", fmt.Errorf("%w: only add_to_goal and add_to_timer take an amount", action.ErrInvalid))
	case o.Op != OpAddToEventList && o.Text != "":
		return field("text", fmt.Errorf("%w: only add_to_event_list takes a text", action.ErrInvalid))
	case o.Op != OpUpdate && o.Data != nil:
		return field("data", fmt.Errorf("%w: only update takes data", action.ErrInvalid))
	default:
		return nil
	}
}

// amountRange is the range of the amounts of the kind widget (B25, B26):
// the amounts may be negative.
func amountRange() action.Range { return action.Between(-1_000_000, 1_000_000) }

// Perform implements engine.Performer. The action is done when the item
// has left the active set (B11), or the operation has run.
func (t Overlay) Perform(ctx context.Context, run *engine.Run) error {
	ports := t.ports
	if ports == nil {
		return errors.New("overlay action: no ports (catalog action)")
	}
	switch t.Kind {
	case KindItem:
		return t.item(ctx, run)
	case KindWidget:
		return t.widget(ctx, run)
	default:
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, t.Kind))
	}
}

// item shows the item on its endpoint and waits for it to leave the
// active set (B11, B18): its duration, the item.ended of a runtime, or
// the time limit of the action (B46). An audio or video item reports its
// end of the playback to the engine (B14, B39).
func (t Overlay) item(ctx context.Context, run *engine.Run) error {
	o := t.Item
	ports := t.ports
	content, kind, err := t.renderedContent(ctx, run)
	if err != nil {
		return field("content", err)
	}
	endpoint := o.Endpoint
	if endpoint == "" {
		endpoint = settings.DefaultEndpointID
	}
	_, end, err := ports.Overlay.ShowItem(ctx, endpoint, overlay.Item{
		Kind:              kind,
		Position:          o.Position,
		Layer:             o.Layer,
		Animation:         o.Animation,
		AnimationDuration: o.AnimationDuration.Std(),
		Duration:          o.Duration.Std(),
		Content:           content,
	})
	if err != nil {
		return field("item", err)
	}
	ports.Logger.InfoContext(ctx, "overlay item shown", "instance_id", run.InstanceID(), "endpoint", endpoint, "kind", kind)
	select {
	case <-end:
		// The item has left the active set: its duration ran out, the
		// runtime reported the end of the playback or a reload dropped
		// the endpoint (B11, B14).
	case <-ctx.Done():
		// B46: the time limit ends the action; without a client report
		// the playback ends with it.
		return ctx.Err()
	}
	if kind == "audio" || kind == "video" {
		run.PlaybackEnds(time.Now())
	}
	return nil
}

// renderedContent renders the templates of the content of the kind item
// and returns it as a document for the runtime, with the kind of the
// item. A clip by user resolves the latest clip of the user (B13, B16).
func (t Overlay) renderedContent(ctx context.Context, run *engine.Run) (any, string, error) {
	c, err := t.decodeContent()
	if err != nil {
		return nil, "", err
	}
	ports := t.ports
	render := func(s action.Template) (string, error) {
		return ports.Templates.Render(ctx, s.Parse(), run.Scope(), template.Text)
	}
	switch c.Kind {
	case "text":
		text, err := render(action.Template(c.Text))
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"text": text, "maxWidth": c.MaxWidth}, "text", nil
	case "image", "video", "audio":
		src, err := render(action.Template(c.Src))
		if err != nil {
			return nil, "", err
		}
		data := map[string]any{"src": src}
		if c.Width > 0 {
			data["width"] = c.Width
		}
		if c.Height > 0 {
			data["height"] = c.Height
		}
		if c.Start > 0 {
			data["start"] = c.Start
		}
		if c.Volume > 0 {
			data["volume"] = c.Volume
		}
		if c.Loop {
			data["loop"] = true
		}
		return data, c.Kind, nil
	case "html":
		html, err := render(action.Template(c.HTML))
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"html": html}, "html", nil
	case "youtube":
		url, err := render(action.Template(c.URL))
		if err != nil {
			return nil, "", err
		}
		data := map[string]any{"url": url, "width": c.Width, "height": c.Height}
		if c.Start > 0 {
			data["start"] = c.Start
		}
		if c.Volume > 0 {
			data["volume"] = c.Volume
		}
		return data, "youtube", nil
	case "clip":
		return t.clipContent(ctx, run, c)
	case "timer":
		direction := c.Direction
		if direction == "" {
			direction = "down"
		}
		format := c.Format
		if format == "" {
			format = "MM:SS"
		}
		return map[string]any{"duration": c.Duration, "direction": direction, "format": format}, "timer", nil
	default:
		return nil, "", fmt.Errorf("%w: unknown item kind %q", action.ErrInvalid, c.Kind)
	}
}

// clipContent is the content of the item kind clip (B13, B15, B16): the
// URL as given, or the latest clip of the user.
func (t Overlay) clipContent(ctx context.Context, run *engine.Run, c *itemContent) (any, string, error) {
	if c.URL != "" {
		url, err := t.ports.Templates.Render(ctx, action.Template(c.URL).Parse(), run.Scope(), template.Text)
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"url": url}, "clip", nil
	}
	login, err := t.ports.Templates.Render(ctx, action.Template(c.User).Parse(), run.Scope(), template.Text)
	if err != nil {
		return nil, "", err
	}
	clip, err := t.ports.Clips.LatestClip(ctx, login)
	if err != nil {
		return nil, "", err
	}
	data := map[string]any{"url": clip.URL}
	// B16, minimal: the duration of the clip lands in the content; a
	// bus event detail follows with the clip interop.
	if clip.Duration > 0 {
		data["duration"] = int(clip.Duration.Seconds())
	}
	return data, "clip", nil
}

// widget runs the operation of the kind widget (B21 to B28, B38).
func (t Overlay) widget(ctx context.Context, run *engine.Run) error {
	o := t.Widget
	ports := t.ports
	sc := run.Scope()
	switch o.Op {
	case OpShow, OpHide:
		visible := o.Op == OpShow
		if err := ports.Overlay.SetVisible(ctx, o.Widget, visible); err != nil {
			return field("widget", err)
		}
		ports.Logger.InfoContext(ctx, "overlay widget", "instance_id", run.InstanceID(), "widget", o.Widget, "visible", visible)
		return nil
	case OpUpdate:
		return t.update(ctx)
	case OpAddToGoal:
		v, err := o.Amount.Eval(ctx, ports.Templates, sc, amountRange())
		if err != nil {
			return field("amount", err)
		}
		amount, err := v.Int64()
		if err != nil {
			return field("amount", err)
		}
		return field("widget", ports.Widgets.AddToGoal(ctx, o.Widget, amount, sc.Platform, sc.User))
	case OpAddToTimer:
		v, err := o.Amount.Eval(ctx, ports.Templates, sc, amountRange())
		if err != nil {
			return field("amount", err)
		}
		seconds, err := v.Int64()
		if err != nil {
			return field("amount", err)
		}
		return field("widget", ports.Widgets.AddToTimer(ctx, o.Widget, time.Duration(seconds)*time.Second))
	case OpPauseTimer:
		return field("widget", ports.Widgets.PauseTimer(ctx, o.Widget))
	case OpAddToEventList:
		text := ""
		if o.Text != "" {
			var err error
			text, err = ports.Templates.Render(ctx, o.Text.Parse(), sc, template.Text)
			if err != nil {
				return field("text", err)
			}
		}
		return field("widget", ports.Widgets.AddToEventList(ctx, o.Widget, displayName(sc.User, sc.Platform), text))
	default:
		return field("op", fmt.Errorf("%w: unknown operation %q", action.ErrInvalid, o.Op))
	}
}

// update runs the operation update (B22): for the timer widget a new
// duration or a reset (B26), for the other kinds the state data as the
// runtime shows it.
func (t Overlay) update(ctx context.Context) error {
	o := t.Widget
	ports := t.ports
	if w, ok := ports.Overlay.Widget(o.Widget); ok && w.Kind == "timer" {
		var doc struct {
			Duration string `json:"duration"`
			Reset    bool   `json:"reset"`
			Pause    bool   `json:"pause"`
		}
		if o.Data != nil {
			data, err := json.Marshal(o.Data)
			if err != nil {
				return field("data", fmt.Errorf("%w: the data is not a JSON document", action.ErrInvalid))
			}
			if err := json.Unmarshal(data, &doc, json.RejectUnknownMembers(true)); err != nil {
				return field("data", fmt.Errorf("%w: the data: %w", action.ErrInvalid, err))
			}
		}
		switch {
		case doc.Pause:
			return field("data", fmt.Errorf("%w: the timer is paused with the pause_timer operation", action.ErrInvalid))
		case doc.Reset:
			return field("widget", ports.Widgets.UpdateTimer(ctx, o.Widget, 0, true))
		case doc.Duration != "":
			d, err := time.ParseDuration(doc.Duration)
			if err != nil {
				return field("data", fmt.Errorf("%w: the duration %q is unknown", action.ErrInvalid, doc.Duration))
			}
			return field("widget", ports.Widgets.UpdateTimer(ctx, o.Widget, d, false))
		default:
			return field("data", fmt.Errorf("%w: the data of the timer widget needs a duration or a reset", action.ErrInvalid))
		}
	}
	if o.Data == nil {
		return field("data", fmt.Errorf("%w: the operation update needs data", action.ErrInvalid))
	}
	return field("widget", ports.Overlay.UpdateWidget(ctx, o.Widget, o.Data))
}

// displayName is the display name of the user on the platform, or its
// login; empty if the run has no user of the platform.
func displayName(u *user.User, p platform.Name) string {
	if u == nil {
		return ""
	}
	ident, ok := u.Identity(p)
	if !ok {
		return ""
	}
	if ident.DisplayName != "" {
		return ident.DisplayName
	}
	return ident.Login
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
