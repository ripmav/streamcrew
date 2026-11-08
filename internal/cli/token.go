// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/apitoken"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/store"
)

// tokenCmd manages the API access tokens of the active profile (roadmap
// 6.2.2, ADR-0013 §5). The tokens live in the profile database: create and
// revoke need a stopped core, list works while the core runs.
type tokenCmd struct {
	Create tokenCreateCmd `cmd:"" help:"Create an API token. The value is printed once. Needs a stopped core."`
	List   tokenListCmd   `cmd:"" help:"List the API tokens. Works while the core runs."`
	Revoke tokenRevokeCmd `cmd:"" help:"Revoke an API token by ID, or by its name. Needs a stopped core."`
}

type tokenCreateCmd struct {
	Name   string   `arg:"" optional:"" help:"Name of the token. Default: cli-<timestamp>."`
	Scopes []string `name:"scope" placeholder:"SCOPE" help:"Scope of the token: read, control, admin, overlay. The overlay scope opens only the overlay server (B5), never the API. Repeatable. Default: admin."`
}

// Run creates the token and prints its value, which is shown once.
func (c tokenCreateCmd) Run(ctx context.Context, e *Env) (err error) {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	l, err := e.lock(cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)
	scopes := make([]apitoken.Scope, len(c.Scopes))
	for i, s := range c.Scopes {
		scopes[i] = apitoken.Scope(s)
	}
	if len(scopes) == 0 {
		scopes = []apitoken.Scope{apitoken.Admin}
	}
	name := c.Name
	if name == "" {
		name = "cli-" + time.Now().Format("20060102-150405")
	}
	st, err := e.tokenStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	tok, value, err := apitoken.New(st).Create(ctx, name, scopes)
	if err != nil {
		return &usageError{err: err}
	}
	fmt.Fprintln(e.Stdout, value)
	fmt.Fprintf(e.Stderr, "created token %q (ID %s) with the scopes %s\n", tok.Name, tok.ID, joinScopes(tok.Scopes))
	return nil
}

type tokenListCmd struct{}

// Run lists the API tokens.
func (c tokenListCmd) Run(ctx context.Context, e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	tokens, err := e.tokenList(ctx, cfg)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		fmt.Fprintln(e.Stdout, "no API tokens")
		return nil
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].CreatedAt.Before(tokens[j].CreatedAt) })
	for _, t := range tokens {
		fmt.Fprintf(e.Stdout, "%-16s %-40s %-16s %s\n", t.Name, t.ID, joinScopes(t.Scopes), t.CreatedAt.Format(time.RFC3339))
	}
	return nil
}

type tokenRevokeCmd struct {
	IDOrName string `arg:"" help:"ID of the token to revoke, or its name."`
}

// Run revokes the token.
func (c tokenRevokeCmd) Run(ctx context.Context, e *Env) (err error) {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	l, err := e.lock(cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)
	st, err := e.tokenStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	ok, err := apitoken.New(st).Revoke(ctx, c.IDOrName)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no token with the name or ID %q", c.IDOrName)
	}
	fmt.Fprintf(e.Stdout, "revoked %s\n", c.IDOrName)
	return nil
}

// tokenStore opens the database of the profile, with a backup before
// migrations, like the other profile commands.
func (e *Env) tokenStore(ctx context.Context, cfg *config.Config) (*store.Store, error) {
	id, err := e.profileID(cfg)
	if err != nil {
		return nil, err
	}
	return e.openProfile(ctx, cfg, id)
}

// tokenList reads the tokens of the profile read-only, so it works while
// the core runs.
func (e *Env) tokenList(ctx context.Context, cfg *config.Config) ([]apitoken.Token, error) {
	id, err := e.profileID(cfg)
	if err != nil {
		return nil, err
	}
	ro, err := store.OpenReadOnly(ctx, e.profiles(cfg).Path(id))
	if err != nil {
		return nil, err
	}
	defer ro.Close()
	return ro.ListAPITokens(ctx)
}

// joinScopes renders the scopes of a token, strongest last.
func joinScopes(scopes []apitoken.Scope) string {
	parts := make([]string, len(scopes))
	for i, s := range scopes {
		parts[i] = string(s)
	}
	return strings.Join(parts, ",")
}
