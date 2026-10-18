// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"google.golang.org/protobuf/types/known/timestamppb"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	"github.com/ripmav/streamcrew/internal/api"
	"github.com/ripmav/streamcrew/internal/connector/mock"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/dryrun"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/prompt"
)

// streamBufferSize is how many events the hub keeps for the since catch-up.
const streamBufferSize = 256

// apiStream is the api.Stream port: it fans out the converted bus events to
// the subscribers and answers prompts (roadmap 6.2.1).
type apiStream struct {
	a    *App
	once sync.Once
	hub  *streamHub
}

var _ api.Stream = (*apiStream)(nil)

func newAPIStream(app *App) *apiStream {
	return &apiStream{a: app}
}

// streamHub subscribes to the bus once, converts the events, keeps the last
// ones for the since catch-up and fans them out to the subscriber channels.
type streamHub struct {
	a    *App
	mu   sync.Mutex
	subs map[chan *v1alpha1.Event]struct{}
	buf  []*v1alpha1.Event
}

func newStreamHub(app *App) *streamHub {
	return &streamHub{a: app, subs: make(map[chan *v1alpha1.Event]struct{})}
}

// run subscribes to the bus and fans out the converted events until the
// context ends.
func (h *streamHub) run(ctx context.Context) {
	sub := h.a.Bus().Subscribe(ctx, event.WithBuffer(1024))
	for e := range sub.C() {
		ev := convertEvent(e)
		if ev == nil {
			continue
		}
		h.publish(ev)
	}
}

// publish keeps the event in the buffer and fans it out to the subscribers.
func (h *streamHub) publish(ev *v1alpha1.Event) {
	h.mu.Lock()
	h.buf = append(h.buf, ev)
	if len(h.buf) > streamBufferSize {
		h.buf = h.buf[len(h.buf)-streamBufferSize:]
	}
	subs := make([]chan *v1alpha1.Event, 0, len(h.subs))
	for ch := range h.subs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default: // drop for a slow subscriber
		}
	}
}

// subscribe registers a subscriber channel and returns a snapshot of the
// buffered events for the since catch-up.
func (h *streamHub) subscribe() (ch chan *v1alpha1.Event, buffered []*v1alpha1.Event) {
	ch = make(chan *v1alpha1.Event, 256)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	buffered = append([]*v1alpha1.Event{}, h.buf...)
	h.mu.Unlock()
	return ch, buffered
}

func (h *streamHub) unsubscribe(ch chan *v1alpha1.Event) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// hub returns the hub, starting it once with the core's run context.
func (x *apiStream) ensureHub() *streamHub {
	x.once.Do(func() {
		h := newStreamHub(x.a)
		x.hub = h
		go h.run(x.a.runCtx)
	})
	return x.hub
}

// Stream returns a channel of converted events matching the filter, starting
// with the last since events from the buffer.
func (x *apiStream) Stream(ctx context.Context, f api.EventFilter, since uint32) <-chan *v1alpha1.Event {
	if x.a.runCtx == nil {
		ch := make(chan *v1alpha1.Event)
		close(ch)
		return ch
	}
	h := x.ensureHub()
	sub, buffered := h.subscribe()
	out := make(chan *v1alpha1.Event, 256)
	go func() {
		defer close(out)
		defer h.unsubscribe(sub)
		if since > 0 {
			replay := min(int(since), len(buffered))
			for _, ev := range buffered[len(buffered)-replay:] {
				if !matchesFilter(ev, f) {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case out <- ev:
				}
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-sub:
				if !ok {
					return
				}
				if !matchesFilter(ev, f) {
					continue
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// AnswerPrompt answers a prompt delivered through the stream.
func (x *apiStream) AnswerPrompt(_ context.Context, id, value string, confirmed bool) error {
	if !x.a.prompts.Answer(id, value, confirmed) {
		return fmt.Errorf("the prompt %s is not pending", id)
	}
	return nil
}

// convertEvent turns a bus event into its protocol form; it returns nil for
// the types the stream does not deliver.
func convertEvent(e event.Envelope) *v1alpha1.Event {
	base := &v1alpha1.Event{OccurredAt: timestamppb.New(e.Time)}
	switch {
	case e.Type == eventtype.ChatMessage:
		return chatEvent(e, base)
	case e.Type == prompt.Type:
		return promptEvent(e, base)
	case e.Type == engine.TypeInstanceQueued || e.Type == engine.TypeInstanceStarted ||
		e.Type == engine.TypeInstanceCompleted || e.Type == engine.TypeInstanceFailed ||
		e.Type == engine.TypeInstanceCanceled:
		return instanceEvent(e, base)
	case isFeedEvent(e.Type):
		return feedEvent(e, base)
	case e.Type == eventtype.Log:
		return logEvent(e, base)
	}
	return nil
}

// logEvent converts a "log" event to its protocol form (roadmap 6.6).
func logEvent(e event.Envelope, base *v1alpha1.Event) *v1alpha1.Event {
	p, ok := event.Payload[LogPayload](e)
	if !ok {
		return nil
	}
	base.Payload = &v1alpha1.Event_Log{Log: &v1alpha1.LogEntry{
		Level:     p.Level,
		Component: p.Component,
		Message:   p.Message,
	}}
	return base
}

func isFeedEvent(t event.Type) bool {
	switch t {
	case eventtype.ChannelFollow, eventtype.ChannelRaid, eventtype.ChannelSubscribe,
		eventtype.ChannelResubscribe, eventtype.ChannelSubscriptionGift,
		eventtype.ChannelSubscriptionMassGift, eventtype.TwitchBitsCheer:
		return true
	default:
		return false
	}
}

// userName is the login and display name of the event's user on its platform.
func userName(p eventtype.Payload) (login, display string) {
	if p.User == nil {
		return "", ""
	}
	for _, idn := range p.User.Identities {
		if idn.Platform == p.Platform {
			return idn.Login, idn.DisplayName
		}
	}
	if len(p.User.Identities) > 0 {
		return p.User.Identities[0].Login, p.User.Identities[0].DisplayName
	}
	return "", ""
}

func chatEvent(e event.Envelope, base *v1alpha1.Event) *v1alpha1.Event {
	p, ok := event.Payload[eventtype.Payload](e)
	if !ok || p.Details.Message == nil {
		return nil
	}
	m := p.Details.Message
	base.Platform = string(p.Platform)
	base.Channel = p.Channel
	login, display := userName(p)
	base.Payload = &v1alpha1.Event_Chat{Chat: &v1alpha1.ChatMessage{
		MessageId:   m.ID,
		Text:        m.Text,
		Emotes:      m.Emotes,
		Login:       login,
		DisplayName: display,
	}}
	return base
}

func instanceEvent(e event.Envelope, base *v1alpha1.Event) *v1alpha1.Event {
	ins, ok := event.Payload[engine.Instance](e)
	if !ok {
		return nil
	}
	base.Platform = string(ins.Platform)
	errText := ""
	if len(ins.Errors) > 0 {
		errText = ins.Errors[0].Message
	}
	base.Payload = &v1alpha1.Event_Command{Command: &v1alpha1.CommandInstance{
		InstanceId:  ins.ID.String(),
		CommandId:   ins.CommandID.String(),
		CommandName: ins.CommandName,
		State:       string(ins.State),
		Source:      string(ins.Source),
		Error:       errText,
	}}
	return base
}

func promptEvent(e event.Envelope, base *v1alpha1.Event) *v1alpha1.Event {
	p, ok := event.Payload[prompt.Prompt](e)
	if !ok {
		return nil
	}
	base.Payload = &v1alpha1.Event_Prompt{Prompt: &v1alpha1.Prompt{Id: p.ID, Kind: p.Kind, Text: p.Text, Options: p.Options}}
	return base
}

func feedEvent(e event.Envelope, base *v1alpha1.Event) *v1alpha1.Event {
	p, ok := event.Payload[eventtype.Payload](e)
	if !ok {
		return nil
	}
	base.Platform = string(p.Platform)
	base.Channel = p.Channel
	login, display := userName(p)
	feed := &v1alpha1.FeedItem{Type: string(e.Type), Login: login, DisplayName: display}
	switch {
	case p.Details.Raid != nil:
		feed.Amount = p.Details.Raid.Viewers
	case p.Details.Subscription != nil:
		feed.Tier = subscriptionTier(p.Details.Subscription.Plan)
	case p.Details.Gift != nil:
		feed.Amount = int64(p.Details.Gift.Count)
	case p.Details.Bits != nil:
		feed.Amount = p.Details.Bits.Amount
	}
	base.Payload = &v1alpha1.Event_Feed{Feed: feed}
	return base
}

func subscriptionTier(plan string) int32 {
	switch plan {
	case "1000":
		return 1
	case "2000":
		return 2
	case "5000":
		return 3
	default:
		return 0
	}
}

// Simulate publishes a simulated event of the mock platform (roadmap
// 6.3): a chat message of the user, or a platform-neutral event like the
// mock console simulates.
func (x *apiStream) Simulate(ctx context.Context, eventType, user, target, text string) (string, error) {
	p, ok := x.a.platforms.Platform(platform.Mock)
	if !ok {
		return "", errors.New("simulate: no mock platform is connected")
	}
	if inner, ok := dryrun.AsInner(p); ok {
		p = inner
	}
	mp, ok := p.(*mock.Platform)
	if !ok {
		return "", errors.New("simulate: the mock platform is unavailable")
	}
	if event.Type(eventType) == eventtype.ChatMessage {
		if user == "" {
			return "", errors.New("simulate: a chat message needs a user")
		}
		sent, err := mp.Say(ctx, user, text)
		return sent.ID, err
	}
	sent, err := mp.Simulate(ctx, mock.Event{Type: event.Type(eventType), User: user, Target: target})
	return sent.ID, err
}

// matchesFilter reports whether the event passes the filter.
func matchesFilter(ev *v1alpha1.Event, f api.EventFilter) bool {
	if len(f.Kinds) > 0 && !hasPrefixAny(ev.GetType(), f.Kinds) {
		return false
	}
	if len(f.Platforms) > 0 && !inList(ev.GetPlatform(), f.Platforms) {
		return false
	}
	if len(f.Channels) > 0 && !inList(ev.GetChannel(), f.Channels) {
		return false
	}
	return true
}

func hasPrefixAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func inList(s string, list []string) bool {
	return slices.Contains(list, s)
}
