# Plan: Roadmap 4.2 — Helix-Client

**Status:** in Ausführung (Tasks 1–3 erledigt, Tasks 4–9 offen)
**Stand:** 2026-10-09, `main` bei `1c082fd` (4.1 gemerged, Stack #146)
**Scope:** nur 4.2. 4.3 (EventSub-WebSocket) und 4.4 (Funktionen) bleiben offen;
dieser Plan legt die Fugen, die sie brauchen (`helix.Client`, Subscription-Endpunkte).

## 1. Was 4.2 liefert (Roadmap)

- [x] Code-ADR-0014 HTTP-Client: Retry mit Backoff, Rate-Limit-Header, Paginierung,
  typisierte Fehler; Reihenfolge Wiederholung → Circuit Breaker → Rate-Limiter → Anfrage (S)
  — ADR akzeptiert (Task 1); `internal/httpclient` umgesetzt (Task 3, PR #156)
- [x] `internal/breaker` nach [Code-ADR-0007](../../adr/code/0007-circuit-breaker.md):
  `sony/gobreaker/v2` mit Standardwerten, Fehlerbewertung, Logging und `ErrUnavailable`;
  Breaker `twitch.helix` und `twitch.auth` (S) — umgesetzt (Task 2, PR #154); die
  Breaker-Instanzen entstehen mit Tasks 4–5
- [ ] Endpunkte (L):
  - Users, Channels (lesen/aktualisieren), Streams
  - Chat: Nachricht senden, löschen, Einstellungen, Ankündigung, Shoutout
  - Moderation: Bann/Timeout/Entbannen, Mods, VIPs
  - Follower, Abos, Kategorien
  - EventSub-Subscriptions

## 2. Befunde der Recherche (verifiziert 2026-10-09)

1. **`github.com/sethvargo/go-retry` v0.5.0** (MIT, bereits indirekt im Modulgraph via
   `pressly/goose`; auf v0.5.0 gezogen, weil v0.5.0 (2026-10-05) die neueste Version ist
   und die in v0.4.0 noch fehlenden Obergrenzen bringt):
   - `Do(ctx, b Backoff, f RetryFunc) error` / `DoValue[T]` — die Schleife wartet auf Timer,
     prüft `context.Cause` zu jedem Durchlauf; Abbruch erscheint als Kontextfehler.
     Bei Stopp liefert `DoValue` den typisierten Fehler (`rerr.Unwrap()`).
   - Wiederholbarkeit wird am Fehler markiert: `retry.RetryableError(err)`; alles andere
     bricht sofort ab.
   - `Backoff` ist eine pull-Schnittstelle: `Next() (wartet, stoppen)`; geliefert sind
     `NewConstant`, `NewExponential(base)` (1, 2, 4, 8 …, ohne Obergrenze),
     `NewFibonacci`, dazu `WithJitter`, `WithJitterPercent`, `WithFullJitter`.
   - Versuchs-Obergrenze (`WithMaxRetries(max)`, zählt die `Next()`-Aufrufe =
     Versuche − 1) und Gesamtwartebudget (`WithMaxDuration(timeout)`, best-effort,
     cappt den letzten Wait auf den Rest).
   - Die eigene Backoff-Hülle (Code-ADR-0014, Punkt 4) setzt damit nur noch die
     429-Override-Wartezeit und das Wiederholungs-Logging.
2. **`golang.org/x/time/rate`** (Token-Bucket): `NewLimiter(rate.Limit, burst)`,
   `Wait(ctx)`. Verträglichkeit mit der `testing/synctest`-Fake-Uhr wird im ersten
   Test-PR geprüft (Code-ADR-0014, Punkt 5).
3. **`github.com/sony/gobreaker/v2` v2.4.0**: durch Code-ADR-0007 festgelegt (generisch,
   rollierendes Fenster, `ReadyToTrip`/`IsSuccessful`/`IsExcluded`, `OnStateChange`,
   `TwoStepCircuitBreaker`).
4. **Helix** (offizielle Doku): Basis-URL `https://api.twitch.tv/helix`; jeder Request
   braucht den Header `Client-Id`, autorisierte Aufrufe zusätzlich
   `Authorization: Bearer <token>`. Paginierung: `after`-Cursor + `first` (max 100),
   Antwort mit `data` + `pagination.cursor` (leer = letzte Seite).
5. **Rate-Limit-Header:** `x-ratelimit-remaining` (verbleibend) und
   `x-ratelimit-reset` (Unix-Zeitstempel, Sekunden); Grenzen je Endpunkt
   (viele Lese-Endpunkte 30/5 s, global 800/min je Client).
6. **Auth (4.1, vorhanden):** Token holen/erneuern/widerrufen laufen gegen
   `id.twitch.tv/oauth2/…` (`POST`, `url.Values`); die Nutzersuche des DCF nutzt den
   Helix-`users`-Endpunkt. `twitchBase` hat einen injizierten `*http.Client` ohne
   Wiederholungen; die Fugen (`post`, `do`) werden von Task 4 ersetzt.

## 3. Design (Details in Code-ADR-0014)

- **Pakete:**
  - `internal/breaker` — dünne Schicht nach Code-ADR-0007: Konstruktor mit
    Standardwerten (Fenster 60 s/10 s, 5 in Folge oder 50 %/10, 30 s offen, 3 Proben),
    Fehlerbewertung (Punkt 5 des ADR), Logging der Zustandswechsel, Übersetzung in
    `ErrUnavailable` (mit API-Name und frühstem Neuvieruch).
  - `internal/httpclient` — nach Code-ADR-0014: `Options`/`New`/`Do`/`DoJSON`/
    `EachPage`/`WithoutRetry`; `StatusError`, `ErrTooManyRequests`, `RateLimit`;
    Standardwerte 3 Versuche, Backoff-Basis 1 s (volles Jitter), 30-s-Budget,
    4/s Burst 8, 10-s-Timeout.
  - `internal/helix` — der Helix-Adapter: `Client` um den `httpclient` mit
    Basis-URL, `Client-Id`- und Bearer-Header und typisierten Endpunkt-Methoden.
- **Breaker-Instanzen** entstehen in der Composition Root (CLI, `internal/app`) je API —
  `twitch.helix` und `twitch.auth` — und werden injiziert (Code-ADR-0002); die Helix-
  Endpunkte und der Auth-Code teilen sich dieselben Instanzen.
- **JSON** durchweg `encoding/json/v2` (Code-ADR-0018); Logging mit `component`
  (Code-ADR-0003); Tests in `testing/synctest` + `httptest` (Code-ADR-0006).

## 4. Konventionen (gelten für alle Tasks)

- **Zweig pro Task**, nie auf `main`; Namensschema `docs/…`, `feat/…`
  (Kleinbuchstaben, Bindestriche). Zweige bauen aufeinander (Stack).
- **Merging:** Der Nutzer merged den ganzen Stack am Stück, von unten nach
  oben (nach Prüfung/E2E). ADR-/Docs-PRs sind Teil des Phasen-Stacks und
  werden mit ihm gemerged — während der Phase wird kein einzelnes PR gemerged.
- Vor **jedem** Commit `scripts/check.sh` (gofmt, lint, Tests in Sandbox).
- Commits **englisch**, Conventional Commits, **GPG-signiert**
  (`commit.gpgsign=true`; Agent-Cache prüfen:
  `echo test | gpg --clearsign >/dev/null` — bei Ablauf: Nutzer entriegelt).
  Footer in jedem Commit: `Assisted-by: Qwen3.8-27B (xHigh) via OpenCode`
- **PRs:** Titel englisch, Body deutsch mit den Abschnitten
  `## Worum geht es`, `## Änderungen`, `## Bitte prüfen (meine Festlegungen)`, `## Tests`.
- **GitHub-Stack:** #155 (Phase 4.2; der 4.1-Stack #146 ist gemerged und
  geschlossen). **Jedes** PR per `POST /repos/ripmav/streamcrew/stacks/{n}/add` mit
  `{"pull_requests":[…]}` (geordnet vom Stack-Top aufwärts) registrieren —
  `gh pr create --base` reicht nicht.
- Roadmap: erledigte Punkte sofort abhaken + Änderungshistorie pflegen
  (je PR einen Eintrag, Datum, Inhalt, Belege).

## 5. Tasks

### Task 1 — Code-ADR-0014 (Docs-PR, unterstes PR im Stack)

**Status:** erledigt, PR #153 (ADR am 2026-10-09 vom Projektinhaber akzeptiert, ohne Änderungen)
**Zweig:** `docs/code-adr-0014-http-client` (von `main`)
**PR:** `docs(adr): HTTP client for external APIs (Code-ADR-0014)`

**Dateien:**
- neu `docs/adr/code/0014-http-client.md` (Format: `docs/adr/TEMPLATE.md`)
- `docs/adr/code/README.md` — Index um 0014 ergänzen (Vorgeschlagen, Datum)
- `docs/plan.md` §12.2 — Zeile 0014: Status „**vorgeschlagen**“
- `docs/roadmap.md` — Änderungshistorie
- Plan-Datei dieses Dokuments wird in das PR eingecheckt

**Inhalt der ADR (Kontext / Entscheidung / Alternativen / Konsequenzen):**
Bibliotheken `sethvargo/go-retry` + `golang.org/x/time/rate` + `sony/gobreaker/v2`
(über `internal/breaker`) in einer dünnen Schicht `internal/httpclient`; Reihenfolge
Wiederholung → Breaker → Rate-Limiter → Anfrage; Standardwerte (3 Versuche, Basis 1 s
volles Jitter, 30-s-Budget, 4/s Burst 8, 10-s-Timeout); `x-ratelimit-*`-Header steuern
Limiter und 429-Wartezeit; Helix-Paginierung (`after`/`first`); `StatusError` +
`ErrTooManyRequests`; `WithoutRetry` für Einmal-Verbrauch (OAuth-Code-Austausch).
Vorgabe des Projektinhabers (2026-10-09): Middleware per Bibliotheken,
kein `hashicorp/go-retryablehttp`.

**Tests:** doc-only PR → CI „Internal links“. ADR-Format manuell prüfen
(Tabelle im Kopf, relative Links, nur existierende ADRs verlinkt).
**Commit:** `docs(adr): HTTP client for external APIs (Code-ADR-0014)`

### Task 2 — `internal/breaker`

**Status:** erledigt, PR #154
**Zweig:** `feat/breaker` (auf Task 1)
**PR:** `feat(breaker): circuit breaker layer for external APIs`

**Dateien:**
- neu `internal/breaker/breaker.go`, `internal/breaker/breaker_test.go`
- `go.mod`/`go.sum` — `github.com/sony/gobreaker/v2` v2.4.0

**Inhalt:**
- `Breaker` um `gobreaker.CircuitBreaker` (Code-ADR-0007): Standardwerte aus ADR-
  Punkt 4 (Fenster 60 s in 10-s-Schritten; Auslösen 5 in Folge oder 50 % bei ≥10;
  30 s offen; 3 Proben halb offen), je API überschreibbar, ohne Nutzerwerte.
- `Execute(ctx, fn)` — der Aufruf läuft einzeln durch den Breaker; `ReadyToTrip`
  nach ADR-Punkt 5 (5xx + Netzwerk Fehler, übrige 4xx Erfolg), `IsExcluded` für
  429 und `context.Canceled`; die Bewertung liest die typisierten Fehler des
  httpclient (`StatusError.StatusCode`).
- Zustandswechsel loggen (`WARN` offen, `INFO` geschlossen, Breaker-Name + `component`);
  der Callback läuft unter der Sperre — nur loggen, nicht blockieren.
- `ErrUnavailable` (mit Name und frühstem Neuvieruch); gobreaker-Typen
  (`ErrOpenState`, `ErrTooManyRequests` = Proben voll) werden hier übersetzt und
  verlassen das Paket nicht.

**Tests (synctest):** Auslösen per 5 in Folge und per Quote, Übergang nach halb offen
(nach >30 s, nicht bei genau 30 s), Schließen nach 3 Erfolgen, Wiederöffnen nach Fehler;
Ausschlüsse (429, `context.Canceled`) zählen nicht; Übersetzung in `ErrUnavailable`;
Logging der Wechsel.
**Commit:** `feat(breaker): circuit breaker layer for external APIs`

### Task 3 — `internal/httpclient`

**Status:** erledigt, PR #156 (2026-10-09; go-retry v0.5.0 statt v0.4.0,
ADR-0014 entsprechend faktual aktualisiert)
**Zweig:** `feat/httpclient` (auf Task 2)
**PR:** `feat(httpclient): resilient HTTP client with retry, breaker and rate limits`

**Dateien:**
- neu `internal/httpclient/` — `client.go` (Options/New/Do/DoJSON), `errors.go`
  (`StatusError`, `ErrTooManyRequests`, `RateLimit`), `ratelimit.go` (Token-Bucket +
  `remaining`/`reset`), `page.go` (`EachPage`), Tests
- `go.mod`/`go.sum` — `github.com/sethvargo/go-retry` direkt, `golang.org/x/time` neu

**Inhalt:**
- Schnittstelle und Reihenfolge nach Code-ADR-0014 Punkt 1–3 (Wrapper um `Do`,
  kein `RoundTripper`); `WithoutRetry` je Aufruf.
- Wiederholung: `retry.Do` + Backoff-Hülle (`NewExponential(1s)` + `WithFullJitter`,
  3 Versuche, 30-s-Budget); 429 wartet bis `x-ratelimit-reset` (Fallback
  `Retry-After`, dann Backoff); Logging `WARN`/`INFO` (Punkt 4).
- Rate-Limit: `x/time/rate` (4/s Burst 8 Standard) + gespeiste Header-Werte
  (Punkt 5); `RateLimit` auf der `Response`.
- `EachPage` nach Helix-Muster (Punkt 7), 200-Seiten-Obergrenze.
- **Prüfung erledigt:** `x/time/rate` ist mit der synctest-Fake-Uhr verträglich
  (`WaitN` nutzt `time.NewTimer` + `time.Now()`, beides im Bubble gefaket).
  **Testaufbau-Befund:** Tests nutzen `httptest.NewTestServer` (Go-1.27-In-Memory-Netzwerk)
  mit `srv.Client()` als Base-Transport — ein Loopback-Server (`NewServer`) hält die
  Fake-Uhr an (Server-Goroutine im Netzwerk-I/O), und sein `Close` via `t.Cleanup`
  (außerhalb der Bubble) schließt Kanäle, die in der Bubble erstellt wurden (fatal).

**Tests (synctest + httptest):** 500→200 Erfolg nach Backoff; 400 ohne Wiederholung;
Netzwerkfehler → Wiederholung; 429 wartet bis Reset; `remaining = 0` → nächste Anfrage
nach Reset; Budget erschöpft → `ErrTooManyRequests`; offener Breaker →
`ErrUnavailable` ohne Anfrage; Paginierung 3 Seiten/leerer Cursor/Fehler aus `fn`;
`WithoutRetry`; Kontext-Abbruch während Wartezeit.
**Commit:** `feat(httpclient): resilient HTTP client with retry, breaker and rate limits`

### Task 4 — `internal/auth` auf dem httpclient (Breaker `twitch.auth`)

**Status:** offen
**Zweig:** `feat/auth-on-httpclient` (auf Task 3)
**PR:** `feat(auth): run token calls through the resilient HTTP client`

**Dateien:**
- `internal/auth/twitch.go` — `post`/`do` werden durch Aufrufe an die
  `httpclient`-Instanzen ersetzt (Token holen: Code-Austausch + DCF-Polling;
  Refresh; Revoke → Breaker `twitch.auth`; Nutzersuche → Breaker `twitch.helix`)
- `internal/auth/auth.go` (Signaturen der Konstrukoren, wenn nötig)
- `internal/app` (Composition Root: beide Clients bauen und injizieren)
- Tests in `internal/auth` (Fakes bleiben, Antworten identisch)

**Inhalt:**
- Code-Austausch (Authorization Code Flow) mit `WithoutRetry` (einmaliger Code);
  DCF-Polling, Refresh und Revoke dürfen wiederholt werden (idempotent).
- Der DCF-Poll-Schleife (eigenes Polling, 4.1 R8) bleibt; jeder Poll läuft
  durch den Client.
- Fehlervertrag bleibt: `StatusError` wird auf die 4.1-Meldungen abgebildet
  (400 beim Token-Endpunkt → `login_required` bzw. DCF-Fehlercode, 401/403 →
  `login_required`); `ErrUnavailable` erscheint als „Twitch-API derzeit nicht
  erreichbar“ im Login-Verlauf.

**Tests:** bestehende Auth-Tests laufen weiter (Fake-Server antwortet unverändert);
neu: offener Breaker → `ErrUnavailable` im Login-Verlauf; Code-Austausch ohne
Wiederholung (Zähler am Fake-Server).
**Commit:** `feat(auth): run token calls through the resilient HTTP client`

### Task 5 — `internal/helix`: Fundament + Users, Channels, Streams

**Status:** offen
**Zweig:** `feat/helix-core` (auf Task 4)
**PR:** `feat(helix): client foundation and user, channel and stream endpoints`

**Dateien:**
- neu `internal/helix/` — `helix.go` (Client, Basis-URL, `Client-Id`/Bearer,
  `httpclient` injiziert), `users.go`, `channels.go`, `streams.go`,
  `types.go` (typisierte Strukturen, json/v2), Tests

**Inhalt:**
- `New(httpclient *httpclient.Client, clientID string)`; je Methode setzt der
  Client die Header (Bearer aus dem Aufrufer-Kontext bzw. injiziertem Token-
  Lieferanten — Fuge für 4.3/4.4), der httpclient den Rest.
- Endpunkte (gegen die Helix-Doku je Endpunkt finalisieren):
  - Users: `GetUsers` (IDs oder Logins, Paginierung)
  - Channels: `GetChannel`, `UpdateChannel` (Titel, `game_id`, `broadcaster_type`)
  - Streams: `GetStreams` (ID/User/Login, Paginierung)

**Tests (Fake-Helix-Server):** Header (`Client-Id`, `Authorization`), JSON-Runde
(json/v2, strenge Felder), Paginierung (2 Seiten), 404 → `StatusError`,
429 → `ErrTooManyRequests` mit `RateLimit`.
**Commit:** `feat(helix): client foundation and user, channel and stream endpoints`

### Task 6 — `internal/helix`: Chat und Moderation

**Status:** offen
**Zweig:** `feat/helix-chat` (auf Task 5)
**PR:** `feat(helix): chat and moderation endpoints`

**Dateien:**
- `internal/helix/chat.go`, `internal/helix/moderation.go`, Tests

**Endpunkte (Roadmap, je gegen die Helix-Doku finalisieren):**
- Chat: Nachricht senden (`chat/messages`), Nachricht löschen,
  Einstellungen lesen/aktualisieren, Ankündigung senden, Shoutout senden
- Moderation: Bann/Entbannen (`bans`), Timeout/Entzeitlichung,
  Moderator:liste (`moderators`), VIPs (lesen/hinzufügen/entfernen)

**Tests:** je Endpunkt: Request-Form (Body/Query), 200-Antwort, 401/403/404 →
`StatusError`; Schreibendpunkte ohne Bearer → 401-Meldung.
**Commit:** `feat(helix): chat and moderation endpoints`

### Task 7 — `internal/helix`: Follower, Abos, Kategorien

**Status:** offen
**Zweig:** `feat/helix-social` (auf Task 6)
**PR:** `feat(helix): follower, subscription and category endpoints`

**Dateien:**
- `internal/helix/followers.go`, `internal/helix/subscriptions.go`,
  `internal/helix/categories.go`, Tests

**Endpunkte:** Follower lesen (Paginierung), Follows des Senders,
Abos des Senders, Kategorien lesen/suchen.

**Tests:** je Endpunkt + Paginierung (Follower über mehrere Seiten).
**Commit:** `feat(helix): follower, subscription and category endpoints`

### Task 8 — `internal/helix`: EventSub-Subscriptions

**Status:** offen
**Zweig:** `feat/helix-eventsub` (auf Task 7)
**PR:** `feat(helix): EventSub subscription endpoints`

**Dateien:**
- `internal/helix/eventsub.go`, Tests

**Endpunkte:** Subscription anlegen (Typ, Konditionen, Version),
widerrufen (204), auflisten (Paginierung) — das Fundament für den
4.3-Subscription-Manager (Soll-Zustand Plan Anhang A.2).

**Tests:** Anlage mit Konditionen, Listen mit Cursor, Widerruf 204,
Limit-Verstoß (400) → `StatusError` mit Snippet.
**Commit:** `feat(helix): EventSub subscription endpoints`

### Task 9 — Doku: README, Roadmap (Docs-PR, oberstes PR)

**Status:** offen
**Zweig:** `docs/helix-docs` (auf Task 8)
**PR:** `docs: the Helix client of phase 4.2`

**Dateien:**
- `README.md` — Status, Paket-Tabelle (`breaker`, `httpclient`, `helix`),
  ggf. Abschnitt zur Twitch-API (Client-Id, Rate-Limits, Breaker-Verhalten)
- `docs/roadmap.md` — 4.2-Punkte abhaken, Historie
- `docs/adr/code/0014-http-client.md` + Index + `plan.md` §12.2 — **nur falls** der
  Projektinhaber das ADR zwischenzeitlich abgenommen hat (Status „Akzeptiert“)
- `docs/adr/code/0007-circuit-breaker.md` — erledigte Folgearbeiten abhaken
  (`internal/breaker` umgesetzt, Breaker `twitch.helix`/`twitch.auth`)

**Commit:** `docs: the Helix client of phase 4.2`

## 6. Scope-Liste

**Dabei:**
- Code-ADR-0014 (vorgeschlagen), `internal/breaker`, `internal/httpclient`
- `internal/auth` auf dem httpclient (Breaker `twitch.auth`, Nutzersuche `twitch.helix`)
- Helix-Endpunkte: Users, Channels, Streams, Chat, Moderation, Follower, Abos,
  Kategorien, EventSub-Subscriptions
- Doku (README, Roadmap, ADR-Index)

**Nicht dabei:**
- EventSub-WebSocket, `session_reconnect`, Deduplizierung (4.3)
- Funktionen/Commands (4.4)
- YouTube, Kick, Integrationen (spätere Phasen — der httpclient ist darauf vorbereitet)
- Nutzer-einstellbare Rate-/Breaker-Werte (vorerst Standardwerte im Code)
- Netguard-Auslieferung der Plattform-Adapter (feste Adressen, Code-ADR-0019)

## 7. Offene Punkte (vor/am Anfang der Ausführung)

1. **Abnahme von Code-ADR-0014** durch den Projektinhaber (nach PR-Review von
   Task 1; falls Änderungen: Tasks 2–3 entsprechend anpassen):
   **erledigt 2026-10-09** — akzeptiert ohne Änderungen (PR #153).
2. **`x/time/rate` + `testing/synctest`:** Verträglichkeit mit der Fake-Uhr im
   ersten Test-Lauf von Task 3 prüfen; Fallback: dünnes eigenes Token-Bucket
   oder ein Test mit echter Zeit (Code-ADR-0014, Punkt 5).
3. **Endpunkt-Liste je Task finalisieren** (Roadmap ist grob): gegen die Helix-Doku
   prüfen, bevor der Task gestartet wird.
4. **Token-Lieferant des `helix.Client`:** injizierte Funktion
   (`func(ctx) (token string, err error)`) statt hartem Token — so bleibt der
   Client für 4.3/4.4 (mehrere Konte, Bot) brauchbar; in Task 5 festlegen.
5. **4.1-Fugen:** `twitchBase.post`/`do` nach Task 4 entfernen (ersetzt);
   `scopeList` und der String-Vertrag der Scopes bleiben (4.1 R7).

## 8. Exit von 4.2

- [x] Code-ADR-0014 (PR Task 1; Status nach Abnahme: Akzeptiert)
- [x] `internal/breaker` mit Tests (Folgearbeit Code-ADR-0007, PR #154)
- [x] `internal/httpclient` mit Tests (Folgearbeit Code-ADR-0014, PR #156)
- [ ] `internal/auth` auf dem httpclient mit Breakern `twitch.auth`/`twitch.helix`
- [ ] Helix-Endpunkte: Users/Channels/Streams, Chat/Moderation,
  Follower/Abos/Kategorien, EventSub-Subscriptions — alle mit Tests
- [ ] Doku: README, Roadmap 4.2 abgehakt, Historie, ADR-Index

## 9. Stack-Aufbau (Zusammenfassung)

```
main (1c082fd)
 └ PR 1  docs/code-adr-0014-http-client  (ADR-0014, plan, index, roadmap)
 └ PR 2  feat/breaker                    (internal/breaker, gobreaker v2.4.0)
 └ PR 3  feat/httpclient                 (internal/httpclient, go-retry v0.5.0, x/time v0.16.0)
 └ PR 4  feat/auth-on-httpclient         (internal/auth auf dem Client, twitch.auth)
 └ PR 5  feat/helix-core                 (Fundament, users, channels, streams)
 └ PR 6  feat/helix-chat                 (chat, moderation)
 └ PR 7  feat/helix-social               (followers, subscriptions, categories)
 └ PR 8  feat/helix-eventsub             (eventsub subscriptions)
 └ PR 9  docs/helix-docs                 (README, roadmap, ADR-Index)
```
