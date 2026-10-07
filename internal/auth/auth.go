// SPDX-License-Identifier: Apache-2.0

// Package auth keeps the platform accounts of a profile (ADR-0014): it
// starts and follows logins, stores the tokens encrypted in the vault,
// refreshes them before they expire, and reports which accounts need a new
// login.
//
// The Service is the single entry for everything token related: the CLI
// and the frontends start and follow logins, the platform adapters ask it
// for a valid access token, and the refresh loop (Run) keeps the tokens
// alive while the core runs. It publishes the events of the auth.* types,
// so that the frontends can show the prompt of a login and follow its
// result.
package auth

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/logging"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/vault"
)

const (
	// defaultInterval is the interval of the refresh loop.
	defaultInterval = 60 * time.Second
	// refreshWindow is how early a token is refreshed before its expiry,
	// so that a request never meets an expired token.
	refreshWindow = 15 * time.Minute
)

var (
	// ErrNoAccount is the platform has no account of the role.
	ErrNoAccount = errors.New("no account")
	// ErrLoginRequired is the account needs a new login: it has no token,
	// its refresh token is gone, or the login is missing scopes.
	ErrLoginRequired = errors.New("login required")
)

// Store keeps the account metadata and, next to it, the encrypted token
// records (ADR-0014); *store.Store implements it.
type Store interface {
	// The token records, in the same store as the accounts, so that both
	// join one transaction (ADR-0012).
	vault.Repository
	UpsertAccount(ctx context.Context, a store.Account) error
	Account(ctx context.Context, platform, role string) (store.Account, bool, error)
	Accounts(ctx context.Context) ([]store.Account, error)
	DeleteAccount(ctx context.Context, platform, role string) (bool, error)
	// Atomically runs fn in one write transaction of the store, so that
	// the account and its token are saved or deleted together
	// (Code-ADR-0008).
	Atomically(ctx context.Context, fn func(tx *store.Store) error) error
}

// Publisher takes the auth events; *event.Bus implements it.
type Publisher interface {
	Publish(ctx context.Context, e event.Envelope) error
}

// Ports are the dependencies of the auth service.
type Ports struct {
	// Store keeps the account metadata (ADR-0014).
	Store Store
	// Vault returns a vault over repo, so that the token records join the
	// transactions of Store (ADR-0012).
	Vault func(repo vault.Repository) *vault.Vault
	// Flows returns the login flow of a platform for a client ID.
	Flows func(p platform.Name, clientID string) (Flow, error)
	// Publisher takes the auth events; a nil publisher discards them.
	Publisher Publisher
	// Logger records the service; a nil logger discards.
	Logger *slog.Logger
	// Clock reports the current time; nil uses the system clock.
	Clock func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithTick sets the interval of the refresh loop; the default is
// defaultInterval.
func WithTick(d time.Duration) Option {
	return func(s *Service) { s.interval = d }
}

// Service is the auth service (ADR-0014): the accounts of a profile,
// their tokens in the vault, and the refresh loop. It is a runnable of the
// supervisor (Code-ADR-0004).
type Service struct {
	ports    Ports
	logger   *slog.Logger
	clock    func() time.Time
	interval time.Duration
}

// New returns the auth service.
func New(p Ports, opts ...Option) (*Service, error) {
	switch {
	case p.Store == nil:
		return nil, errors.New("new auth service: no store")
	case p.Vault == nil:
		return nil, errors.New("new auth service: no vault")
	case p.Flows == nil:
		return nil, errors.New("new auth service: no flows")
	}
	s := &Service{
		ports:    p,
		logger:   slog.New(slog.DiscardHandler),
		clock:    time.Now,
		interval: defaultInterval,
	}
	if p.Logger != nil {
		s.logger = p.Logger
	}
	if p.Clock != nil {
		s.clock = p.Clock
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// State is how an account is doing.
type State string

const (
	// StateOK is the account is connected and complete.
	StateOK State = "ok"
	// StateLoginRequired is the account needs a new login.
	StateLoginRequired State = "login_required"
)

// Status is the state of one account (roadmap 4.1).
type Status struct {
	// Platform and Role name the account.
	Platform platform.Name     `json:"platform"`
	Role     connector.Account `json:"role"`
	// Login and UserID are the account on the platform.
	Login  string `json:"login"`
	UserID string `json:"user_id"`
	// State is ok or login_required.
	State State `json:"state"`
	// Scopes are the scopes the login has.
	Scopes []string `json:"scopes"`
	// Missing are the required scopes the login does not have.
	Missing []string `json:"missing"`
	// Expires is when the access token expires; nil if the platform did
	// not say or the account has no token.
	Expires *time.Time `json:"expires_at"`
}

// StartLogin begins a login of the platform and role and publishes the
// prompt for the frontends (auth.action_required). The returned prompt is
// the same the event carries.
func (s *Service) StartLogin(ctx context.Context, p platform.Name, r connector.Account, clientID string) (Prompt, error) {
	flow, _, err := s.flowFor(p, clientID)
	if err != nil {
		return Prompt{}, err
	}
	da, err := flow.Start(ctx)
	if err != nil {
		return Prompt{}, err
	}
	pr := PromptFrom(da)
	s.publish(ctx, eventtype.AuthActionRequired, ActionRequired{
		Platform: p,
		Role:     r,
		URL:      pr.URL,
		Code:     pr.Code,
		Expires:  pr.Expiry,
	})
	return pr, nil
}

// WaitLogin follows a login started by StartLogin until the user completes
// it. On success it stores the account and its token in one transaction and
// publishes auth.login_completed; on failure it publishes
// auth.login_failed with the reason.
func (s *Service) WaitLogin(ctx context.Context, da *oauth2.DeviceAuthResponse, p platform.Name, r connector.Account, clientID string) (Account, error) {
	flow, clientID, err := s.flowFor(p, clientID)
	if err != nil {
		return Account{}, err
	}
	tok, err := flow.Wait(ctx, da)
	if err != nil {
		s.publishLoginFailed(ctx, p, r, failureReason(err))
		return Account{}, err
	}
	userID, login, err := flow.User(ctx, tok.AccessToken)
	if err != nil {
		s.publishLoginFailed(ctx, p, r, "the platform did not return the account: "+err.Error())
		return Account{}, err
	}
	acc := Account{
		Platform:  p,
		Role:      r,
		Login:     login,
		UserID:    userID,
		Scopes:    scopeList(tok),
		ClientID:  clientID,
		UpdatedAt: s.now().UTC(),
	}
	token := Token{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.Expiry.UTC(),
	}
	if err := s.save(ctx, acc, token); err != nil {
		s.publishLoginFailed(ctx, p, r, "the login could not be stored: "+err.Error())
		return Account{}, err
	}
	s.publish(ctx, eventtype.AuthLoginCompleted, LoginCompleted{Platform: p, Role: r, Login: login})
	return acc, nil
}

// Status reports the state of every account of the profile (roadmap 4.1).
func (s *Service) Status(ctx context.Context) ([]Status, error) {
	rows, err := s.ports.Store.Accounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}
	out := make([]Status, 0, len(rows))
	for _, row := range rows {
		acc := FromStore(row)
		st := Status{
			Platform: acc.Platform,
			Role:     acc.Role,
			Login:    acc.Login,
			UserID:   acc.UserID,
			Scopes:   acc.Scopes,
			Missing:  []string{},
			State:    StateOK,
		}
		// A login without a required scope stays login_required until the
		// user logs in again (ADR-0014).
		if missing := Missing(joinScopes(acc.Scopes), requiredScopes(acc.Platform)); len(missing) > 0 {
			st.Missing = missing
			st.State = StateLoginRequired
		}
		token, found, err := s.token(ctx, acc.Platform, acc.Role)
		if err != nil {
			return nil, err
		}
		if !found {
			st.State = StateLoginRequired
			continue
		}
		expires := token.ExpiresAt.UTC()
		st.Expires = &expires
		if token.RefreshToken == "" {
			st.State = StateLoginRequired
		}
		out = append(out, st)
	}
	return out, nil
}

// Token returns a valid access token of the account (ADR-0014): the stored
// one if it is still good, a fresh one from its refresh token if it is
// not. It returns ErrNoAccount without an account and ErrLoginRequired
// when the account needs a new login.
func (s *Service) Token(ctx context.Context, p platform.Name, r connector.Account) (string, error) {
	row, found, err := s.ports.Store.Account(ctx, string(p), string(r))
	if err != nil {
		return "", fmt.Errorf("token %s %s: %w", p, r, err)
	}
	if !found {
		return "", fmt.Errorf("%s %s: %w", p, r, ErrNoAccount)
	}
	tok, found, err := s.token(ctx, p, r)
	if err != nil {
		return "", err
	}
	if !found || tok.RefreshToken == "" {
		return "", fmt.Errorf("%s %s: %w", p, r, ErrLoginRequired)
	}
	if !tok.expired(s.now()) {
		return tok.AccessToken, nil
	}
	flow, _, err := s.flowFor(p, row.ClientID)
	if err != nil {
		return "", err
	}
	newTok, err := flow.Refresh(ctx, tok.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrTokenExpired) {
			return "", fmt.Errorf("%s %s: %w", p, r, ErrLoginRequired)
		}
		return "", fmt.Errorf("token %s %s: %w", p, r, err)
	}
	return s.saveRefreshed(ctx, FromStore(row), tok, newTok)
}

// Logout revokes the token of an account and removes the account and its
// token (ADR-0014). A failed revoke keeps both, so that the user can try
// again.
func (s *Service) Logout(ctx context.Context, p platform.Name, r connector.Account, clientID string) error {
	row, found, err := s.ports.Store.Account(ctx, string(p), string(r))
	if err != nil {
		return fmt.Errorf("logout %s %s: %w", p, r, err)
	}
	if !found {
		return fmt.Errorf("%s %s: %w", p, r, ErrNoAccount)
	}
	tok, found, err := s.token(ctx, p, r)
	if err != nil {
		return err
	}
	if found {
		// Revoke the refresh token: the platform revokes the whole login
		// with it, not only the access token (ADR-0014).
		target := tok.RefreshToken
		if target == "" {
			target = tok.AccessToken
		}
		if target != "" {
			// The token was made with the client ID of the account; a
			// missing one falls back to the one the caller gives.
			id := row.ClientID
			if id == "" {
				id = clientID
			}
			flow, _, err := s.flowFor(p, id)
			if err != nil {
				return err
			}
			if err := flow.Revoke(ctx, target); err != nil {
				return fmt.Errorf("revoke %s %s: %w", p, r, err)
			}
		}
	}
	return s.delete(ctx, p, r)
}

// Run is the refresh loop of the supervisor (Code-ADR-0004): it refreshes
// the tokens of the accounts before they expire, so that a request never
// meets an expired token. An error of one account never stops the loop.
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick refreshes the accounts whose access token runs out within the
// refresh window.
func (s *Service) tick(ctx context.Context) {
	rows, err := s.ports.Store.Accounts(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "refresh: list the accounts", "error", err)
		return
	}
	for _, row := range rows {
		s.refresh(ctx, FromStore(row))
	}
}

func (s *Service) refresh(ctx context.Context, acc Account) {
	tok, found, err := s.token(ctx, acc.Platform, acc.Role)
	if err != nil {
		s.logger.ErrorContext(ctx, "refresh: read the token", "platform", acc.Platform, "role", acc.Role, "error", err)
		return
	}
	if !found {
		s.logger.WarnContext(ctx, "the account has no token, the login is required", "platform", acc.Platform, "role", acc.Role)
		return
	}
	if tok.ExpiresAt.IsZero() || tok.ExpiresAt.Sub(s.now()) > refreshWindow {
		return
	}
	flow, _, err := s.flowFor(acc.Platform, acc.ClientID)
	if err != nil {
		s.logger.ErrorContext(ctx, "refresh: the login flow", "platform", acc.Platform, "role", acc.Role, "error", err)
		return
	}
	newTok, err := flow.Refresh(ctx, tok.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrTokenExpired) {
			s.logger.ErrorContext(ctx, "the refresh token expired, the login is required", "platform", acc.Platform, "role", acc.Role)
			s.publishLoginFailed(ctx, acc.Platform, acc.Role, "token expired")
		} else {
			s.logger.WarnContext(ctx, "refresh the token, retry on the next tick", "platform", acc.Platform, "role", acc.Role, "error", err)
		}
		return
	}
	if _, err := s.saveRefreshed(ctx, acc, tok, newTok); err != nil {
		s.logger.ErrorContext(ctx, "refresh: store the token", "platform", acc.Platform, "role", acc.Role, "error", err)
	}
}

// save stores the account and its token in one transaction, so that a
// crash never leaves one without the other (Code-ADR-0008).
func (s *Service) save(ctx context.Context, acc Account, tok Token) error {
	name := authName(acc.Platform, acc.Role)
	return s.ports.Store.Atomically(ctx, func(tx *store.Store) error {
		if err := tx.UpsertAccount(ctx, acc.ToStore()); err != nil {
			return fmt.Errorf("account %s %s: %w", acc.Platform, acc.Role, err)
		}
		if err := putToken(ctx, s.ports.Vault(tx), name, tok); err != nil {
			return fmt.Errorf("token %s %s: %w", acc.Platform, acc.Role, err)
		}
		return nil
	})
}

// saveRefreshed stores a refreshed token, keeping the refresh token if the
// platform did not return one, and returns the new access token.
func (s *Service) saveRefreshed(ctx context.Context, acc Account, previous Token, newTok *oauth2.Token) (string, error) {
	refresh := newTok.RefreshToken
	if refresh == "" {
		refresh = previous.RefreshToken
	}
	token := Token{
		AccessToken:  newTok.AccessToken,
		RefreshToken: refresh,
		ExpiresAt:    newTok.Expiry.UTC(),
	}
	if err := putToken(ctx, s.ports.Vault(s.ports.Store), authName(acc.Platform, acc.Role), token); err != nil {
		return "", fmt.Errorf("token %s %s: %w", acc.Platform, acc.Role, err)
	}
	return token.AccessToken, nil
}

// putToken stores the token in the vault under name as JSON.
func putToken(ctx context.Context, v *vault.Vault, name string, tok Token) error {
	data, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("encode token: %w", err)
	}
	return v.Put(ctx, name, logging.Secret(data))
}

// delete removes the account and its token in one transaction.
func (s *Service) delete(ctx context.Context, p platform.Name, r connector.Account) error {
	name := authName(p, r)
	return s.ports.Store.Atomically(ctx, func(tx *store.Store) error {
		if _, err := tx.DeleteAccount(ctx, string(p), string(r)); err != nil {
			return fmt.Errorf("account %s %s: %w", p, r, err)
		}
		if err := s.ports.Vault(tx).Delete(ctx, name); err != nil {
			return fmt.Errorf("token %s %s: %w", p, r, err)
		}
		return nil
	})
}

// token returns the stored token of an account; found is false if the
// account has no token record.
func (s *Service) token(ctx context.Context, p platform.Name, r connector.Account) (Token, bool, error) {
	data, err := s.ports.Vault(s.ports.Store).Get(ctx, authName(p, r))
	if err != nil {
		if errors.Is(err, vault.ErrNotFound) {
			return Token{}, false, nil
		}
		return Token{}, false, fmt.Errorf("token %s %s: %w", p, r, err)
	}
	var tok Token
	if err := json.Unmarshal([]byte(data.Reveal()), &tok); err != nil {
		return Token{}, false, fmt.Errorf("token %s %s: %w", p, r, err)
	}
	return tok, true, nil
}

// flowFor returns the flow of the platform and the client ID it uses, the
// project app when the caller gives none (ADR-0014).
func (s *Service) flowFor(p platform.Name, clientID string) (Flow, string, error) {
	clientID = defaultClientID(p, clientID)
	flow, err := s.ports.Flows(p, clientID)
	if err != nil {
		return nil, clientID, fmt.Errorf("login flow %s %s: %w", p, clientID, err)
	}
	return flow, clientID, nil
}

// publish sends an auth event to the frontends; a publish error never
// fails the call that carries it.
func (s *Service) publish(ctx context.Context, t event.Type, payload any) {
	if s.ports.Publisher == nil {
		return
	}
	e := event.New(event.Source{Kind: event.SourceSystem, Name: "auth"}, t, payload)
	if err := s.ports.Publisher.Publish(ctx, e); err != nil {
		s.logger.WarnContext(ctx, "publish the auth event", "type", t, "error", err)
	}
}

func (s *Service) publishLoginFailed(ctx context.Context, p platform.Name, r connector.Account, why string) {
	s.publish(ctx, eventtype.AuthLoginFailed, LoginFailed{Platform: p, Role: r, Reason: why})
}

// now reports the current time.
func (s *Service) now() time.Time {
	return s.clock()
}

// defaultClientID returns the client ID the platform's flow uses when the
// caller gives none: the project app (ADR-0014).
func defaultClientID(p platform.Name, clientID string) string {
	if clientID == "" && p == platform.Twitch {
		return ClientID
	}
	return clientID
}

// requiredScopes returns the scopes the logins of the platform need
// (ADR-0014); a login without one of them is login_required.
func requiredScopes(p platform.Name) []string {
	if p == platform.Twitch {
		return Scopes
	}
	return nil
}

// scopeList returns the scopes granted by a token, one per entry.
func scopeList(tok *oauth2.Token) []string {
	scope, _ := tok.Extra("scope").(string)
	out := strings.Fields(scope)
	if out == nil {
		return []string{}
	}
	return out
}

// failureReason maps a login error to the short reason of the
// auth.login_failed event.
func failureReason(err error) string {
	switch {
	case errors.Is(err, ErrCodeExpired):
		return "the device code expired"
	case errors.Is(err, ErrAccessDenied):
		return "the user denied the login"
	case errors.Is(err, context.Canceled):
		return "the login was aborted"
	default:
		return err.Error()
	}
}
