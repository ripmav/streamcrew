// SPDX-License-Identifier: MIT

package api

import (
	"strings"

	"github.com/ripmav/streamcrew/internal/apitoken"
)

// scopeForMethod is the scope required per service and method (roadmap
// 6.2.2). A method that is not listed requires admin (fail-closed).
//
//nolint:gochecknoglobals // the static scope table, never mutated
var scopeForMethod = map[string]apitoken.Scope{
	// SystemService.
	"SystemService/Status":       apitoken.Read,
	"SystemService/ListProfiles": apitoken.Read,
	"SystemService/Shutdown":     apitoken.Admin,

	// CounterService.
	"CounterService/List":   apitoken.Read,
	"CounterService/Get":    apitoken.Read,
	"CounterService/Create": apitoken.Admin,
	"CounterService/Update": apitoken.Admin,
	"CounterService/Set":    apitoken.Control,
	"CounterService/Add":    apitoken.Control,
	"CounterService/Reset":  apitoken.Admin,
	"CounterService/Delete": apitoken.Admin,

	// SettingsService.
	"SettingsService/ListSections": apitoken.Read,
	"SettingsService/Get":          apitoken.Read,
	"SettingsService/Update":       apitoken.Admin,

	// UserService.
	"UserService/List":     apitoken.Read,
	"UserService/Get":      apitoken.Read,
	"UserService/Update":   apitoken.Admin,
	"UserService/Merge":    apitoken.Admin,
	"UserService/Rankings": apitoken.Read,

	// ChatService.
	"ChatService/Send":    apitoken.Control,
	"ChatService/Delete":  apitoken.Control,
	"ChatService/Clear":   apitoken.Control,
	"ChatService/History": apitoken.Read,

	// BackupService.
	"BackupService/Create":  apitoken.Admin,
	"BackupService/List":    apitoken.Read,
	"BackupService/Restore": apitoken.Admin,

	// AuthService.
	"AuthService/ListAccounts": apitoken.Read,
	"AuthService/StartLogin":   apitoken.Admin,
	"AuthService/CancelLogin":  apitoken.Admin,
	"AuthService/Logout":       apitoken.Admin,

	// CommandService.
	"CommandService/List":           apitoken.Read,
	"CommandService/Get":            apitoken.Read,
	"CommandService/Create":         apitoken.Admin,
	"CommandService/Update":         apitoken.Admin,
	"CommandService/Delete":         apitoken.Admin,
	"CommandService/Run":            apitoken.Control,
	"CommandService/ListInstances":  apitoken.Read,
	"CommandService/PauseQueue":     apitoken.Control,
	"CommandService/CancelInstance": apitoken.Control,
	"CommandService/RetryInstance":  apitoken.Control,
	"CommandService/TypeCatalog":    apitoken.Read,
	"CommandService/Import":         apitoken.Admin,
	"CommandService/Export":         apitoken.Read,

	// StreamService.
	"StreamService/Subscribe":    apitoken.Read,
	"StreamService/AnswerPrompt": apitoken.Control,
}

// requiredScope reports the scope the path requires. The paths are of the
// form /streamcrew.v1alpha1.<Service>/<Method>; anything else requires
// admin (fail-closed).
func requiredScope(path string) apitoken.Scope {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) != 2 {
		return apitoken.Admin
	}
	service := parts[0]
	if i := strings.LastIndex(service, "."); i >= 0 {
		service = service[i+1:]
	}
	if scope, ok := scopeForMethod[service+"/"+parts[1]]; ok {
		return scope
	}
	return apitoken.Admin
}
