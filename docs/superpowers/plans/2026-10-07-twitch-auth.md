# Plan: Roadmap 4.1 — Twitch-Authentifizierung

**Status:** zur Ausführung bereit
**Stand:** 2026-10-07, `main` bei `84a1ec7` (Phase 3.6 gemerged, Stack #132)
**Scope:** nur 4.1. 4.2 (Helix), 4.3 (EventSub) und 4.4 (Funktionen) bleiben offen;
dieser Plan legt nur die Fugen, die sie brauchen (`auth.Token`-Port, Ereignistypen).

## 1. Was 4.1 liefert (Roadmap)

- [ ] Twitch-App registrieren (öffentlicher Client, Device Code Flow) (S) — **Nutzer-Aufgabe, extern**
- [ ] ADR-0014 OAuth und App-Credentials, inkl. BYO-Option für alle Plattformen (S)
- [ ] `internal/auth` (M): Device Code Flow mit `golang.org/x/oauth2`, verschlüsselter
      Token-Speicher, Refresh nach Ablaufzeit, Scope-Abgleich („Anmeldung erforderlich“),
      Widerruf beim Abmelden
- [ ] Streamer- und Bot-Konto; `auth login twitch [--bot]`, `auth status`, `auth logout` (S)
- [ ] Auth-Aufforderungen (URL, Code, Ablauf) als Ereignisse für Frontends (S)

## 2. Befunde der Recherche (verifiziert 2026-10-07)

1. **`golang.org/x/oauth2` hat Device Code Flow eingebaut** (seit v0.13, aktuell v0.37.0):
   - `Config.DeviceAuth(ctx, opts) (*DeviceAuthResponse, error)` — POST an `Endpoint.DeviceAuthURL`,
     `client_id` und `scope` (raumgetrennt) **im POST-Body** (passt zur Twitch-Forderung).
   - `Config.DeviceAccessToken(ctx, da, opts) (*Token, error)` — pollt intern
     (`da.Interval`, Default 5 s), behandelt `slow_down` (+5 s) und `authorization_pending`,
     gibt bei `access_denied`/`expired_token` sofort zurück und setzt die Context-Deadline
     auf `da.Expiry` (Ablauf = `ctx.DeadlineExceeded` — wir mappen das auf
     „Device-Code abgelaufen“).
   - **Achtung:** Bei Erfolg ist `Token.Expiry` noch **nicht** gesetzt —
     `Expiry = Now + ExpiresIn` müssen wir selbst machen.
   - `x/oauth2/twitch.Endpoint` kennt nur `AuthURL` + `TokenURL`; `DeviceAuthURL`
     (`https://id.twitch.tv/oauth2/device`) tragen wir selbst im Endpunkt.
2. **Twitch-DCF (dokumentiert, dev.twitch.tv/docs/authentication):**
   - `POST https://id.twitch.tv/oauth2/device` → `device_code`, `user_code`,
     `verification_uri`, `interval` (5), `expires_in` (1800).
   - Token: `POST https://id.twitch.tv/oauth2/token` mit
     `grant_type=urn:ietf:params:oauth:grant-type:device_code`.
   - Refresh: gleicher Endpunkt, `grant_type=refresh_token` — **öffentlicher Client:
     nur `client_id`, kein Secret**. Refresh-Tokens verfallen nach 30 Tagen Inaktivität.
   - Widerruf: `POST https://id.twitch.tv/oauth2/revoke` mit `client_id` + `token`.
   - Nutzer zu Token nachlesen: Helix `GET https://api.twitch.tv/helix/users`
     (Bearer) → `id`, `login` (ein Aufruf ohne Argument = das eigene Konto).
3. **PKCE:** Twitch dokumentiert für den DCF kein `code_challenge` (Foren-Belege,
   Stand 2026: Twitch unterstützt PKCE für öffentliche Clients nicht). Der Device-Code
   selbst ist das Geheimnis (nie sichtbar, nur TLS). → ADR-Entscheidung: **DCF ohne PKCE**.
4. **Bestand im Repo (Wiederverwendung):**
   - `internal/vault` (AES-256-GCM, Schlüssel via `vault.NewKeys`:
     `STREAMCREW_SECRET_KEY` → Keyring → Key-Datei; ADR-0012) + `store.Secret`-Tabellen
     → Token-Speicher ist damit fast fertig; nur die Namen und das JSON-Format fehlen.
   - `store.OpenReadOnly` (für `auth status` ohne Lock), `app.LockDataDir`,
     `app.PreMigrationBackup`, `cli.Env.profiles/lock/profileID` (in `internal/cli/data.go`)
     → CLI-Commands öffnen Profile wie `backup`/`profile`, nicht wie `serve`.
   - `settings.Service` (polydoc-Sektionen) — für 4.1 **nicht** nötig; Accounts kommen
     in eine eigene Tabelle (Schema-Skizze Plan §6.13: `accounts` + `secrets`).
   - `internal/domain/eventtype` (Katalog-Konstanten) + `event.Register[T]` +
     `app.newCatalog()` (in `internal/app/data.go`) → Ort für die `auth.*`-Typen.
   - `supervisor` (Runnable) für den Refresh-Loop; `component(logger, "auth")`-Konvention.
   - `netguard.Dialer` wacht nur die Netzwerk-Actions (Server-Modus-Allowlist);
     der Auth-HTTP-Client bekommt ihn als Port übergeben (ADR-Entscheidung, s. u.).
   - `connector.Account` (`AccountStreamer`/`AccountBot`) und
     `internal/domain/platform.Name` (`platform.Twitch`) → bestehende Typen, keine neuen.
5. **x/oauth2 Refresh-Mechanik:** `Config{ClientID, Endpoint}.TokenSource(ctx, t)`
   refresh-t mit `client_id` im Body (kein Secret vorhanden → kein Basic-Auth-Versuch);
   für den expliziten Refresh und den Widerruf schreiben wir eigene Funktionen
   (`Refresh`, `Revoke`), da x/oauth2 weder Refresh mit fester `client_id`-Garantie
   noch Revoke kennt.

## 3. Design (Details werden in ADR-0014 festgeschrieben)

**Paket `internal/auth`** (neu):

```
internal/auth/
  auth.go       // Service, Ports, Status, Token-Port, Run (Refresh-Loop)
  account.go    // Account, Token (Vault-JSON), Vault-Namen, Scope-Abgleich
  flow.go       // Flow-Interface (Abstraktion des DCF für Tests), Prompt
  events.go     // Typen + Nutzlasten + RegisterEvents
  twitch.go     // Twitch-Endpunkte, Pflicht-Scopes, Flow-Implementation, User-Lookup
  *_test.go
```

- **`Flow`-Interface** (für Tests, echte Implementierung `twitchFlow`):
  `Start(ctx) (oauth2.DeviceAuthResponse, error)`,
  `Wait(ctx, da) (*oauth2.Token, error)`,
  `Refresh(ctx, refresh string) (*oauth2.Token, error)`,
  `Revoke(ctx, token string) error`, `User(ctx, token string) (id, login string, err error)`.
- **Speicher:**
  - Tabelle `accounts` (Migration `0016_accounts.sql`):
    `(platform, role, login, user_id, scopes, client_id, updated_at)`,
    `PRIMARY KEY (platform, role)`, `role CHECK IN ('streamer','bot')`.
  - Tokens im Vault, Name `auth/<platform>/<role>`, JSON
    `{"access_token":…, "refresh_token":…, "expires_at":…}` (UTC, RFC3339).
- **Refresh:** `auth.Service` ist ein (nicht-kritisches) Supervisor-Runnable:
  Tick 60 s (injizierbar), Refresh wenn `expires_at − now < 15 min`.
  Zusätzlich lazies `Token(ctx, platform, role) (string, error)`-Port für die
  Adapter ab 4.2 (refresh-t auf Abruf, wenn abgelaufen).
- **Scope-Abgleich:** `Scopes` (Pflichtmenge, Konstante in `twitch.go`) vs.
  gespeicherte `scopes` (aus der Token-Antwort, `scope`-Feld, raumgetrennt).
  Fehlend → Status „Anmeldung erforderlich“ (keine modalen Dialoge; Plan §6.12).
- **Ereignisse** (im Katalog, neutral wie `app.started`; Nutzlasten in `internal/auth`):
  - `auth.action_required`: `{platform, role, url, code, expires_at}`
  - `auth.login_completed`: `{platform, role, login}`
  - `auth.login_failed`: `{platform, role, reason}`
- **CLI:**
  ```
  streamcrew auth login <platform> [--bot] [--client-id ID]   # blockiert bis Erfolg/Ablauf/Abbruch
  streamcrew auth status [--platform P] [-o text|json]        # read-only, ohne Lock
  streamcrew auth logout <platform> [--bot] [--client-id ID]  # revoke + löschen
  ```
  `login`/`logout` nehmen die Daten-Verzeichnis-Sperre (Core muss stehen,
  wie `profile`/`backup`); `status` nutzt `store.OpenReadOnly` und darf
  neben einem laufenden Core laufen.
- **App-Verdrahtung:** `app.New` baut die `auth.Service` (Store, Vault, Bus, Logger,
  HTTP-Client) und adds sie als Runnable `auth`; `newCatalog()` registriert die Typen.
- **BYO / Client-ID:** Die Client-ID des Projekts ist eine Konstante
  (`twitch.ClientID`, öffentlich, kein Secret). Override per `--client-id`
  (CLI) bzw. später Config-Option; BYO-`client_secret` (für andere Plattformen)
  gehört ins Vault — das Schema (Tabelle/Name) legt die ADR fest, 4.1 nutzt es
  für Twitch nicht.
- **Server-Modus:** Der HTTP-Client der Auth-Service wird als Port übergeben;
  in Produktion ist er mit `netguard.Dialer` (Outbound-Allowlist, Code-ADR-0019)
  verbunden, im lokalen Modus und in Tests ist er frei. Konsequenz für die Doku:
  im Server-Modus müssen `id.twitch.tv` und `api.twitch.tv` freigegeben sein
  (`--outbound-allow`).

## 4. Konventionen (gelten für alle Tasks)

- **Zweig pro Task**, nie auf `main`; Namensschema `docs/…`, `feat/…`
  (Kleinbuchstaben, Bindestriche). Zweige bauen aufeinander (Stack).
- Vor **jedem** Commit `scripts/check.sh` (gofmt, lint, Tests in Sandbox).
- Commits **englisch**, Conventional Commits, **GPG-signiert**
  (`commit.gpgsign=true`; Agent-Cache prüfen:
  `echo test | gpg --clearsign >/dev/null` — bei Ablauf: Nutzer entriegelt).
  Footer in jedem Commit: `Assisted-by: Qwen3.8-27B (xHigh) via OpenCode`
- **PRs:** Titel englisch, Body deutsch mit den Abschnitten
  `## Worum geht es`, `## Änderungen`, `## Bitte prüfen (meine Festlegungen)`, `## Tests`.
- **GitHub-Stack:** Neu für Phase 4.1 (zuerst `GET /repos/ripmav/streamcrew/stacks`
  gegenprüfen; es gibt keinen offenen Stack für 4.1). Stack erstellen (z. B.
  `gh stack init <zweig>` + `gh stack submit` oder UI) und **jedes** PR nachträglich
  per `POST /repos/ripmav/streamcrew/stacks/{n}/add` mit
  `{"pull_requests":[…]} ` (geordnet vom Stack-Top aufwärts) registrieren —
  `gh pr create --base` reicht nicht.
- Roadmap: erledigte Punkte sofort abhaken + Änderungshistorie pflegen
  (je PR einen Eintrag, Datum, Inhalt, Belege).

## 5. Tasks

### Task 1 — ADR-0014 (Docs-PR, unterstes PR im Stack)

**Status:** erledigt, PR #144
**Zweig:** `docs/oauth-adr` (von `main`)
**PR:** `docs(adr): OAuth and app credentials (ADR-0014)`

**Dateien:**
- neu `docs/adr/0014-oauth-und-app-credentials.md` (Format: `docs/adr/TEMPLATE.md`)
- `docs/adr/README.md` — Index um 0014 ergänzen (Akzeptiert, Datum)
- `docs/plan.md` §12.1 — Zeile 0014: Status „**akzeptiert**: DCF (Twitch) ohne PKCE,
  öffentlicher Client; Tokens AES-256-GCM im Vault, Accounts in SQLite; BYO
  Client-ID (Twitch ohne Secret), Secret für andere Plattformen im Vault“
- Plan-Datei dieses Dokuments wird in das PR eingecheckt
  (Repo-Konvention kennt sonst keinen plans-Ordner; auf Wunsch lösbar)

**Inhalt der ADR (Kontext / Entscheidung / Alternativen / Konsequenzen):**
1. Flows: DCF (RFC 8628) für Twitch über `golang.org/x/oauth2`
   (`DeviceAuth`/`DeviceAccessToken`), **ohne PKCE** (Twitch dokumentiert
   `code_challenge` nicht; Begründung s. §2.3). Authorization-Code + PKCE
   mit Loopback bleibt für spätere Plattformen (Notiz, nicht umgesetzt).
2. Öffentlicher Client ohne Secret; Projekt-Client-ID als Konstante,
   übersteuerbar (`--client-id`, später Config). BYO: Client-ID pro Plattform
   (nichts Geheimes), Client-Secret nur im Vault (YouTube/Kick, später).
3. Endpunkte (alle vier, inkl. Revoke und Helix-User-Lookup).
4. Token-Speicher: Vault-Name `auth/<platform>/<role>`, JSON-Format,
   Account-Metadaten in `accounts` (Tabelle nach Plan-Skizze §6.13).
5. Refresh: Vorlauf-Refresh im Core (15 min vor Ablauf, Tick 60 s) +
   lazies `Token()`-Port; 30-Tage-Inaktivität des Refresh-Tokens →
   „Anmeldung erforderlich“.
6. Pflicht-Scopes von Twitch (Tabelle Scope → Feature 4.2–4.4, **Entwurf unten
   in §6 finalisieren** — gegen die Twitch-API-Doku prüfen, nur Endpunkte,
   die streamcrew nutzt).
7. Scope-Abgleich: fehlende Scopes → Status „Anmeldung erforderlich“
   (Re-Login), kein Dialog.
8. Widerruf: `POST /oauth2/revoke` beim Logout, dann Account + Vault-Eintrag löschen.
9. Ereignisse: `auth.action_required`, `auth.login_completed`, `auth.login_failed`
   (Nutzlasten), im App-Katalog, dokumentiert in `events.md`.
10. Server-Modus: Auth-HTTP durch den `netguard.Dialer` der Outbound-Allowlist
    (Code-ADR-0019); Doku: `id.twitch.tv`, `api.twitch.tv` freigeben.
11. Alternativen: Authorization-Code-Flow mit Loopback-Redirect (warum nicht:
    Headless-Kern ohne Browser-Port; DCF ist für genau diesen Fall vorgesehen),
    Client-Credentials-Flow (warum nicht: braucht User-Scopes),
    eigener DCF-Client statt x/oauth2 (warum nicht: Bibliothek macht
    Polling/slow_down/Expiry fertig).

**Tests:** doc-only PR → CI „Internal links“. ADR-Format manuell prüfen
(Tabelle im Kopf, relative Links, nur existierende ADRs verlinkt).
**Commit:** `docs(adr): OAuth and app credentials (ADR-0014)`

### Task 2 — `accounts`-Tabelle im Store

**Status:** erledigt, PR #145
**Zweig:** `feat/auth-accounts-store` (auf `docs/oauth-adr`)
**PR:** `feat(store): accounts table for platform logins`

**Dateien:**
- neu `internal/store/migrations/0016_accounts.sql`
- neu `internal/store/queries/accounts.sql`
- regeneriert `internal/store/sqlcgen` (`go generate ./internal/store/...`)
- neu `internal/store/accounts.go` + `internal/store/accounts_test.go`

**Migration (Muster: `0015_stream_sessions.sql`):**
```sql
-- +goose Up
CREATE TABLE accounts (
    platform   TEXT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('streamer', 'bot')),
    login      TEXT NOT NULL,
    user_id    TEXT NOT NULL,
    scopes     TEXT NOT NULL,  -- granted scopes, space separated
    client_id  TEXT NOT NULL,
    updated_at INTEGER NOT NULL, -- Unix milliseconds, UTC (Code-ADR-0009)
    PRIMARY KEY (platform, role)
) STRICT;
-- +goose Down
DROP TABLE accounts;
```

**Queries** (Muster: `queries/users.sql`): `UpsertAccount`, `GetAccount(platform, role)`,
`ListAccounts`, `DeleteAccount(platform, role)`.

**`accounts.go`:** Typ `Account{Platform platform.Name; Role connector.Account? …}` —
**Festlegung:** Der Store bleibt dependency-arm: `Platform string`, `Role string`
(mapping nach `platform.Name`/`connector.Account` passiert in `internal/auth`),
Zeiten als `time.Time` (UnixMilli, Code-ADR-0009). `Store.UpsertAccount`,
`Store.Account(ctx, platform, role) (Account, found bool, err)`,
`Store.Accounts(ctx) ([]Account, error)`, `Store.DeleteAccount`.
Fehler: `ErrNotFound`-Semantik wie elsewhere (`sql.ErrNoRows` → `found=false`).

**Tests:** Migration up/down (Muster `store_test.go`/`store_internal_test.go`),
CRUD inkl. Upsert-Semantik (erneuter Login ersetzt, `updated_at` neu) und
`role`-CHECK (falsche Rolle → Fehler).
**Commit:** `feat(store): accounts table for platform logins`
**Roadmap:** keine eigene Zeile (interne Aufgabe); Historie-Notiz in Task 4/5.

### Task 3 — `internal/auth`: Twitch-Device-Code-Flow

**Status:** erledigt (Stack #146)
**Zweig:** `feat/auth-device-flow` (auf `feat/auth-accounts-store`)
**PR:** `feat(auth): Twitch device code flow`

**Dateien:** neu `internal/auth/{flow.go, twitch.go, twitch_test.go, scopes_test.go}`

**`flow.go`:** Interface `Flow` (s. §3), `Prompt{URL, Code, Expiry}`
(geleitet aus `oauth2.DeviceAuthResponse`: `VerificationURI`, `UserCode`, `Expiry`).

**`twitch.go`:**
- `const ClientID = "…"` (Client-ID der registrierten App; **Platzhalter,
  Task 0 aus §7** — PR erst nach Client-ID bekannt, sonst Konstante
  mit `// TODO(4.1): set the registered client ID` und `--client-id`-Pflicht).
- Endpunkten: `deviceURL`, `tokenURL`, `revokeURL`, `helixUsersURL`
  (https://id.twitch.tv/oauth2/{device,token,revoke}, https://api.twitch.tv/helix/users).
- `Scopes []string` — die Pflicht-Scopes (Liste aus ADR-0014, §6).
- `twitchFlow{clientID, client *http.Client}` (Client via `oauth2.HTTPClient`
  im Context; Default-Timeout 30 s):
  - `Start`: `oauth2.Config{ClientID, Scopes, Endpoint{DeviceAuthURL: deviceURL, TokenURL: tokenURL}}`
    → `DeviceAuth` → `Prompt`.
  - `Wait`: `DeviceAccessToken` → Token; `token.Expiry = Now + ExpiresIn`;
    Fehlermappings: `ctx.DeadlineExceeded` → `ErrCodeExpired`,
    `*oauth2.RetrieveError` mit `access_denied` → `ErrAccessDenied`,
    `expired_token` → `ErrCodeExpired`, sonst verpacken.
  - `Refresh`: POST `tokenURL` `grant_type=refresh_token&client_id=…&refresh_token=…`
    (form-Body; `client_secret` nie senden) → neue Token, `Expiry` setzen,
    `scope` aus Antwort behalten.
  - `Revoke`: POST `revokeURL` `client_id=…&token=…`; 200/400 beides ok
    (Twitch antwortet 400 auf bereits widerrufene Tokens — ADR-Konsequenz),
    4xx/5xx sonst Fehler.
  - `User`: GET `helixUsersURL` mit Bearer → erstes Element: `id`, `login`.
- Scope-Prüfung: `Scopes` + `func Missing(have string, want []string) []string`
  (haben = raumgetrennter String aus der Token-Antwort).

**Tests (`twitch_test.go`):** `httptest`-Server als Fake-OAuth-Server
(Endpunkte injizierbar):
- `Start` → Prompt (URL/Code/Ablauf korrekt, `scope` im Body, raumgetrennt)
- `Wait`: pending → success (inkl. `Expiry`-Setzung), `slow_down` verlangsamt
  (Clock-Fake oder kurze Intervalle), `access_denied` → `ErrAccessDenied`,
  Ablauf → `ErrCodeExpired`
- `Refresh`: ok + `client_secret` wird nie gesendet (Server prüft das)
- `Revoke`: 200 ok, 400 ok, 500 Fehler
- `User`: id + login; leere Liste → Fehler
- `Missing`: Unter-/Übermenge, Reihenfolge egal, Leer-Zeichen tolerant.
**Commit:** `feat(auth): Twitch device code flow`

### Task 4 — `internal/auth`: Service, Token-Speicher, Refresh, Ereignisse

**Status:** erledigt, PR #148
**Zweig:** `feat/auth-service` (auf `feat/auth-device-flow`)
**PR:** `feat(auth): accounts, encrypted tokens and the refresh loop`

**Dateien:** neu `internal/auth/{auth.go, account.go, events.go}` + Tests;
neu `internal/domain/eventtype` (3 Konstanten);
`internal/app/data.go` (`newCatalog`: `auth.RegisterEvents`).

**`account.go`:** `Account` (Domain-Form: `Platform platform.Name`,
`Role connector.Account`, `Login`, `UserID`, `Scopes []string`, `ClientID`,
`UpdatedAt`), Mapping Store↔Domain; `Token{AccessToken, RefreshToken, ExpiresAt}`
mit JSON (RFC3339 UTC); Vault-Name `authName(platform, role)`.

**`events.go`:** Typ-Konstanten in `internal/domain/eventtype`
(`AuthActionRequired = "auth.action_required"`, `AuthLoginCompleted`,
`AuthLoginFailed`) wie `AppStarted`; Nutzlast-Strukturen in `internal/auth`
(s. §3); `RegisterEvents(c *event.Catalog) error`.

**`auth.go`:**
- `Ports{Store, Vault, Flows func(platform platform.Name, clientID string) (Flow, error),
  Publisher event.Publisher? (nil in Tests/CLI-ohne-Bus), Logger, Clock}` —
  **Festlegung:** Publisher ist das minimale Interface
  `Publish(ctx, event.Type, any)` aus `internal/event` (wie die Adapter es
  schon nutzen — bei Implementierung gegen `internal/event` prüfen,
  dort heißt es `Publisher` bzw. `event.Publisher`; exakten Namen übernehmen).
- `Service`:
  - `StartLogin(ctx, platform, role, clientID) (Prompt, error)` —
    Flow.Start + `auth.action_required` publizieren + Prompt zurückgeben.
  - `WaitLogin(ctx, da, platform, role, clientID) (Account, error)` —
    Flow.Wait + Flow.User + Account + Token **atomar** speichern
    (`store.Atomically`: UpsertAccount + Vault.Put; bei Fehler beides zurückrollen,
    ADR-0012/Code-ADR-0008) + `auth.login_completed` (bzw. `auth.login_failed`
    mit Reason bei Fehler) publizieren.
  - `Status(ctx) ([]Status, error)` — je Account: verbunden?, Login, UserID,
    gewählte/fehlende Scopes (`Missing`), Token-Ablauf,
    `State`-Ziel: `ok` | `login_required` (kein Account | Refresh-Token
    abgelaufen/leer | Scopes fehlen) — **klare Signale, keine magischen
    Strings (Code-ADR-0017)**: benannter `State`-Typ.
  - `Logout(ctx, platform, role, clientID) error` — Token laden,
    Flow.Revoke (Fehler loggen, aber löschen trotzdem, wenn Revoke scheitert?
    **Festlegung:** Revoke-Fehler ist ein Fehler — der Nutzer kann neu
    abmelden; Vault-Eintrag und Account nur nach erfolgreichem Revoke löschen;
    Revoke-400 (bereits widerrufen) zählt als Erfolg).
  - `Token(ctx, platform, role) (string, error)` — gültiger Access-Token
    (lazy-Refresh bei Ablauf/fehlendem Account → „Anmeldung erforderlich“-Fehler).
  - `Run(ctx)` — Runnable: Tick (Default 60 s, `WithTick`-Option für Tests),
    bei jedem Tick alle Accounts, Refresh wenn `expires_at − now < 15 min`;
    Fehler loggen (kein Abbruch), Refresh-Token abgelaufen → Log +
    `auth.login_failed` (Reason „token expired“).
- Fehler-Typen (Code-ADR-0003): `ErrNoAccount`, `ErrLoginRequired`,
  `ErrCodeExpired`, `ErrAccessDenied` — benannt, wrap-pflichtig.

**App-Verdrahtung** (`internal/app/app.go`):
- `New`: nach Vault-Bau: `a.auth, err = auth.New(auth.Ports{…})`
  (HTTP-Client: `&http.Client{Timeout: 30s}`; im Server-Modus mit
  `netguard.Dialer` aus `cfg.Rights()` — wie `action/network` es bekommt),
  `newCatalog()` += `auth.RegisterEvents(catalog)` (in `data.go`),
  `a.sup.Add("auth", a.auth)` (nicht kritisch, wie `backup`).
- `a.auth`-Feld in `App` (für spätere Plattformen; Getter nicht nötig in 4.1).

**Tests:**
- `account.go`: Vault-Name, JSON round-trip, Scope-Mapping.
- `auth.go`: mit Fake-Flow + echtem Store+Vault in Temp-Dir (Muster
  `internal/app`/`internal/vault`-Tests): Login-Erfolg speichert Account+Token
  atomar (bei Store-Fehler bleibt Vault leer), fehlgeschlagener Login publiziert
  `auth.login_failed`, `Status` (ok / fehlende Scopes / kein Account),
  `Token` (gültig, abgelaufen → Refresh-Call, kein Account → `ErrNoAccount`),
  `Logout` (Revoke aufgerufen, beide Einträge weg; Revoke-Fehler → bleibt),
  `Run`-Loop (Clock-Fake: Refresh vor Ablauf, kein Refresh außerhalb des Fensters,
  abgelaufener Refresh-Token → `auth.login_failed`).
- Katalog-Test: alle drei Typen sind registriert (Muster `newCatalog`-Test).
**Commit:** `feat(auth): accounts, encrypted tokens and the refresh loop`
**Roadmap:** abhaken „`internal/auth` (M): …“ + „Auth-Aufforderungen … als
Ereignisse“ (mit Belegen), Historie-Notiz.

### Task 5 — CLI `auth` + E2E-Test

**Status:** erledigt, PR #149
**Zweig:** `feat/auth-cli` (auf `feat/auth-service`)
**PR:** `feat(cli): auth login, status and logout`

**Dateien:** neu `internal/cli/auth.go` + `internal/cli/auth_test.go`;
`internal/cli/commands.go` (`Root.Auth authCmd`).

**`auth.go`:** (Muster: `data.go`/`backupCmd` für Profil-Öffnung,
`commands.go` für Exit-Codes; `output`-Struktur für `--output`)
```go
type authCmd struct {
    Login  authLoginCmd  `cmd:"" help:"Log in a Twitch account by the device code flow."`
    Status authStatusCmd `cmd:"" help:"Show the logged-in accounts and their state."`
    Logout authLogoutCmd `cmd:"" help:"Revoke the token and remove the account."`
}
type authLoginCmd struct {
    Platform string `arg:"" enum:"twitch" help:"The platform to log in: ${enum}."`
    Bot      bool   `help:"Log in the bot account instead of the streamer account."`
    ClientID string `env:"-" help:"Client ID of a Twitch app instead of the project app (development, BYO)." default:""`
}
```
- `login.Run`: `e.resolve()` → `e.lock` (Fehler-Meldung bei laufendem Core
  bleibt die von `LockDataDir`) → Profil öffnen (`e.profiles`, `e.profileID`) →
  Store + Keys + Vault bauen (wie `app.New`, ohne den Core) →
  `auth.Service` mit Terminal-Publisher (publiziert nichts, gibt den Prompt
  direkt aus):
  ```
  twitch: open https://www.twitch.tv/activate and enter the code ABCD-EFGH
  (the code expires in 30m)
  ```
  → `WaitLogin` auf dem Prozess-Context (SIGINT bricht ab, Meldung
  „abgebrochen“, Exit 1) → Erfolg: `twitch: logged in as <login> (streamer)`,
  Exit 0. Fehler (abgelaufen/abgelehnt): Meldung + Exit 1
  (`reportedError` nur, wenn die Meldung schon da ist).
- `status.Run`: **ohne** Lock, `store.OpenReadOnly` (Muster `snapshot` in
  `data.go`) → `auth.Service.Status` → Tabelle
  (platform, role, login, state, scopes/missing, expires) als Text
  (`tabwriter`) oder JSON; ohne Accounts: `no accounts` / leeres JSON-Array.
- `logout.Run`: wie `login` (Lock, Store, Vault) → `auth.Service.Logout` →
  `twitch: removed <login> (bot)`.

**Tests (`auth_test.go`):** Muster `mock_test.go`/`cli_test.go`:
- Kong-Struktur: `auth login twitch --bot` parst; falsche Plattform → Usage (Exit 2).
- `login` E2E: Fake-OAuth-Server (aus Task 3) + Temp-Profil → Prompt-Text
  (Golden), danach Account + Token im Store/Vault (direkt prüfen),
  `--bot` → Rolle `bot`; Abbruch per Context → Exit 1.
- `status`: leeres Profil; mit Account (ok + fehlende Scopes); Text-Golden +
  JSON; läuft **ohne** Lock (Lock-Datei gelegt → trotzdem ok).
- `logout`: Revoke-Call (Fake-Server zählt), Account+Vault weg;
  Revoke-Fehler → bleibt stehen, Exit 1.
**Commit:** `feat(cli): auth login, status and logout`
**Roadmap:** abhaken „Streamer- und Bot-Konto; `auth login twitch [--bot]`,
`auth status`, `auth logout` (S)“ + Historie.

### Task 6 — Doku: `events.md`, README, Roadmap-Aufräumen (Docs-PR, oberstes PR)

**Zweig:** `docs/auth-events` (auf `feat/auth-cli`)
**PR:** `docs: auth events, commands and roadmap 4.1`

**Dateien:**
- `docs/spec/events.md`: Abschnitt „Anwendungsereignisse“ (wo `app.started`/
  `app.stopping` stehen) um die drei `auth.*`-Ereignisse ergänzen:
  Nutzlast-Tabelle wie die anderen (Felder, Typen, Bedeutung),
  Änderungshistorie der Spec.
- `README.md`: CLI-Abschnitt um die `auth`-Befehle (ein Absatz wie
  `command validate`-Beschreibung) + Statusparagraf: 4.1-Anker.
- `docs/roadmap.md`: letzter 4.1-Punkt abhaken (soweit nicht in Task 4/5),
  Änderungshistorie: fehlende Zeilen (ADR, Store, Service, CLI) in der
  Reihenfolge der PRs; Phase-4-Zeile im Status bleibt „offen“
  (nur 4.1 von 4.1–4.5).
- `docs/adr/README.md`/`plan.md`: nichts (schon Task 1).

**Tests:** doc-only → „Internal links“.
**Commit:** `docs: auth events, commands and roadmap 4.1`

## 6. Scope-Liste

**Finalisiert in ADR-0014** (Tabelle dort, Stand der offiziellen Scope-Referenz
2026-10-07). Abweichungen vom Entwurf: Twitch hat die Moderator-Scopes fein
gekörnt (`moderator:read:banned_users`, `moderator:manage:warnings`,
`moderator:manage:chat_messages`, `moderator:read:chatters` u. a.); Chat-Lese-
und -Schreibzugriffe laufen über `user:read:chat`/`user:write:chat`
(Helix/EventSub), `chat:edit`/`chat:read` sind nur IRC. „Chat leeren“
(B80 `clear_chat`) hat keinen aktuellen Helix-Endpunkt — 4.2 entscheidet.

## 7. Offene Punkte (vor/am Anfang der Ausführung)

1. **Twitch-App registrieren** (Nutzer, extern): **erledigt 2026-10-07** —
   App „StreamCrew“ als Anwendung, Kategorie Chat Bot, öffentlicher Client
   ohne Secret; Client-ID `5yjhnihgh11abxo4f2cck1lfwx9ovg`
   (verifiziert gegen den Device-Endpunkt).
2. **Manuelle E2E (nach Task 5, vor Merge):** mit echter App + Twitch-Konto:
   `auth login twitch` → Code im Browser eingeben → `auth status` →
   `auth login twitch --bot` → `auth status` → `auth logout twitch` →
   Twitch-Doku-Konto (Connected Apps) zeigt keine Tokens mehr.
   Dauert ein paar Minuten; Nutzer klickt im Browser mit.
3. **GPG:** vor jedem Push Agent-Cache prüfen (s. §4); bei Ablauf Nutzer entriegeln.

## 8. Exit von 4.1

- Alle 6 PRs im Stack grün (CI), von unten nach oben mergebar.
- `scripts/check.sh` bei jedem PR grün.
- Manuelle E2E (Punkt 7.2) einmal komplett durchgelaufen.
- Roadmap 4.1 vollständig abgehakt (außer ggf. „Twitch-App registrieren“,
  falls der Nutzer sie erst nachträglich macht), Historie gepflegt.
- Keine Rückwirkungen auf Phase 3-Tests (Mock-Konsole läuft unverändert).

## 9. Stack-Aufbau (Zusammenfassung)

```
main (84a1ec7)
 └ PR 1  docs/oauth-adr          (ADR-0014, plan.md, adr/README.md)
   └ PR 2  feat/auth-accounts-store   (Migration 0016, queries, store)
     └ PR 3  feat/auth-device-flow    (internal/auth: Flow, twitch)
       └ PR 4  feat/auth-service      (Service, events, app-Wiring)
         └ PR 5  feat/auth-cli        (CLI auth, E2E-Tests)
           └ PR 6  docs/auth-events   (events.md, README, roadmap)
```

## 10. Rework: Authorization Code Flow (2026-10-08)

**Auslöser:** Bei der E2E-Vorbereitung (2026-10-08) hat der Projektinhaber die
DCF-Anmeldung („Gerät aktivieren“) verworfen: Der Login soll wie bei üblichen
Chatbot-Diensten vom Nutzer im Browser starten (Twitch-Login, Autorisierungsseite
mit Scopes, Autorisieren, Redirect zurück).

**Befunde (verifiziert 2026-10-08):**

1. **PKCE unterstützt Twitch nicht** — Staff-Bestätigung im Developer-Forum
   (2024-12: „as it's not supported“), unverändert im Thread von 2026-01.
2. **Öffentliche Clients (ohne Secret) sind laut offizieller Doku (Stand
   2026-10) auf den Device Code Flow beschränkt**; der Authorization Code Flow
   gibt `client_secret` im Token-Austausch als required an.
3. **Loopback-Redirect funktioniert bei Twitch** (Doku-Beispiele verwenden
   `http://localhost:3000`; Redirect-URLs müssen exakt mit dem registrierten
   Eintrag übereinstimmen).
4. **Der offizielle `twitch-cli` macht genau das gewünschte Muster:**
   Authorization Code Flow mit Loopback-Webserver und vom Nutzer mitgebrachten
   Client-ID/-Secret (BYO); DCF nur als `--dcf`-Flagge.

**Entscheidung:** [ADR-0023](../../adr/0023-twitch-login-authorization-code-flow.md)
ersetzt ADR-0014: Standard-Login = Authorization Code Flow mit Loopback-Redirect
(`http://localhost:8741`, state-Parameter, 10-Minuten-Fenster), Client-Credentials
BYO (eigene Confidential-App des Nutzers, Secret im Vault unter
`auth/<platform>/client`); Device Code Flow bleibt Fallback
(`auth login twitch --device-flow`). `accounts` bekommt die Spalte `flow`.

**Aufgaben (Stack #146 vor Ort angepasst, Force-Push, nichts gemerged):**

| R | Aufgabe | Zweig / PR | Status |
|---|---|---|---|
| R1 | ADR-0023, ADR-0014 „Abgelöst“, ADR-Index, dieser Plan-Abschnitt, Roadmap-Historie | `docs/oauth-adr` / #144 | erledigt |
| R2 | `accounts`-Tabelle: Spalte `flow` (`authorization_code`/`device_code`) in Migration 0016, `store.Account`, Round-trip-Test | `feat/auth-accounts-store` / #145 | erledigt |
| R3 | `Flow`-Interface verallgemeinern (Login-Handle + Prompt statt nur DeviceAuthResponse); `twitchCodeFlow`: Authorize-URL, Loopback-Listener (localhost, state, Fehler-Redirect), Code-Austausch mit Secret; DCF-Implementierung bleibt; Tests gegen Fake-Server | `feat/auth-device-flow` / #147 | erledigt |
| R4 | Service: `Credentials` (ID, Secret, DeviceFlow) für Start/Wait (flagless: App aus dem Vault auflösen, sonst Fehler), App-Credentials im Vault (`auth/<platform>/client`, Plattform-Ebene, mit Account/Token transaktional gespeichert), Flow-Auflösung je `flow`-Spalte (Secret aus Vault), Re-Login widerruft vorher best-effort, Logout entfernt Account + Token und behält die App-Credentials, `Status`/`Token` ohne Secret = `login_required`, `auth.action_required` im Code-Flow (URL, leeres `code`, Fensterende) | `feat/auth-service` / #148 | erledigt |
| R5 | CLI: `auth login twitch` = Code-Flow-Standard, `--device-flow` (DCF), `--client-id`/`--client-secret` (erster Login, danach aus dem Vault; Flags überschreiben), Fehlermeldung ohne Credentials, Kong-Tests | `feat/auth-cli` / #149 | offen |
| R6 | Doku: README (BYO-App-Setup + Redirect-Registrierung, `--device-flow`, Statusparagraf), `events.md` (Code-Flow-Semantik), Roadmap 4.1-Abschnitt + Historie, Plan-Status | `docs/auth-events` / #150 | offen |

**E2E (ersetzt §7.2):**

1. Nutzer legt eine **Confidential-App** bei Twitch an und trägt
   `http://localhost:8741` in „OAuth Redirect URLs“ ein (einmalig, ~2 min).
2. `auth login twitch --client-id … --client-secret …` → URL im Browser öffnen
   → Login + Autorisieren → „logged in“ (Streamer-Konto).
3. `auth status` → `ok`; `auth login twitch --bot` (Credentials aus dem Vault)
   → `auth status` → zwei Konten.
4. `auth logout twitch --bot` und `auth logout twitch` → `auth status` →
   „no accounts“; Connected Apps: keine aktiven Tokens mehr.
5. Fallback-Prüfung: `auth login twitch --device-flow` → DCF wie zuvor →
   `auth logout twitch`.

**Exit (ergänzt zu §8):** Zusätzlich E2E-Punkte 2–5 (Code-Flow und
DCF-Fallback) durchgelaufen; Stack #146 [144, 145, 147, 148, 149, 150]
komplett grün und von unten nach oben mergebar.
