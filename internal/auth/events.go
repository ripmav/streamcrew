// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"time"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/event"
)

// ActionRequired is the payload of auth.action_required (ADR-0014): a
// login needs the user to complete it, so the frontends show the URL and
// the code.
type ActionRequired struct {
	// Platform and Role name the login.
	Platform platform.Name     `json:"platform"`
	Role     connector.Account `json:"role"`
	// URL is where the user enters the code.
	URL string `json:"url"`
	// Code is the code the user enters at the URL.
	Code string `json:"code"`
	// Expires is when the code expires.
	Expires time.Time `json:"expires_at"`
}

// LoginCompleted is the payload of auth.login_completed (ADR-0014): the
// user completed a login and the account is stored.
type LoginCompleted struct {
	// Platform and Role name the login.
	Platform platform.Name     `json:"platform"`
	Role     connector.Account `json:"role"`
	// Login is the display name of the account.
	Login string `json:"login"`
}

// LoginFailed is the payload of auth.login_failed (ADR-0014): a login did
// not come through.
type LoginFailed struct {
	// Platform and Role name the login.
	Platform platform.Name     `json:"platform"`
	Role     connector.Account `json:"role"`
	// Reason is why the login failed, in words the frontend can show.
	Reason string `json:"reason"`
}

// RegisterEvents adds the auth event types to the catalog (roadmap 4.1).
func RegisterEvents(c *event.Catalog) error {
	return errors.Join(
		event.Register[ActionRequired](c, eventtype.AuthActionRequired),
		event.Register[LoginCompleted](c, eventtype.AuthLoginCompleted),
		event.Register[LoginFailed](c, eventtype.AuthLoginFailed),
	)
}
