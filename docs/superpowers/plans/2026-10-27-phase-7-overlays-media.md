# Plan: Phase 7 Overlays und Medien

Roadmap: [Phase 7](../../roadmap.md#phase-7-overlays-und-medien). Ziel: OBS-Browserquellen mit
Items und Widgets, Ton, TTS und OBS-Steuerung (M3). Jeder Unter-Punkt 7.x wird als eigener PR
umgesetzt, gestapelt auf einen neuen Stack (unterster PR: Doku mit Plan, Spezifikation und ADRs).

Entscheidungsgrundlage: Arbeitsauftrag des Projektinhabers vom 2026-10-27 (autonome Arbeit bis
7.5). Spezifikation [`docs/spec/overlays.md`](../../spec/overlays.md), ADRs
[0019](../../adr/0019-overlay-architektur.md) und
[0020](../../adr/0020-audio-ausgabe.md).

## 1. Ist-Zustand (Audit)

| Teil | Zustand |
|---|---|
| `internal/httpserver` | Ein HTTP-Server für API/Health, `Rebind` (6.5), `SetAPI` als spätes Binden |
| `internal/settings` | Typisierte, versionierte Sektionen; "overlay" steht noch aus (3.2 übertragen) |
| `internal/action` | Typ-Registry (Code-ADR-0013): Descriptor, `WithKinds`, JSON-Schema, Catalog; Kategorien je Paket |
| `engine.Run.PlaybackEnds` | Existiert (B43): die Aktion meldet das Ende der Wiedergabe, die Engine nutzt es für den Mindestabstand der Begrüßungen |
| `internal/emote` | Emote-Kataloge (Twitch/BTTV/FFZ) mit Cache und Bild-URLs |
| `internal/domain/feed` | Event-Feed je Profil (`LatestFeed`, 200 Einträge) mit den Typen Follow, Sub, Resub, Geschenk, Massengeschenk, Raid, Cheer |
| `internal/store` | goose-Migrationen; letzte Tabelle `user_answer` (0020) |
| `internal/capability` | `host:fs`, `host:process`, `host:audio`, `net:outbound`, `script` je Betriebsmodus |
| `coder/websocket` | Bereits Abhängigkeit (EventSub) — wird für die Overlay-WS-Endpunkte wiederverwendet |
| Build-Werkzeuge | esbuild-Konvention steht in Code-ADR-0016 (gepinnte Version per `go generate`, eingechecktes Bündel, CI-Check) |
| Release | `.goreleaser.yaml` mit Binaries, Modul-Archiv, Container-Image |

Fehlt also komplett: Overlay-Server, Runtime, Items, Widgets, Medien (Audio/TTS), OBS, 7.5-Widgets.

## 2. Architektur

```
                          ┌──────────────────────────── Core ────────────────────────────┐
 Command-Engine ──overlay/sound/tts/OBS-Action──► Services                                 │
 (B43 PlaybackEnds ◄── Rückmeldung über Ports)     │                                       │
                                                   ▼                                       │
                              ┌──────────── overlay.Service (Items + Widgets + Endpunkte) ─┤
                              │      │ item.show / widget.* / tts.speak / item.remove      │
                              ▼      ▼                                                     │
                        Overlay-Server (eigener http.Server, Port 8742)                    │
                        GET /overlay/{id}   → embedded HTML+JS (esbuild-Bundle, 7.1)       │
                        GET /overlay/{id}/ws→ WebSocket (coder/websocket), State-Sync      │
                        GET /media/{root}/… → os.Root + http.ServeContent (Range)          │
                              │                                                             │
                              ▼                     audio: overlay-Item ◄── media.Sink ──► │
                        OBS Browserquelle            (local: Build-Tag, ADR-0020)          │
                                                                                           │
                              obs.Service (goobs, obs-websocket v5) ──► Bus (obs.*-Events) │
```

- **Eigener Overlay-Server** (ADR-0019): eigener `http.Server` neben dem API-Server, eigener
  lauschender Port (`overlay-listen`, Default `127.0.0.1:8742` bzw. Server-Modus `:8742`;
  8741 ist der Auth-Loopback). Runnable beim Supervisor, nicht kritisch.
- **Protokoll** (ADR-0019): JSON-Pakete `{type, data}` über WebSocket. Items (einmalig) und
  Widgets (persistente Elemente mit Zustand). State-Sync beim (Wieder-)Verbinden: der Server
  sendet `hello`, `state.widgets` und die aktiven Items neu. Batching läuft in der Runtime
  (aufeinanderfolgende Items versetzt einblenden).
- **Runtime** (ADR-0019): neu geschriebenes TypeScript in `internal/overlay/runtime/src`,
  gebündelt per `go generate` mit esbuild (CLI per `go run …@v0.28.2`, Code-ADR-0016), eingecheckt
  nach `internal/overlay/web`, per `go:embed` ausgeliefert. Kein Node.js.
- **Widgets**: definiert in der Settings-Sektion "overlay" (je Widget `id`, `kind`, `endpoint`,
  Position, Konfiguration); der veränderliche Zustand (Goal-Fortschritt, Timer, Boss, Rad,
  Credits) liegt in der Store-Tabelle `overlay_widget_state` (JSON je Widget-ID).
- **Widget-Commands des Originals** werden zu Ereignissen auf dem Bus (`overlay.goal.progress`,
  `overlay.wheel.spun`, …), auf die Event-Commands reagieren (Abweichung A2 in der Spezifikation).
- **Medien** (ADR-0020): Audio-Sinks `overlay` (Default, spielt in der Browserquelle) und
  `local` (Build-Tag `localaudio`, `ebitengine/oto/v3`, Geräteauswahl); `agent` ist vorbereitet
  (Port), die Umsetzung mit Phase 9.6. TTS-Provider: `browser` (Web Speech API im Overlay,
  Default), `google`, `azure`, `piper` (externer Prozess).
- **OBS** (7.4): `internal/obs` über `github.com/andreykaipov/goobs` (obs-websocket v5.7);
  Szenen, Quellen, Filter, Stream/Aufnahme/Replay; OBS-Events auf den Bus.
- **Sicherheit**: Overlay-Endpunkte nur lesend, optionaler URL-Token (`overlay-token`);
  eigene HTML-Items in sandboxed iframes ohne Skripte (Plan §6.15); Medien nur unter
  freigegebenen Wurzeln via `os.Root`, an das `host:fs`-Recht gebunden.

## 3. PR-Plan (Stack, Basis `main`)

### 3.0 Doku (unterster PR) — `docs/phase-7-plan-and-specs`

- [x] Plan (diese Datei)
- [x] Spezifikation `docs/spec/overlays.md` (Status Geprüft, Abnahme per Arbeitsauftrag)
- [x] ADR-0019 `docs/adr/0019-overlay-architektur.md` (Status Akzeptiert)
- [x] ADR-0020 `docs/adr/0020-audio-ausgabe.md` (Status Akzeptiert)
- [x] Indices: `docs/adr/README.md`, `docs/spec/README.md` (overlays.md wird zum Link)
- [x] Roadmap: 7.1 Spec-Punkt abhaken, Statusübersicht "in Arbeit", Änderungshistorie

### 3.1 7.1 Overlay-Server und Runtime — `feat/phase-7.1-overlay-server`

- [ ] `internal/overlay`: Endpunkt-Registry (aus der Sektion), Item- und Widget-Registry mit
  State-Sync, `Server` (HTTP+WS Runnable), Token-Prüfung, `Rebind`-fähiges Config
- [ ] Runtime: `runtime/src` (TS), `web/` (index.html + bundle.js eingecheckt), `generate.go`
  mit gepinntem esbuild, `go:embed`; Pakete/Positionen/Ebenen, Ein-/Ausblend-Animationen
  (CSS), Batching, sandboxed iframes für HTML, Reconnect mit Backoff
- [ ] Medien-Handler: `os.Root` über den freigegebenen Wurzeln (Profil-`media`-Verzeichnis +
  `file-root`-Namen), `http.ServeContent` (Range), MIME-Typen; Gate `host:fs`
- [ ] Settings-Sektion "overlay" (Version 1): `endpoints` (+ Default), `widgets` (Leerliste)
- [ ] Config: `overlay-listen`, `overlay-token` (Restart-only)
- [ ] App-Wiring (Supervisor, non-critical), `core.Options`, Tests (Server, WS, Token, Range,
  Runtime-Bundle-Check), Test-Matrizen, Roadmap 7.1 abhaken

### 3.2 7.2 Items und Widgets (P1) — `feat/phase-7.2-items-widgets`

- [ ] Item-Modell + Kinds `text`, `image`, `video`, `audio`, `html`, `youtube`, `clip`,
  `timer`, `emote_effect` (Platzhalter, voll in 7.5); Positionen (simple/percent/pixels/
  random), Ebenen, Animationen; Laufzeit pro Item mit `item.remove`; `item.ended` vom
  Client für die Wiedergabe
- [ ] Runtime: Rendering aller Item-Kinds inkl. YouTube/Clip-Embeds, Timer-Countdown
- [ ] Widget-Store (`overlay_widget_state`), Widget-Service; Kinds `label`, `goal`, `timer`,
  `event_list`, `chat` mit Kernzustand und Live-Updates (Feed, Chat-Events, Feed-Events)
- [ ] Runtime: Rendering der fünf Widget-Kinds (inkl. clientseitiger Clock für Datum/Zeit)
- [ ] Overlay-Action: Typ `overlay`, Kinds `item` und `widget` (show/hide/update +
  Operationen AddToGoal, AddToTimer, PauseTimer, AddToEventList, SpinWheel-Platzhalter),
  Positions-/Animationsschema für Editoren; `VisualAudio`; `PlaybackEnds` für Ton/Video
- [ ] Events `overlay.goal.progress`/`segment_completed` (+ Details im Bus-Katalog, in `All()`)
- [ ] Tests, Test-Matrizen, Roadmap 7.2 abhaken

### 3.3 7.3 Medien — `feat/phase-7.3-media`

- [ ] `internal/media`: Sink-Interface, `overlay`-Sink (Audio-Item auf dem Default-Endpunkt),
  `local`-Sink (Build-Tag `localaudio`, oto/v3, Geräteauswahl `audio-device`; Dekodierung WAV
  + MP3), `agent`-Port vorbereitet
- [ ] Sound-Action: Typ `sound` (file, volume, sink, waitForFinish), `VisualAudio`,
  `PlaybackEnds`
- [ ] TTS: `internal/tts` mit Provider-Interface; `browser` (Packet an die Runtime,
  Web Speech API), `google` (REST, API-Key per Env), `azure` (REST, Key+Region per Env),
  `piper` (externer Prozess, `host:process`); Config `tts-provider`
- [ ] TextToSpeech-Action: Typ `text_to_speech` (text, provider, voice, volume, rate, pitch,
  waitForFinish, sink)
- [ ] Release: Desktop-Variante des Core-Binaries (goreleaser-Build `streamcrew-desktop` mit
  Tag `localaudio`)
- [ ] Tests (inkl. Build-Tag-Stub), Test-Matrizen, Roadmap 7.3 abhaken

### 3.4 7.4 OBS Studio — `feat/phase-7.4-obs`

- [ ] `internal/obs` auf `goobs` v1.11.1 (obs-websocket v5.7): Verbindung (Start +
  Reconnect-Loop alle 5 s nach dem ersten Erfolg, kein Retry vor dem ersten Erfolg),
  Szenen, Quellen, Filter, Stream, Aufnahme, Replay-Buffer
- [ ] OBS-Events auf den Bus: `obs.stream.*`, `obs.recording.*`, `obs.replay.saved`,
  `obs.scene.changed`, `obs.transition.*`, `obs.source.visibility_changed`,
  `obs.filter.visibility_changed` (Details; in `All()`)
- [ ] StreamingSoftware-Action: Typ `streaming_software`, Kinds `set_scene`,
  `start/stop_stream`, `start/stop_recording`, `save_replay`, `set_source`, `set_filter`
- [ ] Config: `obs-host`, `obs-port`, `obs-password` (Restart-only)
- [ ] Tests (Fake-OBS-Server mit obs-websocket-v5-Verhandlung), Test-Matrizen, Roadmap 7.4 abhaken

### 3.5 7.5 Weitere Widgets (P2/P3) — `feat/phase-7.5-more-widgets`

- [ ] `persistent_timer` (persistiert, Ereignis-Beträge additiv/multiplikativ, Pause,
  Display-Format DD/hh/MM/mm/SS/ss, Events `overlay.timer.*`)
- [ ] `stream_boss` (Health, Overkill-, Kill-Bonus, Self-Heal, Compound, Ereignis-Schaden,
  Events `overlay.boss.*`)
- [ ] `end_credits` (Sektionen aus Feed/Sitzung, Custom per Action, `play_credits`)
- [ ] `leaderboard` (Statistiktypen aus den Nutzer-Statistiken, Live-Update)
- [ ] `wheel` (Outcomes, Equal/Set-Probability, Modifiers mit Summe 0, Spin → Event
  `overlay.wheel.spun` + Outcome-Command)
- [ ] `poll` (aktive Twitch-Polls/-Vorhersagen live, aus den Twitch-Events)
- [ ] `emote_effect` voll (Emote-Auflösung über die Kataloge, Effekte rain/burst/random,
  Per-Emote/Max-Anzahl) und `persistent_emote_effect` (Combo-Count + Timeframe im Chat)
- [ ] `game_queue` (Queue-Service join/leave/position, persistiert, Events `gamequeue.*`,
  Widget zeigt die Liste)
- [ ] `discord_voice` (discordgo-Gateway ohne Voice: VOICE_STATE_UPDATE des konfigurierten
  Kanals, Events `discord.voice.*`, Widget listet die Besetzten; Beschränkung dokumentiert)
- [ ] Config: `discord-token` (Env), `discord-voice-channel` (Restart-only)
- [ ] Tests, Test-Matrizen, Roadmap 7.5 + Exit-Kriterien M3 abhaken, Änderungshistorie

## 4. Testing

- Unit- und Integrationstests wie in Code-ADR-0006: Synctest für Zeitpfade, Race in der CI.
- Overlay-Server gegen echten WS-Client (coder/websocket) im Test; State-Sync, Token, Rebind.
- Runtime: Bundle-Regenerierungs-Check in der CI (Code-ADR-0016), dazu ein Smoke-Test, dass
  das eingebettete Bündel die erwarteten Markierungen enthält (kein Headless-Browser in der CI).
- Medien: Fake-Sinks; TTS-Provider gegen httptest-Fakes (Google/Azure), Piper-Prozess-Fake.
- OBS: Fake-Server, der die obs-websocket-v5-Identifizierung spricht (goobs-Client dagegen).
- Jedes Testfile bekommt eine Test-Matrix in `docs/tests/`.

## 5. Risiken und Grenzen

- YouTube-/Clip-Embeds brauchen Netz in der Browserquelle; im Offline-Fall zeigt das Embed
  einen Fehler — dokumentiert, kein Core-Fehler.
- Lokale Audioausgabe unter Linux mit oto: reine Go-Bindings (ALSA via cgo? — zu verifizieren
  bei 7.3; bei CGO-Bedarf bleibt der Default-Server-Build ohne Tag unaffected).
- `discord_voice` ohne Voice-Verbindung erkennt kein Sprechen (P3-Beschränkung, A3).
- OBS v5 verlangt ein passendes obs-websocket in OBS (Doku).
- TTS-Cloud-Provider: Keys nur per Umgebungsvariablen, nie in Logs (ADR-0020).
