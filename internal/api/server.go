// SPDX-License-Identifier: MIT

// Package api exposes the services of the core as a ConnectRPC server
// (ADR-0010, plan §6.14). It is a thin layer over the application services:
// the handlers translate protocol messages to the services and back; they
// hold no business logic. It is mounted on the core's HTTP server as a
// catch-all, so the health endpoints stay on their own routes.
package api

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
)

// Profile is one profile of the core.
type Profile struct {
	// Name is the profile name.
	Name string
	// Path is the profile directory.
	Path string
}

// PlatformState is the state of one platform for SystemService.Status.
type PlatformState struct {
	// Name is the platform, e.g. "twitch".
	Name string
	// Connected is true while the platform's streamer account is connected.
	Connected bool
	// Login is the login of the home channel; empty if unknown.
	Login string
	// Live is true while the stream is live.
	Live bool
}

// Profiles lists the profiles of the core and names the active one.
type Profiles interface {
	List(ctx context.Context) ([]Profile, string, error)
}

// Platforms reports the state of the connected platforms.
type Platforms interface {
	Status(ctx context.Context) []PlatformState
}

// ServerConfig configures a Server.
type ServerConfig struct {
	// Version is the core version.
	Version string
	// Mode is the operating mode: "desktop", "daemon" or "server".
	Mode string
	// Profile is the name of the active profile.
	Profile string
	// StartedAt is when the core started.
	StartedAt time.Time
	// Profiles lists the profiles; nil if unavailable.
	Profiles Profiles
	// Platforms reports the connected platforms; nil if none.
	Platforms Platforms
	// Counters manages the profile counters; nil to skip the service.
	Counters Counters
	// Settings manages the profile settings sections; nil to skip the service.
	Settings Settings
	// Users manages the profile users; nil to skip the service.
	Users Users
	// Chat sends and manages chat messages; nil to skip the service.
	Chat Chat
	// Backups creates, lists and restores profile backups; nil to skip.
	Backups Backup
	// Config manages the configuration of the core; nil to skip the
	// service (roadmap 6.5.2).
	Config Config
	// Auth manages the platform logins; nil to skip the service.
	Auth Auth
	// Commands manages the profile commands; nil to skip the service.
	Commands Commands
	// Stream delivers the live events and answers prompts; nil to skip.
	Stream Stream
	// Tokens protects the API with access tokens (roadmap 6.2.2); nil for
	// an open API.
	Tokens Tokens
	// Shutdown stops the core; nil if the API must not stop it.
	Shutdown func()
}

// Server is the API: it exposes the services of the core as ConnectRPC
// handlers.
type Server struct {
	cfg ServerConfig
}

// New returns the API.
func New(cfg ServerConfig) *Server {
	return &Server{cfg: cfg}
}

// Handler returns the HTTP handler with the routes of all the services.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	server := connect.NewServer()
	streamcrewv1alpha1connect.RegisterSystemServiceHandler(server, &systemService{s: s})
	if c := s.cfg.Counters; c != nil {
		streamcrewv1alpha1connect.RegisterCounterServiceHandler(server, &counterService{c: c})
	}
	if st := s.cfg.Settings; st != nil {
		streamcrewv1alpha1connect.RegisterSettingsServiceHandler(server, &settingsService{s: st})
	}
	if u := s.cfg.Users; u != nil {
		streamcrewv1alpha1connect.RegisterUserServiceHandler(server, &userService{u: u})
	}
	if c := s.cfg.Chat; c != nil {
		streamcrewv1alpha1connect.RegisterChatServiceHandler(server, &chatService{c: c})
	}
	if b := s.cfg.Backups; b != nil {
		streamcrewv1alpha1connect.RegisterBackupServiceHandler(server, &backupService{b: b})
	}
	if c := s.cfg.Config; c != nil {
		streamcrewv1alpha1connect.RegisterConfigServiceHandler(server, &configService{c: c})
	}
	if au := s.cfg.Auth; au != nil {
		streamcrewv1alpha1connect.RegisterAuthServiceHandler(server, &authService{a: au})
	}
	if cm := s.cfg.Commands; cm != nil {
		streamcrewv1alpha1connect.RegisterCommandServiceHandler(server, &commandService{c: cm})
	}
	if st := s.cfg.Stream; st != nil {
		streamcrewv1alpha1connect.RegisterStreamServiceHandler(server, &streamService{s: st})
	}
	// The remaining services (Agent) are added as their application
	// surface is wired (roadmap 6.2.1, 6.1.5).
	// The v1 server accepted messages of any size; keep that with
	// WithReadMaxBytes(0) instead of the v2 default of 4 MiB.
	connecthttp.Mount(mux, server, connecthttp.WithReadMaxBytes(0))
	if t := s.cfg.Tokens; t != nil {
		return tokenAuth(t, mux)
	}
	return mux
}
