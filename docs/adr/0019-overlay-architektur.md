# ADR-0019: Overlay-Architektur (eigener HTTP-Server + Web-Client, Medien über den Streamer-PC)

- **Status:** Akzeptiert
- **Entscheidung durch:** Projektinhaber (autonomer Arbeitsauftrag vom 2026-10-27)
- **Entstanden in:** Phase 7 Planungs-/Autonomie-Sitzung
- **Verwandt:** ADR-0010 (Konsistent-Sync), ADR-0016 (TypeScript/ESBuild-Bundle), ADR-0017 (Konfiguration), ADR-0020 (Audioausgabe)
- **Betroffene Pakete:** `internal/overlay`, `internal/overlay/runtime`, `internal/overlay/web`, `internal/settings`, `internal/store`, `internal/app`, `internal/config`, `internal/domain/eventtype`, `internal/action`, `internal/engine`, `internal/capability`
- **Referenzen:** `docs/spec/overlays.md` (B-Regeln), `docs/superpowers/plans/2026-10-27-phase-7-overlays-media.md` §6, Mix It Up Documentation (Q7–Q17, Q27)

## Kontext

Phase 7 führt Overlay-Items (Text, Bild, Video, Audio, HTML, YouTube-Clips, Emote-Effekte), Widgets (Ziele, Timer, Eventlisten, Chat, Stream-Boss u. a.), Medienwiedergabe und OBS-Steuerung ein. Die zentrale offene Frage: Wie erreicht die Server-App die im OBS-Scene-Layout geöffneten Browser-Pane?

Drei Varianten standen zur Diskussion:

1. **Browser-Pane im Haupt-Webclient** — Overlays wären Tabs des Streamer-UI.
2. **Eigener HTTP-Server + eingebetteter Web-Client** — die Streamcrew-App hostet die Overlay-Seiten selbst; OBS zeigt die URLs in Browser-Panes (Q17).
3. **Externe Overlay-App** (z. B. Streamlabs-Overlay-artig).

Zusätzlich ist offen, wie Medien (Videos, Clips, HTML-Embeds) ohne Internet-Bindung an OBS ausgeliefert werden und wo Audiosounds physisch wiedergegeben werden (→ ADR-0020).

Entscheidende Eingangsgrößen:

- OBS kann Browser-Panes gegen beliebige lokale HTTP-URLs öffnen (Q17); der Browser-Pane lädt HTML und kann WebSocket-Verbindungen halten.
- Das Event-Sub-System liefert uns bereits `coder/websocket` als Dependency; ein zweiter WebSocket-Consumer ist günstig.
- ADR-0016 etabliert bereits die Konvention „TypeScript → esbuild → geprüftes Bundle → `go:embed`"; der Overlay-Client kann dieselbe Pipeline ohne neue Werkzeugmittel wiederverwenden.
- Der Haupt-Webclient (`internal/web`) ist ein TUI-artiges Verwaltungs-UI; eine Overlay-Darstellung darin würde zwei Zielgeräte in einer SPA vermischen.
- Server-Modus (Capability-Modell) verweigert `host:fs`/`host:audio` — Medienlieferung und Audio müssen capability-geprüft bleiben (B17, B18).

## Entscheidung

**Variante 2: eigener, nicht-kritischer HTTP-Server in der Streamcrew-App**, ein dedizierter, eingebetteter Web-Client pro Overlay-Endpoint, Kommunikation über WebSocket (JSON-Protokoll), Medien aus dem Medienverzeichnis über denselben Server.

### 1. Server

- Der Overlay-Server ist ein zweiter `http.Server` neben dem API-Server (`internal/httpserver`), als eigene Supervisor-Runnable **ohne `critical`-Flag** (B3): Ein Ausfall tötet die Streamcrew-App nicht, die Haupt-API bleibt erreichbar.
- Konfiguration: `overlay-listen` (Default `127.0.0.1:8742`; im Server-Modus `:8742`, damit OBS auf einem anderen Rechner per URL/Reverse-Proxy zugreifen kann) und `overlay-token` (leer = kein Token; gesetzt = alle `GET /overlay/*`-Pfade benötigen `?token=`; Neustart erforderlich, B2). Port 8741 ist bereits durch die Auth-Loopback-URL belegt.
- Routen:
  - `GET /overlay/{id}` — eingebettetes HTML des Overlay-Client-Bundles, `Cache-Control: no-store` (B4). `{id}` ist die `endpoints`-ID aus den Overlay-Einstellungen; `default` ist der Reservename für den Standard-Endpoint.
  - `GET /overlay/{id}/ws` — WebSocket-Upgrade, `coder/websocket` (B5), Token wie oben.
  - `GET /media/{root}/{path...}` — Dateien aus dem Medienverzeichnis über `os.Root`-auflösendes `http.ServeContent` mit Range-Support (B17); nur bei `host:fs` (B18); `{root}` ∈ `files` (Default-Wurzeln) oder `generated` (Server-generierte Dateien, z. B. TTS-Audio); Verweigerung außerhalb der Wurzeln per Status 404 statt 403 (B17).
- Ein Server-Neustart (Neustart der App) rebindet wie der API-Server; die Overlay-Seiten müssen nach einem Portwechsel neu geladen werden — das ist akzeptabel und wird in der Doku notiert.

### 2. Web-Client (Runtime)

- Neue TypeScript-Quellen in `internal/overlay/runtime/src/`, Bundle nach ADR-0016: `go:generate` mit **pinnedem** `go run github.com/evanw/esbuild/cmd/esbuild@v0.28.2`, Checked-in-Artefakt in `internal/overlay/web/` (Singlefile, `--minify`, `--target=es2020` — OBS-CEF ist modern genug), `go:embed` im Server, CI-Regen-Check per `git diff --exit-code`.
- Eine Server-Route liefert den Bundle-Byte-String; pro `{id}` wird derselbe Bundle mit einer kleinen JSON-Injektion (`window.__OVERLAY__ = {id, endpointName}`) individualisiert.
- **Kein Framework** (kein React/Vue): Vanilla TS + CSS; das Overlay ist ein stateless Renderer für Server-Zustand, Komplexität bleibt minimal (Ponytail).

### 3. Protokoll (JSON, `{type, data}`)

Server → Client (B6):

| Meldung | Bedeutung |
| --- | --- |
| `hello` | Begrüßung beim (Re-)Connect: Serverzeit, Endpoint-`id`, Widget-Zustands-Seed (B8) |
| `state.widgets` | Vollständiger Widget-Zustand (B8) |
| `item.show` | Item mit allen Feldern zeigen (B9) |
| `item.remove` | Item per `itemId` entfernen |
| `widget.show` / `widget.hide` / `widget.update` | Widget-Zustandsänderungen |
| `tts.speak` | TTS-Provider `browser`: Text + Sprache sprechen (B27) |

Client → Server (B7):

| Meldung | Bedeutung |
| --- | --- |
| `ready` | Client initialisiert; Server sendet vollen Zustand (B8) |
| `item.ended` | Item-Wiedergabe beendet (Video/Audio/Emote-Effekt) → Server entfernt das Item und löst `PlaybackEnds` (B43) |
| `tts.ended` | Browser-TTS fertig |

- Vollständige Zustands-Synchronisation bei (Re-)Connect: `hello` + `state.widgets` (B8); Items werden neu von der Server-Item-Queue gesendet, wenn der Client `ready` meldet.
- **Client-seitiges Reconnect:** exponentieller Backoff 1–30 s (B15). **Server-seitig** werden Client-Slots pro Endpoint gehalten; ein abgefallener Client wird ersetzt, Items bleiben in der Queue (B13).
- **Client-seitiges Batching:** mehrere Item-Aufrufe innerhalb 500 ms werden zu max. 300 ms Stagger umverteilt, damit z. B. fünf Emote-Reaktionen nicht aufeinanderstürzen (Q3, B9).

### 4. Items (B-Regeln B9–B15)

- Items sind serverseitig registrierte Objekte mit `itemId`, `kind` (`text|image|video|audio|html|youtube|clip|timer|emote_effect`), Inhalt, Ziel-Widget, Position, Dauer/`until`, Animation, `source` (Medienpfad oder Embed-URL).
- **Positionsmodelle** (B12): `simple` (9 benannte Punkte: z. B. `top-left`, `top`, `center`), `percent` (`{x, y}` 0–100), `pixels` (`{x, y}`), `random` (Server würfelt `percent`). Client rendert gemäß Modell.
- **Animationen** (B11): `none|fade|slide-left|slide-right|slide-up|slide-down|pop`, Default-Dauer 300 ms, pro Item überschreibbar.
- **HTML-Items** (B12, Q27): in einem `sandbox`-If ohne Scripts (nur `allow-same-origin` für relative `/media/`-Pfaden; Scripts grundsätzlich deaktiviert, weil der Overlay-Client selbst die Einbettung kontrolliert). `allow-same-origin` ohne Scripts ist kein Exfiltrationskanal.
- **YouTube/Clips** (Q6, Q27): Server rendert die embeddable URL (`https://www.youtube-nocookie.com/embed/{id}?autoplay=1` bzw. Clip-Embed); der Client öffnet ein `sandbox`-Iframe; das `item.ended`-Signal kommt beim Clip vom Server (Berechnung aus der Länge) oder entfällt — YouTube sendet nichts.
- **Dauer/`until`** (B10): `after` (Sekunden) oder `until` (ISO-Zeitpunkt); serverseitig geprüfte `after`-Grenzen (z. B. `clip`/`video` max. 120 s, `audio` max. 300 s, sonst Fehlermeldung, Q3).

### 5. Widgets (B16–B26)

- **Definition** in den Einstellungen, neue Sektion `overlay`: `endpoints` (Liste, reservierte ID `default`) und `widgets` (Liste: `id`, `kind`, `name`, `endpoint`, `position`, `visible`, `config` JSON). Widgets werden in der Web-UI administriert, nicht ad-hoc zur Laufzeit angelegt.
- **Laufzeit-Zustand** (mutable Daten wie Ziel-Fortschritt, Timer-Werte, Boss-HP, Rad-Status) in der Store-Tabelle `overlay_widget_state` (JSON pro `widgetId`, Migration ab 0021) — ADR-0010-Konsistenz: Zustand ist in Store und Einstellungen getrennt.
- **Widget-„Commands"** werden zu **Bus-Events**, nicht zu neuen Actions (Abweichung A2): `overlay.goal.progress/segment_completed`, `overlay.timer.adjusted/completed`, `overlay.boss.damage/heal/new`, `overlay.wheel.spun`, `gamequeue.joined/left/position`, `discord.voice.joined/left` — alle in `eventtype.All()` und damit von Event-Commands aus auslösbar und für das Trigger-System sichtbar.
- Widget-Rendering ist client-seitig (TS-Module pro `kind`); der Server hält nur den Zustand und sendet `widget.*`-Meldungen.

### 6. TTS-Provider (B27)

- Provider: `browser` (Default, Web Speech API im Overlay-Client, über `tts.speak`), `google`, `azure`, `piper` (externe Prozess, `host:process` erforderlich).
- Server-seitige Provider schreiben das Audiodatei unter `<data-dir>/<profil>/media/generated/` und zeigen es als `audio`-Item auf dem Default-Endpoint (MIU-Verhalten „TTS über den Streamer-PC" ist damit äquivalent, Q14).
- API-Keys über Umgebungsvariablen (`STREAMCREW_TTS_GOOGLE_KEY`, `STREAMCREW_TTS_AZURE_KEY`/`_REGION`), nicht in der Config-Datei (Secret-Hygiene, ADR-0017).
- Browser-Provider benötigt kein Key; `tts.ended` ackt den Client.

### 7. OBS-Steuerung (B37–B42)

- `internal/obs` auf `github.com/andreykaipov/goobs` v1.11.1 (obs-websocket v5.x-Protokoll). **Nicht** `Omniscimus/goobs` (existiert nicht).
- Konfiguration: `obs-host` (Default `127.0.0.1`), `obs-port` (Default `4455`), `obs-password` (leer = kein Passwort).
- Verbindungs-Verhalten (Q26): verbinden beim Start, **kein Retry vor der ersten erfolgreichen Verbindung** (vermeidet Log-Spam wenn OBS nicht läuft), nach einem erfolgreichen Connect 5-s-Reconnect-Loop bei Abriss.
- Actions `streaming_software` mit `set_scene|start_stream|stop_stream|start_recording|stop_recording|save_replay|set_source|set_filter` (B38, Q26).
- OBS-Events (`obs.stream.*`, `obs.recording.*`, `obs.replay.saved`, `obs.scene.changed`, `obs.transition.*`, `obs.source.visibility_changed`, `obs.filter.visibility_changed`) werden in die EventBus einspeist und in `eventtype.All()` geführt (B39, A2).
- Kein OBS-Modul im Server-Modus (keine Capability dafür; `net:outbound` genügt nicht — es fehlt `host:process`/Lokalisierung; die OBS-Verbindung wird im Server-Modus nicht aufgebaut).

### 8. Medien (B17, B18)

- Medienverzeichnis `<data-dir>/<profil>/media/` mit Unterverzeichnissen `files/` und `generated/`; die URL `/media/{root}/{path...}` mappt `root` ∈ `files|generated` auf das Unterverzeichnis.
- `os.Root`-basierte Auflösung (`os.DirFS` + `http.ServeContent`) verhindert Path Traversal; `ServeContent` liefert Range-Requests (B17), die OBS-CEF-Videowiedergabe braucht.
- MIME-Typen über `mime.TypeByExtension`; unbekannte Endungen werden als `application/octet-stream` ausgeliefert (Download statt Render).
- Capability-Gate: `host:fs` erforderlich; ohne (Server-Modus) ist die `/media/`-Route deaktiviert (B18) und alle Media-Items schlagen mit klarer Fehlermeldung fehl.
- Die Wiedergabe selbst findet **im Browser-Pane des Streamer-PCs** statt (Q7): der Server dient nur Dateien; Audio der Medien-Items geht über ADR-0020.

### 9. Discord Reactive Voice (B52–B54)

- Eigener Gateway-Bot (`discordgo`), **nicht** die Twitch-Verbindung: `discord-token` (Env `STREAMCREW_DISCORD_TOKEN`) + `discord-voice-channel` (Kanal-ID) in der Config.
- Voice-Join/Leave wird über `VOICE_STATE_UPDATE` im Gateway erkannt (B53); daraus entstehen die Events `discord.voice.joined/left` (A2).
- **Keine Talking-Detection** (Abweichung A3): `discordgo` liefert keinen zuverlässigen Talking-State; der Widget-Belegungsstand reicht für die Roadmap-Definition aus.
- Discord-Voice-Audio (eigene Bot-Voice) ist **out of scope** — die Roadmap verlangt nur Reaktivität auf Join/Leave + Belegung (Q23).

### 10. Spielewarteschlange (B48–B51)

- Service `internal/gamequeue`: Warteschlange (max. 10, persistiert in `overlay_widget_state`-Artiger eigener Tabelle oder derselben JSON-Zeile), Events `gamequeue.joined/left/position` (A2).
- **Abo-Priorität** (A1, Q18): in 7.5 bewusst weggelassen — Abo-Stufen gehören in Phase 8.3 (Donations); die Warteschlange ist ohne Prioritäten funktional vollständig.
- **Game-Queue-Action** (A4, Q19): in 7.5 weggelassen, gehört in 8.3.

## Konsequenzen

- **Pro:**
  - OBS-Anbindung ist trivial: Browser-Pane auf `http://127.0.0.1:8742/overlay/default` (Q17); kein externes Overlay-Tool, keine zusätzliche Installation.
  - Medien laufen über den Streamer-PC — lokal, zuverlässig, ohne Internetzwang für Clips/Videos; Range-Support für flüssige Videowiedergabe.
  - Die ADR-0016-Bundle-Pipeline wird wiederverwendet; kein zweites Frontend-Tooling.
  - Nicht-kritischer Server-Process: Overlay-Fehler isolieren sich, die App bleibt stabil (B3).
  - Protokoll ist bewusst klein (7 S→C, 3 C→S Meldungen); Testbarkeit über `httptest` + `coder/websocket`-Testclient.
- **Contra:**
  - Zweiter `http.Server` = zweite Port-Konfiguration und ein zweiter Rebind-Pfad (akzeptabel, `internal/httpserver` liefert das Muster).
  - Der Overlay-Client ist ein neues TypeScript-Paket ohne UI-Framework; Widget-Rendering muss handgerollt werden (bewusste Trade-off gegen Framework-Overhead).
  - Server-Modus hat kein Overlay-Rendering ohne Reverse-Proxy (Browser-Panes müssen über eine URL erreichbar sein); `overlay-listen` = `:8742` + Doku-Notiz decken den Fall.
  - YouTube/Clip-Embeds brauchen im Browser-Pane Internet (Q27); das ist inhärent und kein Bug.

## Abwägungen

| Variante | Pro | Contra |
| --- | --- | --- |
| Browser-Pane im Haupt-Webclient | Kein zweiter Server | Zwei Zielgeräte in einer SPA; TUI-UI ist nicht für OBS gebaut; CEF im Browser-Pane würde das Verwaltungs-UI laden |
| **Eigener HTTP-Server (gewählt)** | Isoliert, einfach, OBS-nativ (Q17), wiederverwendete Bundle-Pipeline | Zweiter Port, zweiter Rebind-Pfad |
| Externe Overlay-App | Getrennter Prozess | Zusätzliche Installation, Duplizität des Protokolls, keine gemeinsame Medienlieferung, gegen das 1-App-Ziel der Roadmap |

## Open Items

- MP3-Decoding für den `local`-Audio-Sink (ADR-0020): Kandidat `github.com/dmarc/ito/mp3` — wird in 7.3 entschieden.
- OBS-Reconnect-Intervall (5 s) ist aus der MIU-Doku übernommen; Anpassbar über Config in späteren Phasen, falls nötig.
- Browser-Pane-URL-Dokumentation (OBS-Einrichtung) gehört in `docs/usage.md` (B55), mit der Umsetzung von 7.1.
