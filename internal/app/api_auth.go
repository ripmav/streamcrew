// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ripmav/streamcrew/internal/api"
	"github.com/ripmav/streamcrew/internal/auth"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
)

// apiAuth is the api.Auth port over the auth service. It starts logins and
// follows them in the background (WaitLogin) until the user completes them;
// the active logins are tracked so CancelLogin can stop one.
type apiAuth struct {
	a *App

	mu     sync.Mutex
	active map[string]context.CancelFunc
}

var _ api.Auth = (*apiAuth)(nil)

func newAPIAuth(app *App) *apiAuth {
	return &apiAuth{a: app, active: make(map[string]context.CancelFunc)}
}

func (x *apiAuth) ListAccounts(ctx context.Context) ([]api.Account, error) {
	st, err := x.a.auth.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	out := make([]api.Account, 0, len(st))
	for _, s := range st {
		out = append(out, api.Account{
			Platform:    string(s.Platform),
			Login:       s.Login,
			DisplayName: s.Login,
			Bot:         s.Role == connector.AccountBot,
			Scopes:      s.Scopes,
		})
	}
	return out, nil
}

//nolint:contextcheck // the login follows the core's run context, not the request context
func (x *apiAuth) StartLogin(ctx context.Context, platformName string, bot, deviceFlow bool) (api.LoginPrompt, error) {
	p := platform.Name(platformName)
	r := connector.AccountStreamer
	if bot {
		r = connector.AccountBot
	}
	c := auth.Credentials{DeviceFlow: deviceFlow}
	key := authKey(platformName, bot)

	x.mu.Lock()
	if _, ok := x.active[key]; ok {
		x.mu.Unlock()
		return api.LoginPrompt{}, fmt.Errorf("%w: %s", api.ErrLoginInProgress, key)
	}
	lctx, cancel, err := x.loginContext()
	if err != nil {
		x.mu.Unlock()
		return api.LoginPrompt{}, err
	}
	x.active[key] = cancel
	x.mu.Unlock()

	l, prompt, err := x.a.auth.StartLogin(ctx, p, r, c)
	if err != nil {
		x.dropActive(key)
		return api.LoginPrompt{}, fmt.Errorf("start login %s: %w", key, err)
	}
	go func() {
		defer x.dropActive(key)
		if _, err := x.a.auth.WaitLogin(lctx, l, p, r, c); err != nil {
			// The login failed or was canceled; WaitLogin already published
			// auth.login_failed with the reason.
			return
		}
	}()
	return api.LoginPrompt{Flow: flowLabel(c), Code: prompt.Code, URL: prompt.URL, ExpiresAt: prompt.Expiry}, nil
}

func (x *apiAuth) CancelLogin(_ context.Context, platformName string, bot bool) error {
	key := authKey(platformName, bot)
	x.mu.Lock()
	cancel, ok := x.active[key]
	if ok {
		delete(x.active, key)
	}
	x.mu.Unlock()
	if ok {
		cancel()
	}
	return nil
}

func (x *apiAuth) Logout(ctx context.Context, platformName string, bot bool) error {
	p := platform.Name(platformName)
	r := connector.AccountStreamer
	if bot {
		r = connector.AccountBot
	}
	if err := x.a.auth.Logout(ctx, p, r); err != nil {
		return fmt.Errorf("logout %s: %w", authKey(platformName, bot), err)
	}
	return nil
}

// loginContext returns the context that follows a login in the background.
// It is derived from the core's run context, not from the request: a login
// must outlive the request that started it and end when the core stops.
func (x *apiAuth) loginContext() (context.Context, context.CancelFunc, error) {
	if x.a.runCtx == nil {
		return nil, nil, errors.New("the core is not running, a login cannot be started")
	}
	lctx, cancel := context.WithCancel(x.a.runCtx)
	return lctx, cancel, nil
}

// dropActive removes a finished login and cancels its context. It is safe to
// call after CancelLogin already removed it.
func (x *apiAuth) dropActive(key string) {
	x.mu.Lock()
	cancel, ok := x.active[key]
	if ok {
		delete(x.active, key)
	}
	x.mu.Unlock()
	if ok {
		cancel()
	}
}

// authKey names a login by platform and role.
func authKey(platformName string, bot bool) string {
	role := "streamer"
	if bot {
		role = "bot"
	}
	return platformName + "/" + role
}

// flowLabel is the API label of a login flow: "device" or "code".
func flowLabel(c auth.Credentials) string {
	if c.DeviceFlow {
		return "device"
	}
	return "code"
}
