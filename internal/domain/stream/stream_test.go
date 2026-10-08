// SPDX-License-Identifier: MIT

package stream_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/stream"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	now := time.Now()
	assert.NoError(t, stream.Initial(platform.Mock).Validate(), "B11: before the first start")
	assert.NoError(t, stream.Session{Platform: platform.Twitch, StartedAt: now, State: stream.StateLive, Since: now}.Validate())
	assert.NoError(t, stream.Session{Platform: platform.Twitch, StartedAt: now, State: stream.StateGrace, Since: now}.Validate())
	for _, s := range []stream.Session{
		{Platform: "", State: stream.StateOffline},
		{Platform: platform.Twitch, State: "paused"},
		{Platform: platform.Twitch, State: stream.StateLive, Since: now},
		{Platform: platform.Twitch, State: stream.StateGrace, StartedAt: now},
	} {
		assert.Error(t, s.Validate(), "%+v", s)
	}
}
