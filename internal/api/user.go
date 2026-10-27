// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// UserIdentity is one platform account of a user.
type UserIdentity struct {
	Platform       string
	PlatformUserID string
	Login          string
	DisplayName    string
	FollowedAt     time.Time
}

// User is a profile user as seen through the API.
type User struct {
	ID           string
	Role         string
	Title        string
	Excluded     bool
	Strikes      uint32
	Identities   []UserIdentity
	WatchMinutes int64
	Messages     int64
	CommandsRun  int64
	Mentions     int64
	FirstSeen    time.Time
	LastSeen     time.Time
}

// RankedUser is one user in a ranking.
type RankedUser struct {
	ID    string
	Login string
	Value int64
}

// Ranking is the top users of one statistic.
type Ranking struct {
	Stat  string
	Users []RankedUser
}

// Users manages the users of a profile (roadmap 6.2.1, API).
type Users interface {
	// List returns the users; prefix filters by login (empty all), stat
	// ranks by a statistic (empty lists by ID), limit is 0 for the default.
	List(ctx context.Context, prefix, stat string, limit int) ([]User, error)
	// Get returns one user by ID and/or platform login ("platform/login").
	Get(ctx context.Context, idStr, platformLogin string) (User, error)
	// Update edits the user: title (nil keeps, an empty value clears),
	// role (empty keeps, added to every identity), excluded and strikes
	// (nil keeps).
	Update(ctx context.Context, idStr, roleStr string, title *string, excluded *bool, strikes *uint32) (User, error)
	// Merge folds the merge account into the keep account; it returns the
	// remaining user ID.
	Merge(ctx context.Context, keepPlatformLogin, mergePlatformLogin string) (string, error)
	// Rankings returns the top users per statistic; limit is 0 for the
	// default.
	Rankings(ctx context.Context, limit int) ([]Ranking, error)
}

// userService implements streamcrewv1alpha1.UserServiceHandler.
type userService struct {
	u Users
}

// view converts a user to its protocol message.
func (us *userService) view(u User) *v1alpha1.UserView {
	v := &v1alpha1.UserView{
		Id:           u.ID,
		Role:         u.Role,
		Title:        u.Title,
		Excluded:     u.Excluded,
		Strikes:      u.Strikes,
		WatchMinutes: u.WatchMinutes,
		Messages:     u.Messages,
		CommandsRun:  u.CommandsRun,
		Mentions:     u.Mentions,
	}
	for _, i := range u.Identities {
		v.Identities = append(v.Identities, &v1alpha1.UserIdentity{
			Platform:       i.Platform,
			PlatformUserId: i.PlatformUserID,
			Login:          i.Login,
			DisplayName:    i.DisplayName,
			FollowedAt:     timestamppb.New(i.FollowedAt),
		})
	}
	if !u.FirstSeen.IsZero() {
		v.FirstSeen = timestamppb.New(u.FirstSeen)
	}
	if !u.LastSeen.IsZero() {
		v.LastSeen = timestamppb.New(u.LastSeen)
	}
	return v
}

// List lists the users, optionally filtered and ranked.
func (us *userService) List(ctx context.Context, req *v1alpha1.ListUsersRequest) (*v1alpha1.ListUsersResponse, error) {
	m := req
	users, err := us.u.List(ctx, m.GetPrefix(), m.GetStat(), int(m.GetLimit()))
	if err != nil {
		return nil, apiError(err)
	}
	resp := &v1alpha1.ListUsersResponse{}
	for _, u := range users {
		resp.Users = append(resp.Users, us.view(u))
	}
	return resp, nil
}

// Get returns one user by ID or by platform login.
func (us *userService) Get(ctx context.Context, req *v1alpha1.GetUserRequest) (*v1alpha1.GetUserResponse, error) {
	m := req
	u, err := us.u.Get(ctx, m.GetId(), m.GetPlatformLogin())
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.GetUserResponse{User: us.view(u)}, nil
}

// Update edits the mutable fields of a user.
func (us *userService) Update(ctx context.Context, req *v1alpha1.UpdateUserRequest) (*v1alpha1.UpdateUserResponse, error) {
	m := req
	u, err := us.u.Update(ctx, m.GetId(), m.GetRole(), m.Title, m.Excluded, m.Strikes)
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.UpdateUserResponse{User: us.view(u)}, nil
}

// Merge folds the accounts of two users into one.
func (us *userService) Merge(ctx context.Context, req *v1alpha1.MergeUsersRequest) (*v1alpha1.MergeUsersResponse, error) {
	m := req
	id, err := us.u.Merge(ctx, m.GetKeepPlatformLogin(), m.GetMergePlatformLogin())
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.MergeUsersResponse{Id: id}, nil
}

// Rankings returns the top users per statistic.
func (us *userService) Rankings(ctx context.Context, req *v1alpha1.RankingsRequest) (*v1alpha1.RankingsResponse, error) {
	rankings, err := us.u.Rankings(ctx, int(req.GetLimit()))
	if err != nil {
		return nil, apiError(err)
	}
	resp := &v1alpha1.RankingsResponse{}
	for _, r := range rankings {
		rr := &v1alpha1.Ranking{Stat: r.Stat}
		for _, u := range r.Users {
			rr.Users = append(rr.Users, &v1alpha1.RankedUser{Id: u.ID, Login: u.Login, Value: u.Value})
		}
		resp.Rankings = append(resp.Rankings, rr)
	}
	return resp, nil
}
