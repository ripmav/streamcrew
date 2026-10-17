// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"fmt"
	"net/http"
	"text/tabwriter"

	"connectrpc.com/connect"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
)

// userClient returns the UserService client of the core.
func (e *Env) userClient() streamcrewv1alpha1connect.UserServiceClient {
	return streamcrewv1alpha1connect.NewUserServiceClient(http.DefaultClient, e.server(), e.connectOptions()...)
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
	resp, err := e.userClient().List(ctx, connect.NewRequest(&v1alpha1.ListUsersRequest{Prefix: c.Prefix, Stat: c.Stat, Limit: c.Limit}))
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.Msg.Users)
	}
	if len(resp.Msg.Users) == 0 {
		fmt.Fprintln(e.Stdout, "no users")
		return nil
	}
	tw := tabwriter.NewWriter(e.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "LOGIN\tROLE\tTITLE\tMESSAGES\tSTRIKES")
	for _, u := range resp.Msg.Users {
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
	resp, err := e.userClient().Get(ctx, connect.NewRequest(&v1alpha1.GetUserRequest{PlatformLogin: platformLogin(c.Platform, c.Login)}))
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.Msg.User)
	}
	u := resp.Msg.User
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
	// Title is the new title; empty keeps it.
	Title string `help:"The new title." placeholder:"TITLE"`
	// Strikes is the new strike count; 0 keeps it.
	Strikes uint32 `help:"The new strike count." placeholder:"N"`
	// Excluded and NoExcluded set the excluded switch.
	Excluded   bool `help:"Exclude the user from rankings and random selection."`
	NoExcluded bool `help:"Include the user again."`
}

// Run changes the role, title, strikes and exclusion of a user of the core.
func (c userSetCmd) Run(ctx context.Context, e *Env) error {
	got, err := e.userClient().Get(ctx, connect.NewRequest(&v1alpha1.GetUserRequest{PlatformLogin: platformLogin(c.Platform, c.Login)}))
	if err != nil {
		return err
	}
	req := &v1alpha1.UpdateUserRequest{
		Id:    got.Msg.User.Id,
		Role:  c.Role,
		Title: c.Title,
	}
	if c.Strikes > 0 {
		req.Strikes = &c.Strikes
	}
	if c.Excluded != c.NoExcluded {
		req.Excluded = &c.Excluded
	}
	resp, err := e.userClient().Update(ctx, connect.NewRequest(req))
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.Msg.User)
	}
	fmt.Fprintf(e.Stdout, "updated %s (role %s)\n", resp.Msg.User.Id, resp.Msg.User.Role)
	return nil
}
