// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	twitchaction "github.com/ripmav/streamcrew/internal/action/twitch"
	"github.com/ripmav/streamcrew/internal/auth"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/dryrun"
	"github.com/ripmav/streamcrew/internal/emote"
	"github.com/ripmav/streamcrew/internal/helix"
	"github.com/ripmav/streamcrew/internal/twitch"
)

// twitchStreamAuth adapts the auth service to twitch.Auth: the streamer
// account of the channel (roadmap 4.3, task 5).
type twitchStreamAuth struct {
	s *auth.Service
}

// twitchOps returns the twitch operations for the action registry: the
// Helix implementation switched with the dry-run recording at runtime
// (roadmap 6.5).
func (a *App) twitchOps() twitchaction.Ops {
	return dryrun.NewSwitchOps(twitchOps{app: a}, dryrun.NewOps(component(a.logger, "dryrun")), &a.dryRun)
}

// Streamer implements twitch.Auth.
func (a twitchStreamAuth) Streamer(ctx context.Context) (twitch.AccountState, error) {
	sts, err := a.s.Status(ctx)
	if err != nil {
		return twitch.AccountState{}, err
	}
	for _, st := range sts {
		if st.Platform == platform.Twitch && st.Role == connector.AccountStreamer {
			return twitch.AccountState{
				AccountID: st.UserID,
				Login:     st.Login,
				Ready:     st.State == auth.StateOK,
			}, nil
		}
	}
	return twitch.AccountState{}, nil
}

// Token implements twitch.Auth.
func (a twitchStreamAuth) Token(ctx context.Context) (string, error) {
	return a.s.Token(ctx, platform.Twitch, connector.AccountStreamer)
}

// Bot implements twitch.Auth: the bot account the additional channels run
// with (twitch-multi-channel.md, B2); without one, an empty state.
func (a twitchStreamAuth) Bot(ctx context.Context) (twitch.AccountState, error) {
	sts, err := a.s.Status(ctx)
	if err != nil {
		return twitch.AccountState{}, err
	}
	for _, st := range sts {
		if st.Platform == platform.Twitch && st.Role == connector.AccountBot {
			return twitch.AccountState{
				AccountID: st.UserID,
				Login:     st.Login,
				Ready:     st.State == auth.StateOK,
			}, nil
		}
	}
	return twitch.AccountState{}, nil
}

// BotToken implements twitch.Auth.
func (a twitchStreamAuth) BotToken(ctx context.Context) (string, error) {
	return a.s.Token(ctx, platform.Twitch, connector.AccountBot)
}

// ClientID implements twitch.Auth.
func (a twitchStreamAuth) ClientID(ctx context.Context) (string, error) {
	id, found, err := a.s.ClientID(ctx, platform.Twitch, connector.AccountStreamer)
	if err != nil {
		return "", err
	}
	if !found {
		return "", auth.ErrNoAccount
	}
	return id, nil
}

// twitchOps implements action/twitch.Ops on top of the Helix API. Every
// operation is bound to the streamer account (roadmap 4.4); the account ID
// and a Helix client are resolved per call from the auth service.
type twitchOps struct {
	app *App
}

// twitchEmotes lists the emotes of the streamer's channel via Helix
// (roadmap 4.4, task 9); the emote catalog caches them.
func (a *App) twitchEmotes(ctx context.Context) ([]emote.Emote, error) {
	c, me, err := twitchOps{app: a}.client(ctx)
	if err != nil {
		return nil, err
	}
	emotes, err := c.GetEmotes(ctx, me)
	if err != nil {
		return nil, err
	}
	out := make([]emote.Emote, 0, len(emotes))
	for _, e := range emotes {
		out = append(out, emote.Emote{ID: e.ID, Code: e.Name, Image: e.Image})
	}
	return out, nil
}

// bttvChannelID resolves a channel login to the Twitch channel ID the
// BetterTTV v2 API needs (roadmap 4.4, task 9).
func bttvChannelID(a *App) func(ctx context.Context, channel string) (string, error) {
	return func(ctx context.Context, channel string) (string, error) {
		c, _, err := twitchOps{app: a}.client(ctx)
		if err != nil {
			return "", err
		}
		users, err := c.GetUsers(ctx, nil, []string{channel})
		if err != nil {
			return "", err
		}
		if len(users) == 0 {
			return "", fmt.Errorf("the channel %q is unknown", channel)
		}
		return users[0].ID, nil
	}
}

// client returns a Helix client bound to the streamer and the streamer's
// account ID.
func (o twitchOps) client(ctx context.Context) (*helix.Client, string, error) {
	sa := twitchStreamAuth{s: o.app.auth}
	st, err := sa.Streamer(ctx)
	if err != nil {
		return nil, "", err
	}
	if !st.Ready || st.AccountID == "" {
		return nil, "", errors.New("the streamer account is not ready")
	}
	cid, err := sa.ClientID(ctx)
	if err != nil {
		return nil, "", err
	}
	tok := sa.Token
	c := helix.New(o.app.twitchHelix, helix.Options{
		ClientID: cid,
		Token: func(ctx context.Context) (string, error) {
			return tok(ctx)
		},
	})
	return c, st.AccountID, nil
}

func (o twitchOps) Clip(ctx context.Context) (string, error) {
	c, me, err := o.client(ctx)
	if err != nil {
		return "", err
	}
	clip, err := c.CreateClip(ctx, me, "")
	if err != nil {
		return "", err
	}
	if clip == nil {
		return "", errors.New("the clip was not made")
	}
	return clip.URL, nil
}

func (o twitchOps) Marker(ctx context.Context, description string) (string, error) {
	c, me, err := o.client(ctx)
	if err != nil {
		return "", err
	}
	return c.CreateStreamMarker(ctx, me, helix.CreateStreamMarkerInput{Description: description})
}

func (o twitchOps) StartAd(ctx context.Context, lengthSeconds int) error {
	c, me, err := o.client(ctx)
	if err != nil {
		return err
	}
	_, err = c.StartAdBreak(ctx, me, lengthSeconds)
	return err
}

func (o twitchOps) Raid(ctx context.Context, toLogin string) error {
	c, me, err := o.client(ctx)
	if err != nil {
		return err
	}
	to, err := o.userID(ctx, c, toLogin)
	if err != nil {
		return err
	}
	return c.SendRaid(ctx, me, to)
}

func (o twitchOps) Shoutout(ctx context.Context, toLogin string) error {
	c, me, err := o.client(ctx)
	if err != nil {
		return err
	}
	to, err := o.userID(ctx, c, toLogin)
	if err != nil {
		return err
	}
	return c.SendShoutout(ctx, helix.ShoutoutInput{BroadcasterID: me, ModeratorID: me, FromID: me, ToID: to})
}

func (o twitchOps) CreatePoll(ctx context.Context, in twitchaction.PollInput) (string, error) {
	c, me, err := o.client(ctx)
	if err != nil {
		return "", err
	}
	poll, err := c.CreatePoll(ctx, helix.PollInput{
		BroadcasterID: me,
		Title:         in.Title,
		Duration:      in.Duration,
		Choices:       in.Choices,
	})
	if err != nil {
		return "", err
	}
	if poll == nil {
		return "", errors.New("the poll was not created")
	}
	return poll.ID, nil
}

func (o twitchOps) PollStatus(ctx context.Context, pollID string) (twitchaction.PollStatus, error) {
	c, me, err := o.client(ctx)
	if err != nil {
		return twitchaction.PollStatus{}, err
	}
	poll, err := c.GetPoll(ctx, me, pollID)
	if err != nil || poll == nil {
		return twitchaction.PollStatus{Ended: false}, err
	}
	winner := ""
	best := int64(-1)
	for _, out := range poll.Outcomes {
		if out.Votes > best {
			best = out.Votes
			winner = out.Choice
		}
	}
	ended := poll.Status != helix.PollVoting
	return twitchaction.PollStatus{Ended: ended, Winner: winner}, nil
}

func (o twitchOps) CreatePrediction(ctx context.Context, in twitchaction.PredictionInput) (string, error) {
	c, me, err := o.client(ctx)
	if err != nil {
		return "", err
	}
	pred, err := c.CreatePrediction(ctx, helix.PredictionInput{
		BroadcasterID: me,
		PredictionA:   in.OutcomeA,
		PredictionB:   in.OutcomeB,
		Duration:      in.Duration,
	})
	if err != nil {
		return "", err
	}
	if pred == nil {
		return "", errors.New("the prediction was not created")
	}
	return pred.ID, nil
}

func (o twitchOps) PredictionStatus(ctx context.Context, id string) (twitchaction.PredictionStatus, error) {
	c, me, err := o.client(ctx)
	if err != nil {
		return twitchaction.PredictionStatus{}, err
	}
	pred, err := c.GetPrediction(ctx, me, id)
	if err != nil || pred == nil {
		return twitchaction.PredictionStatus{Ended: false}, err
	}
	ended := pred.Status != helix.PredictionLocked
	winner := ""
	if ended {
		winner = pred.OutcomeA
		if pred.Outcome == helix.PredictionOutcomeB {
			winner = pred.OutcomeB
		}
	}
	return twitchaction.PredictionStatus{Ended: ended, Winner: winner}, nil
}

func (o twitchOps) CreateReward(ctx context.Context, title string, cost int) (string, error) {
	c, me, err := o.client(ctx)
	if err != nil {
		return "", err
	}
	costI64 := int64(cost)
	r, err := c.CreateReward(ctx, helix.RewardInput{BroadcasterID: me, Title: title, Cost: &costI64})
	if err != nil {
		return "", err
	}
	if r == nil {
		return "", errors.New("the reward was not created")
	}
	return r.ID, nil
}

func (o twitchOps) SetReward(ctx context.Context, title string, enabled bool) error {
	c, me, err := o.client(ctx)
	if err != nil {
		return err
	}
	id, err := o.rewardID(ctx, c, me, title)
	if err != nil {
		return err
	}
	_, err = c.UpdateReward(ctx, me, id, helix.RewardUpdate{IsEnabled: &enabled})
	return err
}

func (o twitchOps) DeleteReward(ctx context.Context, title string) error {
	c, me, err := o.client(ctx)
	if err != nil {
		return err
	}
	id, err := o.rewardID(ctx, c, me, title)
	if err != nil {
		return err
	}
	return c.DeleteReward(ctx, me, id)
}

// userID resolves a login to a user ID.
func (o twitchOps) userID(ctx context.Context, c *helix.Client, login string) (string, error) {
	users, err := c.GetUsers(ctx, nil, []string{login})
	if err != nil {
		return "", err
	}
	if len(users) == 0 {
		return "", fmt.Errorf("no user %q", login)
	}
	return users[0].ID, nil
}

// rewardID resolves a reward title to its reward ID.
func (o twitchOps) rewardID(ctx context.Context, c *helix.Client, broadcasterID, title string) (string, error) {
	rewards, err := c.ListRewards(ctx, broadcasterID)
	if err != nil {
		return "", err
	}
	for _, r := range rewards {
		if strings.EqualFold(r.Title, title) {
			return r.ID, nil
		}
	}
	return "", fmt.Errorf("no custom reward %q", title)
}
