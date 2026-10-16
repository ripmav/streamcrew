// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// Account is one logged-in platform account as seen through the API.
type Account struct {
	// Platform is the platform, e.g. "twitch".
	Platform string
	// Login is the account's login, e.g. the channel name.
	Login string
	// DisplayName is the display name of the account.
	DisplayName string
	// Bot is true for the bot account, false for the streamer account.
	Bot bool
	// Scopes are the scopes the login has.
	Scopes []string
}

// LoginPrompt is what the user must do to complete a login started by
// StartLogin.
type LoginPrompt struct {
	// Flow is "code" or "device".
	Flow string
	// Code is the code the user enters; empty for the authorization code
	// flow.
	Code string
	// URL is where the user completes the login.
	URL string
	// ExpiresAt is when the code expires; zero if unknown.
	ExpiresAt time.Time
}

// Auth is the port the AuthService handler needs.
type Auth interface {
	// ListAccounts lists the logged-in accounts of the profile.
	ListAccounts(ctx context.Context) ([]Account, error)
	// StartLogin starts a login of the platform and role and returns the
	// prompt; the login is followed in the background until it completes.
	StartLogin(ctx context.Context, platformName string, bot, deviceFlow bool) (LoginPrompt, error)
	// CancelLogin cancels a login in progress; it is a no-op if none is.
	CancelLogin(ctx context.Context, platformName string, bot bool) error
	// Logout logs the platform and role out.
	Logout(ctx context.Context, platformName string, bot bool) error
}

// ErrLoginInProgress is returned by an Auth port when a login is already in
// progress for the platform and role.
var ErrLoginInProgress = errors.New("a login is already in progress")

// authService implements the AuthService handler.
type authService struct {
	a Auth
}

func (s *authService) ListAccounts(ctx context.Context, _ *connect.Request[v1alpha1.ListAccountsRequest]) (*connect.Response[v1alpha1.ListAccountsResponse], error) {
	accs, err := s.a.ListAccounts(ctx)
	if err != nil {
		return nil, apiError(err)
	}
	out := &v1alpha1.ListAccountsResponse{}
	for _, acc := range accs {
		out.Accounts = append(out.Accounts, &v1alpha1.Account{
			Platform:    acc.Platform,
			Login:       acc.Login,
			DisplayName: acc.DisplayName,
			Bot:         acc.Bot,
			Scopes:      acc.Scopes,
		})
	}
	return connect.NewResponse(out), nil
}

func (s *authService) StartLogin(ctx context.Context, req *connect.Request[v1alpha1.StartLoginRequest]) (*connect.Response[v1alpha1.StartLoginResponse], error) {
	m := req.Msg
	prompt, err := s.a.StartLogin(ctx, m.Platform, m.Bot, m.DeviceFlow)
	if err != nil {
		return nil, apiError(err)
	}
	resp := &v1alpha1.StartLoginResponse{Flow: prompt.Flow, Code: prompt.Code, Url: prompt.URL}
	if !prompt.ExpiresAt.IsZero() {
		resp.ExpiresAt = timestamppb.New(prompt.ExpiresAt)
	}
	return connect.NewResponse(resp), nil
}

func (s *authService) CancelLogin(ctx context.Context, req *connect.Request[v1alpha1.CancelLoginRequest]) (*connect.Response[v1alpha1.CancelLoginResponse], error) {
	if err := s.a.CancelLogin(ctx, req.Msg.Platform, req.Msg.Bot); err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&v1alpha1.CancelLoginResponse{}), nil
}

func (s *authService) Logout(ctx context.Context, req *connect.Request[v1alpha1.LogoutRequest]) (*connect.Response[v1alpha1.LogoutResponse], error) {
	if err := s.a.Logout(ctx, req.Msg.Platform, req.Msg.Bot); err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&v1alpha1.LogoutResponse{}), nil
}
