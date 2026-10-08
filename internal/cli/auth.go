// SPDX-License-Identifier: Apache-2.0

// The auth commands (roadmap 4.1): they log in the platform accounts of a
// profile by the device code flow, show their state, and remove them again.
// "auth login" and "auth logout" change the profile and take the data
// directory lock; "auth status" reads the profile read-only and works while
// the core runs.

package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/auth"
	"github.com/ripmav/streamcrew/internal/buildinfo"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/vault"
)

// authCmd is the auth command group.
type authCmd struct {
	Login  authLoginCmd  `cmd:"" help:"Log in a Twitch account by the device code flow."`
	Status authStatusCmd `cmd:"" help:"Show the logged-in accounts and their state."`
	Logout authLogoutCmd `cmd:"" help:"Revoke the token and remove the account."`
}

type authLoginCmd struct {
	Platform string `arg:"" enum:"twitch" help:"The platform to log in: ${enum}."`
	Bot      bool   `help:"Log in the bot account instead of the streamer account."`
	ClientID string `env:"-" help:"Client ID of a Twitch app instead of the project app (development, BYO)." default:""`

	// flows is replaced by the tests; the real flows are the ones of the
	// platforms.
	flows func(p platform.Name, clientID string) (auth.Flow, error)
}

// Run logs in the account by the device code flow (ADR-0014): it shows the
// code the user enters at the URL and follows the login until the user
// completes it, then it stores the account and its token.
func (c authLoginCmd) Run(ctx context.Context, e *Env) (err error) {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	id, err := e.profileID(cfg)
	if err != nil {
		return err
	}
	profiles := e.profiles(cfg)
	if !profiles.Exists(id) {
		return fmt.Errorf("%w: %q", profile.ErrNotFound, id)
	}
	l, err := e.lock(cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)

	s, openErr := store.Open(ctx, profiles.Path(id), store.WithBeforeMigrate(
		app.PreMigrationBackup(app.BackupDir(cfg.DataDir), buildinfo.Read().Version, discardLogger())))
	if openErr != nil {
		return fmt.Errorf("open profile %q: %w", id, openErr)
	}
	defer func() { err = errors.Join(err, s.Close()) }()

	keys := vault.NewKeys(cfg.DataDir, e.SecretKey, vault.SystemKeyring{}, discardLogger())
	ks, err := keys.Load(ctx)
	if err != nil {
		return fmt.Errorf("vault key: %w", err)
	}

	flows := c.flows
	if flows == nil {
		flows = realFlows()
	}
	svc, err := authService(s, ks, flows)
	if err != nil {
		return err
	}

	role := connector.AccountStreamer
	if c.Bot {
		role = connector.AccountBot
	}
	p := platform.Name(c.Platform)
	da, pr, err := svc.StartLogin(ctx, p, role, c.ClientID)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "%s: open %s and enter the code %s\n", p, pr.URL, pr.Code)
	fmt.Fprintf(e.Stdout, "(the code expires in %s)\n", until(pr.Expiry))

	acc, err := svc.WaitLogin(ctx, da, p, role, c.ClientID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("%s: login aborted", p)
		}
		return err
	}
	_, err = fmt.Fprintf(e.Stdout, "%s: logged in as %s (%s)\n", p, acc.Login, role)
	return err
}

type authStatusCmd struct {
	output
}

// Run shows the accounts of the profile: which ones are ok, which need a
// new login and why. It reads the profile read-only and without the data
// directory lock, so that it works while the core runs.
func (c authStatusCmd) Run(ctx context.Context, e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	id, err := e.profileID(cfg)
	if err != nil {
		return err
	}
	profiles := e.profiles(cfg)
	if !profiles.Exists(id) {
		return fmt.Errorf("%w: %q", profile.ErrNotFound, id)
	}

	ro, err := store.OpenReadOnly(ctx, profiles.Path(id))
	if err != nil {
		return fmt.Errorf("open profile %q: %w", id, err)
	}
	defer ro.Close()

	keys := vault.NewKeys(cfg.DataDir, e.SecretKey, vault.SystemKeyring{}, discardLogger())
	ks, err := keys.Load(ctx)
	if err != nil {
		return fmt.Errorf("vault key: %w", err)
	}

	svc, err := authService(readonlyStore{ro: ro}, ks, noFlows)
	if err != nil {
		return err
	}
	list, err := svc.Status(ctx)
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, list)
	}
	if len(list) == 0 {
		_, err := fmt.Fprintln(e.Stdout, "no accounts")
		return err
	}
	tw := tabwriter.NewWriter(e.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PLATFORM\tROLE\tLOGIN\tSTATE\tMISSING\tEXPIRES")
	for _, s := range list {
		expires := time.Time{}
		if s.Expires != nil {
			expires = *s.Expires
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Platform, s.Role, s.Login, s.State, strings.Join(s.Missing, " "), formatTime(expires))
	}
	return tw.Flush()
}

type authLogoutCmd struct {
	Platform string `arg:"" enum:"twitch" help:"The platform to log out: ${enum}."`
	Bot      bool   `help:"Log out the bot account instead of the streamer account."`
	ClientID string `env:"-" help:"Client ID of the app the login was made with; defaults to the one of the account." default:""`

	// flows is replaced by the tests; the real flows are the ones of the
	// platforms.
	flows func(p platform.Name, clientID string) (auth.Flow, error)
}

// Run revokes the token of the account at the platform and removes the
// account and its token (ADR-0014). A failed revoke keeps both.
func (c authLogoutCmd) Run(ctx context.Context, e *Env) (err error) {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	id, err := e.profileID(cfg)
	if err != nil {
		return err
	}
	profiles := e.profiles(cfg)
	if !profiles.Exists(id) {
		return fmt.Errorf("%w: %q", profile.ErrNotFound, id)
	}
	l, err := e.lock(cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)

	s, openErr := store.Open(ctx, profiles.Path(id), store.WithBeforeMigrate(
		app.PreMigrationBackup(app.BackupDir(cfg.DataDir), buildinfo.Read().Version, discardLogger())))
	if openErr != nil {
		return fmt.Errorf("open profile %q: %w", id, openErr)
	}
	defer func() { err = errors.Join(err, s.Close()) }()

	keys := vault.NewKeys(cfg.DataDir, e.SecretKey, vault.SystemKeyring{}, discardLogger())
	ks, err := keys.Load(ctx)
	if err != nil {
		return fmt.Errorf("vault key: %w", err)
	}

	flows := c.flows
	if flows == nil {
		flows = realFlows()
	}
	svc, err := authService(s, ks, flows)
	if err != nil {
		return err
	}

	role := connector.AccountStreamer
	if c.Bot {
		role = connector.AccountBot
	}
	p := platform.Name(c.Platform)
	row, found, err := s.Account(ctx, string(p), string(role))
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s %s: %w", p, role, auth.ErrNoAccount)
	}
	if err := svc.Logout(ctx, p, role, c.ClientID); err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Stdout, "%s: removed %s (%s)\n", p, row.Login, role)
	return err
}

// authService is the auth service of the CLI commands over the profile
// store; the tokens join the transactions of the store through the vault
// factory (Code-ADR-0008). The commands publish no auth events.
func authService(st auth.Store, ks *vault.KeySet, flows func(p platform.Name, clientID string) (auth.Flow, error)) (*auth.Service, error) {
	return auth.New(auth.Ports{
		Store:  st,
		Vault:  func(repo vault.Repository) *vault.Vault { return vault.New(repo, ks) },
		Flows:  flows,
		Logger: discardLogger(),
	})
}

// realFlows returns the login flows of the platforms for a client ID; only
// Twitch has one so far (roadmap 4.1), like the flows of the core.
func realFlows() func(p platform.Name, clientID string) (auth.Flow, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	return func(p platform.Name, clientID string) (auth.Flow, error) {
		switch p {
		case platform.Twitch:
			return auth.NewTwitch(clientID, client), nil
		default:
			return nil, fmt.Errorf("no login flow for platform %q", p)
		}
	}
}

// noFlows is the flows port of "auth status", which never starts or
// refreshes a login.
func noFlows(p platform.Name, _ string) (auth.Flow, error) {
	return nil, fmt.Errorf("no login flow for platform %q in auth status", p)
}

// until formats the time until t as whole minutes, "30m".
func until(t time.Time) string {
	m := max(int(time.Until(t).Round(time.Minute)/time.Minute), 1)
	return fmt.Sprintf("%dm", m)
}

// errReadOnly is returned by the write methods of readonlyStore, which
// "auth status" never calls.
var errReadOnly = errors.New("the profile is opened read-only")

// readonlyStore is the auth store of "auth status": it reads the accounts
// and the token records of a profile opened read-only, and refuses the
// writes, which status never makes.
type readonlyStore struct {
	ro *store.ReadOnly
}

// Accounts implements auth.Store.
func (s readonlyStore) Accounts(ctx context.Context) ([]store.Account, error) {
	return s.ro.Accounts(ctx)
}

// GetSecret implements vault.Repository.
func (s readonlyStore) GetSecret(ctx context.Context, name string) (vault.Record, bool, error) {
	return s.ro.GetSecret(ctx, name)
}

func (s readonlyStore) Account(context.Context, string, string) (store.Account, bool, error) {
	return store.Account{}, false, errReadOnly
}

func (s readonlyStore) UpsertAccount(context.Context, store.Account) error {
	return errReadOnly
}

func (s readonlyStore) DeleteAccount(context.Context, string, string) (bool, error) {
	return false, errReadOnly
}

func (s readonlyStore) Atomically(context.Context, func(*store.Store) error) error {
	return errReadOnly
}

func (s readonlyStore) PutSecret(context.Context, vault.Record) error {
	return errReadOnly
}

func (s readonlyStore) DeleteSecret(context.Context, string) (bool, error) {
	return false, errReadOnly
}

func (s readonlyStore) RewriteSecrets(context.Context, func([]vault.Record) ([]vault.Record, error)) error {
	return errReadOnly
}
