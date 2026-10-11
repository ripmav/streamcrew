# Plan: Roadmap 4.5 — Twitch-Tests

**Status:** abgeschlossen (2026-10-11, Stack #190 [188, 189, 191, 192, 193, 194,
195, 196, 197]); offen bleibt nur das Exit-Kriterium des manuellen
4-h-Test-Streams (Projektinhaber)
**Stand:** 2026-10-11, `main` bei `42f0273`; 4.1–4.4 sind gemerged
**Scope:** nur 4.5 (Tests + Dry-Run-Modus). Der `$emote…`-Identifier
(4.4, offener Punkt 5) bleibt offen.

## 1. Was 4.5 liefert (Roadmap)

- [x] Fixtures aus der offiziellen Doku bzw. eigenen Test-Streams
  (anonymisiert) und Contract-Tests (M)
- [x] Integrationstests gegen den Mock-EventSub-Server der Twitch CLI (M)
- [x] Reconnect- und Chaos-Tests: Verbindungsabbruch, Keepalive-Timeout,
  Session-Reconnect (S)
- [x] **Dry-Run-Modus** (M): der Core verbindet sich mit einem echten
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
3. **Reales Frame-Format (verifiziert 2026-10-11 gegen die CLI-Quel-
   le und das laufende Mock-Server):** alle Frames sind
   `{"metadata":{"message_id","message_type","message_timestamp",…},"payload":{…}}`;
   die Typen sind `session_welcome` (`payload.session` mit
   `id`, `keepalive_timeout_seconds`, `reconnect_url`),
   `session_keepalive` (leeres `payload`, 10-s-Takt — nicht PING),
   `notification` (`payload` = EventsubResponse
   `{subscription:{type,version,…}, event:{…}}`), `session_reconnect`
   (`payload.session` mit `status: "reconnecting"` und einer
   selbsttragenden `reconnect_url`, die den Reconnect-Identifikator als
   Query-Parameter trägt) und `revocation` (`payload.revocation.reason`).
   Der Server schickt WS-Pings (5-s-Takt, transparent über die
   Bibliothek) und **schließt Clients mit Close-Code 4001, die
   Anwendungsnachrichten senden** — ein konformer Client ist sendefrei,
   und jede empfangene Frame hält die Verbindung lebendig.
   Konsequenz: der 4.3-Client (hypothetisches `{type, payload}`-
   Format mit `hello`/PING/`event_type`) spricht das echte Protokoll
   nicht — Aufgabe 4 stellt das um.
4. **Subscriptions über REST (verifiziert 2026-10-11 gegen den
   laufenden Mock und die CLI-Quelle):**
   - Der Mock verlangt im Body von `POST /eventsub/subscriptions`
     `transport.method: "websocket"` **und** ein gültiges
     `transport.session_id` (Format `<ServerId>_<ClientName>`, die ID
     der verbundenen EventSub-Session aus dem `session_welcome`;
     unbekannt → 400). Die Subscriptions müssen also erst nach der
     Verbindung angelegt werden und tragen die Session-Id — der
     4.3-Manager tat beides nicht (zweiter Befund neben dem
     Frame-Format).
   - Feldname: der Mock liest/antwortet das Eventthema unter
     `"type"`, die Production-Helix-API tut es unter `"eventtype"`.
     Der Helix-Client schickt beide Felder mit und falt sie beim
     Antworten auf `EventType` zusammen.
   - Der Mock lehnt `channel.chat.message` (400/410) ab — der
     Subscriptions-Manager skippt das Thema wie geplant (400 ist
     kein Fehler).
   - `channel.cheer` trägt in Production die feiernde Person unter
     `from_user_id`/`from_user_login`/`from_user_display_name` und
     den Betrag unter `cumulative_bits` (nicht `user_id`/`bits`);
     der Mapper war auf die falschen Namen geschrieben —
     Integrationsbefund, in Aufgabe 5 behoben.

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

- [x] **1. Test-Harness für die Twitch CLI** (S)
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

- [x] **2. Dry-Run-Modus** (M)
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

- [x] **3. Fixtures und Contract-Tests des Event-Adapters** (M)
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

- [x] **4. EventSub-Client auf das reale Frame-Format** (S)
  Der 4.3-Client spricht das hypothetische `{type, payload}`-Format
  (`hello`, PING/PONG, `event_type`); das reale Protokoll (Befund 3)
  ist anders. `internal/twitch`: `message.go` (envelope, welcome,
  reconnect, Event aus dem EventsubResponse), `ws.go` (session_welcome
  auslesen, Keepalive-Aufsicht über jede Frame, kein Outbound-Traffic,
  Query-Merge in der Reconnect-URL), `map.go` (Typ/Version aus der
  Subscription, Body = `event`, Dedup-Schlüssel = Nutzlast-Inhalt, B22)
  und die Tests: `ws_test.go` und `platform_test.go` sprechen das neue
  Frame-Format, die Fixtures wandeln in die reale Notification-Form
  `{subscription:{type,version}, event:{…}}`. Code-ADR-0015 mit
  Revision ergänzt.
  Verifikation: `go test ./internal/twitch/` (Unit) — und die
  Integrationstests (Aufgabe 5) gegen den echten Mock bestätigen es.

- [x] **5. Integrationstests gegen den Mock-EventSub-Server** (M)
  `internal/twitch/integration_test.go` (Harness aus Aufgabe 1);
  erledigt (PR #194):
  - **Verbindung + Subscriptions:** der Adapter verbindet
    (`session_welcome` wird angenommen), die Subscriptions entstehen
    über REST gegen den Mock mit der Session-Id im Transport (Befund
    4) und erscheinen in der GET-Liste; das abgelehnte Thema
    `channel.chat.message` wird vom Manager übersprungen.
  - **Eventfluss (end-to-end):** `Fire` von `stream.online` (Stream-
    Zustand), `channel.chat.message` v2 (handgefertigter Body — der
    Mock kennt das Thema nicht; Emote-Code und Autor landen korrekt,
    die identische Wiederholung wirft der B22-Dedup), `channel.cheer`
    (Production-Form mit `from_user_*`/`cumulative_bits`) und
    `channel.channel_points_custom_reward_redemption.add` als
    dokumentierte Voll-Body.
  - **Keine Aktion gegen die Mock API:** der generierte Token trägt
    keine Scopes (Mock-`generate`, `Scopes: ""`), und jede Mock-
    Schreib-Endpunkte verlangt einen (401) — es gibt kein
    schreibbares Ziel (offener Punkt 3, entschieden: aus dem Test
    gestrichen, Moderation bleibt Unit- und Produktions-geprüft).
  Verifikation: `go test ./internal/twitch/ -run TestPlatformAgainstTheMock`
  (Skip ohne die twitch-Binary); `go test -race ./...` grün.

- [x] **6. Reconnect- und Chaos-Tests** (S)
  (Harness aus Aufgabe 1; `internal/twitch/chaos_test.go`);
  erledigt (PR #195):
  - **Session-Reconnect:** `Reconnect()` → Adapter folgt der
    `reconnect_url` sofort (ohne Backoff), die Subscriptions werden
    vom Mock mit der Session übernommen (verifiziert), die Events
    nach dem Neustart kommen an (kein Eventverlust), und die
    Wiederholung einer gesehenen Nachricht im Grace-Fenster wirft der
    Dedup weg. Befund dabei: der Dedup-Schlüssel war das komplette
    Notification-Payload (inkl. Subscription-Shell) — die Shell ändert
    sich pro Session (Session-ID, created_at), die Wiederholung nach
    Reconnect wurde also nicht erkannt. Der Schlüssel ist jetzt der
    Inhalt des Event-Objekts (mit der Nachrichten-ID, B22).
  - **Erzwungener Abbruch:** `CloseClient(session, 4006)` (der Mock
    unterstützt 4003–4007, nicht das Produktions-4008) → Adapter
    reconnectet am Base-URL (Backoff), Events kommen wieder an.
  - **Keepalive-Timeout:** `SetKeepalive(session, false)` → Adapter
    erkennt die Stille nach zwei Keepalive-Intervallen (Pong-Faktor
    2, Mock-Boden 10 s) und reconnectet.
  - **Server-Neustart (entschieden):** nicht im Harness
    implementiert — für den Client ist ein toter Server und ein
    abgebrochenes Connection der gleiche Pfad (Lesefehler, Base-URL,
    Backoff), den der erzwungene Abbruch testet. Der Mock hält
    Subscriptions pro Verbindung (ein harter Abbruch deshabiliert
    sie; nur der Session-Reconnect trägt sie über — Produktions-
    Semantik: Subscriptions überleben Reconnects), und die
    Close-/Keepalive-RPCs blockiert der Mock ~30 s während des
    Reconnect-Tests — daher läuft der Reconnect als erstes Szenario
    und der Close wird wiederholt, bis der Mock ihn annimmt.
  Verifikation: `go test ./internal/twitch/ -run TestChaosAgainstTheMock`
  (Skip ohne die twitch-Binary); `go test -race ./...` grün.
  (Der Plan nannte `integration_test.go`; die Tests liegen in der
  eigenen Datei `chaos_test.go`, gleiche Pakete.)

- [x] **7. CI** (S)
  `.github/workflows/ci.yml`, Test-Job: Schritt „Install twitch-cli“
  (Release-Binary v1.1.24, Linux x86_64 — der Job läuft auf
  ubuntu-latest; SHA-256-Prüfsumme aus der `checksums.txt` des
  Releases, in die `PATH`), damit die Integrationstests in CI
  laufen (lokal ohne Binary weiterhin Skip). Erledigt (PR #196);
  die native Windows-/macOS-Jobs bleiben ohne Binary (Skip).
  Befund dabei: `go test ./...` läuft die Pakete parallel, und das
  twitch-Paket (Integration + Chaos) wie das twitchmock-Paket
  (Selbsttest) starten je eine Mock-Umgebung auf dem festen RPC-Port
  44747 — das verlorene Rennen bindet der Mock-Server stillschweigend
  nicht, und die RPC-Requests scheitern mit „connection refused“. Der
  Harness nimmt jetzt die exklusive Sperre von `internal/lockfile`
  (OS-Sperre, kein Stale-Problem) für die Lebensdauer der Umgebung;
  das wartende Paket läuft danach nacheinander.

- [ ] **8. Doku** (S)
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
2. **Fester RPC-Port 44747 (gelöst mit Aufgabe 7):** pro Maschine
   ein WS-Server. Der Harness nimmt die exklusive Sperre von
   `internal/lockfile` für die Lebensdauer der Umgebung, parallele
   Testpakete laufen nacheinander; zwei manuelle Testläufe nebeneinander
   warten ebenso aufeinander (bis zu 4 Minuten).
3. **Mock-API-Scopes (entschieden 2026-10-11):** der vorgefertigte
   Token trägt keine Scopes (`generate` schreibt `Scopes: ""`), und
   jede Mock-Schreib-Endpunkte (POST/PUT) verlangt mindestens einen
   (sonst 401) — verifiziert gegen alle 58 Mock-Endpunkte. Eine
   Aktion gegen die Mock API ist daher nicht testbar; sie ist aus
   Aufgabe 5 gestrichen. Moderation/Chat-Commands bleiben Unit- und
   Produktions-geprüft (Exit-Kriterium 4-h-Test-Stream).
4. **Anonymisierung der Fixtures:** die offizielle Doku nutzt
   Beispiel-IDs/-Logins; wir übernehmen die Strukturen und ersetzen
   IDs/Logins durch durchgehend fiktive Werte (keine echten Accounts).
5. **Dry-Run-Sichtbarkeit:** der Modus ist (bewusst) kein eigener
   `Mode` neben desktop/daemon/server, sondern ein Flag — die UI zeigt
   ihn (noch) nicht an, nur das Info-Log beim Start. Falls das
   gewünscht ist, folgt es im Backlog.
