// SPDX-License-Identifier: MIT

package helix_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/helix"
)

// opsFake records the last request of the fake Helix and answers with
// the configured body and status.
type opsFake struct {
	mu     sync.Mutex
	method string
	path   string
	query  url.Values
	body   string
	answer string
	status int
}

func (f *opsFake) serve(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.method = r.Method
		f.path = r.URL.Path
		f.query = r.URL.Query()
		f.body = ""
		if r.Body != nil {
			b := make([]byte, 4096)
			n, _ := r.Body.Read(b)
			f.body = string(b[:n])
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
		}
		_, _ = fmt.Fprint(w, f.answer)
	}))
}

func (f *opsFake) last(t *testing.T) (method, path string, query url.Values, body string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.method, f.path, f.query, f.body
}

func runOps(t *testing.T, f *opsFake, fn func(c *helix.Client)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		srv := f.serve(t)
		t.Cleanup(srv.Close)
		fn(newClient(t, srv, "tok"))
	})
}

func TestListClips(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"c1","broadcaster_id":"7","title":"Clip","url":"https://twitch.tv/clips/c1","view_count":5,"created_at":"2026-10-10T10:00:00Z"}]}`}
	runOps(t, f, func(c *helix.Client) {
		clips, err := c.ListClips(context.Background(), "7")
		require.NoError(t, err)
		require.Len(t, clips, 1)
		assert.Equal(t, "c1", clips[0].ID)
	})
	method, path, query, _ := f.last(t)
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/clips", path)
	assert.Equal(t, "7", query.Get("broadcaster_id"))
}

func TestCreateClip(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"c2","url":"https://twitch.tv/clips/c2"}]}`}
	runOps(t, f, func(c *helix.Client) {
		clip, err := c.CreateClip(context.Background(), "7", "3")
		require.NoError(t, err)
		require.NotNil(t, clip)
		assert.Equal(t, "c2", clip.ID)
	})
	method, path, _, body := f.last(t)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/clips", path)
	assert.JSONEq(t, `{"broadcaster_id":"7","target_id":"3"}`, body)
}

func TestListStreamMarkers(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"m1","position":300,"created_at":"2026-10-10T10:00:00Z","tag_description":"d"}]}`}
	runOps(t, f, func(c *helix.Client) {
		markers, err := c.ListStreamMarkers(context.Background(), "7")
		require.NoError(t, err)
		require.Len(t, markers, 1)
		assert.Equal(t, int64(300), markers[0].Position)
	})
	method, path, _, _ := f.last(t)
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/streamers/7/stream_markers", path)
}

func TestCreateStreamMarker(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"m2"}]}`}
	runOps(t, f, func(c *helix.Client) {
		id, err := c.CreateStreamMarker(context.Background(), "7", helix.CreateStreamMarkerInput{Position: 42, Description: "d"})
		require.NoError(t, err)
		assert.Equal(t, "m2", id)
	})
	method, path, _, body := f.last(t)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/streamers/7/stream_markers", path)
	assert.JSONEq(t, `{"position":42,"description":"d"}`, body)
}

func TestSendRaid(t *testing.T) {
	f := &opsFake{status: http.StatusNoContent}
	runOps(t, f, func(c *helix.Client) {
		assert.NoError(t, c.SendRaid(context.Background(), "7", "9"))
	})
	method, path, _, body := f.last(t)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/raids", path)
	assert.JSONEq(t, `{"from_id":"7","to_id":"9"}`, body)
}

func TestStartAdBreak(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"length":30,"ad_url":"https://ads"}]}`}
	runOps(t, f, func(c *helix.Client) {
		ad, err := c.StartAdBreak(context.Background(), "7", 30)
		require.NoError(t, err)
		require.NotNil(t, ad)
		assert.Equal(t, 30, ad.Length)
	})
	method, path, query, _ := f.last(t)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/ads", path)
	assert.Equal(t, "7", query.Get("broadcaster_id"))
	assert.Equal(t, "30", query.Get("length"))
}

func TestGetPoll(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"p1","title":"Poll","status":"COMPLETED","outcomes":[{"id":"o1","choice":"a","votes":3}]}]}`}
	runOps(t, f, func(c *helix.Client) {
		poll, err := c.GetPoll(context.Background(), "7", "p1")
		require.NoError(t, err)
		require.NotNil(t, poll)
		assert.Equal(t, helix.PollCompleted, poll.Status)
		assert.Equal(t, int64(3), poll.Outcomes[0].Votes)
	})
	_, path, query, _ := f.last(t)
	assert.Equal(t, "/polls", path)
	assert.Equal(t, "p1", query.Get("id"))
	assert.Equal(t, "7", query.Get("broadcaster_id"))
}

func TestCreatePoll(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"p2","status":"VOTING"}]}`}
	runOps(t, f, func(c *helix.Client) {
		poll, err := c.CreatePoll(context.Background(), helix.PollInput{
			BroadcasterID: "7", Title: "Poll", Duration: 60, Choices: []string{"a", "b"},
		})
		require.NoError(t, err)
		require.NotNil(t, poll)
		assert.Equal(t, "p2", poll.ID)
	})
	method, path, _, body := f.last(t)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/polls", path)
	assert.JSONEq(t, `{"broadcaster_id":"7","title":"Poll","duration":60,"choices":["a","b"]}`, body)
}

func TestEndPoll(t *testing.T) {
	f := &opsFake{status: http.StatusNoContent}
	runOps(t, f, func(c *helix.Client) {
		assert.NoError(t, c.EndPoll(context.Background(), "p1"))
	})
	method, path, query, _ := f.last(t)
	assert.Equal(t, http.MethodDelete, method)
	assert.Equal(t, "/polls", path)
	assert.Equal(t, "p1", query.Get("id"))
}

func TestGetPrediction(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"pr1","outcome_a":"a","outcome_b":"b","status":"RESOLVED","outcome":1,"total_coins":100}]}`}
	runOps(t, f, func(c *helix.Client) {
		pred, err := c.GetPrediction(context.Background(), "7", "pr1")
		require.NoError(t, err)
		require.NotNil(t, pred)
		assert.Equal(t, helix.PredictionResolved, pred.Status)
		assert.Equal(t, helix.PredictionOutcomeA, pred.Outcome)
	})
	_, path, query, _ := f.last(t)
	assert.Equal(t, "/predictions", path)
	assert.Equal(t, "pr1", query.Get("id"))
}

func TestCreatePrediction(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"pr2","status":"LOCKED"}]}`}
	runOps(t, f, func(c *helix.Client) {
		pred, err := c.CreatePrediction(context.Background(), helix.PredictionInput{
			BroadcasterID: "7", PredictionA: "a", PredictionB: "b", Duration: 300,
		})
		require.NoError(t, err)
		require.NotNil(t, pred)
		assert.Equal(t, "pr2", pred.ID)
	})
	method, path, _, body := f.last(t)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/predictions", path)
	assert.JSONEq(t, `{"broadcaster_id":"7","prediction_a":"a","prediction_b":"b","duration":300}`, body)
}

func TestEndPrediction(t *testing.T) {
	f := &opsFake{status: http.StatusNoContent}
	runOps(t, f, func(c *helix.Client) {
		assert.NoError(t, c.EndPrediction(context.Background(), helix.EndPredictionInput{
			PredictionID:   "pr1",
			WinningOutcome: helix.PredictionOutcomeB,
		}))
	})
	method, path, _, body := f.last(t)
	assert.Equal(t, http.MethodDelete, method)
	assert.Equal(t, "/predictions", path)
	assert.JSONEq(t, `{"prediction_id":"pr1","winning_outcome":2,"cancel_prediction":false}`, body)
}

func TestListRewards(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"r1","title":"Reward","cost":100,"is_enabled":true}]}`}
	runOps(t, f, func(c *helix.Client) {
		rewards, err := c.ListRewards(context.Background(), "7")
		require.NoError(t, err)
		require.Len(t, rewards, 1)
		assert.Equal(t, "r1", rewards[0].ID)
		assert.Equal(t, int64(100), rewards[0].Cost)
	})
	_, path, query, _ := f.last(t)
	assert.Equal(t, "/rewards", path)
	assert.Equal(t, "7", query.Get("broadcaster_id"))
}

func TestCreateReward(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"r2","title":"Reward","cost":100}]}`}
	runOps(t, f, func(c *helix.Client) {
		reward, err := c.CreateReward(context.Background(), helix.RewardInput{BroadcasterID: "7", Title: "Reward"})
		require.NoError(t, err)
		require.NotNil(t, reward)
		assert.Equal(t, "r2", reward.ID)
	})
	method, path, _, body := f.last(t)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/rewards", path)
	assert.JSONEq(t, `{"broadcaster_id":"7","title":"Reward","is_user_input_required":false,"is_stock_enabled":false}`, body)
}

func TestUpdateReward(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"r1","title":"New","cost":100}]}`}
	runOps(t, f, func(c *helix.Client) {
		title := "New"
		reward, err := c.UpdateReward(context.Background(), "7", "r1", helix.RewardUpdate{Title: &title})
		require.NoError(t, err)
		require.NotNil(t, reward)
		assert.Equal(t, "New", reward.Title)
	})
	method, path, query, body := f.last(t)
	assert.Equal(t, http.MethodPatch, method)
	assert.Equal(t, "/rewards/r1", path)
	assert.Equal(t, "7", query.Get("broadcaster_id"))
	assert.JSONEq(t, `{"title":"New"}`, body)
}

func TestDeleteReward(t *testing.T) {
	f := &opsFake{status: http.StatusNoContent}
	runOps(t, f, func(c *helix.Client) {
		assert.NoError(t, c.DeleteReward(context.Background(), "7", "r1"))
	})
	method, path, query, _ := f.last(t)
	assert.Equal(t, http.MethodDelete, method)
	assert.Equal(t, "/rewards/r1", path)
	assert.Equal(t, "7", query.Get("broadcaster_id"))
}

func TestListRedemptions(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"d1","reward_id":"r1","status":"UNFULFILLED","user_id":"3","user_name":"user","redeemed_at":"2026-10-10T10:00:00Z"}]}`}
	runOps(t, f, func(c *helix.Client) {
		dones, err := c.ListRedemptions(context.Background(), "7", "r1", helix.RedemptionUnfulfilled)
		require.NoError(t, err)
		require.Len(t, dones, 1)
		assert.Equal(t, "d1", dones[0].ID)
	})
	_, path, query, _ := f.last(t)
	assert.Equal(t, "/redemptions", path)
	assert.Equal(t, "7", query.Get("broadcaster_id"))
	assert.Equal(t, "r1", query.Get("reward_id"))
	assert.Equal(t, helix.RedemptionUnfulfilled, query.Get("status"))
}

func TestUpdateRedemption(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"d1","status":"FULFILLED"}]}`}
	runOps(t, f, func(c *helix.Client) {
		done, err := c.UpdateRedemption(context.Background(), "7", "d1", helix.RedemptionFulfilled)
		require.NoError(t, err)
		require.NotNil(t, done)
		assert.Equal(t, helix.RedemptionFulfilled, done.Status)
	})
	method, path, _, body := f.last(t)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/redemptions", path)
	assert.JSONEq(t, `{"redemptions":[{"id":"d1","status":"FULFILLED"}]}`, body)
}

func TestGetEmotes(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"id":"e1","name":"Kappa","format":"png","tier":"0","image":"https://emotes/e1"}]}`}
	runOps(t, f, func(c *helix.Client) {
		emotes, err := c.GetEmotes(context.Background(), "7")
		require.NoError(t, err)
		require.Len(t, emotes, 1)
		assert.Equal(t, "Kappa", emotes[0].Name)
	})
	_, path, query, _ := f.last(t)
	assert.Equal(t, "/emotes", path)
	assert.Equal(t, "7", query.Get("user_id"))
}

func TestGetCheermotes(t *testing.T) {
	f := &opsFake{answer: `{"data":[{"emote_id":"ch1","color":"#ff0000","is_animated":true,"tiers":[{"tier":1,"minimum_amount":100}]}]}`}
	runOps(t, f, func(c *helix.Client) {
		cheers, err := c.GetCheermotes(context.Background(), "7")
		require.NoError(t, err)
		require.Len(t, cheers, 1)
		assert.Equal(t, int64(100), cheers[0].Tiers[0].MinimumAmount)
	})
	_, path, query, _ := f.last(t)
	assert.Equal(t, "/bits/cheermotes", path)
	assert.Equal(t, "7", query.Get("broadcaster_id"))
}
