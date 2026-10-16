# Plan: 5.6 SetTitle und SetGame

**Status:** erledigt
**Stand:** 2026-10-16
**Stack:** auf PR #250 (Preset-Mechanismus)

## Goal
Die P0-Presets `!settitle <text>` und `!setgame <name>` setzen Titel und
Kategorie des Streams (Roadmap 5.6, P0; Anhang A.3).

## Design decisions
- **Plattform-Capability** `connector.StreamControl` (optional, wie
  `Chatters`/`PlatformData`): `SetTitle(ctx, ch, title)` und `SetGame(ctx,
  ch, game)`. Die Plattform implementiert sie, wenn sie Titel/Kategorie
  ändern kann; die Action prüft sie per Type-Assertion.
  - **Mock:** setzt `channel.Title`/`channel.Game`, loggt.
  - **Twitch:** `SetTitle` → Helix `POST /channels` (Titel); `SetGame` →
    Spiel-ID über `GET /games?name=` suchen, dann `POST /channels`
    (GameID). Nur der eigene Kanal (Broadcaster = Streamer-Konto).
- **Action** `internal/action/stream`: Typ `stream`, Kinds `set_title` und
  `set_game`. Ports: `Templates` (rendern des Textes), `Platforms` (die
  Plattform auflösen, wie die Moderation-Action), `General` (Default-
  Plattform), `Logger`. Der Text kommt aus `$allargs`.
- **Presets:** `settitle` und `setgame`, je ein stream-Action mit `$allargs`.

## Files
- `internal/connector/platform.go`: `StreamControl`-Interface.
- `internal/connector/mock/simulate.go`: `SetTitle`/`SetGame`.
- `internal/twitch/stream.go`: `*Platform.SetTitle`/`SetGame`.
- `internal/action/stream/stream.go`: die Action.
- `internal/app/app.go`: Action registrieren.
- `internal/preset/preset.go`: die Presets.
- Tests: mock, action, preset.
- `docs/roadmap.md`, dieses Planfile.

## Tests
| Test | Level |
|------|-------|
| Mock SetTitle/SetGame setzt channel.Title/Game | unit (mock) |
| Action set_title/set_game ruft StreamControl auf (Text aus $allargs) | unit (action) |
| Action ohne Capability → Fehler/Warnung | unit (action) |
| Presets settitle/setgame existieren mit stream-Action | unit (preset) |
