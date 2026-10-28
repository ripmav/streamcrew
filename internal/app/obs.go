// SPDX-License-Identifier: MIT

package app

import (
	"context"

	streamingaction "github.com/ripmav/streamcrew/internal/action/streaming"
	"github.com/ripmav/streamcrew/internal/obs"
)

// obsActionOps adapts the OBS connection of the core to the OBS port of
// the streaming_software action (roadmap 7.4, B42): the other operations
// are the methods of the obs service, and the change of set_source of
// the action is the Source of the obs service.
type obsActionOps struct {
	*obs.Service
}

var _ streamingaction.OBS = obsActionOps{}

// SetSource implements the OBS port of the streaming_software action.
func (o obsActionOps) SetSource(ctx context.Context, ch streamingaction.SourceChange) error {
	return o.Service.SetSource(ctx, obs.Source{
		Source:  ch.Source,
		Visible: ch.Visible,
		X:       ch.X,
		Y:       ch.Y,
	})
}
