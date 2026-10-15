# Plan: Phase 5, Teil 5.2.3 – Titelregeln (Title rules)

Status: **abgeschlossen**

**Goal:** The streamer's title rules (users-and-roles.md, B5): a custom title per user, otherwise the first matching rule (name, minimum role, minimum months), otherwise "Kein Titel"/"No title" in the profile language.

## Scope

| # | Item | Source | Size |
|---|------|--------|------|
| 1 | `user.TitleRule` + matching (`User.DefaultTitle`), whole months from follow/subscription start | users-and-roles.md B5 | S |
| 2 | Settings: title rules in the section `users`; engine + scope plumbing; template `$usertitle` | users-and-roles.md B5, Q2 | S |
| 3 | i18n key for "Kein Titel"/"No title" in both catalogs | users-and-roles.md B5 | S |

## Design decisions

- **Rule shape:** `user.TitleRule{Name string, MinRole role.Role, MinMonths int}` in the domain; the settings section `users` stores `titleRules` as this type. A v1 document without the key decodes with the default empty list (absent slice field = nil), so the section stays at version 1.
- **Matching (B5):** the user's roles on the platform of the run (`User.Roles(p)`) must meet `MinRole` (`role.Set.Meets`), and the months must be at least `MinMonths`. Rules are checked in the order of the **highest minimum role first, then the most minimum months** (decision: "geprüft wird nach der höchsten Rolle, dann nach den meisten Monaten" = that order, first match wins).
- **Months start (B5):** a rule is a *subscriber rule* when its minimum role is `subscriber` or `youtube_member`; it counts from the identity's `SubscribedAt`. Every other rule counts from `FollowedAt` (decision: a rule with a higher minimum role, e.g. regular or moderator, counts from the follow, since every subscriber followed first). Zero (unreported) dates count as 0 months, so a rule with `minMonths` 0 matches regardless of reported dates.
- **Whole months:** `monthsBetween(start, now)` = full calendar months (year/month arithmetic, day adjustment), 0 when start is zero or future.
- **Order of precedence (B5, template):** the custom title of the user, then the first matching rule, then "Kein Titel". The fallback to the primary role name (2026-09-30 decision) is replaced by "Kein Titel" when the scope carries the localized text; scopes without it (unit tests) keep the role name.
- **i18n:** new key `user.no_title` ("No title" / "Kein Titel"), rendered by the app into `engine.Config.NoTitle` with the profile language; the template prints it as is.
- No new store queries: `User.Title` (custom title) already exists; the rules live in the settings.

## Files

- `internal/domain/user/title.go` (new): `TitleRule`, `DefaultTitle`, `monthsBetween`; tests.
- `internal/settings/settings.go`: `Users.TitleRules []user.TitleRule`, validation (name non-empty single line, known role, months ≥ 0); tests.
- `internal/i18n/keys.go` + `locales/en.json` + `locales/de.json`: `user.no_title`.
- `internal/engine`: `Config.TitleRules` + `Config.NoTitle`, passed to the scope; `template.Scope.TitleRules` + `Scope.NoTitle`.
- `internal/template/userprops.go`: the `title` property per the precedence above; tests.
- `internal/app/wiring.go`: `engineConfig` loads the section `users` and renders the no-title text (the app keeps the message catalog).
- `docs/roadmap.md` 5.2.3, `docs/spec/users-and-roles.md` (B5, acceptance, change history), this plan.

## Tests

| Test | Level |
|------|-------|
| `DefaultTitle`: custom-title precedence is the template's job; rule by role; ordering highest role then most months; months from follow vs subscription start; 0 months with zero dates; no match | unit (domain) |
| `monthsBetween`: full months, day adjustment, zero/future start | unit (domain) |
| Settings: v1 document without `titleRules` decodes (migration), save/load round-trip, validation | unit |
| Template: custom title wins; matching rule; no match → "No title"; empty `NoTitle` → role name (legacy) | unit |

## Open questions

(none – decisions documented above)
