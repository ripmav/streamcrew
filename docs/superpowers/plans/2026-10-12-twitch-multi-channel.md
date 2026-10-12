# Plan: Roadmap 4.6 — Multi-Channel (zusätzliche Kanäle)

**Status:** offen
**Stand:** 2026-10-12, `main` bei `42f0273`; 4.1–4.4 sind gemerged, 4.5 steht in
Stack #190, ADR-0024 (eingebaute App + Aktivierung) in Stack #201
**Scope:** nur 4.6 — der Bot-Konto-Betrieb in mehreren Kanälen. Pro-Channel-
Command-Konfiguration und app_token-Subscriptions bleiben offen (Abschnitt 5).

## 1. Was 4.6 liefert (Roadmap)

- [ ] Settings-Sektion „channels": Kanäle, in denen der Bot zusätzlich
  arbeitet — **kein zusätzlicher Login** nötig (S)
- [ ] Channel-Dimension in den Connector-Ports und Engine-Routing:
  ereignisgetriggerte Aktionen wirken auf den Channel des auslösenden Events;
  Aktionen ohne Channel-Kontext auf dem eigenen Channel (M)
- [ ] Multi-Channel-EventSub: Subscriptions für die zusätzlichen Kanäle und
  WS-Transport mit dem Bot-Token; Mod-Check mit Status-Warnung (L)
- [ ] Chat, Chat löschen und Moderation in den zusätzlichen Kanälen über das
  Bot-Konto; Emote-Kataloge je Channel (M)
- [ ] Ownership-Gate: Owner-Actions (Raid, Werbung, Titel/Kategorie, Clip,
  Marker, Umfrage, Vorhersage, Belohnungen) nur auf dem eigenen Channel,
  sonst klare Fehlermeldung (S)
- [ ] Tests: Mock-Plattform multi-channel, Integrationstests, E2E auf echten
  Kanälen (M)

## 2. Machbarkeit und Befunde (Recherche 2026-10-12)

**Frage:** Kann der Bot in anderen Kanälen sein, ohne zusätzlichen Login?
**Antwort: Ja** — mit einer einzigen Voraussetzung: das **Bot-Konto muss
Moderator** in jedem Ziel-Channel sein. Die Channel-Owner gewähren den Mod-
Status über die Twitch-UI (`/mod`) — das ist eine Berechtigung, kein OAuth,
kein Login, keine App. So werden alle Twitch-Chat-Bots installiert; streamcrew
braucht dafür keinen zweiten Login.

1. **Kein zusätzlicher Login nötig.** Chat senden/löschen und Moderation
   laufen über das Bot-Konto; in jedem Channel, in dem es Mod ist, gelten die
   Rechte. Ein einziges Bot-Konto deckt beliebig viele Kanäle ab.
2. **Ereignisse empfangen (EventSub):** Die `channel.*`-Topics über den
   WebSocket-Transport (user_token) liefern die Channels, in denen der
   Token-Nutzer Broadcaster oder Moderator ist. Mod-Status des Bot-Kontos
   reicht also auch für den Event-Empfang. *(Die exakte je-Topic-Empfangs-
   Vorgabe der Twitch-Doku — die Seite war bei der Recherche bot-geschützt,
   HTTP 403 — wird im E2E je Topic verifiziert, Task 7; das Design geht
   ohnehin von der sicheren Mod-Voraussetzung aus.)*
3. **Owner-Actions sind channelgebunden:** Raid, Werbung, Titel/Kategorie,
   Clip, Marker, Umfrage, Vorhersage und Belohnungen brauchen die
   Broadcaster-Rechte **auf dem jeweiligen Channel** (sonst 403). Auf
   zusätzlichen Kanälen sind sie nicht darstellbar → klare Fehlermeldung,
   in der UI als deaktiviert.
4. **Lese-Daten überall:** Channel-Info (Titel/Kategorie/Live-Zustand),
   Users und Emote-Kataloge sind öffentliche Helix-Endpunkte und funktionieren
   für alle Channels.
5. **Architektur-Befund: der Channel ist heute eine Singleton-Annahme**
   (im Code verifiziert):
   - `connector.Chat.Send(ctx, m)` mit `Message = {Text, From}` — kein
     Channel; der Adapter sendet in den einen Plattform-Channel.
   - `connector.Moderation` ohne Channel-Parameter.
   - `twitch.Platform` ist singleton an das Streamer-Konto gebunden (eine
     `Auth.Streamer`, ein `EmoteChannel`); der Subscription-Manager
     (`internal/twitch/subscriptions.go`) trägt genau ein `accountID` und
     setzt alle `broadcaster_user_id`-Bedingungen darauf.
   - Das kanonische Event (`internal/event`, `Envelope = {ID, Time, Source,
     Type, Payload}`) hat keine Channel-Dimension.
   - Die Engine-Trigger sind pro Profil, nicht pro Channel.

   → Der Kern von 4.6 ist die **Channel-Dimension als erste Größe** in Ports,
   Adaptern und Routing.

## 3. Design (Festlegungen)

1. **Konfiguration:** neue Settings-Sektion `channels` mit den Logins der
   zusätzlichen Kanäle (Liste). Der eigene Channel (Channel des
   Streamer-Kontos) ist implizit dabei und nicht konfigurierbar.
   Validierung: keine Leer-Einträge, keine Duplikate, der eigene Channel wird
   abgelehnt (er ist schon drin).
2. **Mod-Voraussetzung:** Beim Start (und beim Reconcile) prüft der Adapter
   je zusätzlichem Channel über Helix `GET /mods?broadcaster_id=<channel>&
   moderator_id=<bot>`, ob das Bot-Konto Mod ist. Fehlt der Mod-Status: keine
   Subscriptions und keine Chat-Zielsetzung für diesen Channel, Status-Warnung
   `mod_missing` (sichtbar in Status/Logs). Kein automatisches Hinzufügen
   (das bräuchte Owner-Rechte und wäre falsch).
3. **Token-Zuordnung:**
   - eigener Channel: wie heute (Streamer-Token, volle Funktionsbreite).
   - zusätzliche Kanäle: **Bot-Token** (Mod-Rechte).
   - Subscriptions der zusätzlichen Kanäle: `broadcaster_user_id = <channel>`;
   - Whisper bleibt `user_id = <empfangendes Konto>` (Bot-Konto).
4. **Channel-Dimension:** `connector.Message` erhält ein `Channel`-Feld
   (Login); `Chat.Send`/`Replier.Reply`/`Delete` und die Moderations-Methoden
   operieren auf dem Channel der Nachricht/der Auslösung.
   **Routing-Regel:** ereignisgetriggerte Aktionen wirken auf den Channel des
   auslösenden Events; Aktionen ohne Channel-Kontext (Timer, manuell per
   CLI/TUI) wirken auf den eigenen Channel (v1).
5. **Emotes:** der Katalog wird je Channel aufgelöst (Key pro Channel), nicht
   mehr als ein einzelnes `EmoteChannel`.
6. **Ownership-Gate:** die Twitch-Owner-Actions (Raid, Werbung, Titel,
   Kategorie, Clip, Marker, Umfrage, Vorhersage, Belohnungen) prüfen
   `channel == eigener Channel` → sonst klare Fehler (`channel not owned`),
   die die UI als deaktiviert darstellen kann.
7. **Konfiguration geteilt (v1):** Commands, Requirements und Timer gelten
   identisch für alle Kanäle — es gibt keine pro-Channel-Config. Die
   Chat-Rate-Limits sind je Channel eigene Buckets, nicht einer global.
8. **Kein neues ADR:** Funktionsausbau ohne Architekturwandel der ADR-Würde;
   die Mod-Voraussetzung und die Token-Zuordnung sind hier und in der Spez
   dokumentiert. (Falls die E2E-Empfangs-Vorgaben ein ADR erforderlich machen
   — z. B. wenn Empfang ohne Mod geht und wir es trotzdem verlangen — wird
   das in Task 1 entschieden.)

## 4. Tasks

### Task 1 — Spezifikation (S)
Channel-Dimension in der Connector-Spezifikation: Channel in `Message`,
Channel in `Chat`/`Moderation`-Methoden, Routing-Regel, Ownership-Gate;
Anmerkung in `docs/spec/twitch-events.md` (Channel in den Payloads);
Doku-Status der Mod-Voraussetzung und der je-Topic-Empfangs-Vorgabe
(„E2E-verifiziert“ je Task 7).

### Task 2 — Settings-Sektion „channels“ (S)
Konfiguration, Validierung (Abschnitt 3.1), Persistenz; `config`-Anzeige.
Keine Profiländerung.

### Task 3 — Channel-Dimension in den Connector-Ports + Engine-Routing (M)
`Message.Channel`; Signaturen von `Chat`/`Replier`/`Moderation`;
`connectortest`-Fakes; alle Adapter (twitch, mock); Engine: Events tragen den
Channel weiter bis zur Port-Aufruf; Rate-Limit-Buckets je Channel.

### Task 4 — Twitch-Plattform multi-channel (L)
- Mod-Check (Helix `GET /mods`) + Status-Warnung `mod_missing`.
- Subscription-Manager: Sollzustand je (Channel × Topic) — eigener Channel
  wie heute, zusätzliche Kanäle mit `broadcaster_user_id` des Channels.
- WS-Transport für die zusätzlichen Kanäle mit dem Bot-Token (eine zweite
  Verbindung; ob alle Kanäle auch in einer Verbindung laufen, klärt Task 7
  — v1: getrennt).
- Chat/Moderation/ops je Channel (`broadcaster_id` je Ziel).
- Emote-Katalog je Channel.
- Ownership-Gate in den ops.

### Task 5 — Mock-Plattform multi-channel (M)
`internal/connector/mock`: mehrere Channels, je Channel Chat und Moderation;
Engine-Tests fürs Cross-Channel-Routing (Event aus Channel X → Aktion wirkt
in X, nicht in Y).

### Task 6 — Integrationstests (M)
Gegen die twitch-cli-Mock-Server: zwei Channel-Kontexte — falls die Mock-
Server nur einen Broadcaster pro Instanz darstellen, das als Beschränkung
dokumentieren und stattdessen den Mod-Check (Helix-Ebene) und die
Subscription-Sollzustände je Channel abdecken.

### Task 7 — E2E und Doku (S)
Echt: eigener Channel + ein zusätzlicher Channel, in dem das Bot-Konto Mod
ist (Test-Channel des Projektinhabers). Verifiziert und dokumentiert: je
Topic die Empfangs-Vorgabe (Abschnitt 2.2), Whisper-Lieferung über den
Bot-Transport, Shared Chat in zusätzlichen Kanälen (Abschnitt 5). README
(„Zusätzliche Kanäle“: Mod-Voraussetzung, Ownership-Gate), Roadmap/Plan-
Status.

## 5. Offene Punkte

1. **Pro-Channel-Command-/Requirement-Konfiguration** (v2): v1 gilt die
   Konfiguration identisch für alle Kanäle.
2. **Eine WS-Verbindung für alle Kanäle** statt je Token — E2E (Task 4/7);
   v1 bleibt bei der getrennten Verbindung.
3. **app_token-Subscriptions** (Kanäle ohne Mod-Status, dafür mit
   Rate-Limits) — wird v1 nicht gebaut; Mod-Status bleibt die
   Voraussetzung.
4. **Shared Chat** in zusätzlichen Kanälen: ob der Bot den Shared-Chat-
   Events in fremden Channels als Mod erhält, im E2E klären.
5. **Mock-Server-Beschränkung:** ob twitch-cli mehrere Broadcaster pro Instanz
   darstellt, ist unklar → Task 6 dokumentiert den erzielten Abdeckungssatz.

## 6. Aufwand

Schätzung: **3–4 PW** (Task 4 ist der größte Block; Tasks 1/2/7 klein).
