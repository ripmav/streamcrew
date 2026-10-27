// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	overlayaction "github.com/ripmav/streamcrew/internal/action/overlay"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/overlaywidgets"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/template"
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

// newOverlayWidgets builds the widget service of the P1 widgets
// (roadmap 7.2, spec overlays.md, B19 to B28): it runs the label, goal,
// timer, event_list and chat widgets and pushes their state to the
// overlay service.
func (a *App) newOverlayWidgets(logger *slog.Logger) (*overlaywidgets.Service, error) {
	return overlaywidgets.New(overlaywidgets.Ports{
		Overlay:      a.overlay.Service(),
		Bus:          a.bus,
		Section:      a.overlaySection,
		Feed:         a.store,
		State:        a.store,
		Templates:    a.templates,
		Scope:        a.widgetScope,
		StreamStates: streamStates{set: a.platforms},
		SessionStats: a.store,
		Locale:       a.widgetLocale,
		Logger:       logger,
	})
}

// widgetScope returns the template scope of the widget labels (B24): the
// platform of the home channel, the time zone and the locale of the
// profile, and the user lookup for the identifiers of the label text.
func (a *App) widgetScope(ctx context.Context) (*template.Scope, error) {
	c, err := settings.Load(ctx, a.settings, settings.DefaultCommands())
	if err != nil {
		return nil, fmt.Errorf("settings commands: %w", err)
	}
	t, err := settings.Load(ctx, a.settings, settings.DefaultTime())
	if err != nil {
		return nil, fmt.Errorf("settings time: %w", err)
	}
	loc, err := t.Location()
	if err != nil {
		// A stored zone this build cannot load: UTC instead of failing
		// the label (Code-ADR-0009).
		a.logger.WarnContext(ctx, "invalid profile time zone", "error", err)
		loc = time.UTC
	}
	l, err := settings.Load(ctx, a.settings, settings.DefaultLocale())
	if err != nil {
		return nil, fmt.Errorf("settings locale: %w", err)
	}
	u, err := settings.Load(ctx, a.settings, settings.DefaultUsers())
	if err != nil {
		return nil, fmt.Errorf("settings users: %w", err)
	}
	noTitle, err := a.messages.Render(l.Language, i18n.Message{Key: i18n.KeyNoTitle})
	if err != nil {
		return nil, fmt.Errorf("render no title: %w", err)
	}
	return &template.Scope{
		Finder:       a.userSvc,
		Platform:     platform.Twitch, // the home channel operates on Twitch (ADR-0004)
		ArgDelimiter: c.ArgDelimiter,
		Location:     loc,
		Locale:       a.formatLocale(ctx, l.Locale),
		TitleRules:   u.TitleRules,
		NoTitle:      noTitle,
	}, nil
}

// widgetLocale returns the locale of the profile, as the runtime formats
// the dates and times of the label kinds date and time (B24, A5).
func (a *App) widgetLocale(ctx context.Context) (template.Locale, error) {
	l, err := settings.Load(ctx, a.settings, settings.DefaultLocale())
	if err != nil {
		return template.LocaleUSEnglish, fmt.Errorf("settings locale: %w", err)
	}
	return a.formatLocale(ctx, l.Locale), nil
}

// overlayActionOps implements the ports of the overlay action
// (action/overlay): the overlay service and the widget service, resolved
// per call, so the action registry can be built before the overlay
// server and the widget service (roadmap 7.2).
type overlayActionOps struct {
	app *App
}

// ShowItem implements overlayaction.Service.
func (o overlayActionOps) ShowItem(ctx context.Context, endpoint string, item overlay.Item) (string, <-chan struct{}, error) {
	return o.app.overlay.Service().ShowItem(ctx, endpoint, item)
}

// SetVisible implements overlayaction.Service.
func (o overlayActionOps) SetVisible(ctx context.Context, widgetID string, visible bool) error {
	return o.app.overlay.Service().SetVisible(ctx, widgetID, visible)
}

// Widget implements overlayaction.Service.
func (o overlayActionOps) Widget(widgetID string) (settings.OverlayWidget, bool) {
	return o.app.overlay.Service().Widget(widgetID)
}

// UpdateWidget implements overlayaction.Service.
func (o overlayActionOps) UpdateWidget(ctx context.Context, widgetID string, state any) error {
	return o.app.overlay.Service().UpdateWidget(ctx, widgetID, state)
}

// AddToGoal implements overlayaction.Widgets.
func (o overlayActionOps) AddToGoal(ctx context.Context, widgetID string, amount int64, p platform.Name, u *user.User) error {
	return o.app.widgets.AddToGoal(ctx, widgetID, amount, p, u)
}

// UpdateTimer implements overlayaction.Widgets.
func (o overlayActionOps) UpdateTimer(ctx context.Context, widgetID string, d time.Duration, reset bool) error {
	return o.app.widgets.UpdateTimer(ctx, widgetID, d, reset)
}

// AddToTimer implements overlayaction.Widgets.
func (o overlayActionOps) AddToTimer(ctx context.Context, widgetID string, d time.Duration) error {
	return o.app.widgets.AddToTimer(ctx, widgetID, d)
}

// PauseTimer implements overlayaction.Widgets.
func (o overlayActionOps) PauseTimer(ctx context.Context, widgetID string) error {
	return o.app.widgets.PauseTimer(ctx, widgetID)
}

// AddToEventList implements overlayaction.Widgets.
func (o overlayActionOps) AddToEventList(ctx context.Context, widgetID, user, text string) error {
	return o.app.widgets.AddToEventList(ctx, widgetID, user, text)
}

// LatestClip implements overlayaction.Clips: the latest clip of the
// channel with the login, via Helix (B13, B16).
func (o overlayActionOps) LatestClip(ctx context.Context, login string) (overlayaction.Clip, error) {
	c, _, err := twitchOps(o).client(ctx)
	if err != nil {
		return overlayaction.Clip{}, err
	}
	users, err := c.GetUsers(ctx, nil, []string{login})
	if err != nil {
		return overlayaction.Clip{}, err
	}
	if len(users) == 0 {
		return overlayaction.Clip{}, fmt.Errorf("the user %q is unknown", login)
	}
	clip, err := c.LatestClip(ctx, users[0].ID)
	if err != nil {
		return overlayaction.Clip{}, err
	}
	if clip == nil {
		return overlayaction.Clip{}, fmt.Errorf("the channel %s has no clip", login)
	}
	seconds, _ := strconv.ParseFloat(clip.Duration, 64) // 0 if the API names no duration
	return overlayaction.Clip{URL: clip.URL, Duration: time.Duration(seconds * float64(time.Second))}, nil
}
