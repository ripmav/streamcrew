# Plan: Twitch-IRC-Chat-Adapter für zusätzliche Channels

**Status:** offen
**Stand:** 2026-10-20, `main` bei `ace609a`
**Spec:** [twitch-multi-channel.md](../../spec/twitch-multi-channel.md), B15–B20 (dieser Plan setzt sie um)
**Auslöser:** E2E-Log (2026-10-20): der Mod-Check der zusätzlichen Channels
schlägt mit HTTP 401 fehl — Helix `GetModerators` verlangt
`broadcaster_id` = Token-Inhaber und ist mit dem Bot-Token gegen fremde
Channels nie bestellbar. Folge: zusätzliche Channels werden im
`eventsub`-Modus nie betrieben, auch wenn der Bot dort Mod ist.

## 1. Ziel

Den Bot in einen Chat stellen, in dem er **kein** Mod ist. Der Twitch-Chat
per IRC (irc.chat.twitch.tv, IRCv3 mit Tags) ist ohne Mod-Status les- und
schreibbar (Scope `chat:edit`) und liefert beim Join per USERSTATE
(`badges`-Tag) den Mod-Status des Bot-Kontos — damit wird der kaputte
Helix-Mod-Check ersetzt und der Mod-Status wird zum Information statt zur
Voraussetzung.

## 2. Design (minimal, bisheriges Verhalten erhalten)

- **Config-Option** in der Settings-Sektion `channels`: `chatAdapter`,
  Werte `eventsub` (Default) oder `irc`. Nur beim Core-Start gelesen — die
  Plattform wird beim Start gebaut, der Config-Watcher (Roadmap 6.5)
  erfasst die Sektion nicht, Hot-Reload gibt es nicht.
- **`eventsub` (Default): unverändert.** Der eigene Channel läuft über
  EventSub/Streamer-Token, zusätzliche Channels wie heute (EventSub-Session
  mit Bot-Token, Helix-Mod-Gate B3 — bleibt so, wie es ist).
- **`irc`: zusätzliche Channels über IRC.**
  - Eine IRC-Verbindung des Bot-Kontos (Bibliothek
    `github.com/gempir/go-twitch-irc/v2` v2.8.1 — IRCv3-Tags, Whisper,
    USERSTATE, eigener Rate-Limiter; kein stdlib-Äquivalent) tritt die
    zusätzlichen Channels bei; der eigene Channel bleibt EventSub.
  - **Empfang:** PRIVMSG → `connector.Incoming` (existierende
    `chatMessage`→`incoming`-Mapping aus `map.go` wiederverwenden: Emote-
    Auflösung über den Katalog, Badge→Rollen, `FromBot`-Flag, Dedup nach
    Message-ID), WHISPER → `ChatWhisper`-Event, CLEARMSG →
    `ChatMessageDelete`-Event.
  - **Mod-Status:** aus dem USERSTATE `badges`-Tag nach Join (`B17`);
    ohne Mod wird der Channel trotzdem betrieben, einmalige Warnung
    `mod_missing`.
  - **Senden:** Chat-Send auf einem zusätzlichen Channel geht über IRC
    (PRIVMSG) statt Helix; die vorhandenen Send-Buckets je Channel (B9,
    `chat.go`) bleiben.
  - **Keine EventSub-Subscriptions** für IRC-Channels (ihre
    `channel.*`-Topics brauchen ohnehin Bot-Mod-Status/Broadcaster).
- **Bot-Konto ohne Token:** zusätzliche Channels werden nicht betrieben
  (B19), wie heute.
- **Auth:** Scope `chat:edit` (IRC-Senden) in `internal/auth/twitch.go`
  ergänzt → bestehende Token tragen ihn nicht, beide Accounts müssen einmal
  neu eingelogen werden (das Auth-Flow markiert den Login als required).

### Bewusst nicht erledigt (Ponytail)

- Circuit-Breaker für die IRC-Verbindung (Code-ADR-0007): `go-twitch-irc`
  reconnectet intern und lässt keinen eigenen Dialer zu — `shortcut:`-Kommentar
  im Code, nachziehen, wenn IRC-Instabilitäten auffallen.
- IRC für den eigenen Channel (bleibt EventSub), IRC-Empfang von
  `channel.*`-Events (Folgt/Cheer/Raid … — brauchen Mod-Status, gehören
  nicht in den No-Mod-Betrieb).
- Follower-/Subs-Only-Channels: Senden schlägt mit klarer Fehlermeldung
  fehl (B20), Empfang geht weiter; automatisches Folgen ist nicht möglich.

## 3. PR-Aufteilung (Stack, Basis `main`)

| # | Branch | Inhalt | Basis |
|---|--------|--------|-------|
| 1 | `docs/twitch-irc-chat` | Spec-Ergänzung B15–B20 + E2E-Befund + dieser Plan | main |
| 2 | `feat/twitch-irc-chat` | Umsetzung: Config-Option, IRC-Adapter (Empfang + Mod-Badge + Senden), Scope, CLI, Tests | PR 1 |

PR 2 im Detail (eine Abgabe, Feature muss end-to-end funktionieren):

1. **Settings:** `Channels.ChatAdapter` (JSON `chatAdapter`),
   Validierung (`""`/`eventsub`/`irc`, sonst Ablehnung),
   Default `eventsub` — gespeicherte Dokumente ohne Feld bleiben
   Default, keine Sektions-Migration nötig. Tests in
   `internal/settings/settings_test.go`.
2. **CLI:** `profile channels chat <adapter>` in
   `internal/cli/channels.go` (Muster der Add/Remove-Commands, gesperrter
   Core), `profile channels list` zeigt den Adapter. Tests in
   `internal/cli/channels_test.go`.
3. **IRC-Adapter:** `internal/twitch/irc.go` —
   `NewIRCSession(IRCOptions{…})` mit `Base` (Test-Server), `Login`,
   `Token`, `BotID`, `Emotes`, `Logger`; `Run(ctx)` (Connect, Join,
   Callback-Verdrahtung, Reconnect), `Send(ctx, channel, text)`;
   Mapping über die bestehenden `chatMessage`/`identity`/`emoteCodes`-
   Bausteine.
4. **Platform:** `PlatformOptions.ChatAdapter`; bei `irc`
   `additionalChannels` liefert keine EventSub-Sessions, sondern startet
   eine IRC-Session; `chat.Send` routet IRC-Channels über die
   IRC-Verbindung. `internal/app/app.go` liest `chatAdapter` aus den
   Settings und reicht es an.
5. **Auth:** Scope `chat:edit` an `Scopes` in
   `internal/auth/twitch.go`.
6. **Tests:** `internal/twitch/irc_test.go` — kleiner Mock-IRC-Server
   (IPC-Textprotokoll, TLS-los per `IrcAddress`/`TLS=false`): Join →
   USERSTATE (Mod ja/nein), PRIVMSG (Tags: emotes, badges, user-id,
   msg-id) → `Incoming` wie im EventSub-Pfad, WHISPER, CLEARMSG,
   NOTICE bei Send-Verweigerung; Reconnect. dazu Platform-Tests:
   `irc`-Modus startet keine EventSub-Sessions für zusätzliche Channels,
   `eventsub`-Modus bleibt bit-verträglich. Test-Matrix in
   `docs/tests/I-4.6.1-irc-chat.md`.

## 4. Vor-Commit-Checkliste (pro Go-Commit)

`go fix ./...`, `gofmt -w .`, `go vet ./...`,
`~/.local/bin/golangci-lint run ./...`, `govulncheck ./...`,
`go test ./...` (IRC-Code: zusätzlich `-race -shuffle=on`).

## 5. E2E-Checkliste (manuell, nach Merge, mit echten Accounts)

- [ ] `profile channels chat irc`, Core-Neustart
- [ ] Bot (neu eingelogen wegen `chat:edit`) tritt einen Channel bei,
      in dem er **kein** Mod ist → Warnung `mod_missing` einmalig,
      Commands feuern auf Chat-Eingaben
- [ ] Bot sendet dort (Command-Action) → Nachricht erscheint
- [ ] Channel, in dem der Bot Mod ist → kein `mod_missing`, Mod-Actions
      via Helix funktionieren (bestätigt die offene je-Topic-Frage
      praktisch)
- [ ] Whispers an den Bot aus beiden Channels
- [ ] Follower-Only-Channel: Senden schlägt mit klarer Fehlermeldung fehl
- [ ] `profile channels chat eventsub` zurück → altes Verhalten
