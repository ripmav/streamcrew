# Plan: Roadmap 4.5 — Twitch-Tests

**Status:** offen
**Stand:** 2026-10-11, `main` bei `42f0273`; 4.1–4.4 sind gemerged
**Scope:** nur 4.5 (Tests + Dry-Run-Modus). Der `$emote…`-Identifier
(4.4, offener Punkt 5) bleibt offen.

## 1. Was 4.5 liefert (Roadmap)

- [ ] Fixtures aus der offiziellen Doku bzw. eigenen Test-Streams
  (anonymisiert) und Contract-Tests (M)
- [ ] Integrationstests gegen den Mock-EventSub-Server der Twitch CLI (M)
- [ ] Reconnect- und Chaos-Tests: Verbindungsabbruch, Keepalive-Timeout,
  Session-Reconnect (S)
- [ ] **Dry-Run-Modus** (M): der Core verbindet sich mit einem echten
  Channel, liest und verarbeitet wie gewöhnlich, führt aber keine
  schreibende Aktion aus — stattdessen zeichnet er sie als Debug-Logs
  auf, was er machen würde (Zweck: Funktionen in echt testen ohne
  Channel-Seiteneffekte, Bugs finden; auch für den manuellen
  4-h-Test-Stream sicher)

**Exit-Kriterien (Roadmap):**

- [ ] Ein Test-Stream läuft mindestens vier Stunden mit Streamer- und
  Bot-Konto. *(Manuell, Projektinhaber — nicht in CI.)*
- [ ] Chat-, Event- und Channel-Points-Commands sowie die Moderation
  funktionieren. *(Abgedeckt durch die Integrationstests gegen die Mocks.)*
- [ ] Erzwungene Reconnects verlaufen ohne Eventverlust.
  *(Abgedeckt durch die Chaos-Tests.)*

## 2. Befunde der Recherche (verifiziert 2026-10-11)

Das **offizielle `twitch-cli`** (github.com/twitchdev/twitch-cli,
Letztrelease v1.1.24, `go 1.20`) bietet zwei Mock-Server:

1. **Mock API** (`twitch mock-api`):
   - `generate -c N` legt eine SQLite-Datenbank mit Nutzern (einer davon
     Partner „mit allen Units"), einer Kategorie, Streams, Abos,
     Channel-Points-Belohnungen und einem **Client** samt
     **vorgefertigtem Authorization-Token** an; Client-ID, Secret,
     Partner-User-ID und Token stehen im Konsolen-Output (parsbar).
   - `start -p PORT` serviert die Helix-Endpunkte unter
     `http://HOST:PORT/mock/…` und die Units (ohne Auth) unter
     `/units/…`. Die **Auth-Middleware validiert**: `Client-ID`-Header
     (mindestens 30 Zeichen), `Authorization: Bearer <token>` gegen die
     Datenbank, Ablaufdatum und Scopes pro Methode.
   - **Nicht** enthalten: Extensions, Code-Entitlements, Webhooks,
     EventSub-REST-Endpunkte.
   - Die DB liegt in `$HOME/.config/twitch-cli/eventCache.db` — über
     `HOME` in ein Temp-Verzeichnis verlegbar (im Test pro Run frisch).
2. **Mock EventSub WebSocket**
   (`twitch event websocket start-server -p PORT`):
   - `/ws` auf demselben Port: vollständiges EventSub-WebSocket-Protokoll
     (`session_welcome` mit Session-ID `<server>_<client>`,
     `session_keepalive` im Keepalive-Takt, WS-Ping/Pong,
     `session_reconnect` mit Grace-Periode).
   - `GET/POST/DELETE /eventsub/subscriptions` auf demselben Port:
     REST-Subscriptions im Helix-Format (exakt das, was der
     Subscriptions-Manager des Adapters tut). `POST` validiert
     Type+Version gegen die bekannte Themenliste — akzeptiert wurden
     im Test: `stream.online` v1, `stream.offline` v1,
     `channel.follow` v2, `channel.raid` v1, `channel.cheer` v1,
     `channel.channel_points_custom_reward_redemption.add` v1;
     abgelehnt (400/410): `channel.chat.message` v2,
     `channel.goal.start` v1, `channel.hype_train.start` v1 u. a. —
     der CLI-Mock kennt die alten (2023/2024) Eventsub-Namen
     (`channel.goal.begin`, `channel.shoutout.receive`,
     `channel.ad_break.begin`, …) und **keine** Chat-Events.
   - JSON-RPC 2.0 (net/rpc) auf dem **festen Port 44747**:
     `RPCHandler.ExecuteGenericRPC` mit
     - `EventSubWebSocketForwardEvent`: nimmt einen **rohen
       EventsubResponse-JSON-Body** (`{subscription, event}`) und
       leitet ihn an alle Clients (oder `ClientName`-gezielt) weiter —
       **ohne Themenprüfung**, d. h. auch `channel.chat.message` v2 und
       alle anderen neuen Namen sind so triggerbar.
     - `EventSubWebSocketReconnect`: simuliert den Production-
       Loadbalancer-Neustart (`session_reconnect`, ~30 s Grace-Periode,
       dann Close 4008; Subscriptions wandern mit `reconnect_id` auf den
       neuen Server über).
     - `EventSubWebSocketCloseClient` (`--session`, `--reason`):
       Close mit beliebigem Code aus der Doku (4003–4008).
     - `EventSubWebSocketKeepalive` (`--enabled=false`): stoppt die
       `session_keepalive`-Nachrichten des Servers.

**Konsequenzen für die Testarchitektur:**

- Der Test-Adapter zeigt `Base` (WS) auf den WS-Server und `HelixBase`
  auf einen **kleinen Test-Router**, der `/eventsub/subscriptions` an
  den WS-Server-Port und den Rest an die Mock API (mit `/mock`-Präfix)
  weiterleitet — die Helix-Endpunkte und die EventSub-REST-Endpunkte
  liegen auf zwei unterschiedlichen Servern.
- Der Auth-Port des Adapters wird im Test durch eine statische
  Implementierung bedient (Token + Client-ID aus dem `generate`-Output,
  Account = der Partner-User).
- Subscriptions, die der CLI-Mock nicht kennt, schlägt der
  Subscriptions-Manager gegen die Mocks fehl (400/410) — er loggt das
  und fährt fort (nicht fatal); die Events selbst kommen über die
  RPC-Weiterleitung trotzdem an (Non-Strict-Modus).
- Der feste RPC-Port 44747 erlaubt pro Maschine genau einen
  WS-Server; die Integrationstests laufen daher nicht parallel (und
  sind die einzigen, die ihn beanspruchen).
- **Installation:** Release-Binary (Linux x86_64/arm64, ~5 MB) mit
  geprüfter Summe statt `go install` (großer, langsamer
  Abhängigkeitsbaum). CI lädt es in den Test-Job; lokal und in
  Entwicklung wird die Binary über `exec.LookPath("twitch")` gefunden
  — ohne Binary springen die Integrationstests über (`t.Skip`), wie die
  `go`-Binary in `internal/action/helper_test.go`.

## 3. Aufgaben

Auf `main`, ein PR pro Aufgabe, gestapelt.

- [ ] **1. Test-Harness für die Twitch CLI** (S)
  `internal/twitch/twitchmock/`: verwaltet den Lebenszyklus beider
  Mock-Server für Go-Tests —
  - `twitch`-Binary auflösen (`LookPath`), fehlend → `Skip`.
  - `generate` in frischem `HOME` (Temp), Output parsen
    (Client-ID, Partner-User-ID, Token, Secret).
  - Mock API + WS-Server auf freien Ports starten, Health-Check,
    `t.Cleanup` (Kill beider Prozesse).
  - RPC-Client (net/rpc, Port 44747): `Fire(eventBody)` (roher
    EventsubResponse-Body, optional gezielt an eine Session),
    `Reconnect()`, `CloseClient(session, reason)`,
    `SetKeepalive(session, enabled)`.
  - `HelixRouter`: ein `httptest`-Handler, der
    `/eventsub/subscriptions` → WS-Server-Port und `/<rest>` →
    Mock-API `/mock/<rest>` weiterleitet; liefert `HelixBase`.
  - `Auth`: statische `twitch.Auth`-Implementierung
    (Streamer/Token/ClientID aus dem generate-Output).
  Verifikation: `go test ./internal/twitch/twitchmock/` mit einer
  Selbsttest-Suite (Server starten, connect, Event fire, empfangen).

- [ ] **2. Dry-Run-Modus** (M)
  `internal/dryrun/`: Wrapper um die Plattform-, damit der Core in
  einen echten Channel integriert werden kann, ohne dort irgendetwas
  auszulösen — das Bot liest und verarbeitet wie gewöhnlich, und
  zeichnet jede schreibende Aktion als Debug-Log auf, was es machen
  würde (Funktionen in echt testen, Bugs finden).
  - Neues Top-Level-Flag `--dry-run` (Default aus; in der Datei
    `dry_run`), ein Info-Log „dry run enabled" beim Start.
  - `Platform`-Wrapper um `connector.Platform`: `Name`, `Status`,
    `Users` (Lookup) und `Channel` bleiben unverändert durch; `Chat`
    (Send, Delete) und `Moderation` (Timeout, Purge, ClearChat, Ban,
    Unban, Mod, Unmod) werden mit allen Parametern als Debug-Log
    (Komponente `dryrun`) aufgezeichnet und geben Erfolg zurück.
  - `Ops`-Wrapper um `action/twitch.Ops` (4.4): alle 13 Operationen
    werden aufgezeichnet und geben synthetische Werte zurück (feste
    `dry-run`-IDs), damit der Ablauf weiternlaufen kann; Status-Abfragen
    zu dry-run-geschaffenen Entitäten (Poll, Prediction) melden
    „beendet".
  - Die Verbindung (EventSub, Helix-Lesungen, Subscriptions) bleibt
    aktiv — nur Schreibungen werden unterdrückt. Die Verdrahtung in
    `internal/app/app.go` wickelt `twitch.NewPlatform` und `twitchOps`
    bei `cfg.DryRun` in die Wrapper ein.
  - Nutzen für das manuelle Exit-Kriterium (4-h-Test-Stream): der Bot
    kann in den echten Channel des Streamers ohne Seiteneffekte.
  Verifikation: Unit-Tests für beide Wrapper (jede Methode wird geloggt
  und gibt den richtigen Wert, Lese-Ports durch), App-Test (dry run
  an/aus) und Config-Test für das neue Flag.

- [ ] **3. Fixtures und Contract-Tests des Event-Adapters** (M)
  - `internal/twitch/testdata/`: je Event-Typ des `desired`-Katalogs
    (23 Typen) eine **vollständige, anonymisierte Payload** aus der
    offiziellen Eventsub-Doku als JSON-Datei (statt der heute
    teils-minimalen Inline-Strings in `map_test.go`).
  - Contract-Test: Tabelle `fixture → erwartetes mapped
    connector.Event` (Typ, Identifier gemäß
    `docs/spec/twitch-events.md`, Channel-Login, Entität). Die
    existierenden Map-Tests bleiben für die Teilfalle (Bot-Nachrichten,
    Emote-Auflösung, Unmapped-Drop); die Contract-Suite prüft die
    Vollformate.
  - Helix-Contract-Tests: die existierenden `httptest`-Tests
    (`internal/helix/*_test.go`) bleiben; fehlende Endpunkte der 4.4-
    Liste bekommen je einen Response-Contract-Test mit dem
    dokumentierten Vollformat.

- [ ] **4. Integrationstests gegen den Mock-EventSub-Server** (M)
  `internal/twitch/integration_test.go` (Harness aus Aufgabe 1):
  - **Verbindung + Subscriptions:** der Adapter verbindet,
    `session_welcome` wird angenommen, die akzeptierbaren
    Subscriptions (stream.online, stream.offline, channel.follow,
    channel.raid, channel.cheer,
    channel.channel_points_custom_reward_redemption.add) entstehen über
    REST gegen den Mock (202) und erscheinen in der GET-Liste.
  - **Eventfluss (end-to-end):** `Fire` einer Chat-Nachricht
    (`channel.chat.message` v2, handgefertigter Body — der CLI-Mock
    kennt das Thema nicht) → Adapter mappt → Receiver erhält das
    `connector.Event` mit korrektem Typ, `$user…`/`$message…`-
    Entitäten und dedupliziertem ID; ebenso `stream.online`,
    `channel.cheer` und eine Channel-Points-Einlösung
    (`channel.channel_points_custom_reward_redemption.add`) als
    dokumentierte Voll-Body.
  - **Aktion gegen die Mock API:** ein Chat-Command auf ein
    getriggertes Event sendet über Helix `POST /mock/chat/messages`
    (Token + Client-ID aus dem generate-Output) — die Antwort 200 mit
    Message-ID wird vom Adapter bestätigt.

- [ ] **5. Reconnect- und Chaos-Tests** (S)
  (Harness aus Aufgabe 1; `internal/twitch/integration_test.go`)
  - **Session-Reconnect:** `Reconnect()` → Adapter erhält
    `session_reconnect`, verbindet an der `reconnect_url` mit der
    Session-ID, **ohne Eventverlust** (Events nach dem Neustart
    kommen an; Duplikate der Grace-Periode werden vom Dedup geworfen).
  - **Erzwungener Abbruch:** `CloseClient(session, 4008)` → Adapter
    reconnectet am Base-URL (Backoff), Subscriptions werden neu
    angelegt; `CloseClient(session, 4003/4006)` (Revocation/Failure)
    → entsprechendes Verhalten (4003: frischer Token; 4006: Backoff).
  - **Keepalive-Timeout:** `SetKeepalive(session, false)` → Adapter
    erkennt den fehlenden Keepalive (pongFactor), bricht die
    Verbindung ab und reconnectet.
  - **Server-Neustart:** WS-Server-Prozess killen und neu starten →
    Adapter überlebt (Backoff), Verbindung stellt sich neu her.

- [ ] **6. CI** (S)
  `.github/workflows/ci.yml`, Test-Job: Schritt „Install twitch-cli"
  (Release-Binary v1.1.24, Plattform abhängig, SHA-256-Prüfsumme, nach
  `PATH`), damit die Integrationstests in CI laufen (lokal ohne Binary
  weiterhin Skip).

- [ ] **7. Doku** (S)
  Roadmap 4.5: Checkboxen + Statuszeile + Historieneintrag; README-
  Testabsatz (falls vorhanden) aktualisieren; Plan als abgeschlossen
  markieren. Der manuelle 4-h-Test-Stream bleibt als offenes
  Exit-Kriterium im Plan.

## 4. Nicht dabei

- Der manuelle 4-Stunden-Test-Stream mit echten Twitch-Konten
  (Exit-Kriterium, Projektinhaber).
- Die Multiplattform-Aufgaben (Backlog), YouTube/Kick.
- Antworten auf Chat-Nachrichten (Backlog, 4.4/5.x).
- Strikt-Modus-Tests (Server schließt Clients ohne Subscriptions nach
  10 s) — der Adapter abonniert über REST, nicht über das WS-Protokoll;
  Notwendigkeit prüfen, wenn Aufgabe 3 es verlangt.
- Fuzzing der Event-Payloads (gibt es seit 4.3/4.4 für die Mapper).

## 5. Offene Punkte

1. **Twitch-CLI-Version:** v1.1.24 ist der letzte Release (2024-07);
   main ist neuer, aber unversioniert. Wir pinnen v1.1.24 (Release-
   Binary, stabile Summe). Wenn main die neuen Eventsub-Namen
   übernimmt, können die Hand-Body-Fires für die akzeptierten Themen
   wegfallen.
2. **Fester RPC-Port 44747:** pro Maschine ein WS-Server. Die
   Integrationstests sind die einzigen Consumer; in CI läuft ein Test-
   Job pro Runner. Lokale Kollisionen (zwei Testläufe nebeneinander)
   sind möglich — dann die zweite Suite überspringen oder sequenziell
   laufen (Doku im Harness-Kommentar).
3. **Mock-API-Scopes:** der vorgefertigte Token trägt die Scopes des
   generierten Users; wenn ein Helix-Call im Integrationstest mit 401
   „Missing required scope" scheitert, prüft der Test die
   generate-Scopes und notiert sie im Plan (oder der Router gibt einen
   Scope-vollen Token über `POST /mock/…` — nicht vorgesehen).
4. **Anonymisierung der Fixtures:** die offizielle Doku nutzt
   Beispiel-IDs/-Logins; wir übernehmen die Strukturen und ersetzen
   IDs/Logins durch durchgehend fiktive Werte (keine echten Accounts).
5. **Dry-Run-Sichtbarkeit:** der Modus ist (bewusst) kein eigener
   `Mode` neben desktop/daemon/server, sondern ein Flag — die UI zeigt
   ihn (noch) nicht an, nur das Info-Log beim Start. Falls das
   gewünscht ist, folgt es im Backlog.
