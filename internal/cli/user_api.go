// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"errors"
	"fmt"
	"text/tabwriter"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
)

// userClient returns the UserService client of the core.
func (e *Env) userClient() streamcrewv1alpha1connect.UserServiceClient {
	return streamcrewv1alpha1connect.NewUserServiceClient(e.connectClient())
}

// platformLogin is the "platform/login" form of a user identity.
func platformLogin(platform, login string) string {
	return platform + "/" + login
}

type userListCmd struct {
	output
	// Prefix filters by a case-insensitive login prefix; empty is all.
	Prefix string `help:"Only show users with this login prefix." placeholder:"PREFIX"`
	// Stat ranks by a statistic: watch_minutes, messages, commands_run or
	// mentions; empty lists by ID.
	Stat string `help:"Rank the users by this statistic." placeholder:"STAT"`
	// Limit is the maximum number of users; 0 uses the default.
	Limit uint32 `help:"The maximum number of users; 0 uses the default." placeholder:"N"`
}

// Run lists the users of the core.
func (c userListCmd) Run(ctx context.Context, e *Env) error {
	resp, err := e.userClient().List(ctx, &v1alpha1.ListUsersRequest{Prefix: c.Prefix, Stat: c.Stat, Limit: c.Limit})
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.Users)
	}
	if len(resp.Users) == 0 {
		fmt.Fprintln(e.Stdout, "no users")
		return nil
	}
	tw := tabwriter.NewWriter(e.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "LOGIN\tROLE\tTITLE\tMESSAGES\tSTRIKES")
	for _, u := range resp.Users {
		login := "-"
		if len(u.Identities) > 0 {
			login = u.Identities[0].Login
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\n", login, u.Role, u.Title, u.Messages, u.Strikes)
	}
	return tw.Flush()
}

type userShowCmd struct {
	output
	Login    string `arg:"" required:"" help:"The login of the user."`
	Platform string `required:"" help:"The platform of the login, e.g. twitch." placeholder:"PLATFORM"`
}

// Run shows one user of the core.
func (c userShowCmd) Run(ctx context.Context, e *Env) error {
	resp, err := e.userClient().Get(ctx, &v1alpha1.GetUserRequest{PlatformLogin: platformLogin(c.Platform, c.Login)})
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.User)
	}
	u := resp.User
	fmt.Fprintf(e.Stdout, "user %s (%s)\n", u.Id, u.Role)
	if u.Title != "" {
		fmt.Fprintf(e.Stdout, "title: %s\n", u.Title)
	}
	if u.Strikes > 0 {
		fmt.Fprintf(e.Stdout, "strikes: %d\n", u.Strikes)
	}
	for _, i := range u.Identities {
		fmt.Fprintf(e.Stdout, "%s: %s (%s)\n", i.Platform, i.Login, i.DisplayName)
	}
	fmt.Fprintf(e.Stdout, "watch minutes: %d, messages: %d, commands: %d\n", u.WatchMinutes, u.Messages, u.CommandsRun)
	return nil
}

type userSetCmd struct {
	output
	Login    string `arg:"" required:"" help:"The login of the user."`
	Platform string `required:"" help:"The platform of the login, e.g. twitch." placeholder:"PLATFORM"`
	// Role is the new role, e.g. "moderator"; empty keeps it.
	Role string `help:"The new role." placeholder:"ROLE"`
	// Title is the new title; unset keeps it. Use --clear-title to
	// remove the current title.
	Title      string `help:"The new title." placeholder:"TITLE"`
	ClearTitle bool   `help:"Remove the current title."`
	// Strikes is the new strike count; 0 clears the counter, unset keeps
	// it (kong leaves a nil pointer flag at its zero value).
	Strikes *uint32 `help:"The new strike count, 0 clears it." placeholder:"N"`
	// Excluded sets the excluded switch; NoExcluded includes the user
	// again.
	Excluded   bool `help:"Exclude the user from rankings and random selection."`
	NoExcluded bool `help:"Include the user again."`
}

// Run changes the role, title, strikes and exclusion of a user of the core.
func (c userSetCmd) Run(ctx context.Context, e *Env) error {
	got, err := e.userClient().Get(ctx, &v1alpha1.GetUserRequest{PlatformLogin: platformLogin(c.Platform, c.Login)})
	if err != nil {
		return err
	}
	req := &v1alpha1.UpdateUserRequest{
		Id:   got.User.Id,
		Role: c.Role,
	}
	switch {
	case c.ClearTitle && c.Title != "":
		return errors.New("--title and --clear-title are mutually exclusive")
	case c.ClearTitle:
		req.Title = new(string)
	case c.Title != "":
		req.Title = &c.Title
	}
	if c.Strikes != nil {
		req.Strikes = c.Strikes
	}
	if c.Excluded != c.NoExcluded {
		req.Excluded = &c.Excluded
	}
	resp, err := e.userClient().Update(ctx, req)
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.User)
	}
	fmt.Fprintf(e.Stdout, "updated %s (role %s)\n", resp.User.Id, resp.User.Role)
	return nil
}
