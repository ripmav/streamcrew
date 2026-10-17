# Phase 6.2.2: Zugriffsschutz (Tokens, Scopes, CORS, `token` CLI)

Status: **in Arbeit** (2026-10-16)
Branch/PR: `feat/api-auth`, auf `feat/api-stream-service` gestapelt (Stack #258).

## Ziel

Der API-Server (6.2.1) ist offen. 6.2.2 schützt ihn:

1. **API-Tokens mit Scopes** (`read`, `control`, `admin`, `overlay`)
2. **`streamcrew token create|list|revoke`** — CLI-Befehl
3. **`CrossOriginProtection`** (CORS) mit Allowlist

## Entscheidungen

### Tokens

- **Datei** `<data-dir>/api-tokens.json`, Mode `0600`, atomarer Write
  (Temp+Rename) — passt zum Datenverzeichnis-Layout (ADR-0012) und wird in
  6.2.5 (Desktop-Modus: „Token im Datenverzeichnis (0600)") wiederverwendet.
- **Wert**: `sc_` + 64 Hex-Zeichen (32 Bytes `crypto/rand`). Im File liegt nur
  die SHA-256-Prüfsumme (Hex), nie der Klartext.
- **Metadaten pro Token**: `id` (UUIDv7), `name`, `scopes`, `created`.
- **Store** (`internal/apitoken`): `Open(dir)` bindet an den Pfad;
  `Create(name, scopes) (Token, value, error)`, `List()`,
  `Revoke(idOrName) (bool, error)`, `Validate(value) (Token, bool)`,
  `Enabled() bool`. Jede Operation liest die Datei frisch (kleines File,
  lokale Platte) — dadurch sehen ein laufender Core und die CLI denselben
  Zustand, ohne Sync-Mechanismus. Ein Mutex schützt Write (Read-Modify-Write).
- **Scopes** mit Hierarchie: `admin` ⊃ `control` ⊃ `read`; `overlay` erfüllt
  `read`. Ein Token erfüllt den benötigten Scope, wenn sein höchster Scope in
  der Hierarchie gleich oder darüber liegt (oder es `overlay` hat und nur
  `read` verlangt wird).

### Scope-Tabelle (benötigter Scope je RPC)

| Service | read | control | admin |
|---|---|---|---|
| System | Status, ListProfiles | — | Shutdown |
| Counter | List, Get | Set, Add | Create, Update, Reset, Delete |
| Settings | ListSections, Get | — | Update |
| User | List, Get, Rankings | — | Update, Merge |
| Chat | History | Send, Delete, Clear | — |
| Backup | List | — | Create, Restore |
| Auth | ListAccounts | — | StartLogin, CancelLogin, Logout |
| Command | List, Get, ListInstances, TypeCatalog, Export | Run, PauseQueue, CancelInstance, RetryInstance | Create, Update, Delete, Import |
| Stream | Subscribe | AnswerPrompt | — |

Nicht in der Tabelle (z. B. ein neuer, noch nicht gemappter RPC) ⇒ `admin`
(fail-closed). Die Tabelle liegt in `internal/api/scopes.go`.

### Durchsetzung

- Port `api.Tokens`: `Enabled() bool; Validate(string) (apitoken.Token, bool)`
  — als `Config.Tokens` (späte Bindung wie die anderen Ports).
- Middleware im `api.Server.Handler()`: **keine Tokens ⇒ offen**
  (Bootstrap; Loopback-Standard nach ADR-0003). **Mindestens ein Token ⇒**
  jeder API-Request braucht `Authorization: Bearer <token>`; fehlend/ungültig
  ⇒ Connect-Fehler `unauthenticated` (HTTP 401), zu wenig Scope ⇒
  `permission denied` (HTTP 403). Der Service/Method wird aus dem Pfad
  `/streamcrew.v1alpha1.<Service>/<Method>` gelesen.
- Der API-Catch-all bleibt der einzige geschützte Bereich; Health/pprof liegen
  außerhalb (wie bisher).

### CORS

- `httpserver.CrossOriginProtection(allow func(origin string) bool, next)`
  (exportiert; der Roadmap-Name `http.CrossOriginProtection`):
  - Kein `Origin`-Header ⇒ unverändert weiter (nicht-cross-origin).
  - Origin nicht erlaubt ⇒ `403` (auch Preflight).
  - Erlaubt ⇒ `Access-Control-Allow-Origin: <origin>` + `Vary: Origin`;
    Preflight (`OPTIONS` + `Access-Control-Request-Method`) ⇒ `204` mit
    Allow-Methods/Allow-Headers (Connect- und gRPC-Header) + Max-Age.
- **Allowlist**: neue Kong-Option `--cors-allow ORIGIN` (repeatable) in
  `config.Config`; leer ⇒ alle cross-origin-Requests abgelehnt (fail-closed).
- Verdrahtung: `a.http.SetAPI(httpserver.CrossOriginProtection(allow,
  a.api.Handler()))` in der App.

### CLI

`streamcrew token` (neues Kommando, `internal/cli/token.go`):

- `token create [NAME] --scope SCOPE` — erzeugt ein Token, druckt den Wert
  **einmal** nach stdout. Default-Name `cli-<YYYYmmdd-HHMMSS>`,
  Default-Scope `admin` (lokale First-Party-Clients).
- `token list` — Tabelle Name/ID/Scopes/Erstellt.
- `token revoke NAME|ID` — entfernt das Token.

Die Kommandos arbeiten direkt auf der Token-Datei im Datenverzeichnis —
kein laufender Core nötig (wie Backup-Restore, ADR-0012).

## Tests

- `internal/apitoken`: Store-Roundtrip, 0600-Mode, Validate (gültig/ungültig),
  Revoke, ungültige Scopes, `Enabled`.
- `internal/api` (über die App, echter ConnectRPC-Client):
  - ohne Tokens: offen (bestehende Tests bleiben unberührt)
  - mit Tokens: 401 ohne Header, `read`-Token auf Counter-List ✓,
    `read`-Token auf Counter-Create ✗ (403), `admin`-Token ✓,
    `overlay`-Token auf Counter-Get ✓ und Command-List ✗.
- `internal/httpserver`: CORS-Unit-Test (Preflight erlaubt/abgelehnt,
  Simple-Request mit/ohne Origin).
- `internal/cli`: token create/list/revoke gegen ein Temp-Datenverzeichnis.

## Reihenfolge

1. `internal/apitoken` + Test
2. `internal/api`: Scope-Tabelle, `Tokens`-Port, Auth-Middleware + Test
3. `httpserver.CrossOriginProtection` + Config-Option + Verdrahtung + Test
4. CLI `token` + Test
5. Roadmap/Changelog, Checks, PR
