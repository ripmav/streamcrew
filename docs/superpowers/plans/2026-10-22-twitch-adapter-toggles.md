# Plan: EventSub- und IRC-Adapter per Config an- und abschaltbar

**Status:** abgeschlossen (2026-10-22)
**Stand:** 2026-10-22, Branch `fix/eventsub-drop-wait`
**Begründung:** Der EventSub-Endpunkt ist auf der Streaming-PC-IP per
WAF-403 blockiert (Analyse 2026-10-21, F2); der Daemon braucht einen
Wege, EventSub abzuschalten und ausschließlich über IRC zu laufen —
inklusive dem Heimkanal. Umgekehrt soll IRC abschaltbar sein, ohne den
Profil-Adapter umzustellen.

## Semantik

Zwei unabhängige Schalter in der Startkonfiguration (Flag, Env,
Konfigurationsdatei; Präfix `twitch-`), Startzeit-Wert wie die
Stream-Größen (kein Hot-Reload):

| Schalter | Default | Wirkung |
|---|---|---|
| `--twitch-eventsub on\|off` (`$STREAMCREW_TWITCH_EVENTSUB`, `twitch_eventsub`) | `on` | EventSub-Adapter: Heimkanal-Session + EventSub-Sessions der zusätzlichen Kanäle |
| `--twitch-irc on\|off` (`$STREAMCREW_TWITCH_IRC`, `twitch_irc`) | `on` | IRC-Adapter: IRC-Session der zusätzlichen Kanäle; ohne EventSub übernimmt sie zusätzlich den Heimkanal |

Routing (minimale, eindeutige Regel):

- Heimkanal: **EventSub, wenn EventSub an**; sonst **IRC** (die
  IRC-Session joinnt den Heimkanal; die Streamer-Events tragen keinen
  Channel-Stempel wie in der EventSub-Pfad).
- Zusätzliche Kanäle: **IRC, wenn IRC an**; sonst EventSub.
- Beide an: Heimkanal über EventSub, zusätzliche über IRC (heutiger
  Zustand des Users).
- **Beide aus: der Core startet nicht** — `Config.Resolve` meldet den
  Usage-Fehler, `streamcrew serve` beendet sich mit der Meldung.

Folgen:

- Der Settings-Feld `channels.chatAdapter` und der CLI-Befehl
  `streamcrew profile channels chat <adapter>` sind damit überflüssig
  (konkurrierende Schalter) und werden **entfernt**: Sections-Version
  2 mit Migration, die das Feld aus dem gespeicherten Dokument streicht.
- Heimkanal ohne EventSub: der Stream-State kommt nicht mehr aus
  EventSub, sondern aus einem Helix-Poll (30 s, `stream.online`/
  `stream.offline`-Ersatz); der Status (connected) bleibt über die
  IRC-Session gesetzt.
- Senden des Heimkanals läuft weiter über Helix (api.twitch.tv ist
  nicht vom WAF-Block betroffen).

## Debug-Logs (Pfadaufklärung)

Debug-Level-Logs (sichtbar via `log_level: debug` bzw.
`component-level twitch=debug`/`auth=debug`):

- `httpclient`: jede Request/Response (Method, URL, Status, Dauer) —
  deckt Helix, Auth, Emotes ab.
- `twitch/ws.go`: Dial-Ziel (ohne Token), Frame-arten, Reconnect-Entscheid.
- `twitch/irc.go`: jede empfangene Zeile (gekürzt), Join-Zeile.
- `twitch/platform.go`: der gewählte Pfad im `connect` (Adapter,
  Heimkanal-Transport), Account-Übergänge.
- `twitch/map.go`: jedes eingehende Event (Typ, Kanal, User) nach
  der Dedup.
- Einmalige WARN statt Log-Spam, wenn der Bot-Account für den IRC-Pfad
  nicht bereit ist (Transition-Log wie beim Streamer-Account).

## Schritte

- [x] `internal/config`: `TwitchConfig` (`twitch-eventsub`,
      `twitch-irc`, Default `on`, enum `on,off`), `check()` in
      `Resolve` (mindestens einer an), Tests.
- [x] `core.Options` + `Run`: Felder spiegeln (Konvention 6.2.4),
      `serveCmd` mappt sie weiter.
- [x] `internal/app`: Platform-Builder liest die Schalter aus
      `cfg.Twitch` statt `chatAdapter`.
- [x] `internal/twitch`: `PlatformOptions.EventSub/IRC` (String,
      `"off"` schaltet ab), `connect` routet Heimkanal und zusätzliche
      Kanäle; `IRCSession` bekommt `Home` (kein Channel-Stempel, keine
      Mod-Prüfung); `streamStatePoll` (30 s) ohne EventSub; `chat.go`
      routet über `p.irc`; `ChatAdapterIRC/Eventsub`-Konstanten weg.
- [x] `internal/settings`: `Channels.ChatAdapter` raus, Version 2 +
      `migrateChannelsV1` (Feld streichen), Tests.
- [x] `internal/cli/channels.go`: `chat`-Subkommando raus,
      Adapter-Ausgabe in `list` raus, Tests.
- [x] Debug-Logs wie oben (httpclient, ws, irc, platform, map).
- [x] Tests: config-Validierung (beide aus ⇒ Fehler), Plattform
      (IRC-only ⇒ Heimkanal per IRC, kein EventSub-Dial; EventSub-only
      ⇒ zusätzliche Kanäle per EventSub), IRC-Home (kein
      Channel-Stempel, Stream-Poll).
- [x] Doku: README (Flag-Tabelle, Multi-Channel-Abschnitt),
      `docs/usage.md`, `docs/spec/twitch-multi-channel.md` (B15),
      Roadmap-Historie, Test-Matrix `docs/tests/`.
- [x] `go build`, `go vet`, golangci-lint, `go test ./...`.
