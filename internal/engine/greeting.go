// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"slices"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/id"
)

// mediaGate lets the pictures and sounds of greetings play one after the
// other, with a gap after each playback (B43). Its fields are guarded by
// Engine.mu.
type mediaGate struct {
	// holder is the greeting whose action has the gate; zero while it is
	// free.
	holder id.ID
	// free is when the next action may have the gate: the end of the last
	// playback plus the gap.
	free time.Time
	// queue are the actions that wait for the gate, in the order they began
	// to wait.
	queue []*mediaTurn
	// timer gives the gate to the first in the queue at free; nil while
	// none is set.
	timer *time.Timer
}

// mediaTurn is an action of a greeting that waits for the gate.
type mediaTurn struct {
	greeting id.ID
	// ready is closed when the action has the gate.
	ready chan struct{}
}

// PlaybackEnds tells the engine when the playback that the running action
// started ends, e.g. of a sound (B43). For the pictures and sounds of
// greetings, the gap to the next one counts from then; without it, from the
// end of the action. The latest time counts.
func (r *Run) PlaybackEnds(t time.Time) {
	if t.After(r.playbackEnd) {
		r.playbackEnd = t
	}
}

// awaitMedia waits until action a of run may run (B43). An action that
// shows a picture or plays a sound in a greeting waits for the gate; other
// actions, and those that run within the action that has the gate, go on
// at once. The time limit of the action does not run meanwhile (B72),
// because it starts with perform. awaitMedia returns the function to call
// after the action, or the error of ctx if it ended while the action waited
// for the gate.
func (e *Engine) awaitMedia(ctx context.Context, run *Run, a Performer) (done func(), err error) {
	greeting := run.in.greeting
	if greeting.IsZero() || !e.types.VisualAudio(a.DocType()) {
		return func() {}, nil
	}
	e.mu.Lock()
	if e.media.holder == greeting {
		e.mu.Unlock()
		return func() {}, nil
	}
	turn := &mediaTurn{greeting: greeting, ready: make(chan struct{})}
	e.media.queue = append(e.media.queue, turn)
	e.grantMediaLocked()
	e.mu.Unlock()

	select {
	case <-turn.ready:
	case <-ctx.Done():
		e.mu.Lock()
		defer e.mu.Unlock()
		select {
		case <-turn.ready:
			// The gate came at the same time; nothing played, so the gap
			// stays as it was.
			e.media.holder = id.ID{}
			e.grantMediaLocked()
		default:
			e.media.queue = slices.DeleteFunc(e.media.queue, func(t *mediaTurn) bool { return t == turn })
		}
		return nil, ctx.Err()
	}
	run.playbackEnd = time.Time{}
	return func() { e.releaseMedia(run.in.mediaGap, run.playbackEnd) }, nil
}

// releaseMedia gives the gate back after an action whose playback ends at
// playbackEnd, or now if that is earlier; the next action may start gap
// later.
func (e *Engine) releaseMedia(gap time.Duration, playbackEnd time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	end := time.Now()
	if playbackEnd.After(end) {
		end = playbackEnd
	}
	e.media.holder = id.ID{}
	e.media.free = end.Add(gap)
	e.grantMediaLocked()
}

// grantMediaLocked gives the gate to the first waiting action once it is
// free. e.mu is held.
func (e *Engine) grantMediaLocked() {
	g := &e.media
	if !g.holder.IsZero() || len(g.queue) == 0 {
		return
	}
	if wait := time.Until(g.free); wait > 0 {
		if g.timer == nil {
			g.timer = time.AfterFunc(wait, func() {
				e.mu.Lock()
				defer e.mu.Unlock()
				g.timer = nil
				e.grantMediaLocked()
			})
		}
		return
	}
	turn := g.queue[0]
	g.queue[0] = nil
	g.queue = g.queue[1:]
	g.holder = turn.greeting
	close(turn.ready)
}

// CancelEntrance cancels the greetings that have not ended (B41), e.g. when
// the stream ends: queued ones, those whose picture or sound waits for its
// turn (B43), and running ones.
func (e *Engine) CancelEntrance(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	canceled := 0
	for _, in := range e.active {
		if in.greeting.IsZero() {
			continue
		}
		e.cancelLocked(ctx, in)
		canceled++
	}
	e.scheduleLocked(ctx)
	if canceled > 0 {
		e.logger.InfoContext(ctx, "greetings canceled", "count", canceled)
	}
}
