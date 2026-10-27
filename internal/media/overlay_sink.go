// SPDX-License-Identifier: MIT

package media

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/settings"
)

// mediaURLPrefix is the URL of the media directory under the overlay
// server (B6): /media/<root>/<path> with the root media.
const mediaURLPrefix = "/media/media/"

// Overlay is the overlay service the overlay sink plays on (B40,
// ADR-0020 §1); the overlay.Service of the core satisfies it.
type Overlay interface {
	// ShowItem shows the item on the endpoint and returns its ID and a
	// channel that is closed when the item leaves the active set (B11).
	ShowItem(ctx context.Context, endpoint string, item overlay.Item) (string, <-chan struct{}, error)
}

// OverlaySink plays the file as an audio item on the default endpoint of
// the overlay (ADR-0020 §1, Q21): the browser pane of the streaming PC
// plays it. The runtime reports the end of the playback with item.ended
// (B14), which closes the returned channel.
type OverlaySink struct {
	overlay  Overlay
	mediaDir string
}

// NewOverlay returns the overlay sink on the service with the media
// directory the files must live under.
func NewOverlay(o Overlay, mediaDir string) *OverlaySink {
	return &OverlaySink{overlay: o, mediaDir: mediaDir}
}

// Name implements Sink.
func (s *OverlaySink) Name() string { return SinkOverlay }

// Required implements Sink: the overlay sink plays on the browser pane,
// the core needs no capability of its own (B40).
func (s *OverlaySink) Required() capability.Capability { return "" }

// Play implements Sink. The file must be under the media directory, so
// the runtime can load it from the media route (B6, B47).
func (s *OverlaySink) Play(ctx context.Context, file string, volume float64) (<-chan struct{}, error) {
	rel, err := filepath.Rel(s.mediaDir, file)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return nil, errors.New("media: the overlay sink plays files under the media directory only")
	}
	// An audio item has no picture; the position is where it would show,
	// the runtime does not place it (B9, B13).
	_, end, err := s.overlay.ShowItem(ctx, settings.DefaultEndpointID, overlay.Item{
		Kind:     "audio",
		Position: settings.Position{Model: settings.PositionSimple, Point: "top-left"},
		Content: map[string]any{
			"src":    mediaURLPrefix + filepath.ToSlash(rel),
			"volume": volume,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("media: the overlay sink: %w", err)
	}
	return end, nil
}
