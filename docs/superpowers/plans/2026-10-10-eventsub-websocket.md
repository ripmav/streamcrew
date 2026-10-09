# Plan: Roadmap 4.3 — EventSub-WebSocket

**Status:** in Ausführung (Tasks 1–6 offen)
**Stand:** 2026-10-10, `main` bei `56cfdeb`; 4.2 steht in Stack #162
(PR #158–#161, #163–#167, Merging ausstehend)
**Scope:** nur 4.3. 4.4 (Funktionen, inkl. der Spezifikation
`twitch-events.md`) und 4.5 (Tests) bleiben offen; dieser Plan liefert
den Adapter, der die Ereignisse an den Event-Service übergibt.

## 1. Was 4.3 liefert (Roadmap)

- [ ] Code-ADR-0015 WebSocket-Bibliothek (S)
- [ ] WebSocket-Client (L):
  - Welcome-Nachricht, Keepalive-Überwachung
  - `session_reconnect` ohne Eventverlust
  - Revocation
  - Deduplizierung per `message_id`
- [ ] Subscription-Manager (M):
  - Sollzustand nach Plan Anhang A.2
  - Abgleich mit bestehenden Subscriptions
  - Versionen, Bedingungen, Limits
- [ ] Abbildung auf kanonische Events und das Chat-Modell: Fragmente,
  Emotes, Cheermotes, Badges, Antworten, Shared Chat (L)

## 2. Befunde der Recherche (verifiziert 2026-10-10)

1. **`github.com/coder/websocket` v1.8.15** (2026-06-15, ISC, ~5,5k
   Stars, aktiv gepflegte Fortsetzung von `nhooyr/websocket` — der
   Modul-Proxy löst beide Pfade seit v1.8.14 auf denselben Code):
   - Kontext-API: `Dial(ctx, url, opts)`, `Conn.Read(ctx) (Message,
     error)`, `Conn.Write(ctx, op, data)`, `Conn.Close(code, reason)`;
     jede Lese-/Schiebeoperation bricht am Aufrufer-Kontext ab.
   - `Message{Operation, Payload}` mit `TextMessage`/`BinaryMessage`/
     `CloseMessage`; `CloseError` trägt den Schließcode; `Ping(ctx)`.
   - Kein automatisches Ping/Keepalive — passt, denn Twitch EventSub
     hält den Keepalive auf **Nachrichten-Ebene** (PING/PONG-Texte).
   - ISC ist in der go-licenses-Allowlist (Code-ADR-0001).
2. **`github.com/gorilla/websocket` v1.5.3** (2024-06, BSD-2-Clause):
   seit über zwei Jahren ohne Release; API ohne Kontext (Deadlines pro
   Verbindung) — passt nicht zur Code-ADR-0004-Konvention (alles am
   Kontext abbrechen).
3. **EventSub-WebSocket-Protokoll** (offizielle Doku):
   - Basis-URL `wss://eventsub.wss.twitch.tv/ws` mit
     `?access_token=<token>`; danach `?eventsub-session-id=<session>`.
   - Erste Antwort `hello`: `session_id`, `expiry` (Unix),
     `keepalive_interval_seconds` (10), `reconnect_url`.
   - Keepalive: Twitch schickt ~alle 10 s den Text `{"type":"PING"}`;
     die Antwort `{"type":"PONG"}` muss im Intervall folgen, sonst
     wird die Verbindung beendet.
   - Ereignisse als `{"id": <message_id>, "event": {"event_type",
     "version", "condition", "event": {...}}}`.
   - `session_reconnect`: mit `reconnect_url` + Session-ID
     neu verbinden; Ereignisse seit dem letzten verarbeiteten
     `message_id` können wieder ankommen → Deduplizierung per
     `message_id` (B22).
   - `revocation`: das Access-Token ist ungültig; mit neuem Token neu
     verbinden.
   - Limits: je Subscription-Typ und Bedingung je ein Slot pro
     Broadcaster; insgesamt pro Broadcaster begrenzt — Verstoß kommt
     als 400 beim Helix-Endpunkt.
4. **Eingang des Adapters** (vorhanden, Phase 3): `connector.Receiver`
   (`Message`, `Join`, `Event`, `Stream`) mit
   `connector.Dedup` (10-min-TTL, B22); der Event-Service wendet die
   Regeln von `docs/spec/events.md` an. Der Twitch-Adapter übergibt,
   was er empfängt, in Empfangsreihenfolge — exakt wie die
   Mock-Plattform.
5. **Chat-Modell** (vorhanden): `eventtype.Message{ID, Text,
   Emotes}`; `connector.Incoming{Platform, Author, FromBot, Message}`;
   Rollen in `user.Identity`. Das kanonische Modell kennt kein
   Antwort-Feld (Reply) — die EventSub-Payloads tragen
   `reply_parent_message_*`.

## 3. Design

- **Paket `internal/twitch`** (der Twitch-Adapter, parallel zu
  `internal/helix` und `internal/auth`):
  - `ws.go` — der WebSocket-Client: Verbindung, `hello`,
    PING/PONG-Keepalive, `session_reconnect`, `revocation`,
    ungeplante Abbrüche mit Backoff, `message` an einen Handler.
  - `subscriptions.go` — der Subscription-Manager: Soll-Zustand
    (Typ + Version + Bedingung je Event), Abgleich über die Helix-
    Endpunkte aus 4.2 (`GetSubscriptions`/`CreateSubscription`/
    `RevokeSubscription`), Limits als 400 → überspringen und loggen.
  - `map.go` — die Abbildung der EventSub-Payloads auf
    `connector.Receiver` (Entscheidungen in Task 4).
- **Token-Lieferant:** `auth.Service.Token` (Streamer-Konto) als
  `helix.TokenFunc`/`func(ctx) (string, error)`; bei `revocation`
  ruft der Adapter den Token erneut ab (der Auth-Service hat
  gerefresht).
- **Composition Root** (Task 5): der Adapter startet, wenn das
  Streamer-Konto einen Token hat; `Stream`-Zustand nach jedem
  Verbinden über `GetStreams` (B11).
- **JSON** `encoding/json/v2` (Code-ADR-0018); Logging mit
  `component` (Code-ADR-0003); Tests in `testing/synctest`
  (Code-ADR-0006).

## 4. Konventionen (wie 4.2)

- Zweig pro Task, Stack; Merging am Stück von unten nach oben.
- Vor jedem Commit `scripts/check.sh`; Commits englisch,
  Conventional, signiert, Footer `Assisted-by: Qwen3.8-27B (xHigh)
  via OpenCode`; PRs Titel englisch, Body deutsch
  (`## Worum geht es`, `## Änderungen`, `## Bitte prüfen (meine
  Festlegungen)`, `## Tests`).
- Roadmap: erledigte Punkte sofort abhaken + Historie.

## 5. Tasks

### Task 1 — Code-ADR-0015 WebSocket-Bibliothek (Docs-PR)

**Zweig:** `docs/code-adr-0015-websocket` (von `docs/helix-docs`)
**Inhalt:** ADR (Format `docs/adr/code/TEMPLATE.md`): Bibliothek
`github.com/coder/websocket` v1.8.15 (ISC, in der Allowlist) statt
`gorilla/websocket` (stagnant, API ohne Kontext); Kontext-API,
CloseError, kein automatisches Keepalive (Twitchs Keepalive ist
Nachrichten-Ebene). Index, Plan §12.2, Roadmap-Historie, Plan-Datei.
**Commit:** `docs(adr): WebSocket library (Code-ADR-0015)`

### Task 2 — WebSocket-Client

**Zweig:** `feat/eventsub-ws` (auf Task 1)
**Dateien:** `internal/twitch/ws.go`, `internal/twitch/message.go`
(typisierte Protokoll-Nachrichten), Tests
**Inhalt:**
- `Client` um `coder/websocket`: `Connect(ctx, url, onMessage)`;
  `hello` auslesen (Session-ID, Expiry, Keepalive-Intervall,
  Reconnect-URL), PING→PONG im Intervall, PONG-Timeout schließt die
  Verbindung.
- `message` an den Handler (Typ `Event` mit `ID` und Payload als
  `jsontext.Value`, die Aufgabe Task 4 legt den Rest aus);
  `session_reconnect` → schließen und mit der Reconnect-URL +
  Session-ID neu verbinden (kein Eventverlust: Wiederholungen
  dedupliziert der Adapter, B22); `revocation` an einen Callback.
- Ungeplanter Abbruch: exponentieller Backoff (Basis 1 s, volles
  Jitter, 30-s-Budget — wie der httpclient, ohne dessen
  Wiederholungs-Schleife, die für Verbindungen nicht gilt).
- `Close()` schließt geordnet (Code 1000).
**Tests (synctest + In-Memory-WS-Server):** hello auslesen,
PING/PONG-Intervall, PONG-Timeout → Reconnect, message an den Handler,
session_reconnect mit Session-ID in der URL, revocation an den
Callback, ungeplanter Abbruch → Backoff-Reconnects, geordnetes
Schließen.
**Commit:** `feat(twitch): the EventSub WebSocket client`

### Task 3 — Subscription-Manager

**Zweig:** `feat/eventsub-subscriptions` (auf Task 2)
**Dateien:** `internal/twitch/subscriptions.go`, Tests
**Inhalt:**
- Soll-Zustand: die Event-Typen aus Task 4 mit fester Version und
  Bedingung (Streamer-Kanal `broadcaster_user_id`, `user.whisper` mit
  `user_id`); die Liste steht als Tabelle im Code (Plan Anhang A.2
  als Bezugsgröße, nur was streamcrew abbildet).
- `Reconcile(ctx)`: Ist-Zustand per `helix.GetSubscriptions`,
  fehlende anlegen (`CreateSubscription`), Überflüssige entfernen
  (`RevokeSubscription`); 400 (Limit/Bedingung belegt) → überspringen
  und WARN-Log, kein Fehler.
- Läuft beim Start und alle 10 Minuten (Timer am Kontext, synctest-
  fassbar); nach `revocation` einmal sofort.
**Tests (Fake-Helix + synctest):** fehlend → anlegen, überflüssig →
entfernen, identisch → keine Aufrufe, 400-Limit → übersprungen,
Bedingungen/Versionen exakt.
**Commit:** `feat(twitch): the EventSub subscription manager`

### Task 4 — Abbildung auf kanonische Events und das Chat-Modell

**Zweig:** `feat/eventsub-mapping` (auf Task 3)
**Dateien:** `internal/twitch/map.go`, `internal/twitch/map_test.go`
**Inhalt (Festlegungen):**
- `channel.chat.message` → `connector.Receiver.Message` mit
  `eventtype.Message{ID, Text, Emotes}`: `message` → Text, `id` → ID,
  die Emote-`id`s aus `emotes`/`emoji` → `Emotes` (die Codes wie in
  Text); Cheermotes bleiben Text (die Bit-Menge trägt das separate
  `channel.cheer`-Event).
- Badges → Rollen in `user.Identity`: `broadcaster` → streamer,
  `moderator`/`broadcaster`-Badge → moderator, `vip` → vip,
  `subscriber`/`sub` → subscriber (die übrigen Badges —
  Premium, Turbo, Ränge — ignoriert; sie sind keine Rollen des
  Core-Modells). `FromBot` aus `user_id == Bot-ID`.
- `reply_parent_message_*`: **in 4.3 verworfen** — das kanonische
  `eventtype.Message` hat kein Antwort-Feld; Antworten werden in 4.4/
  5.x mit dem Spec-Nachtrag abgebildet (Backlog-Eintrag).
- `stream.online`/`stream.offline` → `Receiver.Stream(true/false)`
  (B11); `channel.follow` → `Event` mit dem Follower als Nutzer;
  `channel.subscribe*`/`gift`/`mass_gift` → `Event` mit
  `eventtype.Subscription`/`Gift` (Plan: `tier` → Plan,
  `cumulative_months` → PlanName leer); `channel.raid` → `Raid` mit
  `viewer_count`.
- `channel.cheer` → `twitch.bits.cheer` mit Bits + Nachricht;
  `channel.moderate` → `chat.user.timeout`/`chat.user.ban` (Typ aus
  `moderation_action`) und die Twitch-Fassungen;
  `channel.chat.message_delete` → `chat.message.delete`.
- `user.whisper.message` → `chat.whisper`.
- Shared Chat (`channel.shared_chat.*`): neue plattformspezifische
  Typen `twitch.shared_chat.start/update/end` im Katalog
  (`internal/domain/eventtype`) — die Nutzlast formalisiert die
  4.4-Spezifikation `twitch-events.md`; die Subscription kommt mit in
  den Soll-Zustand von Task 3 (Nachtrag, wenn der Typ dazukommt).
- `channel.chat.notification` trägt Abo, Resub und Geschenk
  (Anhang A.2: die separaten subscribe-Subscriptions sind im
  Original auskommentiert) → aus derselben Quelle mappen.
- Deduplizierung: `connector.Dedup` auf dem `message_id` **vor** der
  Abbildung (B22), auch nach `session_reconnect`.
**Tests:** je Ereignisfamilie ein Tabellentest (Payload → Receiver-
Aufruf), Emote-/Badge-Mapping, Bot-Nachricht `FromBot`, Dedup nach
Reconnect, Whisper, Moderations-Typen.
**Commit:** `feat(twitch): map EventSub events onto the event model`

### Task 5 — Einbau in die Composition Root

**Zweig:** `feat/twitch-adapter` (auf Task 4)
**Inhalt:** der Adapter startet im Core/CLI, wenn das Streamer-Konto
einen Token hat (`auth` beobachten: `login_required` → stoppen,
Token → starten), Token-Lieferant `auth.Service.Token`, Receiver vom
Event-Service, `helix`-Client `twitch.helix` (4.2) für den Manager;
`Stream`-Zustand nach jedem Verbinden per `GetStreams` (B11);
geordnetes Herunterfahren (WS schließen, Manager stoppen).
**Tests:** Start mit/ohne Token, Token-Verlust → Stopp, Herunterfahren.
**Commit:** `feat(app): start the Twitch adapter with the core`

### Task 6 — Doku: README, Roadmap (Docs-PR, oberstes PR)

**Zweig:** `docs/eventsub-docs` (auf Task 5)
**Inhalt:** README-Statussatz (EventSub-WebSocket + Adapter), Roadmap
4.3 abhaken + Historie, ADR-0015-Index (falls angenommen),
Backlog-Eintrag „Antworten in der Chat-Nachricht“ (Task 4), Plan-
Datei abschließen.
**Commit:** `docs: the EventSub WebSocket of phase 4.3`

## 6. Scope-Liste

**Dabei:** Code-ADR-0015, `internal/twitch` (WS-Client,
Subscription-Manager, Abbildung, Einbau), neue Katalog-Typen für
Shared Chat, Doku.

**Nicht dabei:** Event-Commands und Actions (4.4), die Spezifikation
`twitch-events.md` (4.4), Fixtures/Contract-Tests gegen die Twitch-CLI
(4.5), Antworten im Chat-Modell (Backlog), YouTube/Kick (später).

## 7. Offene Punkte

1. **Annahme von Code-ADR-0015** durch den Projektinhaber (nach
   Task-1-Review); bei Änderung des Bibliothekswunsches: Task 2
   anpassen.
2. **In-Memory-Netzwerk + WebSocket:** `coder/websocket` über
   `httptest.NewTestServer` (Upgrade im In-Memory-Netz) — im ersten
   Test-Lauf von Task 2 prüfen; Fallback: Loopback-WS-Server außerhalb
   der synctest-Bubble (nur die Protokoll-Tests, die keine
   Fake-Uhr brauchen).
3. **Soll-Zustand exakt nach A.2 oder nur abgebildete Typen:** der
   Manager subscribed nur, was Task 4 abbildet (sonst laufen
   Ereignisse ohne Handler); A.2 bleibt die Bezugsgröße, aus der 4.4
   den Rest übernehmen kann.

## 8. Exit von 4.3

- [ ] Code-ADR-0015 WebSocket-Bibliothek
- [ ] WebSocket-Client mit Keepalive, `session_reconnect`, Revocation
  und Dedup (alle mit Tests)
- [ ] Subscription-Manager mit Soll-Zustand, Abgleich und
  Limit-Verhalten
- [ ] Abbildung auf `connector.Receiver` mit Chat-Modell (Emotes,
  Badges, Shared Chat), Dedup nach Reconnects
- [ ] Einbau: der Adapter läuft mit dem Core, Token-abhängig
- [ ] Doku: README, Roadmap 4.3 abgehakt, Historie

## 9. Stack-Aufbau (Zusammenfassung)

```
docs/helix-docs (4.2, Stack #162)
 └ PR 1  docs/code-adr-0015-websocket   (ADR-0015, plan, index, roadmap)
 └ PR 2  feat/eventsub-ws               (internal/twitch: ws.go, message.go)
 └ PR 3  feat/eventsub-subscriptions    (internal/twitch: subscriptions.go)
 └ PR 4  feat/eventsub-mapping          (internal/twitch: map.go, Katalog-Typen)
 └ PR 5  feat/twitch-adapter            (Composition Root: Adapter starten)
 └ PR 6  docs/eventsub-docs             (README, roadmap, ADR-Index, backlog)
```
