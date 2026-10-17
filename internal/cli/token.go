// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/apitoken"
)

// tokenCmd manages the API access tokens of the data directory (roadmap
// 6.2.2). The tokens protect the API of a running core; the commands work
// on the token file directly, no running core is needed.
type tokenCmd struct {
	Create tokenCreateCmd `cmd:"" help:"Create an API token. The value is printed once."`
	List   tokenListCmd   `cmd:"" help:"List the API tokens."`
	Revoke tokenRevokeCmd `cmd:"" help:"Revoke an API token by name or ID."`
}

type tokenCreateCmd struct {
	Name   string   `arg:"" optional:"" help:"Name of the token. Default: cli-<timestamp>."`
	Scopes []string `name:"scope" placeholder:"SCOPE" help:"Scope of the token: read, control, admin, overlay. Repeatable. Default: admin."`
}

// Run creates the token and prints its value, which is shown once.
func (c tokenCreateCmd) Run(e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
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
	tok, value, err := apitoken.Open(cfg.DataDir).Create(name, scopes)
	if err != nil {
		return &usageError{err: err}
	}
	fmt.Fprintln(e.Stdout, value)
	fmt.Fprintf(e.Stderr, "created token %q (ID %s) with the scopes %s\n", tok.Name, tok.ID, joinScopes(tok.Scopes))
	return nil
}

type tokenListCmd struct{}

// Run lists the API tokens.
func (c tokenListCmd) Run(e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	tokens, err := apitoken.Open(cfg.DataDir).List()
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
	IDOrName string `arg:"" help:"Name or ID of the token to revoke."`
}

// Run revokes the token.
func (c tokenRevokeCmd) Run(e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	ok, err := apitoken.Open(cfg.DataDir).Revoke(c.IDOrName)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no token with the name or ID %q", c.IDOrName)
	}
	fmt.Fprintf(e.Stdout, "revoked %s\n", c.IDOrName)
	return nil
}

// joinScopes renders the scopes of a token, strongest last.
func joinScopes(scopes []apitoken.Scope) string {
	parts := make([]string, len(scopes))
	for i, s := range scopes {
		parts[i] = string(s)
	}
	return strings.Join(parts, ",")
}
