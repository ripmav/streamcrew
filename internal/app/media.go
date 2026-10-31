// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/media"
	"github.com/ripmav/streamcrew/internal/netguard"
	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/settings"
	ttspkg "github.com/ripmav/streamcrew/internal/tts"
)

// mediaFiles resolves the files of the sound and text_to_speech actions
// under the media directory of the profile (B40, B47): a path that
// escapes the directory is a clear error, so the action rejects the
// file without showing it.
type mediaFiles struct{ a *App }

// File implements the media port of the media actions.
func (m mediaFiles) File(file string) (string, error) {
	root, err := os.OpenRoot(m.a.mediaDir)
	if err != nil {
		return "", fmt.Errorf("the media directory is unavailable: %w", err)
	}
	defer root.Close()
	// root.Stat resolves file inside the root (escaping paths and
	// symlinks out of it are errors) without opening a handle (Review
	// 345-9), and the sinks open the resolved path themselves.
	info, err := root.Stat(file)
	if err != nil {
		return "", fmt.Errorf("the file %q is not under the media directory or not readable: %w", file, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("the file %q is a directory, not a file", file)
	}
	// root.Stat accepted file, so it resolves under the media directory.
	return filepath.Join(m.a.mediaDir, filepath.Clean(file)), nil
}

// ttsBrowser speaks the text of the provider browser on the default
// endpoint of the overlay (B41, ADR-0020 §3).
type ttsBrowser struct{ a *App }

// Speak implements the browser port of the text_to_speech action.
func (b ttsBrowser) Speak(ctx context.Context, s ttspkg.Speech) (<-chan struct{}, error) {
	// The runtime takes the volume from 0 to 1 (protocol), the action
	// carries it from 0 to 100 (B40).
	return b.a.overlay.Service().TTSSpeak(ctx, settings.DefaultEndpointID, overlay.TTSSpeakData{
		Text:   s.Text,
		Voice:  s.Voice,
		Volume: s.Volume / 100,
		Rate:   s.Rate,
		Pitch:  s.Pitch,
	})
}

// mediaSinks resolves the media service of the core at the call
// (Review 345-1): the sound and text_to_speech actions are wired before
// the service is built, and a direct value would hold a nil *media.Service
// in the port interface, which panics on the call instead of an error.
type mediaSinks struct{ a *App }

// Play implements the sinks port of the media actions.
func (m mediaSinks) Play(ctx context.Context, name, file string, volume float64, rights capability.Source) (<-chan struct{}, error) {
	if m.a.media == nil {
		return nil, errors.New("media: the media service is not built")
	}
	return m.a.media.Play(ctx, name, file, volume, rights)
}

// ttsService resolves the tts service of the core at the call
// (Review 345-1); see mediaSinks.
type ttsService struct{ a *App }

// Generate implements the tts port of the text_to_speech action.
func (t ttsService) Generate(ctx context.Context, provider ttspkg.Name, s ttspkg.Speech) (string, error) {
	if t.a.tts == nil {
		return "", errors.New("tts: the tts service is not built")
	}
	return t.a.tts.Generate(ctx, provider, s)
}

// piperRun runs the piper process of the tts package (B41): the text
// goes on stdin, and the stderr of the process comes back with the
// error.
func piperRun(ctx context.Context, args []string, text string) error {
	cmd := exec.CommandContext(ctx, "piper", args...)
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// newMedia builds the media service of the core (roadmap 7.3,
// ADR-0020 §1): the sinks overlay (default; the browser pane plays the
// audio item), local (build tag localaudio, capability host:audio) and
// agent (stub until Phase 9.6). The capability checks stay at the
// actions with the rights of the core now (ADR-0020 §2).
func (a *App) newMedia(logger *slog.Logger) *media.Service {
	return media.New(
		media.NewOverlay(a.overlay.Service(), a.mediaDir),
		media.NewLocal(component(logger, "localaudio")),
		media.NewAgent(),
	)
}

// newTTS builds the tts service of the core (roadmap 7.3, ADR-0020
// §3): the file providers google, azure and piper render the speech
// under the generated directory of the media directory, and the browser
// provider speaks directly on the overlay (ttsBrowser). The keys of the
// providers come from the core configuration (ADR-0019 §6) and are hot
// (Code-ADR-0021, Nachtrag 2026-10-29).
func (a *App) newTTS(cfg config.Config, logger *slog.Logger) (*ttspkg.Service, error) {
	svc, err := ttspkg.New(ttspkg.Ports{
		GeneratedDir: filepath.Join(a.mediaDir, "generated"),
		PiperModel:   cfg.TTSPiperModel,
		Dialer: netguard.Dialer{
			Protect:   cfg.Mode == config.ModeServer,
			Allowlist: a.rights.Outbound,
		},
		Piper:  piperRun,
		Logger: component(logger, "tts"),
	})
	if err != nil {
		return nil, err
	}
	svc.SetKeys(ttspkg.Keys{
		Google:      cfg.TTSGoogleKey,
		Azure:       cfg.TTSAzureKey,
		AzureRegion: cfg.TTSAzureRegion,
	})
	return svc, nil
}
