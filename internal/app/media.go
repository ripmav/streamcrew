// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	f, err := root.Open(file)
	if err != nil {
		return "", fmt.Errorf("the file %q is not under the media directory: %w", file, err)
	}
	info, err := f.Stat()
	if err == nil {
		_ = f.Close()
	}
	if err != nil {
		return "", fmt.Errorf("the file %q is not readable: %w", file, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("the file %q is a directory, not a file", file)
	}
	// root.Open accepted file, so it resolves under the media directory.
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
// providers come from the environment at the call (ADR-0020 §2).
func (a *App) newTTS(cfg config.Config, logger *slog.Logger) (*ttspkg.Service, error) {
	return ttspkg.New(ttspkg.Ports{
		GeneratedDir: filepath.Join(a.mediaDir, "generated"),
		PiperModel:   cfg.TTSPiperModel,
		//nolint:forbidigo // the provider keys are read from the environment at the call (ADR-0020 §2), not core configuration
		Env: os.Getenv,
		Dialer: netguard.Dialer{
			Protect:   cfg.Mode == config.ModeServer,
			Allowlist: a.rights.Outbound,
		},
		Piper:  piperRun,
		Logger: component(logger, "tts"),
	})
}
