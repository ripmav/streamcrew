# Offene Findings: Stand nach der dritten Nachprüfung (Code-Review 2026-10-24)

- **Grundlage:** [`code-review-2026-10-24.md`](code-review-2026-10-24.md),
  [`fix-plan-2026-10-24.md`](fix-plan-2026-10-24.md),
  [`open-findings-2026-10-24.md`](open-findings-2026-10-24.md) und
  [`recheck-2026-10-25.md`](recheck-2026-10-25.md)
- **Geprüft:** Branch `fix/code-review-2026-10-24` bei `491a204`
- **Ergebnis:** Alle Findings der Prioritäten 1–6 des Fix-Plans sind beseitigt,
  auch T1, T1a, T1b, N2a und N3a aus der zweiten Nachprüfung (`d4b047f`).
  Offen sind die Umsetzung von V3/C14 und `log-file`, die laut Fix-Plan
  zurückgestellten Findings und die Prozesspunkte.

## 1. Offen aus dem Fix-Plan (entschieden, nicht umgesetzt)

| # | Sev. | Stand | Ort |
|---|---|---|---|
| V3/C14 | Mittel | Entschieden am 2026-10-24 (Nachtrag zu Code-ADR-0021, Branch `docs/config-api-rechte-nur-einschraenken`): `ConfigService/Update` darf Rechte, `file-root`, `outbound-allow`, `mode`, `cors-allow`, `listen`, TLS und `dev` nur einschränken, gemessen am aktuell wirksamen Stand. Erst danach wird der Watcher in `serve` verdrahtet (`core.Run` ruft `Load` nicht auf). Umsetzung offen (Roadmap 6.5). | `internal/config/watch.go`, `core/core.go:158-160` |
| `log-file` | Mittel | Entschieden (Nachtrag Punkt 6): `log-file` gilt nur beim Start und wechselt von hot zu restart, für die Datei wie für die API. Umsetzung offen (Roadmap 6.5). | `internal/config/watch.go` |

## 2. Hinweis ohne Befund

| # | Stand |
|---|---|
| T1 | Der Fix setzt voraus, dass Twitch die Sammelmeldung (`community_sub_gift`) vor den Einzelgeschenken liefert. Ein Einzelgeschenk, das vorher ankommt, feuert trotzdem. Das ist die übliche Reihenfolge bei Twitch, deshalb kein Befund. Falls es in der Praxis anders auftritt: Einzelgeschenke mit `CommunityGiftID` kurz zurückhalten. |

## 3. Laut Fix-Plan zurückgestellt (nicht angegangen)

Die Einzelheiten stehen im [Review](code-review-2026-10-24.md).

### Sicherheit

| # | Sev. | Kurz |
|---|---|---|
| S5 | Mittel | API-Tokens global in `<data-dir>/api-tokens.json` statt in der Profil-DB, ohne ADR; README.md behauptet „pro Profil, verschlüsselt“. Vor Abweichung vom ADR den Besitzer fragen. |
| S6 | Mittel | Nur minimal umgesetzt: die CLI-API-Befehle nutzen Unix-Socket und `runtime.json` weiter nicht. |
| S8 | Niedrig | Scope `overlay` zählt als `read` und darf personenbezogene Daten lesen. |
| S9 | Niedrig | Eigener CORS/CSRF-Handler statt `http.CrossOriginProtection`; Same-Origin-POSTs bei leerer Allowlist mit 403 abgelehnt. |
| S10 | Niedrig | Kein h2c, native gRPC-Clients brauchen TLS. |
| S11 | Niedrig | Keine API-Version im Handshake; Mode-Kommentar im Proto ohne `daemon`. |
| S12 | Niedrig | Doppelte Token-Namen, `Revoke(name)` entfernt alle; Read-Modify-Write nur prozessintern gesperrt; offene Streams überleben eine Rücknahme. |

### Twitch, Chat, Events, Moderation

| # | Sev. | Kurz |
|---|---|---|
| T5 | Mittel | Keine Whispers/Replies über Twitch, obwohl Roadmap 5.1 als erledigt gilt. |
| T6 | Mittel | Stream-Status nach EventSub-Reconnect nicht neu gemeldet. |
| T7 | Mittel | EventSub: Reader-Goroutine leckt je Reconnect; Frames der alten Session gehen verloren. |
| T8 | Mittel | `channel.moderate` nur für Ban/Timeout gemappt; Rest als Warn „unmapped“. |
| T9 | Mittel | Globaler Mutex der Event Service über Netzwerk-I/O. |
| T10 | Mittel | Anonyme Cheers vermutlich verworfen (P). |
| T11 | Mittel | Follow-Zeit nur aus dem Cache des Heimkanals (P). |
| T12 | Mittel | Ein EventSub-WebSocket je Zusatzkanal überschreitet Twitch-Limits (P). |
| T13 | Niedrig | IRC-Lebenszyklus: Close-Goroutine, Backoff, Welcome-Timeout, Poller je Reconnect. |
| T14 | Niedrig | IRC: Tag-Werte nicht unescaped, CR/LF in `Send` nicht entfernt (siehe §5), nur ein Rate-Bucket. |
| T15 | Niedrig | `Reconcile` ohne Mutex, kann parallel laufen. |
| T16 | Niedrig | Link-Regex zu weit, Wortfilter kompiliert je Nachricht. |
| T17 | Niedrig | `timer.Run` lädt jede Sekunde alles; Mute-Ausnahme kanalübergreifend; `Event.ID = string(Body)`. |

### Nutzer, Counter, API, CLI, TUI

| # | Sev. | Kurz |
|---|---|---|
| U4 | Mittel | `CounterService.Create` ruft `CheckReserved` nicht auf (`date` überschattet `$date`). |
| U5 | Mittel | Fehler-Mapping: viele Validierungsfehler werden zu `CodeInternal` (HTTP 500). |
| U6 | Mittel | TUI-Stream ohne Reconnect, stille Verluste ab 64 Events, `context.Background()`. |
| U7 | Niedrig | `refreshData` ohne Timeout auf der Event-Loop; `watchTick` scannt auch offline. |
| U8 | Niedrig | Merge lässt die alte ID in `active`; Rollen-Update nicht atomar. |
| U9 | Niedrig | Instabiles `SortFunc` in `DefaultTitle`, `RegularHours` ohne Obergrenze, `FollowedAt` Jahr 1. |

### Commands, Actions, Templates, Requirements

| # | Sev. | Kurz |
|---|---|---|
| A1 | Mittel | `command_admin update` meldet Erfolg ohne Chat-Action. |
| A2 | Mittel | `command_admin find` unterscheidet „keins“, „mehrere“ und Fehler nicht. |
| A3 | Mittel | Schwellen-Requirement speicherbar, scheitert aber immer mit `ErrNotSupported`. |
| A4 | Mittel | `user_command`-Antworten am Namen geschlüsselt, Umbenennung verwaist sie. |
| A5 | Niedrig | Race beim Zeitlimit. |
| A6 | Niedrig | Template-Cache-Schlüssel case-sensitiv. |
| A7 | Niedrig | Bits-Requirement und Reihenfolge nicht in der Spec. |
| A8 | Niedrig | Unbegrenzter Zustand (Zufallsgedächtnis, globale Werte). |
| A9 | Niedrig | `addcmd !hello` erzeugt vermutlich `!!hello`; `find`/`Save` nicht atomar (P). |
| A10 | Niedrig | Kleinere Punkte in Web-Request, File-Locks und Engine-Shutdown. |

### Code-ADRs

| # | Sev. | Kurz |
|---|---|---|
| C2 | Niedrig | `json.Marshal` teils ohne `Deterministic(true)`. |
| C3 | Mittel | Nackte `go`-Anweisungen ohne Besitzer/Warten. |
| C4 | Niedrig | `context.Context` in Structs, `context.Background()` in Produktionspfaden. |
| C5 | Niedrig | Fehler in `App.Run` geloggt und zurückgegeben. |
| C6 | Niedrig | `errors.As` statt `errors.AsType` an ca. 8 Stellen. |
| C7 | Niedrig | `twitchmock` importiert `internal/config`. |
| C8 | Niedrig | Paket-Zustand ohne dokumentierte Freigabe; `sync.Once` in `appcreds.go`. |
| C9 | Niedrig | Magische Werte (`-1`, `""` für „alle“). |
| C10 | Niedrig | Fehlende SPDX-Header in generiertem Code und `scripts/modulecheck.sh`. |
| C11 | Niedrig | `checksums.txt` deckt nicht alle Release-Artefakte ab. |
| C12 | Niedrig | BTTV/FFZ werden immer kontaktiert, ohne Einstellung (P). |
| C13 | Info | `core.Options` mit ca. 30 Feldern, breiter als in ADR-0006. |

### Doku

| # | Sev. | Kurz |
|---|---|---|
| D7 | Niedrig | Schwache B-ID-Rückverfolgbarkeit in Tests. |
| D8 | Info | 17 Test-Matrizen gegen 203 `_test.go`-Dateien. |

## 4. Prozess

- **Signaturen:** Bis auf `02e946d` (G) und `021ee40` (E) sind alle Commits des Branches unsigniert (`git log --format=%G?` zeigt `N`).
- **Stack:** Kein PR-Stack, alles liegt auf einem Branch. Der Doku-Commit (D1–D6) liegt oben statt ganz unten.
- **Commit-Titel:** `docs:Doku…` (`55001c3`) und `refactor:encoding…` (`a0981f9`) ohne Leerzeichen nach dem Doppelpunkt.
- **Footer:** `d4b047f` trägt `Assisted-by: Qwen 3.8 27B (medium) via OpenCode`, ältere Commits `Claude (high) via Pi` ohne Modellnamen; `Co-Authored-By` fehlt.

## 5. Unterhalb der Schwelle (Review §7)

| # | Kurz |
|---|---|
| Rollen | Badges aus Zusatzkanälen werden global zu `Streamer`/`Moderator`; Rollen pro Kanal ableiten (users-and-roles.md B25). |
| IRC-CRLF | `IRCSession.Send`/`writeLine` entfernen CR/LF nicht (zusammen mit T14). |

## Tools bei der Nachprüfung

| Schritt | Ergebnis |
|---|---|
| `go version` | go1.27.3 |
| `gofmt -l`, `go fix -diff`, `go vet` | keine Befunde |
| `golangci-lint` | 0 issues |
| `govulncheck` | No vulnerabilities found |
| `scripts/sandbox.sh go test ./...` | alle Pakete grün |
| `scripts/sandbox.sh go test -race` für `eventservice`, `app`, `api`, `twitch` | alle grün |
