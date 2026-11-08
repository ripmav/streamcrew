// SPDX-License-Identifier: MIT

package tts

import (
	"context"
	"errors"
	"fmt"
)

// piper renders the speech with the external piper process (B41,
// ADR-0020 §3): the model is in tts-piper-model, the text goes on
// stdin, and the process writes a WAV to the generated directory.
func (s *Service) piper(ctx context.Context, sp Speech) (string, error) {
	model := s.ports.PiperModel
	if model == "" {
		return "", errors.New("tts: the provider piper needs a model (tts-piper-model)")
	}
	if s.ports.Piper == nil {
		return "", errors.New("tts: the provider piper needs a process runner (host:process)")
	}
	out, err := s.newFile(s.ports.GeneratedDir, ".wav")
	if err != nil {
		return "", err
	}
	rate := 1.0
	if sp.Rate > 0 {
		rate = sp.Rate
	}
	// shortcut: piper has no pitch flag; the pitch of the provider is
	// ignored. Upgrade: a piper build with pitch control, or a post
	// process.
	// No --volume: the sink applies the volume, the provider renders
	// with the gain of 1 (Review 345-5).
	args := []string{
		"--model", model,
		"--output_file", out,
		"--length-scale", fmt.Sprintf("%.4f", 1/rate),
	}
	if err := s.ports.Piper(ctx, args, sp.Text); err != nil {
		return "", fmt.Errorf("tts: piper: %w", err)
	}
	s.ports.Logger.InfoContext(ctx, "tts: audio generated", "provider", Piper, "file", out)
	return out, nil
}
