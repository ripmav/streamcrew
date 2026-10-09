// SPDX-License-Identifier: Apache-2.0

// The auth commands (roadmap 4.1): they log in the platform accounts of a
// profile by the authorization code flow (the device code flow with
// --device-flow), show their state, and remove them again (ADR-0023).
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
	"github.com/ripmav/streamcrew/internal/breaker"
	"github.com/ripmav/streamcrew/internal/buildinfo"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/httpclient"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/vault"
)

// authCmd is the auth command group.
type authCmd struct {
	Login  authLoginCmd  `cmd:"" help:"Log in a Twitch account by the authorization code flow (the device code flow with --device-flow)."`
	Status authStatusCmd `cmd:"" help:"Show the logged-in accounts and their state."`
	Logout authLogoutCmd `cmd:"" help:"Revoke the token and remove the account."`
}

type authLoginCmd struct {
	Platform     string `arg:"" enum:"twitch" help:"The platform to log in: ${enum}."`
	Bot          bool   `help:"Log in the bot account instead of the streamer account."`
	DeviceFlow   bool   `help:"Use the device code flow instead of the authorization code flow (fallback, ADR-0023)."`
	ClientID     string `env:"-" help:"Client ID of your own Twitch app (BYO); without it the app of the last login is used." default:""`
	ClientSecret string `env:"-" help:"Client secret of your own Twitch app, stored encrypted in the profile; given together with the client ID." default:""`

	// flows is replaced by the tests; the real flows are the ones of the
	// platforms.
	flows func(p platform.Name, c auth.Credentials) (auth.Flow, error)
}

// Run logs in the account (ADR-0023): by default it starts the
// authorization code flow, shows the URL the user opens to authorize the
// app, and follows the login until the loopback redirect completes it; with
// --device-flow it shows the code the user enters at the URL. The given app
// (--client-id/--client-secret) is stored encrypted in the profile; without
// it, the app of the last login of the platform is used.
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
	if !c.DeviceFlow && (c.ClientID == "") != (c.ClientSecret == "") {
		return errors.New("the client ID and the client secret are given together: --client-id and --client-secret")
	}
	creds := auth.Credentials{ID: c.ClientID, Secret: c.ClientSecret, DeviceFlow: c.DeviceFlow}
	lg, pr, err := svc.StartLogin(ctx, p, role, creds)
	if err != nil {
		return err
	}
	if pr.Code == "" {
		fmt.Fprintf(e.Stdout, "%s: open %s and authorize the app\n", p, pr.URL)
		fmt.Fprintf(e.Stdout, "(the login expires in %s)\n", until(pr.Expiry))
	} else {
		fmt.Fprintf(e.Stdout, "%s: open %s and enter the code %s\n", p, pr.URL, pr.Code)
		fmt.Fprintf(e.Stdout, "(the code expires in %s)\n", until(pr.Expiry))
	}

	acc, err := svc.WaitLogin(ctx, lg, p, role, creds)
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

	// flows is replaced by the tests; the real flows are the ones of the
	// platforms.
	flows func(p platform.Name, c auth.Credentials) (auth.Flow, error)
}

// Run revokes the token of the account at the platform with the client of
// the account row and removes the account and its token (ADR-0023); the app
// credentials of the platform stay for the next login. A failed revoke keeps
// the account and the token, so that the logout can be retried.
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
	if err := svc.Logout(ctx, p, role); err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Stdout, "%s: removed %s (%s)\n", p, row.Login, role)
	return err
}

// authService is the auth service of the CLI commands over the profile
// store; the tokens join the transactions of the store through the vault
// factory (Code-ADR-0008). The commands publish no auth events.
func authService(st auth.Store, ks *vault.KeySet, flows func(p platform.Name, c auth.Credentials) (auth.Flow, error)) (*auth.Service, error) {
	return auth.New(auth.Ports{
		Store:  st,
		Vault:  func(repo vault.Repository) *vault.Vault { return vault.New(repo, ks) },
		Flows:  flows,
		Logger: discardLogger(),
	})
}

// realFlows returns the login flows of the platforms for their
// credentials; only Twitch has one so far (roadmap 4.1), like the flows of
// the core. The token calls run through the resilient HTTP client
// (Code-ADR-0014) with the breakers twitch.auth and twitch.helix
// (Code-ADR-0007); the CLI logs neither the retries nor the breaker
// states, like the rest of the auth commands.
func realFlows() func(p platform.Name, c auth.Credentials) (auth.Flow, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	twitchAuth := httpclient.New(httpclient.Options{
		Name:    "twitch.auth",
		Base:    client,
		Breaker: breaker.New("twitch.auth", nil, httpclient.Evaluator),
	})
	twitchHelix := httpclient.New(httpclient.Options{
		Name:    "twitch.helix",
		Base:    client,
		Breaker: breaker.New("twitch.helix", nil, httpclient.Evaluator),
	})
	return func(p platform.Name, c auth.Credentials) (auth.Flow, error) {
		switch p {
		case platform.Twitch:
			return auth.NewTwitch(c, client, twitchAuth, twitchHelix)
		default:
			return nil, fmt.Errorf("no login flow for platform %q", p)
		}
	}
}

// noFlows is the flows port of "auth status", which never starts or
// refreshes a login.
func noFlows(p platform.Name, _ auth.Credentials) (auth.Flow, error) {
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
