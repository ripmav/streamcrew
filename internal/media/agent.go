// SPDX-License-Identifier: MIT

package media

import (
	"context"
	"errors"

	"github.com/ripmav/streamcrew/internal/capability"
)

// ErrAgentUnavailable is the error of the agent sink in 7.3
// (ADR-0020 §1): the target choice and the capability check are real,
// the passthrough of the audio to the remote host follows with Phase
// 9.6.
var ErrAgentUnavailable = errors.New("media: the sink agent is not available in this phase; the audio passthrough follows with Phase 9.6 (ADR-0020)")

// AgentSink is the port of the agent sink: the audio plays on the remote
// host of the agent.
type AgentSink struct{}

// NewAgent returns the agent sink.
func NewAgent() *AgentSink { return &AgentSink{} }

// Name implements Sink.
func (s *AgentSink) Name() string { return SinkAgent }

// Required implements Sink: the passthrough runs over the agent
// protocol, the core needs no capability of its own (ADR-0020 §1).
func (s *AgentSink) Required() capability.Capability { return "" }

// Play implements Sink.
func (s *AgentSink) Play(_ context.Context, _ string, _ float64) (<-chan struct{}, error) {
	return nil, ErrAgentUnavailable
}
