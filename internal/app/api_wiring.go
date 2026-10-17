// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"strings"

	"github.com/ripmav/streamcrew/internal/api"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/domain/counter"
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

// apiCounters adapts the store to the api.Counters port (roadmap 6.2.1, API).
type apiCounters struct{ a *App }

func (c apiCounters) toAPI(cc counter.Counter) api.Counter {
	return api.Counter{
		ID:           cc.ID.String(),
		Name:         cc.Name,
		Value:        cc.Value.String(),
		Step:         cc.Step.String(),
		ResetOnStart: cc.ResetOnStart,
		UpdatedAt:    cc.UpdatedAt,
	}
}

// List returns the counters, filtered by a case-insensitive name prefix.
func (c apiCounters) List(ctx context.Context, prefix string) ([]api.Counter, error) {
	all, err := c.a.store.Counters(ctx)
	if err != nil {
		return nil, err
	}
	prefix = strings.ToLower(prefix)
	out := make([]api.Counter, 0, len(all))
	for _, cc := range all {
		if prefix == "" || strings.HasPrefix(strings.ToLower(cc.Name), prefix) {
			out = append(out, c.toAPI(cc))
		}
	}
	return out, nil
}

// Get returns one counter by name.
func (c apiCounters) Get(ctx context.Context, name string) (api.Counter, error) {
	cc, err := c.a.store.Counter(ctx, name)
	if err != nil {
		return api.Counter{}, err
	}
	return c.toAPI(cc), nil
}

// Create creates a counter; empty value is 0, empty step the default.
func (c apiCounters) Create(ctx context.Context, name, value, step string, resetOnStart bool) (api.Counter, error) {
	cc := counter.New(name)
	if value != "" {
		v, err := decimal.Parse(value)
		if err != nil {
			return api.Counter{}, err
		}
		cc.Value = v
	}
	if step != "" {
		s, err := decimal.Parse(step)
		if err != nil {
			return api.Counter{}, err
		}
		cc.Step = s
	}
	cc.ResetOnStart = resetOnStart
	created, err := c.a.store.CreateCounter(ctx, cc)
	if err != nil {
		return api.Counter{}, err
	}
	return c.toAPI(created), nil
}

// Update changes the step (empty keeps it) and reset-on-start (nil keeps it).
func (c apiCounters) Update(ctx context.Context, name, step string, resetOnStart *bool) (api.Counter, error) {
	updated, err := c.a.store.UpdateCounter(ctx, name, func(cc *counter.Counter) error {
		if step != "" {
			s, err := decimal.Parse(step)
			if err != nil {
				return err
			}
			cc.Step = s
		}
		if resetOnStart != nil {
			cc.ResetOnStart = *resetOnStart
		}
		return cc.Validate()
	})
	if err != nil {
		return api.Counter{}, err
	}
	return c.toAPI(updated), nil
}

// Set sets the value of a counter.
func (c apiCounters) Set(ctx context.Context, name, value string) (api.Counter, error) {
	v, err := decimal.Parse(value)
	if err != nil {
		return api.Counter{}, err
	}
	updated, err := c.a.store.UpdateCounter(ctx, name, func(cc *counter.Counter) error {
		cc.Set(v)
		return nil
	})
	if err != nil {
		return api.Counter{}, err
	}
	return c.toAPI(updated), nil
}

// Add adds the amount to a counter; empty adds one step.
func (c apiCounters) Add(ctx context.Context, name, amount string) (api.Counter, error) {
	updated, err := c.a.store.UpdateCounter(ctx, name, func(cc *counter.Counter) error {
		if amount == "" {
			return cc.Increment()
		}
		d, err := decimal.Parse(amount)
		if err != nil {
			return err
		}
		return cc.Add(d)
	})
	if err != nil {
		return api.Counter{}, err
	}
	return c.toAPI(updated), nil
}

// Reset sets the value of a counter to 0.
func (c apiCounters) Reset(ctx context.Context, name string) (api.Counter, error) {
	updated, err := c.a.store.UpdateCounter(ctx, name, func(cc *counter.Counter) error {
		cc.Reset()
		return nil
	})
	if err != nil {
		return api.Counter{}, err
	}
	return c.toAPI(updated), nil
}

// Delete removes a counter.
func (c apiCounters) Delete(ctx context.Context, name string) error {
	return c.a.store.DeleteCounter(ctx, name)
}
