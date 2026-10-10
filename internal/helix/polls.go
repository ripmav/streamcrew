// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
	"time"
)

// The statuses of a poll (Helix /polls).
const (
	PollVoting     = "VOTING"
	PollCompleted  = "COMPLETED"
	PollCanceled   = "CANCELED"
	PollTerminated = "TERMINATED"
)

// PollOutcome is an outcome of a poll with its votes.
type PollOutcome struct {
	// ID is the outcome ID.
	ID string `json:"id"`
	// Choice is the outcome text.
	Choice string `json:"choice"`
	// Votes is the number of votes for the outcome.
	Votes int64 `json:"votes"`
}

// Poll is a channel poll (Helix GET/POST/DELETE /polls).
type Poll struct {
	// ID is the poll ID.
	ID string `json:"id"`
	// Title is the poll title.
	Title string `json:"title"`
	// Status is one of PollVoting, PollCompleted, PollCanceled and
	// PollTerminated.
	Status string `json:"status"`
	// Outcomes are the poll outcomes with their votes.
	Outcomes []PollOutcome `json:"outcomes"`
	// ChatMessage is the message sent to the chat after the poll ends.
	ChatMessage string `json:"chat_message,omitempty"`
	// StartedAt is the start of the poll; nil if the poll has not
	// started.
	StartedAt *time.Time `json:"started_at"`
	// EndedAt is the end of the poll; nil while it runs.
	EndedAt *time.Time `json:"ended_at"`
}

// PollInput is the body of CreatePoll (Helix POST /polls). The poll
// takes two to four choices and thirty to thirty-six hundred seconds.
type PollInput struct {
	// BroadcasterID is the channel the poll belongs to.
	BroadcasterID string `json:"broadcaster_id"`
	// Title is the poll title.
	Title string `json:"title"`
	// Duration is the poll length, in seconds.
	Duration int `json:"duration"`
	// Choices are the poll outcomes, two to four.
	Choices []string `json:"choices"`
	// BitReward is the bits reward for voting; nil for none.
	BitReward *int `json:"bit_reward,omitempty"`
	// ChatMessage is the message sent to the chat after the poll ends.
	ChatMessage string `json:"chat_message,omitempty"`
}

// GetPoll returns the poll with the ID pollID (Helix GET /polls).
func (c *Client) GetPoll(ctx context.Context, broadcasterID, pollID string) (*Poll, error) {
	q := url.Values{"id": {pollID}}
	if broadcasterID != "" {
		q.Set("broadcaster_id", broadcasterID)
	}
	req, err := c.request(ctx, "GET", "/polls", q, nil)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []Poll `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	return &envelope.Data[0], nil
}

// CreatePoll creates a poll on the channel (Helix POST /polls, scope
// moderator:manage:polls).
func (c *Client) CreatePoll(ctx context.Context, in PollInput) (*Poll, error) {
	req, err := c.request(ctx, "POST", "/polls", nil, in)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []Poll `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	return &envelope.Data[0], nil
}

// EndPoll ends the poll with the ID pollID (Helix DELETE /polls, scope
// moderator:manage:polls), answers 204.
func (c *Client) EndPoll(ctx context.Context, pollID string) error {
	q := url.Values{"id": {pollID}}
	req, err := c.request(ctx, "DELETE", "/polls", q, nil)
	if err != nil {
		return err
	}
	return c.doAnswer(ctx, req)
}
