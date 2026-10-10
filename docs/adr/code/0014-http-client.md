# Code-ADR-0014: HTTP-Client für externe APIs

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-10-09 |
| **Aktualisiert** | 2026-10-09: `sethvargo/go-retry` ist auf v0.5.0 gezogen (Latest-Regel); `WithMaxRetries`/`WithMaxDuration` ersetzen die eigene Zählung im Backoff — Kontext, Punkt 1 und Punkt 4 angepasst |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §12.2 (ADR-Backlog, 0014); Roadmap Phase 4.2; [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0003](0003-fehler-und-logging.md), [Code-ADR-0006](0006-teststrategie.md), [Code-ADR-0007](0007-circuit-breaker.md), [Code-ADR-0018](0018-json-v2.md), [Code-ADR-0019](0019-host-rechte-in-der-startkonfiguration.md) |

## Kontext

- Ab Phase 4 ruft der Core die Twitch-Helix-API auf, ab 4.3 zusätzlich EventSub; später YouTube, Kick und die Integrationen (Code-ADR-0007). Viele Aufrufe kommen aus Actions der Command-Engine.
- Die Roadmap (Phase 4.2) verlangt einen HTTP-Client mit Retry und Backoff, Auswertung der Rate-Limit-Header, Paginierung und typisierten Fehlern. Die Reihenfolge der Schichten ist in [Code-ADR-0007](0007-circuit-breaker.md) (Punkt 6) festgelegt: Wiederholung (außen) → Breaker → Rate-Limiter → Anfrage mit Timeout.
- Die Standardbibliothek bietet davon nichts: `net/http` macht eine einzelne Anfrage ohne Wiederholung, Drosselung oder Paginierung. Der Auth-Code (`internal/auth`, 4.1) nutzt einen injizierten `http.Client` ohne Wiederholungen.
- Der Circuit Breaker ist entschieden (Code-ADR-0007): `sony/gobreaker/v2` in einer dünnen Schicht `internal/breaker`, ein Breaker je Dienst-API (`twitch.helix`, `twitch.auth`), einheitliche Fehlerbewertung (Netzwerk/5xx Fehler, 4xx Erfolg, 429 und `context.Canceled` ausgeschlossen), Übersetzung in den Domänenfehler `ErrUnavailable`.
- Der Projektinhaber hat am 2026-10-09 entschieden: die Middleware (Wiederholung, Drosselung) kommt per Bibliotheken, aber nicht von HashiCorp (kein `hashicorp/go-retryablehttp`).
- Bibliotheken (Stand 2026-10-09, alle MIT bzw. `golang.org/x`):
  - `github.com/sethvargo/go-retry` (v0.5.0): Wiederholungs-Schleife mit Backoff, kontextbewusst (prüft `context.Cause`, wartet auf Timer), Wiederholbarkeit wird am Fehler markiert (`RetryableError`), das Backoff wird pull-artig abgefragt (`Backoff.Next() (wartet, stoppen)`), mit `NewExponential(base)` (1, 2, 4, 8 …), `WithFullJitter`, Versuchs-Obergrenze (`WithMaxRetries`) und Gesamtwartebudget (`WithMaxDuration`). Die dünne eigene Hülle über dem Backoff setzt damit nur noch die 429-Wartezeit und das Wiederholungs-Logging. Liegt bereits im Modulgraph (indirekt via `pressly/goose`).
  - `golang.org/x/time/rate`: Token-Bucket (`NewLimiter(rate, burst)`, `Wait(ctx)`), der Standard-Rate-Limiter der `golang.org/x`-Familie (das Projekt nutzt bereits `x/oauth2`, `x/sys`, `x/text`).
  - `github.com/sony/gobreaker/v2` (v2.4.0): durch Code-ADR-0007 festgelegt.
- Twitch drosselt pro Client-ID und antwortet (offizielle Doku) mit den Headern `x-ratelimit-remaining` (verbleibende Anfragen) und `x-ratelimit-reset` (Unix-Zeitstempel in Sekunden); die Grenzen sind je Endpunkt unterschiedlich (viele Lese-Endpunkte 30 Anfragen je 5 s, insgesamt 800/min). Ein konservativer Client-Rate-Limiter plus serverseitiges 429-Verhalten decken das ab.
- Plattform-Adapter haben feste Adressen (Twitch: `api.twitch.tv`, `id.twitch.tv`) und gehen nach Code-ADR-0019 nicht durch die outbound-Allowlist (`internal/netguard`); der injizierte Basis-`http.Client` wird unverändert verwendet.
- `internal/auth` (4.1) bekommt in 4.2 ebenfalls einen Breaker (`twitch.auth`, Roadmap 4.2); die Migration seiner Token-Aufrufe auf diesen Client ist Folgearbeit (Punkt 3).

## Entscheidung

1. **Wir verwenden drei Bibliotheken, kombiniert in einer dünnen eigenen Schicht `internal/httpclient`:**
   - Wiederholungs-Schleife und Backoff: `github.com/sethvargo/go-retry`
   - Clientseitige Drosselung (Token-Bucket): `golang.org/x/time/rate`
   - Circuit Breaker: `github.com/sony/gobreaker/v2` über `internal/breaker` (Code-ADR-0007)
   - Eigener Code bleibt dünn: Backoff-Hülle (429-Override-Wartezeit und Wiederholungs-Logging; die Versuchs- und Zeitbegrenzung kommen per `WithMaxRetries`/`WithMaxDuration` aus der Bibliothek), Bewertung der Wiederholbarkeit, Auswertung der Rate-Limit-Header, Paginierung, typisierte Fehler. Die Kombination und Reihenfolge der Schichten ist der eigene Anteil.
   - Die Schichten sind **kein** `http.RoundTripper`, sondern Wrapper um `Do` (Punkt 2): Die Wiederholung muss die Antwort (Status und Header) sehen, um Wartezeit und Abbruch zu entscheiden — das ist als Wrapper klarer und testbarer als im Transport. Der Breaker läuft je Versuch mit zusammengehörigem Anfang und Ende in `Execute`, daher braucht es `TwoStepCircuitBreaker` (Code-ADR-0007, Punkt 4) nicht.

2. **Aufbau und Reihenfolge** (Code-ADR-0007, Punkt 6, konkretisiert):

   ```
   Client.Do(ctx, req):
     Wiederholungs-Schleife (go-retry, Budget siehe Punkt 4)
       breaker.Execute (internal/breaker, je API eine Instanz)
         Rate-Limiter: Wait (Token-Bucket) + bei verbleibend = 0 bis Reset warten
           Basis-Client: eine einzelne Anfrage mit Timeout (Punkt 8)
   ```

   - Jeder Versuch läuft einzeln durch den Breaker. Ist der Breaker offen oder halb offen voll, wird nicht wiederholt (Code-ADR-0007, Punkt 6); der Fehler erscheint als `breaker.ErrUnavailable`.
   - Die Rate-Limit-Header der Antwort liest die äußerste Schicht aus und speist den Limiter (Punkt 5).

3. **Schnittstelle** (in `internal/httpclient`; JSON nach Code-ADR-0018):

   ```go
   type Options struct {
       Name    string           // API-Name, z. B. "twitch.helix" (Breaker, Logging)
       Base    *http.Client     // injizierter Transport (Code-ADR-0002); Timeout 10 s, wenn null
       Breaker *breaker.Breaker // aus internal/breaker, für alle Aufrufer dieselbe Instanz
       Rate    rate.Limit       // clientseitig, Standard 4/s
       Burst   int              // Standard 8
       Retry   RetryPolicy      // Standard: 3 Versuche, Basis 1 s, volles Jitter, 30 s Budget
       Logger  *slog.Logger     // mit component-Attribut (Code-ADR-0003)
   }
   func New(o Options) *Client
   func (c *Client) Do(ctx context.Context, req *http.Request) (*Response, error)
   func (c *Client) DoJSON(ctx context.Context, req *http.Request, v any) error
   func (c *Client) EachPage[T any](ctx context.Context, req *http.Request, first int,
       fn func(items []T, nextCursor string) error) error
   func WithoutRetry(ctx context.Context) context.Context
   ```

   - `Do` führt die Anfrage durch und liefert die Antwort (Status, Header, Body, `RateLimit`); der Aufrufer schließt den Body. `DoJSON` dazu: auf 2xx prüfen, Body schließen, mit `encoding/json/v2` in `v` entschlüsseln.
   - Der Client entsteht je API in der Composition Root (Code-ADR-0002) und wird in die Adapter injiziert (Helix, Auth). Es gibt keine Clients auf Paketebene.
   - `WithoutRetry` schaltet die Wiederholung je Aufruf ab: für Einmal-Verbrauch, z. B. der OAuth-Code-Austausch (der Code ist einmalig; eine Wiederholung nach einer verlorenen Antwort würde ohnehin fehlschlagen).
   - `EachPage`: Paginierung nach dem Helix-Muster (Punkt 7).
   - Migration des 4.1-Auth-Codes (`internal/auth`) auf diesen Client mit dem Breaker `twitch.auth` (Token holen, erneuern, widerrufen) ist Folgearbeit; der Code-Austausch läuft mit `WithoutRetry`.

4. **Wiederholung:**

   | Ergebnis | Verhalten |
   |---|---|
   | Netzwerkfehler, Timeout der Anfrage, HTTP 5xx | wiederholbar: exponentieller Backoff (Basis 1 s, volles Jitter) |
   | HTTP 429 | wiederholbar: Wartezeit bis `x-ratelimit-reset` (Fallback: `Retry-After`, Fallback: Backoff); ohne Jitter, der Server nennt den Zeitpunkt |
   | HTTP 4xx außer 429 | nicht wiederholbar: `StatusError` (Punkt 6) |
   | Breaker offen oder halb offen voll | nicht wiederholbar: `breaker.ErrUnavailable` (Code-ADR-0007, Punkt 7) |
   | abgelaufener oder abgebrochener Kontext | nicht wiederholbar: der Kontextfehler erscheint |

   - **Budget:** höchstens 3 Versuche und höchstens 30 s Gesamtwartezeit (Backoff- und 429-Wartezeiten zusammen); wird es überschritten, erscheint der letzte typisierte Fehler (bei 429: `ErrTooManyRequests` mit `RateLimit`-Angaben). Der Aufrufer entscheidet über einen späteren Versuch (Command-Warteschlange); eine Anfrage blockiert nicht ohne Grenzen.
   - **Backoff-Implementierung:** `retry.NewExponential(1s)` mit `retry.WithFullJitter`, begrenzt per `retry.WithMaxRetries` (Versuche − 1) und `retry.WithMaxDuration` (30 s), umhüllt von einer dünnen `Backoff`-Hülle, die die 429-Override-Wartezeit setzt (servergenannte Zeit, ohne Jitter) und jede Wiederholung loggt.
   - **Logging** (Code-ADR-0003): je Wiederholung `WARN` (Versuch, Status oder Fehler, Wartezeit), je 429 `INFO` (Warten bis Reset); keine Anfrage-Protokollierung pro Erfolg.

5. **Rate-Limit (zwei Ebenen, beide lesen die Antwort-Header):**
   - **Clientseitig (Token-Bucket, `x/time/rate`):** Standard 4/s mit Burst 8 — unter den je-Fenster-Grenzen der Helix-Lese-Endpunkte (30/5 s) und der globalen (800/min); je API im Code überschreibbar, vorerst ohne Einstellung für Nutzer (analog Code-ADR-0007, Punkt 4). Der Limiter glättet Burst-Last aus der Command-Engine, bevor sie bei Twitch ankommt.
   - **Serverseitig (Header):** `x-ratelimit-remaining` und `x-ratelimit-reset` werden aus jeder Antwort (auch 2xx) gelesen und in den Limiter gespeist; bei `remaining = 0` geht die nächste Anfrage nicht vor `x-ratelimit-reset` (vermeidet das 429).
   - Bei 429 wartet die Wiederholung (Punkt 4).
   - `RateLimit` (`Remaining int`, `Reset time.Time`) erscheint zusätzlich auf der `Response`, damit Aufrufer es nutzen können (z. B. Statusanzeige, Zeitplanung).
   - Die Verträglichkeit von `x/time/rate` mit der `testing/synctest`-Fake-Uhr (Code-ADR-0006) wird im ersten Test-PR geprüft, wie die Prüfung von gobreaker in Code-ADR-0007 (Punkt 11).

6. **Typisierte Fehler** (Code-ADR-0003):

   ```go
   type StatusError struct {
       StatusCode int
       RateLimit  RateLimit // aus den Antwort-Headern, wenn vorhanden
       Snippet    string    // kurzes Body-Auszug (gekürzt, ohne Geheimnisse)
   }
   var ErrTooManyRequests = errors.New("rate limited") // 429; StatusError(429) trifft per errors.Is
   ```

   - Jede Antwort außerhalb 2xx wird zu `*StatusError` (Code, Rate-Limit-Angaben, kurzes Snippet des Bodies).
   - Netzwerkfehler und Timeouts bleiben Standard-Go-Fehler (z. B. `*url.Error`) unverändert.
   - Der Breaker erscheint nach außen nur als `breaker.ErrUnavailable` (definiert in `internal/breaker` mit API-Namen und frühestem Neuvieruch, Code-ADR-0007 Punkt 7); gobreaker-Typen verlassen `internal/breaker` und `internal/httpclient` nicht.
   - Die Fehlerbewertung des Breakers (Code-ADR-0007, Punkt 5) liest `*StatusError.StatusCode` und die Fehlerarten: 5xx und Netzwerkfehler zählen als Fehler, übrige 4xx als Erfolg, 429 und `context.Canceled` sind ausgeschlossen.
   - Die Helix-Schicht bildet `StatusError` bei Bedarf auf Endpunkt-Meinungen ab (z. B. 404 „Benutzer nicht gefunden“).

7. **Paginierung:** `EachPage` nach dem Helix-Muster: `first` als Seitengröße (Standard 100, je Adapter konstant), Cursor über `after`; die Seite liefert `data` und `pagination.cursor`; die Schleife endet bei leerem Cursor oder Fehler aus `fn`; Sicherheitsobergrenze 200 Seiten je Aufruf.

8. **Timeout:** 10 s je Anfrage (Basis-Client, Punkt 3); der Kontext des Aufrufers greift zusätzlich (wer zuerst abläuft).

9. **Tests** (`testing/synctest` + `httptest`, Code-ADR-0006):
   - Wiederholung: 500 dann 200 → Erfolg nach Backoff (Fake-Uhr); 400 → keine zweite Anfrage; Netzwerkfehler → Wiederholung; Budget erschöpft → typisierter Fehler.
   - 429: Warten genau bis `x-ratelimit-reset` (Fake-Uhr); `remaining = 0` → nächste Anfrage erst nach dem Reset; 429 jenseits des Budgets → `ErrTooManyRequests`.
   - Breaker: offener Breaker → sofort `ErrUnavailable`, es geht gar keine Anfrage raus.
   - Paginierung: 3 Seiten mit Cursor, Ende bei leerem Cursor, Fehler aus `fn` bricht ab.
   - `WithoutRetry`: 500 → keine Wiederholung.
   - Kontext: Abbruch während Backoff- bzw. 429-Wartezeit → der Kontextfehler erscheint.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `hashicorp/go-retryablehttp` | vom Projektinhaber ausgeschlossen (2026-10-09); monolithisch (Wiederholung, TLS, Logging fest verdrahtet), die Reihenfolge Breaker → Rate-Limiter → Anfrage nur schwer zu setzen, Lizenz MPL-2.0 |
| `cenkalti/backoff/v5` | solide Alternative (Backoff-Algorithmen + Wiederholungs-Schleife); nicht gewählt, weil es nicht im Modulgraph liegt und die Markierung der Wiederholbarkeit am Fehler (`RetryableError`) direkter ist; `sethvargo/go-retry` ist die gepflegte Autorlinie und bereits vorhanden |
| `avast/retry-go` | v4 ist nicht mehr die Hauptlinie; die gepflegte Nachfolge ist `sethvargo/go-retry` |
| `failsafe-go` | in Code-ADR-0007 abgelehnt: vor 1.0, API breiter als gebraucht |
| eigene Implementierung (nur Standardbibliothek) | die Wiederholungs-Schleife ist klein, aber Jitter, Kontext-Handling und der Breaker-Zustandsautomat sind in Code-ADR-0007 als eigene Implementierung abgelehnt; `x/time/rate` bräuchte es ohnehin, der Standardbibliothek-Nutzen wäre gering |
| Middleware als `http.RoundTripper` | die Wiederholung muss die Antwort (Status + Header) auswerten; im Transport müsste der Body gelesen und verworfen werden, um erneut zu versuchen; der Wrapper um `Do` ist klarer und testbarer |
| nur 429-Handling, ohne Client-Rate-Limiter | die Reihenfolge in Code-ADR-0007 (Punkt 6) sieht einen Rate-Limiter vor; das Token-Bucket glättet Burst-Last aus der Command-Engine und reduziert 429 |

## Konsequenzen

**Positiv:**

- Alle Plattform-APIs teilen sich einen getesteten Client: Wiederholung, Breaker, Drosselung, Paginierung, typisierte Fehler. Die Endpunkte aus 4.2 und die späteren Plattformen bauen darauf auf.
- Die Reihenfolge aus Code-ADR-0007 ist umgesetzt und getestbar; ein gestörter Dienst scheitert schnell (Breaker) und ein drosselnder wird nicht belastet (429-Handling).
- Kleine, gepflegte Bibliotheken ohne eigene Laufzeitabhängigkeiten; zwei davon liegen bereits im Modulgraph.

**Negativ und Risiken:**

- Eine neue direkte Abhängigkeit: `golang.org/x/time` (go-retry liegt bereits indirekt im Graph und wird direkt; gobreaker ist durch Code-ADR-0007 festgelegt).
- `sethvargo/go-retry` ist jung (v0.x): Die API kann sich mit einer Hauptversion ändern. Der Nutzen ist auf `Do`, `NewExponential`, `WithFullJitter`, `WithMaxRetries`, `WithMaxDuration` und das `Backoff`-Interface begrenzt, der Wechsel bleibt lokal.
- Die Standardwerte (3 Versuche, Basis 1 s, Budget 30 s, 4/s Burst 8, Timeout 10 s) sind Schätzungen; sie werden mit dem Test-Stream aus Phase 4 und den Chaos-Tests aus Phase 11 überprüft (analog Code-ADR-0007).
- Die Verträglichkeit von `x/time/rate` mit der synctest-Fake-Uhr muss sich im Test zeigen; fällt sie aus, kommt ein dünnes eigenes Token-Bucket oder ein Test mit echter Zeit.
- Das 429-Handling setzt die `x-ratelimit-*`-Header voraus (von Twitch dokumentiert); ohne sie greifen `Retry-After` bzw. der Backoff.

**Folgearbeiten:**

- [ ] `internal/breaker` nach Code-ADR-0007 (Roadmap 4.2)
- [ ] `internal/httpclient` mit der Schnittstelle, Wiederholung, Drosselung, Paginierung und Fehlern dieses ADR (Roadmap 4.2)
- [ ] `internal/auth`: Token-Aufrufe (Token holen, erneuern, widerrufen) auf den httpclient mit dem Breaker `twitch.auth`, Code-Austausch mit `WithoutRetry` (Code-ADR-0007, Folgearbeit; Roadmap 4.2)
- [ ] Helix-Endpunkte auf dem httpclient mit dem Breaker `twitch.helix` (Roadmap 4.2)
- [ ] Standardwerte mit dem Test-Stream (Phase 4) und den Chaos-Tests (Phase 11) überprüfen
- [x] Abnahme durch den Projektinhaber, Status gesetzt und Index in [`README.md`](README.md) angepasst, erledigt 2026-10-09
