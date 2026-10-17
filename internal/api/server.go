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

// Config configures a Server.
type Config struct {
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
	// Shutdown stops the core; nil if the API must not stop it.
	Shutdown func()
}

// Server is the API: it exposes the services of the core as ConnectRPC
// handlers.
type Server struct {
	cfg Config
}

// New returns the API.
func New(cfg Config) *Server {
	return &Server{cfg: cfg}
}

// Handler returns the HTTP handler with the routes of all the services.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(streamcrewv1alpha1connect.NewSystemServiceHandler(&systemService{s: s}))
	if c := s.cfg.Counters; c != nil {
		mux.Handle(streamcrewv1alpha1connect.NewCounterServiceHandler(&counterService{c: c}))
	}
	if st := s.cfg.Settings; st != nil {
		mux.Handle(streamcrewv1alpha1connect.NewSettingsServiceHandler(&settingsService{s: st}))
	}
	if u := s.cfg.Users; u != nil {
		mux.Handle(streamcrewv1alpha1connect.NewUserServiceHandler(&userService{u: u}))
	}
	// The remaining services (Auth, Stream, Chat, Command, Backup, Agent)
	// are added as their application surface is wired (roadmap 6.2.1).
	return mux
}
