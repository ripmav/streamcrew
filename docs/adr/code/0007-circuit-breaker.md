# Code-ADR-0007: Circuit Breaker für externe Dienste

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §6.11, §6.18, §13 (R4); Roadmap Phase 4.1 und 4.2; [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0003](0003-fehler-und-logging.md), [Code-ADR-0004](0004-nebenlaeufigkeit-und-supervisor.md), [Code-ADR-0006](0006-teststrategie.md); geplantes Code-ADR zum HTTP-Client (ADR-Backlog in Plan §12.2) |

## Kontext

- Der Core ruft laufend externe Dienste auf: ab Phase 4 die Twitch-Helix-API und den OAuth-Endpunkt von Twitch, später YouTube, Kick und die Integrationen (Streamlabs, StreamElements, Discord, TTS-Dienste). Viele Aufrufe kommen aus Actions, also aus der Command-Engine.
- Solche Dienste fallen zeitweise aus oder antworten nur noch mit Fehlern oder Timeouts. Ohne Schutz wartet jeder Aufruf bis zu seinem Timeout, Wiederholungen verlängern das, die Warteschlange der Commands staut sich, und der Core hämmert weiter auf einen Dienst, der sich gerade erholen will. Twitch kann eine Client-ID bei auffälligem Verhalten drosseln.
- Langlebige Verbindungen (EventSub-WebSocket, OBS-WebSocket) sind Runnables im Supervisor und werden dort mit Backoff neu aufgebaut ([Code-ADR-0004](0004-nebenlaeufigkeit-und-supervisor.md)). Für einzelne Anfragen gibt es bisher nichts Vergleichbares.
- Wiederholungen, Rate-Limits und typisierte HTTP-Fehler regelt das geplante Code-ADR zum HTTP-Client (Phase 4.2). Ein Circuit Breaker ergänzt das: Er erkennt einen gestörten Dienst und lässt Aufrufe sofort scheitern, bis der Dienst wieder antwortet.
- Die Standardbibliothek und `golang.org/x/…` haben keinen Circuit Breaker. Der Projektinhaber hat `github.com/sony/gobreaker` vorgeschlagen.
- `github.com/sony/gobreaker/v2` (v2.4.0, Januar 2026, MIT):
  - generisch (`CircuitBreaker[T]`), ohne Abhängigkeiten zur Laufzeit; testify braucht die Bibliothek nur für ihre eigenen Tests
  - Zustände geschlossen, offen, halb offen
  - Zählfenster fest oder rollierend (`BucketPeriod`), eigene Auslöse-, Erfolgs- und Ausschlussregeln (`ReadyToTrip`, `IsSuccessful`, `IsExcluded`), Callback bei Zustandswechseln
  - `TwoStepCircuitBreaker` für Stellen, an denen Anfang und Ende eines Aufrufs getrennt sind
  - Der Callback `OnStateChange` läuft, während der Breaker gesperrt ist.
  - Der Wechsel von offen zu halb offen geschieht erst beim nächsten Aufruf bzw. bei `State()`, nicht per Timer.
  - Die Bibliothek nutzt das Paket `time` und funktioniert deshalb mit der Fake-Uhr von `testing/synctest` (geprüft 2026-09-29).

## Entscheidung

1. **Wir verwenden `github.com/sony/gobreaker/v2`** als Circuit Breaker für ausgehende Anfragen an externe Dienste.
2. **Einsatzbereich:**

   | Mit Breaker | Ohne Breaker |
   |---|---|
   | Anfrage-Antwort-Aufrufe an Plattform-APIs: Twitch Helix, später YouTube Data API, Kick REST, Velora, VPZone | langlebige Verbindungen (EventSub-, OBS-WebSocket, YouTube `streamList`): Sie laufen als Runnables mit Backoff im Supervisor |
   | OAuth-Endpunkte (Token holen und erneuern) | lokale Abhängigkeiten wie SQLite und das Dateisystem |
   | HTTP-APIs der Integrationen | die WebRequest-Action mit frei gewählten Zielen: Der Nutzer bestimmt das Ziel, und je Ziel einen Breaker zu führen hieße, ihre Zahl nicht zu begrenzen |

   Auch Aufrufe, die ein Runnable beim Verbindungsaufbau macht, etwa das Anlegen der EventSub-Subscriptions über Helix, laufen durch den Breaker ihrer API.
3. **Ein Breaker je Dienst-API**, nicht je Endpunkt und nicht je Konto.
   - Namen nach dem Muster `<plattform-oder-integration>.<api>`, z. B. `twitch.helix`, `twitch.auth`.
   - Grund: Ausfälle betreffen den Dienst, einzelne Endpunkte haben zu wenig Verkehr für eine aussagekräftige Zählung, und Probleme eines Kontos (abgelaufenes Token, fehlende Scopes) sind keine Störung des Dienstes.
   - Alle Aufrufer einer API teilen sich eine Instanz. Sie entsteht im Konstruktor des Adapters bzw. in der Composition Root und wird injiziert; es gibt keine Breaker auf Paketebene ([Code-ADR-0002](0002-dependency-injection.md)).
4. **Standardwerte**, im Code je API überschreibbar, vorerst ohne Einstellung für Nutzer:

   | Einstellung | Wert | Bedeutung |
   |---|---|---|
   | Zählfenster | 60 s, rollierend in 10-s-Schritten (`Interval`, `BucketPeriod`) | Alte Fehler verfallen gleichmäßig statt schlagartig. |
   | Auslösen (`ReadyToTrip`) | mindestens 5 Fehler in Folge, oder mindestens 10 Anfragen mit mindestens 50 % Fehlern im Fenster | Die erste Regel erkennt Ausfälle auch bei wenig Verkehr; die zweite erfasst gehäufte Fehler bei viel Verkehr. |
   | offen (`Timeout`) | 30 s | Danach lässt der Breaker Probe-Anfragen durch (halb offen). |
   | halb offen (`MaxRequests`) | 3 | Höchstens 3 Probe-Anfragen, weitere scheitern sofort; 3 Erfolge in Folge schließen den Breaker, ein Fehler öffnet ihn wieder. |

5. **Was als Fehler zählt**, einheitlich für alle HTTP-APIs. Der HTTP-Client liefert dafür typisierte Fehler mit Statuscode ([Code-ADR-0003](0003-fehler-und-logging.md)):

   | Ergebnis | Bewertung |
   |---|---|
   | Netzwerkfehler, Timeout der Anfrage, HTTP 5xx | Fehler |
   | HTTP 2xx, 3xx und 4xx außer 429 | Erfolg: Der Dienst arbeitet, die Anfrage war falsch oder nicht erlaubt. |
   | HTTP 429 | ausgeschlossen (`IsExcluded`): Drosselung ist keine Störung; der Rate-Limiter des HTTP-Clients wartet bis `Ratelimit-Reset`. |
   | vom Aufrufer abgebrochener Kontext (`context.Canceled`) | ausgeschlossen |

6. **Reihenfolge im HTTP-Client:** Wiederholung (außen) → Breaker → Rate-Limiter → Anfrage mit Timeout.
   - Jeder Versuch läuft einzeln durch den Breaker. Wiederholungen eines gestörten Dienstes lösen ihn deshalb schnell aus.
   - Ist der Breaker offen oder halb offen voll, wird nicht wiederholt.
   - Wo Anfang und Ende eines Aufrufs getrennt sind, etwa in einem `http.RoundTripper`, dient `TwoStepCircuitBreaker`. Die Einzelheiten legt das Code-ADR zum HTTP-Client fest.
7. **Fehler nach außen:** `gobreaker.ErrOpenState` und `gobreaker.ErrTooManyRequests` übersetzt der Adapter an seiner Grenze in den Domänenfehler `ErrUnavailable`, mit dem Namen der API und dem frühesten Zeitpunkt für einen neuen Versuch.
   - Actions scheitern damit sofort mit einer klaren Meldung, etwa „Twitch-API derzeit nicht erreichbar“. Die Command-Engine arbeitet weiter.
   - Aufrufer prüfen mit `errors.Is(err, ErrUnavailable)`; gobreaker-Typen erscheinen außerhalb der Adapter und des HTTP-Clients nicht.
8. **Zustand und Beobachtbarkeit:**
   - Wechsel werden geloggt: `WARN` beim Öffnen, `INFO` beim Schließen, mit dem Namen des Breakers und der `component` des Adapters.
   - Ab dem Event-Bus (Phase 2.3) wird jeder Wechsel zusätzlich als Ereignis zum Verbindungsstatus veröffentlicht. So zeigen API und Frontends „Twitch-API gestört“.
   - Der Callback läuft unter der Sperre des Breakers. Er darf deshalb nicht blockieren und den Breaker nicht aufrufen: nur loggen und nicht blockierend veröffentlichen.
   - Ein offener Breaker macht den Core nicht „nicht bereit“ (`/readyz`), weil der Core ohne den Dienst weiterarbeitet.
9. **Eine dünne eigene Schicht** (`internal/breaker`) erzeugt Breaker mit den Standardwerten, der Fehlerbewertung, dem Logging und der Übersetzung aus Punkt 7. Die Adapter nutzen nur sie. Ein Wechsel der Bibliothek betrifft damit ein Paket.
10. **Nicht verwendet:** `DistributedCircuitBreaker`. Der Core ist ein einzelner Prozess, ein gemeinsamer Speicher für den Zustand ist unnötig.
11. **Tests** laufen in `testing/synctest` ([Code-ADR-0006](0006-teststrategie.md)): Auslösen, Übergang nach halb offen und Schließen, gegen einen `httptest`-Server, der Fehler liefert. Der Breaker wird erst halb offen, wenn mehr als `Timeout` vergangen ist, nicht bei genau `Timeout`.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| kein Breaker, nur Wiederholungen und Timeouts | Bei einem Ausfall wartet jede Action die volle Zeit ab; die Warteschlange staut sich, und der Dienst bekommt weiter Last. |
| eigene Implementierung | ohne Abhängigkeit, aber Zustandsautomat, rollierendes Fenster und begrenzte Proben im halb offenen Zustand sind fehleranfällig; gobreaker ist klein, erprobt und hat keine Laufzeitabhängigkeiten |
| `github.com/sony/gobreaker` v1 | nicht generisch, ohne rollierendes Fenster und ohne `IsExcluded`; v2 ist die gepflegte Linie |
| `github.com/failsafe-go/failsafe-go` | umfassend (Wiederholung, Breaker, Bulkhead, Rate-Limit, Timeout), aber vor Version 1.0 und mit viel mehr API, als gebraucht wird; überschneidet sich mit `golang.org/x/time/rate` und dem eigenen HTTP-Client |
| `github.com/afex/hystrix-go` | seit 2018 ohne Release; globale Konfiguration und eine Goroutine je Aufruf |
| `github.com/eapache/go-resiliency` (breaker) | sehr einfach: nur Fehler in Folge, kein rollierendes Fenster, keine Ausschlüsse |
| `github.com/mercari/go-circuitbreaker` | seit 2022 ohne Release, wenig verbreitet |
| Breaker je Endpunkt | zu wenig Verkehr je Endpunkt für eine verlässliche Zählung; viele Instanzen ohne Mehrwert |

## Konsequenzen

**Positiv:**

- Bei einer Störung scheitern Aufrufe sofort mit einer verständlichen Meldung. Die Command-Engine staut sich nicht, und der gestörte Dienst bekommt Ruhe.
- Einheitliche Fehlerbewertung und Standardwerte für alle externen HTTP-APIs.
- Der Zustand der Dienste ist in Log, Ereignissen und Frontends sichtbar.

**Negativ und Risiken:**

- Eine Abhängigkeit mehr: `github.com/sony/gobreaker/v2` (MIT, in der Allowlist).
- Die Standardwerte sind Schätzungen. Sie werden mit dem Test-Stream aus Phase 4 und den Chaos-Tests aus Phase 12 überprüft.
- Ein Breaker je API kann bei einem einzelnen dauerhaft defekten Endpunkt die ganze API sperren, wenn nur dieser Endpunkt aufgerufen wird. Die Quote-Regel und das Zählfenster mildern das; tritt es auf, bekommt der Endpunkt einen eigenen Breaker.
- Der Zustand „halb offen“ wird erst beim nächsten Aufruf sichtbar, nicht genau nach Ablauf der 30 s.

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen und den Index in [`README.md`](README.md) anpassen, erledigt 2026-09-29
- [ ] `internal/breaker` mit Standardwerten, Fehlerbewertung, Logging und Übersetzung in `ErrUnavailable` umsetzen (Roadmap Phase 4.2)
- [ ] Im Code-ADR zum HTTP-Client die Reihenfolge Wiederholung → Breaker → Rate-Limiter → Anfrage festhalten und den Breaker einbauen (Roadmap Phase 4.2)
- [ ] Breaker für `twitch.helix` und `twitch.auth` (Roadmap Phase 4.1 und 4.2), später für die übrigen Plattformen und Integrationen
- [ ] Zustandswechsel als Ereignis auf den Event-Bus legen und in API und Frontends anzeigen (Roadmap Phase 4 bzw. 6)
- [ ] Standardwerte nach dem Test-Stream (Phase 4) und den Chaos-Tests (Phase 12) überprüfen
