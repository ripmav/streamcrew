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
4. **Reconnect:** `session_reconnect` wird mit der mitgelieferten `reconnect_url` und der Session-ID sofort neu verbunden (Wiederholungen liefert die Plattform erneut — die Deduplizierung (B22) wirft sie weg). Ungeplante Abbrüche verbinden nach einem festen 10-s-Wart neu (Revision 2026-10-21); ein offener Circuit-Breaker mit bekanntem `retry-after` wird im eigenen Takt ge-probt (der längere der beiden Wartezeiten).
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
- Die Ereignisse kommen als `notification` mit `payload` = EventsubResponse (`subscription` mit `type`/`version` + `event`); die Wiederholungen nach Reconnects (B22) werden anhand des Inhalts des Event-Objekts verworfen — die Subscription-Shell (Session-ID, created_at) ändert sich pro Session, das Event-Objekt (mit der Nachrichten-ID) bleibt gleich.
- `session_reconnect` trägt `payload.session` mit `status: "reconnecting"` und der `reconnect_url` (selbsttragend, inkl. Reconnect-Identität); `revocation` kommt als `payload.revocation.reason`.

Umgesetzt in `internal/twitch` (Plan 4.5, Aufgabe 4); die 4.3-Tests
sprechen seither das echte Frame-Format.

**Folgearbeiten:**

- [ ] WebSocket-Client von Roadmap 4.3 (Paket `internal/twitch`), Plan `docs/superpowers/plans/2026-10-10-eventsub-websocket.md`
- [x] Im ersten Test-Lauf prüfen, ob das In-Memory-Netzwerk von `httptest.NewTestServer` WebSocket-Upgrades trägt (erledigt 2026-10-21 in `fix/eventsub-drop-wait`): es trägt sie, auch in der synctest-Bubble (Fake-Clock) — die Drop-Wait-Tests laufen in virtuellen Sekunden, kein Loopback-Fallback nötig

## Revision 2026-10-21 (Busy-Loop-Fix, Log-Analyse F1)

Punkt 4 der Entscheidung (Reconnect) ändert das Retry-Verhalten bei
ungeplanten Abbrüchen: statt exponentiellem Backoff (Basis 1 s, volles
Jitter, 30-s-Lifetime-Budget) wartet `Client.Run` einen **festen 10-s-Wart**
(`dropWait`, in `Options.DropWait` für Tests übersteuerbar) und dialt dann
erneut am Base-URL.

**Begründung:** Der alte Backoff lieferte nach Ablauf des 30-s-Budgets
`(0, true)` („aufhören"); `Run` warf den Stop-Flag weg (`d, _ :=`), und
`time.After(0)` feuerte sofort — ein Busy-Loop mit ~286 Dial-Versuchen/s,
CPU-Spin und ~600 MB Log/Tag (Log-Analyse 2026-10-21, F1). Der feste 10-s-
Rhythmus ist vorhersagbar, und ein offener Circuit-Breaker (Code-ADR-0007)
mit bekanntem `retry-after` bestimmt das Tempo: die Wartezeit ist der
längere der beiden Werte (10 s oder `retry-after`), damit ein 30 s offener
Breaker 1–2 Versuche/min statt 286/s sieht. `session_reconnect` bleibt
unverändert sofort; Token-Verlust (`ErrToken`) endet ohne Wartung.

## Revision 2026-10-22 (Session-Lebenszyklus, EventSub-Reference-Abgleich)

Der Abgleich mit der EventSub Reference (Plan
`docs/superpowers/plans/2026-10-22-eventsub-reference-fix.md`, Findings
F9–F12) ändert den Session-Lebenszyklus:

1. **`session_reconnect` (F9):** Die `reconnect_url` wird **unverändert**
   verwendet (Doku: „Use the reconnect URL as is; do not modify it“) —
   der `access_token` wird nicht mehr in die Query gemergt (die URL ist
   selbsttragend, in Produktion wie im Mock). Relative URLs werden gegen
   die Base absolut aufgelöst (die Query bleibt). Die **alte Session
   bleibt offen, bis die neue Session ihre `session_welcome`-Message
   gebracht hat** (Doku: „You should not close the old connection until
   you receive a Welcome message on the new connection“), damit kein
   Event verloren geht: die neue Session wird parallel gedialt, die alte
   liest weiter. Ohne Welcome innerhalb der Reconnect-Frist (30 s) dient
   die alte Session den Rest der Frist und wird dann am Base-URL neu
   verbunden. Die `transport.session_id`-Felder der Subscriptions ändern
   sich bei einem `session_reconnect` nicht — die Subscriptions tragen
   mit der Session über (Doku), und das Re-pointing (Punkt 3) berührt
   sie nicht (Status bleibt `enabled`).
2. **Revocation (F11):** Der `revocation`-Frame kommt als
   `payload.subscription` mit `id`, `type`, `version` und `status` (nicht
   `payload.revocation.reason` wie in der Revision 2026-10-11
   vermutet). Je Status: `authorization_revoked` — das Token ist weg,
   `OnRevoked` + Session neu mit frischem Token (wie bisher);
   `user_removed` (der Nutzer existiert nicht mehr) und
   `version_removed` (Typ und Version werden nicht mehr unterstützt) —
   nur **eine** Subscription ist weg, die Session bleibt offen;
   `OnSubscriptionRevoked` meldet sie an den Subscription-Manager, der
   sie aus der aktuellen Soll-Tabelle ersetzt.
3. **Re-pointing nach Drop (F10):** Ein Abbruch der Session deaktiviert
   ihre Subscriptions automatisch (Doku: „If you disconnect from a
   WebSocket session, all subscriptions associated with that session are
   automatically disabled“). Der Manager erkennt sie am Status: eine
   eigene Subscription, die im Soll-Zustand steht, aber nicht mehr den
   Status `enabled` trägt, wird revokiert und für die aktuelle Session
   neu erstellt. Der Status ist das Kriterium statt der
   `transport.session_id`, weil die ID bei einem `session_reconnect` mit
   den Subscriptions unverändert bleibt (Punkt 1) und sie dort **nicht**
   neu erstellt werden dürfen.
4. **10-s-Fenster (F12):** Nach jeder `session_welcome` muss innerhalb
   des `keepalive_timeout_seconds`-Fensters (Default 10 s, Close 4003
   „connection unused“) mindestens eine Subscription existieren. Die
   Plattform meldet daher jede neue Session-ID an den Manager, der
   **sofort** nach jeder neuen Session reconciliert (die
   `session_reconnect`-Reconciliation ist ein No-Op, Punkt 1).
