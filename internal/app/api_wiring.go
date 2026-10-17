// SPDX-License-Identifier: MIT

package app

import (
	"context"

	"github.com/ripmav/streamcrew/internal/api"
	"github.com/ripmav/streamcrew/internal/connector"
)

// apiProfiles adapts the profile manager to the api.Profiles port.
type apiProfiles struct{ a *App }

// List returns the profiles of the core and the name of the active one.
func (p apiProfiles) List(ctx context.Context) ([]api.Profile, string, error) {
	profiles, err := p.a.profiles.List(ctx)
	if err != nil {
		return nil, "", err
	}
	out := make([]api.Profile, len(profiles))
	for i, prof := range profiles {
		out[i] = api.Profile{Name: prof.Name, Path: prof.Path}
	}
	return out, p.a.profile.Name, nil
}

// apiPlatforms adapts the platform set and the event service to the
// api.Platforms port.
type apiPlatforms struct{ a *App }

// Status reports the state of the connected platforms.
func (p apiPlatforms) Status(_ context.Context) []api.PlatformState {
	connected := p.a.platforms.Connected()
	out := make([]api.PlatformState, len(connected))
	for i, pl := range connected {
		st := api.PlatformState{
			Name:      string(pl.Name()),
			Connected: pl.Status().Connected(),
			Live:      p.a.events.IsLive(pl.Name()),
		}
		if id, ok := pl.(connector.Identities); ok {
			if ident, ok := id.Identity(connector.AccountStreamer); ok {
				st.Login = ident.Login
			}
		}
		out[i] = st
	}
	return out
}
