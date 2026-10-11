# Code-ADR-0015: WebSocket-Bibliothek

| | |
|---|---|
| **Status** | Vorgeschlagen |
| **Datum** | 2026-10-10 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Roadmap 4.3 (EventSub-WebSocket); [Code-ADR-0001](0001-go-toolchain-und-linting.md) (Abhängigkeiten, Allowlist), [Code-ADR-0003](0003-fehler-und-logging.md), [Code-ADR-0004](0004-nebenlaeufigkeit-und-supervisor.md), [Code-ADR-0006](0006-teststrategie.md), [Code-ADR-0018](0018-json-v2.md); [ADR-0004](../0004-plattformumfang-zum-start.md) |

## Kontext

Phase 4.3 verbindet sich mit dem Twitch-EventSub-WebSocket
(`wss://eventsub.wss.twitch.tv/ws`) und braucht eine WebSocket-Bibliothek für Go. Der Client hält eine dauerhafte Verbindung, hält den Keepalive (PING/PONG-Texte im 10-s-Intervall), verbindet nach `session_reconnect` und ungeplanten Abbrüchen neu und bricht alles am Aufrufer-Kontext ab (Code-ADR-0004).

Kandidaten (verifiziert 2026-10-10):

| Bibliothek | Version | Lizenz | Pflege | API |
|---|---|---|---|---|
| `github.com/coder/websocket` | v1.8.15 (2026-06-15) | ISC | aktiv (gepflegte Fortsetzung von `nhooyr/websocket`; der Modul-Proxy löst beide Pfade auf denselben Code) | Kontext-API: `Dial(ctx, …)`, `Conn.Read(ctx)`, `Conn.Write(ctx, …)`, `Conn.Close(code, reason)`; `CloseError` mit Schließcode; kein automatisches Keepalive |
| `github.com/gorilla/websocket` | v1.5.3 (2024-06) | BSD-2-Clause | seit über zwei Jahren ohne Release | Deadlines pro Verbindung (`SetReadDeadline`), keine Kontext-API |

Der EventSub-Keepalive läuft auf Nachrichten-Ebene (Twitch schickt die
PING-Texte, der Client antwortet mit PONG-Texten) — das automatisierte
WS-Ping einer Bibliothek würde ihn nicht ersetzen, sondern nur
doppeln.

## Entscheidung

1. **Bibliothek:** `github.com/coder/websocket` v1.8.15. Die ISC-Lizenz steht in der Allowlist (Code-ADR-0001); die Bibliothek ist die aktiv gepflegte Fortsetzung des `nhooyr/websocket`-Kodes.
2. **Kontext-API:** Verbindung, Lesen und Schreiben laufen mit dem Aufrufer-Kontext (`Dial(ctx, …)`, `Read(ctx)`, `Write(ctx, …)`); das Herunterfahren schließt die Verbindung geordnet (`Close(1000, …)`). Deadlines pro Verbindung (das `gorilla`-Muster) werden nicht verwendet.
3. **Keepalive selbst:** Der Client antwortet auf die PING-Texte von Twitch mit PONG-Texten im vorgegebenen Intervall (`keepalive_interval_seconds` aus `hello`) und schließt die Verbindung, wenn die eigene Antwort nicht mehr passt (Code-ADR-0003: Log-Eintrag). Ein WS-eigenes Ping (z. B. `Conn.Ping`) kommt nicht dazu.
4. **Reconnect:** `session_reconnect` wird mit der mitgelieferten `reconnect_url` und der Session-ID neu verbunden (Ereignisse seit dem letzten verarbeiteten `message_id` kommen erneut — die Deduplizierung per `message_id` von Roadmap 4.3/B22 wirft die Wiederholungen weg). Ungeplante Abbrüche verbinden mit exponentiellem Backoff neu (Basis 1 s, volles Jitter, 30-s-Budget — die Werte des HTTP-Client, Code-ADR-0014, Punkt 4).
5. **JSON:** Die Protokoll-Nachrichten und die Ereignis-`event`-Objekte werden mit `encoding/json/v2` gelesen (Code-ADR-0018); das innere `event`-Feld bleibt als `jsontext.Value` liegen, bis die Abbildung (Roadmap 4.3) es typisiert ausliest.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `github.com/gorilla/websocket` | Stagnant (seit 2024 ohne Release) und ohne Kontext-API: Deadlines pro Verbindung passen nicht zu Code-ADR-0004, und ein abgebrochenes Herunterfahren müsste per Kanal gemeldet werden. |
| `github.com/nhooyr/websocket` (direkt) | Derselbe Code wie `coder/websocket`, aber der Original-Pfad ist die nicht mehr gepflegte Hälfte; der Fortführungs-Pfad ist die klarere Abhängigkeit. |
| Eigenes WebSocket | Das Protokoll (Frames, Masking, Upgrade) ist fehleranfällig und nicht unser Zweck. |

## Konsequenzen

**Positiv:**

- Kontext-API ohne Deadlines: das Herunterfahren bricht Lese- und Schreiboperationen am Kontext ab (Code-ADR-0004).
- Aktiv gepflegt, ISC (in der Allowlist), kleiner und ohne weitere Abhängigkeiten.
- `CloseError` mit Schließcode: Abbrüche (Twitch 4000/4001, Netzwerk) lassen sich typisiert unterscheiden und loggen.

**Negativ und Risiken:**

- Jünger als `gorilla/websocket`; Änderungen an der API zwischen den Releases sind möglich (Pinning + Allowlist-Meldung in der CI).
- Der Keepalive bleibt eigene Logik (Intervall-Timer, PONG-Timeout) — wird in den 4.3-Tests mit der Fake-Uhr geprüft (Code-ADR-0006).

## Revision 2026-10-11 (Phase 4.5)

Die 4.5-Tests gegen den offiziellen `twitch-cli`-Mock (die Referenz-
implementierung des Protokolls) haben gezeigt, dass die Annahme im
Kontext zu Punkt 3 falsch war. Tatsächliches Protokoll (verifiziert
gegen twitch-cli v1.1.24 und die EventSub-WebSocket-Dokumentation):

- Alle Frames sind `{"metadata":{"message_id","message_type",…},"payload":{…}}`; die Typen sind `session_welcome`, `session_keepalive`, `notification`, `session_reconnect`, `revocation`.
- Der Server hält die Verbindung mit `session_keepalive`-Frames lebendig (im 10-s-Takt), nicht mit `PING`-Texten; der Client sendet gar keine Anwendungsnachrichten (der Mock schließt Clients mit Close-Code 4001, die Inbound-Traffic senden), und jede empfangene Frame hält die Verbindung lebendig (WS-Ping/pong läuft transparent über die Bibliothek).
- Die Ereignisse kommen als `notification` mit `payload` = EventsubResponse (`subscription` mit `type`/`version` + `event`); die Wiederholungen nach Reconnects (B22) werden anhand des Nutzlast-Inhalts verworfen.
- `session_reconnect` trägt `payload.session` mit `status: "reconnecting"` und der `reconnect_url` (selbsttragend, inkl. Reconnect-Identität); `revocation` kommt als `payload.revocation.reason`.

Umgesetzt in `internal/twitch` (Plan 4.5, Aufgabe 4); die 4.3-Tests
sprechen seither das echte Frame-Format.

**Folgearbeiten:**

- [ ] WebSocket-Client von Roadmap 4.3 (Paket `internal/twitch`), Plan `docs/superpowers/plans/2026-10-10-eventsub-websocket.md`
- [ ] Im ersten Test-Lauf prüfen, ob das In-Memory-Netzwerk von `httptest.NewTestServer` WebSocket-Upgrades trägt; Fallback: Loopback-WS-Server außerhalb der synctest-Bubble (Plan 4.3, offener Punkt 2)
