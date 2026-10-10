// SPDX-License-Identifier: MIT

// Package twitch has the twitch action (roadmap 4.4): one action type with
// the Twitch operations a command can run on the streamer channel — clip,
// stream marker, ad break, raid, shoutout, poll, prediction and channel
// points reward. The operations go through Helix (internal/helix), bound to
// the streamer account by the composition root (Ports.Ops).
//
// A poll or prediction runs a sublist of child actions once it has ended,
// with the identifier $pollchoice or $predictionoutcome set to the winner
// (spec twitch-events.md, B17, A4).
package twitch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeTwitch is the type ID of the twitch action (Code-ADR-0013, point 1).
const TypeTwitch = "twitch"

// The result identifiers the poll and prediction sublists receive.
const (
	// PollChoice is the winning choice of a finished poll, for the sublist
	// of the kind poll (spec twitch-events.md, B17).
	PollChoice = "pollchoice"
	// PredictionOutcome is the winning outcome of a finished prediction,
	// for the sublist of the kind prediction (spec twitch-events.md, B17).
	PredictionOutcome = "predictionoutcome"
)

// Kind is the Twitch operation a twitch action runs (roadmap 4.4).
type Kind string

// The kinds of the twitch action.
const (
	// KindClip makes a clip of the stream. New twitch actions do this.
	KindClip Kind = "clip"
	// KindMarker makes a stream marker.
	KindMarker Kind = "marker"
	// KindAd starts an ad break.
	KindAd Kind = "ad"
	// KindRaid raids another channel.
	KindRaid Kind = "raid"
	// KindShoutout sends a shoutout to another channel.
	KindShoutout Kind = "shoutout"
	// KindPoll starts a poll and runs its sublist when the poll ends.
	KindPoll Kind = "poll"
	// KindPrediction starts a prediction and runs its sublist when it ends.
	KindPrediction Kind = "prediction"
	// KindReward manages a channel points reward.
	KindReward Kind = "reward"
)

// Kinds returns the kinds of the twitch action, in the order editors show
// them.
func Kinds() []Kind {
	return []Kind{
		KindClip, KindMarker, KindAd, KindRaid, KindShoutout,
		KindPoll, KindPrediction, KindReward,
	}
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	return slices.Contains(Kinds(), k)
}

// RewardOp is the operation of the kind reward.
type RewardOp string

// The operations of the kind reward.
const (
	// RewardCreate creates the named reward. New reward operations do this.
	RewardCreate RewardOp = "create"
	// RewardEnable makes the named reward redeemable.
	RewardEnable RewardOp = "enable"
	// RewardDisable stops the named reward being redeemed.
	RewardDisable RewardOp = "disable"
	// RewardDelete deletes the named reward.
	RewardDelete RewardOp = "delete"
)

// RewardOps returns the operations of the kind reward.
func RewardOps() []RewardOp {
	return []RewardOp{RewardCreate, RewardEnable, RewardDisable, RewardDelete}
}

// Valid reports whether o is a known reward operation.
func (o RewardOp) Valid() bool {
	return slices.Contains(RewardOps(), o)
}

// PollStatus is the state of a poll as the action needs it.
type PollStatus struct {
	// Ended reports whether the poll has ended (completed, canceled or
	// terminated).
	Ended bool
	// Winner is the winning choice; empty while the poll runs.
	Winner string
}

// PredictionStatus is the state of a prediction as the action needs it.
type PredictionStatus struct {
	// Ended reports whether the prediction has ended (resolved or canceled).
	Ended bool
	// Winner is the winning outcome; empty while the prediction runs.
	Winner string
}

// Ops are the Twitch operations the action runs on the streamer channel,
// bound to the account by the composition root (internal/helix). Every
// method returns an error naming the operation it failed.
type Ops interface {
	// Clip makes a clip of the stream and returns its URL.
	Clip(ctx context.Context) (string, error)
	// Marker makes a stream marker with the description and returns its ID.
	Marker(ctx context.Context, description string) (string, error)
	// StartAd starts an ad break of lengthSeconds, 30 or 60.
	StartAd(ctx context.Context, lengthSeconds int) error
	// Raid raids the channel toLogin.
	Raid(ctx context.Context, toLogin string) error
	// Shoutout sends a shoutout to the channel toLogin.
	Shoutout(ctx context.Context, toLogin string) error
	// CreatePoll starts a poll and returns its ID.
	CreatePoll(ctx context.Context, in PollInput) (string, error)
	// PollStatus reports the state of the poll with the ID.
	PollStatus(ctx context.Context, pollID string) (PollStatus, error)
	// CreatePrediction starts a prediction and returns its ID.
	CreatePrediction(ctx context.Context, in PredictionInput) (string, error)
	// PredictionStatus reports the state of the prediction with the ID.
	PredictionStatus(ctx context.Context, predictionID string) (PredictionStatus, error)
	// CreateReward creates a channel points reward with the title and
	// returns its ID.
	CreateReward(ctx context.Context, title string, cost int) (string, error)
	// SetReward makes the reward with the title redeemable or not.
	SetReward(ctx context.Context, title string, enabled bool) error
	// DeleteReward deletes the reward with the title.
	DeleteReward(ctx context.Context, title string) error
}

// PollInput is the poll the kind poll starts.
type PollInput struct {
	// Title is the poll title.
	Title string
	// Choices are the poll outcomes, two to four.
	Choices []string
	// Duration is the poll length, in seconds.
	Duration int
}

// PredictionInput is the prediction the kind prediction starts.
type PredictionInput struct {
	// OutcomeA and OutcomeB are the prediction outcomes.
	OutcomeA, OutcomeB string
	// Duration is the betting time, in seconds.
	Duration int
}

// Ports are what the twitch types need.
type Ports struct {
	// Templates renders the operation members.
	Templates *template.Engine
	// Ops run the operations on the streamer channel.
	Ops Ops
	// PollTick is the wait between the status checks of a running poll or
	// prediction; tests shorten it. Empty takes the default.
	PollTick time.Duration
	// Logger records the operations and their results.
	Logger *slog.Logger
}

// defaultPollTick is the wait between the status checks of a running poll
// or prediction.
const defaultPollTick = time.Second

// ports are the ports of the twitch types.
type ports struct {
	Ports
	tick time.Duration
}

// Descriptors returns the twitch type with its ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Templates == nil:
		return nil, errors.New("twitch action types: no template engine")
	case p.Ops == nil:
		return nil, errors.New("twitch action types: no operations")
	case p.Logger == nil:
		return nil, errors.New("twitch action types: no logger")
	}
	tick := p.PollTick
	if tick <= 0 {
		tick = defaultPollTick
	}
	return descriptors(&ports{Ports: p, tick: tick}), nil
}

// Catalog returns the twitch type without ports, for the type catalog and
// commands as code (Code-ADR-0013, point 3): its actions decode, validate
// and encode, but must not run.
func Catalog() []action.Descriptor {
	return descriptors(nil)
}

// descriptors returns the twitch type with its ports, which are nil in the
// catalog.
func descriptors(ports *ports) []action.Descriptor {
	return []action.Descriptor{
		action.Descriptor{
			Type:     TypeTwitch,
			Version:  1,
			Category: action.CategoryTwitch,
			Schema:   twitchSchema(),
		}.WithKinds(KindClip, newTwitch(ports)),
	}
}

// twitchSchema returns the schema of the twitch action: each kind carries
// the members of its operation; poll and prediction carry a sublist.
func twitchSchema() *schema.Schema {
	return schema.Kinds(
		nil,
		schema.Variant{Kind: string(KindClip)},
		schema.Variant{Kind: string(KindMarker), Props: []schema.Property{
			{Name: "description", Schema: schema.NonEmpty(schema.UITemplate)},
		}},
		schema.Variant{Kind: string(KindAd), Props: []schema.Property{
			{Name: "length", Schema: schema.Choice("30", "60")},
		}},
		schema.Variant{Kind: string(KindRaid), Props: []schema.Property{
			{Name: "target", Schema: schema.NonEmpty(schema.UITemplate)},
		}},
		schema.Variant{Kind: string(KindShoutout), Props: []schema.Property{
			{Name: "target", Schema: schema.NonEmpty(schema.UITemplate)},
		}},
		schema.Variant{Kind: string(KindPoll), Props: []schema.Property{
			{Name: "title", Schema: schema.NonEmpty(schema.UITemplate)},
			{Name: "choices", Schema: schema.List(schema.NonEmpty(schema.UITemplate), 2)},
			{Name: "durationMinutes", Schema: schema.Amount(1, 60, true)},
			{Name: "actions", Schema: schema.Actions()},
		}},
		schema.Variant{Kind: string(KindPrediction), Props: []schema.Property{
			{Name: "outcomeA", Schema: schema.NonEmpty(schema.UITemplate)},
			{Name: "outcomeB", Schema: schema.NonEmpty(schema.UITemplate)},
			{Name: "durationMinutes", Schema: schema.Amount(1, 90, true)},
			{Name: "actions", Schema: schema.Actions()},
		}},
		schema.Variant{Kind: string(KindReward), Props: []schema.Property{
			{Name: "op", Schema: schema.Choice(string(RewardCreate), string(RewardEnable), string(RewardDisable), string(RewardDelete))},
			{Name: "title", Schema: schema.NonEmpty(schema.UITemplate)},
			{Name: "cost", Schema: schema.Amount(1, 1_000_000, true)},
		}},
	)
}

// Twitch is the twitch action (roadmap 4.4). Which members it has depends
// on its kind; poll and prediction carry a sublist.
type Twitch struct {
	action.Common `json:",embed"`
	Kind          Kind `json:"kind"`
	// Marker is the description of a marker; nil for the other kinds.
	Marker *MarkerOptions `json:",embed"`
	// Ad is the length of an ad break in seconds; nil for the other kinds.
	Ad *AdOptions `json:",embed"`
	// Target is the login of the raided or shouted-out channel; nil for the
	// other kinds.
	Target *TargetOptions `json:",embed"`
	// Poll are the options of a poll; nil for the other kinds.
	Poll *PollOptions `json:",embed"`
	// Prediction are the options of a prediction; nil for the other kinds.
	Prediction *PredictionOptions `json:",embed"`
	// Reward are the options of a reward operation; nil for the other kinds.
	Reward *RewardOptions `json:",embed"`
	ports  *ports
}

// MarkerOptions are the options of the kind marker.
type MarkerOptions struct {
	// Description is the marker text; it is not empty.
	Description action.Template `json:"description"`
}

// AdOptions are the options of the kind ad.
type AdOptions struct {
	// Length is the length of the ad break, in seconds: 30 or 60.
	Length int `json:"length"`
}

// TargetOptions are the options of the kinds raid and shoutout.
type TargetOptions struct {
	// Target is the login of the other channel, as a template.
	Target action.Template `json:"target"`
}

// PollOptions are the options of the kind poll.
type PollOptions struct {
	// Title is the poll title; it is not empty.
	Title action.Template `json:"title"`
	// Choices are the poll outcomes, two to four; each is not empty.
	Choices []action.Template `json:"choices"`
	// DurationMinutes is the poll length, in minutes.
	DurationMinutes int `json:"durationMinutes"`
	// Actions run after the poll has ended, with $pollchoice set.
	Actions []command.Action `json:"actions"`
}

// PredictionOptions are the options of the kind prediction.
type PredictionOptions struct {
	// OutcomeA and OutcomeB are the outcomes; each is not empty.
	OutcomeA action.Template `json:"outcomeA"`
	OutcomeB action.Template `json:"outcomeB"`
	// DurationMinutes is the betting time, in minutes.
	DurationMinutes int `json:"durationMinutes"`
	// Actions run after the prediction has ended, with $predictionoutcome
	// set.
	Actions []command.Action `json:"actions"`
}

// RewardOptions are the options of the kind reward.
type RewardOptions struct {
	// Op is the reward operation.
	Op RewardOp `json:"op"`
	// Title is the reward title; it is not empty.
	Title action.Template `json:"title"`
	// Cost is the points a redemption costs; set for a create.
	Cost int `json:"cost"`
}

// newTwitch makes a twitch action of kind k with the defaults of that kind
// and the ports of the type; ok is false for an unknown kind.
func newTwitch(ports *ports) func(Kind) (Twitch, bool) {
	return func(k Kind) (Twitch, bool) {
		t := Twitch{Common: action.On(), Kind: k, ports: ports}
		switch k {
		case KindMarker:
			t.Marker = &MarkerOptions{}
		case KindAd:
			t.Ad = &AdOptions{Length: 30}
		case KindRaid, KindShoutout:
			t.Target = &TargetOptions{}
		case KindPoll:
			t.Poll = &PollOptions{DurationMinutes: 1}
		case KindPrediction:
			t.Prediction = &PredictionOptions{DurationMinutes: 1}
		case KindReward:
			t.Reward = &RewardOptions{Op: RewardCreate}
		case KindClip:
			// no options
		default:
			return Twitch{}, false
		}
		return t, true
	}
}

// DocType implements command.Action.
func (Twitch) DocType() string { return TypeTwitch }

// Children implements command.Parent: the sublist of a poll or prediction;
// none for the other kinds.
func (t Twitch) Children() []command.Action {
	switch t.Kind {
	case KindPoll:
		if t.Poll != nil {
			return t.Poll.Actions
		}
	case KindPrediction:
		if t.Prediction != nil {
			return t.Prediction.Actions
		}
	default:
		// Not a container (clip, marker, ad, raid, shoutout, reward).
	}
	return nil
}

// Validate implements command.Action.
func (t Twitch) Validate() error {
	switch {
	case !t.Kind.Valid():
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, t.Kind))
	case t.Kind == KindMarker && (t.Marker == nil || t.Marker.Description == ""):
		return field("description", fmt.Errorf("%w: empty marker description", action.ErrInvalid))
	case t.Kind != KindMarker && t.Marker != nil:
		return field("description", fmt.Errorf("%w: only a marker has a description", action.ErrInvalid))
	case t.Kind == KindAd && (t.Ad == nil || (t.Ad.Length != 30 && t.Ad.Length != 60)):
		return field("length", fmt.Errorf("%w: an ad break is 30 or 60 seconds", action.ErrInvalid))
	case t.Kind != KindAd && t.Ad != nil:
		return field("length", fmt.Errorf("%w: only an ad break has a length", action.ErrInvalid))
	case t.Kind == KindRaid || t.Kind == KindShoutout:
		if t.Target == nil || t.Target.Target == "" {
			return field("target", fmt.Errorf("%w: empty target", action.ErrInvalid))
		}
		return nil
	case t.Kind != KindRaid && t.Kind != KindShoutout && t.Target != nil:
		return field("target", fmt.Errorf("%w: only a raid or shoutout has a target", action.ErrInvalid))
	case t.Kind == KindPoll:
		return t.pollInvalid()
	case t.Kind != KindPoll && t.Poll != nil:
		return field("title", fmt.Errorf("%w: only a poll has its options", action.ErrInvalid))
	case t.Kind == KindPrediction:
		return t.predictionInvalid()
	case t.Kind != KindPrediction && t.Prediction != nil:
		return field("outcomeA", fmt.Errorf("%w: only a prediction has its options", action.ErrInvalid))
	case t.Kind == KindReward:
		return t.rewardInvalid()
	case t.Kind != KindReward && t.Reward != nil:
		return field("op", fmt.Errorf("%w: only a reward has its options", action.ErrInvalid))
	default:
		return nil
	}
}

// pollInvalid names the invalid member of the kind poll; nil if it is
// valid.
func (t Twitch) pollInvalid() error {
	p := t.Poll
	if p == nil {
		return field("title", fmt.Errorf("%w: a poll needs its options", action.ErrInvalid))
	}
	switch {
	case p.Title == "":
		return field("title", fmt.Errorf("%w: empty poll title", action.ErrInvalid))
	case len(p.Choices) < 2 || len(p.Choices) > 4:
		return field("choices", fmt.Errorf("%w: a poll takes two to four choices", action.ErrInvalid))
	case slices.ContainsFunc(p.Choices, func(c action.Template) bool { return c == "" }):
		return field("choices", fmt.Errorf("%w: an empty poll choice", action.ErrInvalid))
	case p.DurationMinutes < 1 || p.DurationMinutes > 60:
		return field("durationMinutes", fmt.Errorf("%w: a poll is one to sixty minutes", action.ErrInvalid))
	default:
		return nil
	}
}

// predictionInvalid names the invalid member of the kind prediction.
func (t Twitch) predictionInvalid() error {
	p := t.Prediction
	if p == nil {
		return field("outcomeA", fmt.Errorf("%w: a prediction needs its options", action.ErrInvalid))
	}
	switch {
	case p.OutcomeA == "" || p.OutcomeB == "":
		return field("outcomeA", fmt.Errorf("%w: empty prediction outcome", action.ErrInvalid))
	case p.DurationMinutes < 1 || p.DurationMinutes > 90:
		return field("durationMinutes", fmt.Errorf("%w: a prediction is one to ninety minutes", action.ErrInvalid))
	default:
		return nil
	}
}

// rewardInvalid names the invalid member of the kind reward.
func (t Twitch) rewardInvalid() error {
	r := t.Reward
	if r == nil {
		return field("op", fmt.Errorf("%w: a reward needs its options", action.ErrInvalid))
	}
	switch {
	case !r.Op.Valid():
		return field("op", fmt.Errorf("%w: unknown reward operation %q", action.ErrInvalid, r.Op))
	case r.Title == "":
		return field("title", fmt.Errorf("%w: empty reward title", action.ErrInvalid))
	case r.Op == RewardCreate && r.Cost < 1:
		return field("cost", fmt.Errorf("%w: a created reward costs at least one point", action.ErrInvalid))
	default:
		return nil
	}
}

// Perform implements engine.Performer. The action is done when the
// operation has run; a poll or prediction is done when its sublist has run.
func (t Twitch) Perform(ctx context.Context, run *engine.Run) error {
	ports := t.ports
	if ports == nil {
		return errors.New("twitch action: no ports (catalog action)")
	}
	log := ports.Logger
	switch t.Kind {
	case KindClip:
		url, err := ports.Ops.Clip(ctx)
		if err != nil {
			return field("clip", err)
		}
		log.InfoContext(ctx, "twitch clip made", "instance_id", run.InstanceID(), "url", url)
		return nil
	case KindMarker:
		id, err := t.marker(ctx, run)
		if err != nil {
			return err
		}
		log.InfoContext(ctx, "twitch marker made", "instance_id", run.InstanceID(), "id", id)
		return nil
	case KindAd:
		if err := ports.Ops.StartAd(ctx, t.Ad.Length); err != nil {
			return field("ad", err)
		}
		log.InfoContext(ctx, "twitch ad break started", "instance_id", run.InstanceID(), "length", t.Ad.Length)
		return nil
	case KindRaid:
		login, err := t.target(ctx, run)
		if err != nil {
			return err
		}
		if err := ports.Ops.Raid(ctx, login); err != nil {
			return field("raid", err)
		}
		log.InfoContext(ctx, "twitch raid sent", "instance_id", run.InstanceID(), "target", login)
		return nil
	case KindShoutout:
		login, err := t.target(ctx, run)
		if err != nil {
			return err
		}
		if err := ports.Ops.Shoutout(ctx, login); err != nil {
			return field("shoutout", err)
		}
		log.InfoContext(ctx, "twitch shoutout sent", "instance_id", run.InstanceID(), "target", login)
		return nil
	case KindPoll:
		return t.poll(ctx, run)
	case KindPrediction:
		return t.prediction(ctx, run)
	case KindReward:
		return t.reward(ctx, run)
	default:
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, t.Kind))
	}
}

// marker renders the description and makes the marker.
func (t Twitch) marker(ctx context.Context, run *engine.Run) (string, error) {
	text, err := t.ports.Templates.Render(ctx, t.Marker.Description.Parse(), run.Scope(), template.Text)
	if err != nil {
		return "", err
	}
	return t.ports.Ops.Marker(ctx, text)
}

// target renders the target login.
func (t Twitch) target(ctx context.Context, run *engine.Run) (string, error) {
	return t.ports.Templates.Render(ctx, t.Target.Target.Parse(), run.Scope(), template.Text)
}

// poll starts the poll, waits for it to end, sets $pollchoice to the
// winner and runs the sublist (spec twitch-events.md, B17).
func (t Twitch) poll(ctx context.Context, run *engine.Run) error {
	s := []template.Template{t.Poll.Title.Parse()}
	for _, c := range t.Poll.Choices {
		s = append(s, c.Parse())
	}
	rendered, err := t.ports.Templates.RenderEach(ctx, s, run.Scope())
	if err != nil {
		return err
	}
	in := PollInput{
		Title:    rendered[0].Text,
		Choices:  texts(rendered[1:]),
		Duration: t.Poll.DurationMinutes * 60,
	}
	id, err := t.ports.Ops.CreatePoll(ctx, in)
	if err != nil {
		return field("poll", err)
	}
	winner, err := t.ports.untilEnded(ctx, id, t.ports.pollStatus)
	if err != nil {
		return field("poll", err)
	}
	run.Scope().SetValue(PollChoice, template.TextValue(winner))
	return t.ports.runSublist(ctx, run, t.Poll.Actions)
}

// prediction starts the prediction, waits for it to end, sets
// $predictionoutcome to the winner and runs the sublist.
func (t Twitch) prediction(ctx context.Context, run *engine.Run) error {
	rendered, err := t.ports.Templates.RenderEach(ctx,
		[]template.Template{t.Prediction.OutcomeA.Parse(), t.Prediction.OutcomeB.Parse()},
		run.Scope())
	if err != nil {
		return err
	}
	in := PredictionInput{
		OutcomeA: rendered[0].Text,
		OutcomeB: rendered[1].Text,
		Duration: t.Prediction.DurationMinutes * 60,
	}
	id, err := t.ports.Ops.CreatePrediction(ctx, in)
	if err != nil {
		return field("prediction", err)
	}
	winner, err := t.ports.untilEnded(ctx, id, t.ports.predictionStatus)
	if err != nil {
		return field("prediction", err)
	}
	run.Scope().SetValue(PredictionOutcome, template.TextValue(winner))
	return t.ports.runSublist(ctx, run, t.Prediction.Actions)
}

// reward renders the title and runs the reward operation.
func (t Twitch) reward(ctx context.Context, run *engine.Run) error {
	title, err := t.ports.Templates.Render(ctx, t.Reward.Title.Parse(), run.Scope(), template.Text)
	if err != nil {
		return err
	}
	switch t.Reward.Op {
	case RewardCreate:
		id, err := t.ports.Ops.CreateReward(ctx, title, t.Reward.Cost)
		if err != nil {
			return field("reward", err)
		}
		t.ports.Logger.InfoContext(ctx, "twitch reward created", "instance_id", run.InstanceID(), "id", id)
		return nil
	case RewardEnable:
		return field("reward", t.ports.Ops.SetReward(ctx, title, true))
	case RewardDisable:
		return field("reward", t.ports.Ops.SetReward(ctx, title, false))
	case RewardDelete:
		return field("reward", t.ports.Ops.DeleteReward(ctx, title))
	default:
		return field("op", fmt.Errorf("%w: unknown reward operation %q", action.ErrInvalid, t.Reward.Op))
	}
}

// untilEnded polls status for the ID until it reports ended and returns
// the winner, or until ctx ends.
func (p *ports) untilEnded(ctx context.Context, id string, status func(context.Context, string) (string, bool, error)) (string, error) {
	for {
		winner, ended, err := status(ctx, id)
		if err != nil {
			return "", err
		}
		if ended {
			return winner, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(p.tick):
		}
	}
}

// pollStatus adapts Ops.PollStatus to the untilEnded signature.
func (p *ports) pollStatus(ctx context.Context, id string) (string, bool, error) {
	st, err := p.Ops.PollStatus(ctx, id)
	if err != nil {
		return "", false, err
	}
	return st.Winner, st.Ended, nil
}

// predictionStatus adapts Ops.PredictionStatus to the untilEnded signature.
func (p *ports) predictionStatus(ctx context.Context, id string) (string, bool, error) {
	st, err := p.Ops.PredictionStatus(ctx, id)
	if err != nil {
		return "", false, err
	}
	return st.Winner, st.Ended, nil
}

// runSublist runs the child actions of a poll or prediction through the
// engine, so the engine applies its rules to them (Code-ADR-0013).
func (p *ports) runSublist(ctx context.Context, run *engine.Run, actions []command.Action) error {
	for i := range actions {
		if _, err := run.PerformChild(ctx, i); err != nil {
			return err
		}
	}
	return nil
}

// texts returns the text of each rendered template.
func texts(rendered []template.Rendered) []string {
	out := make([]string, len(rendered))
	for i, r := range rendered {
		out[i] = r.Text
	}
	return out
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
