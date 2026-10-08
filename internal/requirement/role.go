// SPDX-License-Identifier: MIT

package requirement

import (
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/i18n"
)

// checkRole checks the minimum role (B4, B10 to B12) and returns the
// rejection if it is not met. The primary role of the user on the platform
// of the run must reach it (users-and-roles.md, B22 to B24); without a role
// requirement it is user, which only banned users miss. A run without a
// user is checked as the streamer, who meets every role (B4). Banned users
// are never told (B12).
func checkRole(cmd command.Command, p engine.Params) (engine.Rejection, bool) {
	minimum := role.User
	if r, ok := find[command.RoleRequirement](cmd); ok {
		minimum = r.Role
	}
	if p.User == nil {
		return engine.Rejection{}, false
	}
	roles := p.User.Roles(p.Platform)
	if roles.Meets(minimum) {
		return engine.Rejection{}, false
	}
	return roleRejection(minimum, roles.Primary() != role.Banned && told(p)), true
}

// roleRejection returns the rejection that names the minimum role (B12).
func roleRejection(minimum role.Role, tell bool) engine.Rejection {
	return engine.Rejection{
		Requirement: command.TypeRole,
		Reason:      i18n.Message{Key: i18n.KeyRequirementRole, Args: map[string]i18n.Value{"role": i18n.Text(string(minimum))}},
		Tell:        tell,
	}
}
