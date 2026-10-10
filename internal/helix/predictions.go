// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
	"time"
)

// The outcomes of a prediction (Helix /predictions): 1 is outcome A,
// 2 is outcome B.
const (
	PredictionOutcomeA = 1
	PredictionOutcomeB = 2
)

// The statuses of a prediction.
const (
	PredictionLocked   = "LOCKED"
	PredictionResolved = "RESOLVED"
	PredictionCanceled = "CANCELED"
)

// Prediction is a channel prediction (Helix GET/POST/DELETE
// /predictions).
type Prediction struct {
	// ID is the prediction ID.
	ID string `json:"id"`
	// OutcomeA and OutcomeB are the prediction outcomes.
	OutcomeA string `json:"outcome_a"`
	OutcomeB string `json:"outcome_b"`
	// Status is one of PredictionLocked, PredictionResolved and
	// PredictionCanceled.
	Status string `json:"status"`
	// Outcome is the winning outcome (PredictionOutcomeA or
	// PredictionOutcomeB), zero while the prediction runs.
	Outcome int `json:"outcome"`
	// TotalBets is the number of bets; TotalCoins the total channel
	// points wagered.
	TotalBets  int64 `json:"total_bets"`
	TotalCoins int64 `json:"total_coins"`
	// StartedAt is the start of the prediction; EndedAt its end, nil
	// while it runs.
	StartedAt *time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
}

// PredictionInput is the body of CreatePrediction (Helix
// POST /predictions).
type PredictionInput struct {
	// BroadcasterID is the channel the prediction belongs to.
	BroadcasterID string `json:"broadcaster_id"`
	// PredictionA and PredictionB are the prediction outcomes.
	PredictionA string `json:"prediction_a"`
	PredictionB string `json:"prediction_b"`
	// Duration is the betting time, in seconds.
	Duration int `json:"duration"`
}

// EndPredictionInput is the body of EndPrediction (Helix
// DELETE /predictions).
type EndPredictionInput struct {
	// PredictionID is the prediction to end.
	PredictionID string `json:"prediction_id"`
	// WinningOutcome is the winning outcome (PredictionOutcomeA or
	// PredictionOutcomeB), zero when the prediction is canceled.
	WinningOutcome int `json:"winning_outcome"`
	// CancelPrediction ends the prediction as canceled.
	CancelPrediction bool `json:"cancel_prediction"`
}

// GetPrediction returns the prediction with the ID predictionID (Helix
// GET /predictions).
func (c *Client) GetPrediction(ctx context.Context, broadcasterID, predictionID string) (*Prediction, error) {
	q := url.Values{"id": {predictionID}}
	if broadcasterID != "" {
		q.Set("broadcaster_id", broadcasterID)
	}
	req, err := c.request(ctx, "GET", "/predictions", q, nil)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []Prediction `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	return &envelope.Data[0], nil
}

// CreatePrediction creates a prediction on the channel (Helix
// POST /predictions, scope moderator:manage:predictions).
func (c *Client) CreatePrediction(ctx context.Context, in PredictionInput) (*Prediction, error) {
	req, err := c.request(ctx, "POST", "/predictions", nil, in)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []Prediction `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	return &envelope.Data[0], nil
}

// EndPrediction resolves or cancels the prediction (Helix
// DELETE /predictions, scope moderator:manage:predictions), answers 204.
func (c *Client) EndPrediction(ctx context.Context, in EndPredictionInput) error {
	req, err := c.request(ctx, "DELETE", "/predictions", nil, in)
	if err != nil {
		return err
	}
	return c.doAnswer(ctx, req)
}
