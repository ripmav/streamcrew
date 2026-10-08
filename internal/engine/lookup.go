// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

// userLookup finds users by login name for one trigger and the runs it
// leads to, including the commands they call (B17). It asks the users of
// the engine; an attempt that fails or takes longer than the time limit of
// the settings is tried again, as often as the settings say. A user it
// found holds for the whole run: the target, the requirements, the
// templates and the actions do not look up the same name again. A name it
// did not find is looked up again when asked. Its methods are safe for
// concurrent use.
type userLookup struct {
	users    Users
	attempts int
	timeout  time.Duration
	logger   *slog.Logger

	mu    sync.Mutex
	found map[lookupKey]user.User
}

// lookupKey is a login name on a platform, in lowercase.
type lookupKey struct {
	platform platform.Name
	name     string
}

// newLookup returns a lookup with the settings of cfg.
func (e *Engine) newLookup(cfg Config) *userLookup {
	return &userLookup{
		users:    e.users,
		attempts: cfg.Commands.UserLookupAttempts,
		timeout:  cfg.Commands.UserLookupTimeout.Std(),
		logger:   e.logger,
		found:    make(map[lookupKey]user.User),
	}
}

var _ Users = (*userLookup)(nil)

// UserByName implements Users. Without users of the engine it finds
// nobody. An error means every attempt failed, or ctx ended.
func (l *userLookup) UserByName(ctx context.Context, p platform.Name, name string) (user.User, bool, error) {
	if l.users == nil {
		return user.User{}, false, nil
	}
	key := lookupKey{platform: p, name: strings.ToLower(name)}
	l.mu.Lock()
	u, ok := l.found[key]
	l.mu.Unlock()
	if ok {
		return u, true, nil
	}
	var last error
	for attempt := 1; attempt <= l.attempts; attempt++ {
		u, ok, err := l.try(ctx, p, name)
		if err == nil {
			if ok {
				l.mu.Lock()
				l.found[key] = u
				l.mu.Unlock()
			}
			return u, ok, nil
		}
		if ctx.Err() != nil {
			return user.User{}, false, fmt.Errorf("look up user %q on %s: %w", name, p, context.Cause(ctx))
		}
		last = err
		if attempt < l.attempts {
			l.logger.WarnContext(ctx, "looking up a user failed, trying again",
				"platform", p, "attempt", attempt, "attempts", l.attempts, "error", err)
		}
	}
	return user.User{}, false, fmt.Errorf("look up user %q on %s after %d attempts: %w", name, p, l.attempts, last)
}

// try makes one attempt within the time limit.
func (l *userLookup) try(ctx context.Context, p platform.Name, name string) (user.User, bool, error) {
	actx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	return l.users.UserByName(actx, p, name)
}
