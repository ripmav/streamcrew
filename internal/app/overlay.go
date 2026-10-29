// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"log/slog"

	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/settings"
)

// overlaySection loads the overlay section of the profile: the
// endpoints and the widget definitions (roadmap 7.1, spec overlays.md,
// B2, B19).
func (a *App) overlaySection(ctx context.Context) (settings.Overlay, error) {
	return settings.Load(ctx, a.settings, settings.DefaultOverlay())
}

// newOverlayServer builds the overlay service and its dedicated server
// (roadmap 7.1, ADR-0019): the service keeps the endpoints, the active
// items and the widget state; the server serves the embedded runtime
// and the released media. The server runs under the supervisor without
// the critical flag, so a failure does not stop the core.
func newOverlayServer(ctx context.Context, cfg config.Config, a *App, mediaDir string, logger *slog.Logger) (*overlay.Server, error) {
	svc, err := overlay.New(ctx, overlay.Ports{
		Section: a.overlaySection,
		Version: a.version,
		Logger:  logger,
	})
	if err != nil {
		return nil, err
	}
	return overlay.NewServer(overlay.Config{
		Listen:       cfg.OverlayListen,
		Token:        cfg.OverlayToken,
		Version:      a.version,
		Service:      svc,
		MediaDir:     mediaDir,
		Capabilities: a.rights,
		Root:         a.rights.Root,
		Logger:       logger,
	})
}
