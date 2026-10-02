// SPDX-License-Identifier: Apache-2.0

package requirement

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/i18n"
)

// Cooldowns stores the running cooldowns and knows the cooldown groups
// (B20 to B23); *store.Store implements it.
type Cooldowns interface {
	// CooldownGroups returns all cooldown groups.
	CooldownGroups(ctx context.Context) ([]command.CooldownGroup, error)
	// CooldownEnd returns the end of the cooldown key; ok is false if none
	// was started. It may return an end that has passed.
	CooldownEnd(ctx context.Context, key command.CooldownKey) (ends time.Time, ok bool, err error)
	// PutCooldown sets the end of the cooldown key, and forgets the
	// cooldowns that ended at or before now.
	PutCooldown(ctx context.Context, key command.CooldownKey, ends, now time.Time) error
	// DeleteCooldown deletes the cooldown key if it still ends at ends.
	DeleteCooldown(ctx context.Context, key command.CooldownKey, ends time.Time) error
}

// Streamer finds the user of the streamer's account (B4).
type Streamer interface {
	// StreamerUser returns the ID of the user of the streamer's account on
	// platform p. An error means it is not known.
	StreamerUser(ctx context.Context, p platform.Name) (id.ID, error)
}

// durationUnits are the units of the remaining time of a cooldown, the
// largest first (B24).
func durationUnits() []time.Duration {
	return []time.Duration{24 * time.Hour, time.Hour, time.Minute, time.Second}
}

// remaining returns d rounded up so that it has at most two units, the
// largest unit of d and the one below it (B24): 61 s stay 61 s, "1 minute
// 1 second", 3 601 s become 3 660 s, "1 hour 1 minute", and 86 399 s
// become a day. Below a minute it rounds up to whole seconds. A cooldown
// thus never ends later than the message says.
func remaining(d time.Duration) time.Duration {
	units := durationUnits()
	d = roundUp(d, units[len(units)-1])
	for i, u := range units[:len(units)-1] {
		if d >= u {
			return roundUp(d, units[i+1])
		}
	}
	return d
}

// roundUp rounds d up to a multiple of unit. Within one unit of the largest
// duration, where that does not fit, it rounds down instead.
func roundUp(d, unit time.Duration) time.Duration {
	r := d % unit
	if r == 0 {
		return d
	}
	if d > math.MaxInt64-unit {
		return d - r
	}
	return d - r + unit
}

// cooldownUser returns the user whose cooldown a run of the scopes per user
// counts against (B22): the user of the run, and in a run without a user
// the streamer on the platform of the run, without a platform on the
// default platform (B4).
func (s *Service) cooldownUser(ctx context.Context, p engine.Params) (id.ID, error) {
	if p.User != nil {
		return p.User.ID, nil
	}
	name := cmp.Or(p.Platform, platform.Default)
	userID, err := s.ports.Streamer.StreamerUser(ctx, name)
	if err != nil {
		return id.ID{}, fmt.Errorf("find the streamer on %s: %w", name, err)
	}
	return userID, nil
}

// cooldownKey returns the key of the cooldown r of cmd that a run p is
// checked against (B4, B20, B22).
func (s *Service) cooldownKey(ctx context.Context, cmd command.Command, r command.CooldownRequirement, p engine.Params) (command.CooldownKey, error) {
	var userID id.ID
	if r.Scope.PerUser() {
		var err error
		if userID, err = s.cooldownUser(ctx, p); err != nil {
			return command.CooldownKey{}, err
		}
	}
	return r.Key(cmd.ID, userID)
}

// checkCooldown returns the rejection of a run p while the cooldown r runs
// under key at now (B20 to B24). The message names the remaining time; for
// the scopes per user it addresses the user.
func (s *Service) checkCooldown(ctx context.Context, r command.CooldownRequirement, key command.CooldownKey, p engine.Params, now time.Time) (engine.Rejection, bool, error) {
	ends, ok, err := s.ports.Cooldowns.CooldownEnd(ctx, key)
	if err != nil {
		return engine.Rejection{}, false, err
	}
	if !ok || !now.Before(ends) {
		return engine.Rejection{}, false, nil
	}
	msg := i18n.KeyRequirementCooldownAll
	if r.Scope.PerUser() {
		msg = i18n.KeyRequirementCooldownUser
	}
	return engine.Rejection{
		Requirement: command.TypeCooldown,
		Reason: i18n.Message{Key: msg, Args: map[string]i18n.Value{
			"remaining": i18n.Duration(remaining(ends.Sub(now))),
		}},
		Tell: told(p),
	}, true, nil
}

// cooldownGroup returns the cooldown group groupID; ok is false if it does
// not exist.
func (s *Service) cooldownGroup(ctx context.Context, groupID id.ID) (command.CooldownGroup, bool, error) {
	groups, err := s.ports.Cooldowns.CooldownGroups(ctx)
	if err != nil {
		return command.CooldownGroup{}, false, fmt.Errorf("list cooldown groups: %w", err)
	}
	i := slices.IndexFunc(groups, func(g command.CooldownGroup) bool { return g.ID == groupID })
	if i < 0 {
		return command.CooldownGroup{}, false, nil
	}
	return groups[i], true, nil
}

// startCooldown starts the cooldown r under key at now (B21, B23): for the
// scopes standard and per_user as long as r says, for the grouped scopes as
// long as the cooldown group says now. It returns how to take the start
// back.
func (s *Service) startCooldown(ctx context.Context, r command.CooldownRequirement, key command.CooldownKey, now time.Time) (func(context.Context) error, error) {
	d := r.Duration.Std()
	if r.Scope.Grouped() {
		g, ok, err := s.cooldownGroup(ctx, r.Group)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("start cooldown: cooldown group %s does not exist", r.Group)
		}
		d = g.Duration
	}
	ends := now.Add(d)
	if err := s.ports.Cooldowns.PutCooldown(ctx, key, ends, now); err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.ports.Cooldowns.DeleteCooldown(ctx, key, ends)
	}, nil
}
