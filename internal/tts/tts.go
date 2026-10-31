// SPDX-License-Identifier: MIT

// Package tts speaks text with the providers of the core (roadmap 7.3,
// spec overlays.md, B41, ADR-0020 §3): google and azure are REST APIs
// whose keys come from the environment and never appear in logs, and
// piper is an external process that needs host:process. The browser
// provider speaks on the overlay runtime and generates no file; the
// core plays its text with the overlay service, not with this package.
// The generated files of the file providers are swept from the
// generated directory after 24 h (Review 345-10).
package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Name is the name of a provider (B41).
type Name string

// The providers of the core.
const (
	// Browser is the Web Speech API of the overlay runtime; it speaks
	// on the browser pane and needs no key and no capability (default).
	Browser Name = "browser"
	// Google is the Text-to-Speech REST API; the key is in
	// EnvGoogleKey.
	Google Name = "google"
	// Azure is the Speech REST API; the key and the region are in
	// EnvAzureKey and EnvAzureRegion.
	Azure Name = "azure"
	// Piper is the external piper process; the model is in
	// tts-piper-model, and it needs host:process.
	Piper Name = "piper"
)

// All returns the provider names, in the order editors show them.
func All() []Name { return []Name{Browser, Google, Azure, Piper} }

// Valid reports whether n is a known provider.
func (n Name) Valid() bool { return slices.Contains(All(), n) }

// IsFileProvider reports whether the provider generates a file the
// sinks play (google, azure, piper); the browser provider speaks on the
// overlay (B41, ADR-0020 §3).
func (n Name) IsFileProvider() bool {
	return n == Google || n == Azure || n == Piper
}

// generatedAge is the age at which the generated files are swept (Review
// 345-10): far beyond any playback, so the generated directory stays
// small.
const generatedAge = 24 * time.Hour

// The environment variables of the provider keys (ADR-0020 §2): they are
// read at the call, so a change of the environment applies without a
// restart, and they never appear in logs.
const (
	// EnvGoogleKey is the API key of the google provider.
	EnvGoogleKey = "STREAMCREW_TTS_GOOGLE_KEY"
	// EnvAzureKey is the API key of the azure provider.
	EnvAzureKey = "STREAMCREW_TTS_AZURE_KEY"
	// EnvAzureRegion is the region of the azure provider.
	EnvAzureRegion = "STREAMCREW_TTS_AZURE_REGION"
)

// requestTimeout is the time a provider request has, with reading the
// answer (longer than a web request: a long text takes a while to
// synthesize).
const requestTimeout = 30 * time.Second

// Speech is what to speak (B41).
type Speech struct {
	// Text is the text to speak.
	Text string
	// Voice is the name of the voice, provider specific; empty for the
	// default voice.
	Voice string
	// Volume is the volume from 0 to 100.
	Volume float64
	// Rate is the speaking rate; 1 for normal.
	Rate float64
	// Pitch is the pitch; 1 for normal.
	Pitch float64
}

// Dialer connects to the endpoints of the providers (ADR-0013); the
// netguard.Dialer of the core satisfies it.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// PiperRun runs the piper process with args, feeding it text on stdin
// (B41); it returns the error of the process, with its stderr.
type PiperRun func(ctx context.Context, args []string, text string) error

// Ports are the dependencies of a Service.
type Ports struct {
	// GeneratedDir is the directory the generated audio files are
	// written to, under the media directory of the profile (ADR-0020
	// §3); the overlay sink plays them from the media route.
	GeneratedDir string
	// PiperModel is the onnx model of the piper provider
	// (tts-piper-model); empty if the provider is not configured.
	PiperModel string
	// Env reads the environment of the provider keys; os.Getenv in the
	// production.
	Env func(string) string
	// Dialer connects to the REST endpoints of google and azure; nil if
	// the providers are not configured.
	Dialer Dialer
	// GoogleEndpoint is the synthesize endpoint of the google provider;
	// empty for the production endpoint.
	GoogleEndpoint string
	// AzureBase returns the endpoint of the azure provider for the
	// region; nil for the production endpoint.
	AzureBase func(region string) string
	// Piper runs the piper process; nil if the provider is not
	// configured.
	Piper PiperRun
	// Now returns the clock for the age check of the generated files.
	Now func() time.Time
	// Logger records the synthesis and its results.
	Logger *slog.Logger
}

// Service speaks the text with the file providers (B41): google, azure
// and piper render the speech to a file under the generated directory.
type Service struct {
	ports Ports
	// client is the one HTTP client of the service; the requests share
	// its transport, so the keep-alive connections are reused (Review
	// 345-8).
	client *http.Client
}

// New returns a service of the providers; the keys and the endpoints
// are checked at the call, so a provider becomes available when its key
// lands in the environment.
func New(p Ports) (*Service, error) {
	if p.GeneratedDir == "" {
		return nil, errors.New("tts: no generated directory")
	}
	if p.Env == nil {
		//nolint:forbidigo // the provider keys are read from the environment at the call (ADR-0020 §2), not core configuration
		p.Env = os.Getenv
	}
	if p.Now == nil {
		p.Now = time.Now
	}
	if p.Logger == nil {
		p.Logger = slog.New(slog.DiscardHandler)
	}
	if p.GoogleEndpoint == "" {
		p.GoogleEndpoint = googleURL
	}
	if p.AzureBase == nil {
		p.AzureBase = azureBase
	}
	client := &http.Client{Timeout: requestTimeout}
	if p.Dialer != nil {
		// Through the dialer of the core, so the server mode with its
		// outbound allowlist applies (ADR-0013, point 4).
		client.Transport = &http.Transport{
			DialContext:         p.Dialer.DialContext,
			TLSHandshakeTimeout: requestTimeout,
		}
	}
	return &Service{ports: p, client: client}, nil
}

// Generate renders the speech of the provider to a file under the
// generated directory and returns its path (B41, ADR-0020 §3). The
// provider browser generates no file; it speaks on the overlay.
func (s *Service) Generate(ctx context.Context, n Name, sp Speech) (string, error) {
	switch n {
	case Google:
		return s.google(ctx, sp)
	case Azure:
		return s.azure(ctx, sp)
	case Piper:
		return s.piper(ctx, sp)
	case Browser:
		return "", errors.New("tts: the provider browser speaks on the overlay, it generates no file")
	default:
		return "", fmt.Errorf("tts: unknown provider %q", n)
	}
}

// newFile returns the name of the next generated file in dir with the
// extension ext: the directory is created if needed, the old generated
// files are swept, and the name comes from os.CreateTemp, so two calls
// in the same nanosecond cannot collide (Review 345-10).
func (s *Service) newFile(dir, ext string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("tts: the generated directory: %w", err)
	}
	s.sweep(dir)
	f, err := os.CreateTemp(dir, "tts-*"+ext)
	if err != nil {
		return "", fmt.Errorf("tts: the generated file: %w", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("tts: the generated file: %w", err)
	}
	return path, nil
}

// sweep removes the old generated files of the service from dir; the
// age is measured against the ports clock, and only the own files
// (prefix tts-) are touched (Review 345-10).
func (s *Service) sweep(dir string) {
	cutoff := s.ports.Now().Add(-generatedAge)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "tts-") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// write stores the synthesized audio under the generated directory and
// returns its path; the keys of the provider never reach the log
// (ADR-0020 §2).
func (s *Service) write(ctx context.Context, provider Name, raw []byte, ext string) (string, error) {
	path, err := s.newFile(s.ports.GeneratedDir, ext)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("tts: the generated file: %w", err)
	}
	s.ports.Logger.InfoContext(ctx, "tts: audio generated", "provider", provider, "file", path, "bytes", len(raw))
	return path, nil
}

// readTruncated reads at most n bytes from r, for the error messages.
func readTruncated(r io.Reader, n int) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, int64(n)))
}
