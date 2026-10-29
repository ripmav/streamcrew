# ADR-0019: Overlay-Architektur (eigener HTTP-Server + Web-Client, Medien über den Streamer-PC)

- **Status:** Akzeptiert
- **Entscheidung durch:** Projektinhaber (autonomer Arbeitsauftrag vom 2026-10-27)
- **Entstanden in:** Phase 7 Planungs-/Autonomie-Sitzung
- **Verwandt:** [ADR-0012](0012-persistenz.md) (Persistenz je Profil), [Code-ADR-0016](code/0016-codegenerierung.md) (TypeScript/ESBuild-Bundle), [Code-ADR-0005](code/0005-konfiguration.md) (Konfiguration), [ADR-0020](0020-audio-ausgabe.md) (Audioausgabe)
- **Betroffene Pakete:** `internal/overlay`, `internal/overlay/runtime`, `internal/overlay/web`, `internal/settings`, `internal/store`, `internal/app`, `internal/config`, `internal/domain/eventtype`, `internal/action`, `internal/engine`, `internal/capability`
- **Referenzen:** [`docs/spec/overlays.md`](../spec/overlays.md) (B-Regeln und Q-Quellen), [`docs/superpowers/plans/2026-10-27-phase-7-overlays-media.md`](../superpowers/plans/2026-10-27-phase-7-overlays-media.md) §2, Mix It Up Documentation (Quellen Q1–Q27 der Spezifikation)

## Kontext

Phase 7 führt Overlay-Items (Text, Bild, Video, Ton, HTML, YouTube, Clips, Emote-Effekte), Widgets (Ziele, Timer, Eventlisten, Chat, Stream-Boss u. a.), Medienwiedergabe und OBS-Steuerung ein. Die zentrale offene Frage: Wie erreicht die Server-App die im OBS-Scene-Layout geöffneten Browser-Panes?

Drei Varianten standen zur Diskussion:

1. **Browser-Pane im Web-Client (separates Web-Repo `streamcrew-web`)** — Overlays wären Tabs des Streamer-UI.
2. **Eigener HTTP-Server + eingebetteter Web-Client** — die Streamcrew-App hostet die Overlay-Seiten selbst; OBS zeigt die URLs in Browser-Panes (Q1).
3. **Externe Overlay-App** (z. B. Streamlabs-Overlay-artig).

Zusätzlich ist offen, wie Medien (Videos, Clips, HTML-Embeds) ohne Internet-Bindung an OBS ausgeliefert werden und wo Audiosounds physisch wiedergegeben werden (→ [ADR-0020](0020-audio-ausgabe.md)).

Entscheidende Eingangsgrößen:

- OBS kann Browser-Panes gegen beliebige lokale HTTP-URLs öffnen (Q1); der Browser-Pane lädt HTML und kann WebSocket-Verbindungen halten.
- Das Event-Sub-System liefert uns bereits `coder/websocket` als Dependency; ein zweiter WebSocket-Consumer ist günstig.
- Code-ADR-0016 etabliert bereits die Konvention „TypeScript → esbuild → geprüftes Bundle → `go:embed`"; der Overlay-Client kann dieselbe Pipeline ohne neue Werkzeugmittel wiederverwenden.
- Server-Modus (Capability-Modell) verweigert `host:fs`/`host:audio` — Medienlieferung und Audio müssen capability-geprüft bleiben (B6).

## Entscheidung

**Variante 2: eigener, nicht-kritischer HTTP-Server in der Streamcrew-App**, ein dedizierter, eingebetteter Web-Client pro Overlay-Endpunkt, Kommunikation über WebSocket (JSON-Protokoll), Medien aus dem Medienverzeichnis über denselben Server.

### 1. Server

- Der Overlay-Server ist ein zweiter `http.Server` neben dem API-Server (`internal/httpserver`), als eigene Supervisor-Runnable **ohne `critical`-Flag**: Ein Ausfall tötet die Streamcrew-App nicht, die Haupt-API bleibt erreichbar (B1).
- Konfiguration: `overlay-listen` (Default `127.0.0.1:8742`; im Server-Modus `:8742`, damit OBS auf einem anderen Rechner per URL/Reverse-Proxy zugreifen kann; Neustart erforderlich, B1). Port 8741 ist bereits durch die Auth-Loopback-URL belegt.
- **Zugriffsschutz** (B5; geändert 2026-10-29, Review 341-6): Die Overlay-Endpunkte nutzen die API-Tokens des Profils aus [ADR-0013](0013-sicherheitsmodell.md) §5, keinen eigenen Token in der Startkonfiguration (`overlay-token` entfällt).
  - Page, WebSocket und Medien-URLs nehmen den Token als Query-Parameter `token`. Gültig ist nur ein Token mit dem Scope `overlay`. Andere Scopes (auch `admin`) öffnen den Overlay-Server nicht, damit kein starker Token in einer OBS-URL landet.
  - **Der Scope `overlay` öffnet nur Overlay-Seite, WebSocket und Medien, nie die API.** Er ist kein `read`-Scope: Ein Token aus einer OBS-URL steht in Szenen-Exporten, Screenshots und beim Bildschirm-Teilen und darf deshalb weder personenbezogene Daten noch Command-Definitionen lesen (behebt S8 aus [`open-findings-2026-10-26.md`](../reviews/open-findings-2026-10-26.md)). Ein Token, der nur `overlay` trägt, bekommt von der API 403.
  - Ohne Token ist der Zugriff nur erlaubt, wenn alles drei gilt: Desktop- oder Daemon-Modus, `overlay-listen` bindet ausschließlich Loopback, und der `Host`-Header der Anfrage nennt einen Loopback-Namen (`localhost`, `127.0.0.1`, `[::1]`, jeweils mit beliebigem Port). Sonst antwortet der Server ohne gültigen Token mit 401.
  - **DNS-Rebinding** (Review 341-7): Die `Host`-Prüfung verhindert, dass eine fremde Website, deren Name auf `127.0.0.1` zeigt, im Browser des Streamers den Overlay-Zustand oder Dateien unter `/media/` (einschließlich der `file-root`-Wurzeln) liest. Die Origin-Prüfung des WebSockets allein hilft dagegen nicht, weil nach dem Rebinding Origin und Host übereinstimmen. Mit gültigem Token gilt die `Host`-Prüfung nicht, damit OBS auf einem anderen Rechner über einen beliebigen Namen zugreifen kann.
  - Ein mitgeschickter, aber ungültiger oder widerrufener Token ergibt immer 401.
  - Overlay-Tokens entstehen wie alle Tokens mit `streamcrew token create --scope overlay` bzw. über die Token-API; ein Widerruf wirkt sofort, auch für offene WebSockets.
- Routen:
  - `GET /overlay/{id}` — eingebettete Overlay-Runtime (HTML+JS), `Cache-Control: no-store` (B3). `{id}` ist die Endpunkt-`id` aus den Overlay-Einstellungen; `default` existiert immer, unbekanntes `{id}` antwortet mit 404 (B2).
  - `GET /overlay/{id}/ws` — WebSocket-Upgrade, `coder/websocket` (B3), Token wie oben (B5).
  - `GET /media/{root}/{path...}` — Dateien aus den freigegebenen Verzeichnissen über `os.Root`-auflösendes `http.ServeContent` mit Range-Support und korrekten MIME-Typen (B6); nur bei `host:fs`, sonst 404 (B6); `{root}` ∈ `media` (Medien-Verzeichnis des Profils) oder den freigegebenen Wurzeln (`file-root`); Verweigerung außerhalb der Wurzeln per Status 404 statt 403 (B47).
- Ein Server-Neustart (Neustart der App) rebindet wie der API-Server; die Overlay-Seiten müssen nach einem Portwechsel neu geladen werden — das ist akzeptabel und wird in der Doku notiert.

### 2. Web-Client (Runtime)

- Neue TypeScript-Quellen in `internal/overlay/runtime/src/`, Bundle nach Code-ADR-0016: `go:generate` mit **gepinntem** `go run github.com/evanw/esbuild/cmd/esbuild@v0.28.2`, Checked-in-Artefakt in `internal/overlay/web/` (Singlefile, `--minify`, `--target=es2020` — der OBS-Browser ist modern genug), `go:embed` im Server, CI-Regen-Check per `git diff --exit-code`.
- Eine Server-Route liefert den Bundle-Byte-String; pro `{id}` wird derselbe Bundle mit einer kleinen JSON-Injektion (`window.__OVERLAY__ = {id, endpointName}`) individualisiert.
- **Kein Framework** (kein React/Vue): Vanilla TS + CSS; die Overlay-Runtime ist ein stateless Renderer für Server-Zustand, Komplexität bleibt minimal.

### 3. Protokoll (JSON, `{type, data}`; B3)

Server → Client:

| Meldung | Bedeutung |
| --- | --- |
| `hello` | Begrüßung beim (Re-)Connect: Server-Version, Endpunkt, Serverzeit (B4) |
| `state.widgets` | Alle sichtbaren Widgets des Endpunkts mit Zustand (B4) |
| `item.show` | Aktives Item mit allen Feldern zeigen (B4, B9–B12) |
| `item.remove` | Item per `itemId` entfernen (B11) |
| `widget.show` / `widget.hide` / `widget.update` | Widget schalten bzw. Zustandsänderung ohne Reload (B21, B22) |
| `tts.speak` | TTS-Provider `browser`: Text + Parameter sprechen (B41) |

Client → Server:

| Meldung | Bedeutung |
| --- | --- |
| `ready` | Client initialisiert; Server sendet vollen Zustand (B4, B7) |
| `item.ended` | Item-Wiedergabe beendet (Ton/Video/Emote-Effekt) → Server entfernt das Item und löst `PlaybackEnds` (B14) |
| `tts.ended` | Browser-TTS fertig (B41) |

- Vollständige Zustands-Synchronisation bei (Re-)Connect in dieser Reihenfolge: `hello`, `state.widgets`, dann die aktuell aktiven Items neu als `item.show` (B4).
- **Client-seitiges Reconnect:** Backoff 1 s bis 30 s, danach `ready` (B7). **Server-seitig** werden Client-Slots pro Endpunkt gehalten; zwei Clients am selben Endpunkt sehen denselben Stand, `item.ended` zählt, wenn der erste Client es meldet (B49).
- **Client-seitiges Batching:** aufeinanderfolgende Items desselben Endpunkts, die sich zeitlich überlappen würden, werden versetzt eingeblendet: ein Item, das innerhalb von 500 ms nach dem vorherigen gezeigt wird, startet 300 ms später als dieses (B8, Q5).

### 4. Items (B9–B12; Kinds in 7.2)

- Items sind serverseitig registrierte Objekte mit `itemId`, `kind` (`text|image|video|audio|html|youtube|clip|timer|emote_effect`), Inhalt, Ziel-Endpunkt, Position, Ebene, Dauer, Animation, `source` (Medienpfad oder Embed-URL) (B13).
- **Positionen und Ebenen** (B9): `simple` (neun benannte Bildschirmpunkte: z. B. `top-left`, `top`, `center`), `percent` (`{x, y}` 0–100), `pixels` (`{x, y}`), `random` (Zufallspunkt im Pixel-Bereich, vom Server beim Zeigen gewürfelt). Ebenen sind ganze Zahlen; höhere liegen darüber.
- **Animationen** (B10): `none|fade|slide-left|slide-right|slide-up|slide-down|pop`, eigene Dauer in Millisekunden (Default 300); die Animation läuft als CSS-Übergang in der Runtime.
- **HTML-Items** (B12): in einem sandboxed iframe (`srcdoc`) ohne Skript- und ohne same-origin-Zugriff; das HTML darf nur relative `/media/`- und absolute HTTPS-URLs referenzieren (Q2, Q6). Ergänzt 2026-10-29 (Review 341-9):
  - **Medien mit Token:** Bevor die Runtime das HTML in `srcdoc` setzt, hängt sie an jede URL, die mit `/media/` beginnt (`src`, `href`, `poster`, `srcset`), den `token` der Seite an (derselbe Helper wie für WebSocket und Medien-Items). Ohne Token bleiben die URLs unverändert.
  - **Durchsetzung per CSP:** Die Runtime stellt dem HTML ein `<meta http-equiv="Content-Security-Policy">` voran: `default-src 'none'; img-src <overlay-origin> https:; media-src <overlay-origin> https:; style-src 'unsafe-inline' <overlay-origin> https:; font-src <overlay-origin> https:`. `<overlay-origin>` ist der Origin der Overlay-Seite (der iframe hat einen opaken Origin, `'self'` würde nicht greifen). Skripte sind ohnehin durch die Sandbox gesperrt.
- **YouTube/Clips** (B15): laden als Embeds in der Browserquelle (YouTube: Embed-Frame mit `youtube-nocookie.com`; Clip: Clip-Seite in einem Frame); brechen sie ohne Netz, bleibt das Overlay ansonsten funktionsfähig.
- **Laufzeit** (B11): ein Item verschwindet nach seiner Dauer (Default 10 s); der Server sendet `item.remove` und streicht es aus dem aktiven Satz. Ton- und Video-Items ohne gesetzte Dauer laufen bis zur Wiedergabe (`item.ended`, B14), maximal bis zum Zeitlimit der Aktion (B46).

### 5. Widgets (B19–B23; Arten in 7.2/7.5)

- **Definition** in den Einstellungen, neue Sektion `overlay`: `endpoints` (Liste, reservierte ID `default`, je Endpunkt `id` und `name`) und `widgets` (Liste: `id` eindeutig im Profil, `kind`, `name`, `endpoint` (Default `default`), `visible`, Position (B9), `config` JSON) (B2, B19).
- **Laufzeit-Zustand** (mutable Daten wie Ziel-Fortschritt, Timer-Werte, Boss-HP, Rad-Status) in der Store-Tabelle `overlay_widget_state` (ein JSON-Dokument je Widget, Migration `0023_overlay_widget_state`); ein Neustart ändert nichts am Zustand (B20).
- **Widget-„Commands"** des Originals werden zu **Bus-Ereignissen** mit Details, auf die Event-Commands reagieren (Abweichung A2): `overlay.goal.progress/segment_completed`, `overlay.timer.adjusted/completed`, `overlay.boss.damage/heal/new`, `overlay.wheel.spun`, `gamequeue.joined/left/position`, `discord.voice.joined/left` — alle in `eventtype.All()` und damit von Event-Commands aus auslösbar (B23).
- Widget-Rendering ist client-seitig (TS-Module pro `kind`); der Server hält nur den Zustand und sendet `widget.*`-Meldungen (B21, B22).

### 6. TTS-Provider (B41)

- Provider: `browser` (Default, Web Speech API im Overlay-Client, über `tts.speak`), `google`, `azure`, `piper` (externer Prozess, `host:process` erforderlich).
- Server-seitige Provider schreiben die Audiodatei unter `<data-dir>/<profil>/media/generated/` und zeigen sie als `audio`-Item auf dem Default-Endpunkt — damit ist das MIU-Verhalten „TTS über den Streamer-PC" äquivalent (Q21).
- API-Keys über Umgebungsvariablen (`STREAMCREW_TTS_GOOGLE_KEY`, `STREAMCREW_TTS_AZURE_KEY`/`_REGION`), nie in Logs; fehlender Key ist eine klare Fehlermeldung der Aktion (B54, Code-ADR-0005).
- Browser-Provider benötigt keinen Key; `tts.ended` ackt den Client.

### 7. OBS-Steuerung (B42–B44)

- `internal/obs` mit eigenem obs-websocket-v5-Client auf `github.com/coder/websocket` (Code-ADR-0015, Q27): die Kandidatin `github.com/andreykaipov/goobs` fällt weg, weil ihre Abhängigkeit `github.com/hashicorp/logutils` (MPL-2.0) nicht in der Lizenz-Allowlist steht (Code-ADR-0001; 2026-10-28 nach dem Lizenzergebnis der CI entschieden).
- Konfiguration: `obs-host` (Default `127.0.0.1`), `obs-port` (Default `4455`), `obs-password` (leer = kein Passwort) (B43).
- Verbindungs-Verhalten (B43, Q25): verbinden beim Start, **kein Retry vor der ersten erfolgreichen Verbindung** (Info-Log, vermeidet Log-Spam wenn OBS nicht läuft), nach einem erfolgreichen Connect 5-s-Reconnect-Loop bei Abriss.
- Action `streaming_software` mit Kinds `set_scene|start_stream|stop_stream|start_recording|stop_recording|save_replay|set_source|set_filter` (B42, Q26); ohne aktive Verbindung ist jede Ausführung eine klare Fehlermeldung (B55).
- OBS-Ereignisse (`obs.stream.started/stopped`, `obs.recording.started/stopped`, `obs.replay.saved`, `obs.scene.changed`, `obs.transition.started/ended`, `obs.source.visibility_changed`, `obs.filter.visibility_changed`) laufen als Bus-Ereignisse mit Details, unabhängig vom Verursacher (B44).
- Im Server-Modus wird die OBS-Verbindung nicht aufgebaut (OBS ist ein lokaler Desktop-Prozess; `net:outbound` genügt nicht für die Lokalisierung).

### 8. Medien (B6, B47)

- Medien-Verzeichnis des Profils `<data-dir>/<profil>/media/` plus die freigegebenen Wurzeln (`file-root`); die URL-Form `/media/{root}/{path...}` mappt `{root}` auf das jeweilige Verzeichnis.
- `os.Root`-basierte Auflösung verhindert Path Traversal (Punkt-Slash, absolute Pfade); `http.ServeContent` liefert Range-Requests (B6), die die Videowiedergabe in der Browserquelle braucht.
- MIME-Typen über `mime.TypeByExtension`; unbekannte Endungen werden als `application/octet-stream` ausgeliefert (Download statt Render).
- Capability-Gate: ohne `host:fs` (Server-Modus) liefert der Server keine Medien — 404 (B6); die Aktion lehnt das Zeigen mit klarer Fehler ab (B47).
- Die Wiedergabe selbst findet **im Browser-Pane des Streamer-PCs** statt (Q21): der Server dient nur Dateien; Audio der Medien-Items geht über [ADR-0020](0020-audio-ausgabe.md).

### 9. Discord Reactive Voice (B37, P3)

- Eigener Gateway-Bot (`discordgo`), **nicht** die Twitch-Verbindung: `discord-token` (Env `STREAMCREW_DISCORD_TOKEN`) + `discord-voice-channel` (Kanal-ID) in der Config; ohne Token bleibt der Dienst inaktiv (Info-Log).
- Voice-Join/Leave wird über `VOICE_STATE_UPDATE` im Gateway erkannt (B37); daraus entstehen die Events `discord.voice.joined/left`.
- **Keine Talking-Detection** (Abweichung A3): ohne Voice-Verbindung wird nicht erkannt, wer spricht.
- Discord-Voice-Audio (eigene Bot-Voice) ist **out of scope** — die Roadmap verlangt nur Reaktivität auf Join/Leave + Belegung (Q20).

### 10. Spielewarteschlange (B36, P2)

- Service `internal/gamequeue`: Warteschlange (Beitritt/Verlassen/Position, max. Größe aus der Widget-Konfiguration, Default 10), persistiert (B20), Events `gamequeue.joined/left/position` mit Details (B36).
- **Abo-Priorität und GameQueue-Action** (B36): in 7.5 bewusst weggelassen — sie folgen in Phase 8.3 (Donations/Abo); die Warteschlange ist ohne Prioritäten funktional vollständig (Q19).

## Konsequenzen

- **Pro:**
  - OBS-Anbindung ist trivial: Browser-Pane auf `http://127.0.0.1:8742/overlay/default` (Q1); kein externes Overlay-Tool, keine zusätzliche Installation.
  - Medien laufen über den Streamer-PC — lokal, zuverlässig, ohne Internetzwang für Clips/Videos; Range-Support für flüssige Videowiedergabe.
  - Die Code-ADR-0016-Bundle-Pipeline wird wiederverwendet; kein zweites Frontend-Tooling, kein Node.js im Build.
  - Nicht-kritischer Server-Process: Overlay-Fehler isolieren sich, die App bleibt stabil.
  - Protokoll ist bewusst klein (6 S→C, 3 C→S Meldungen); Testbarkeit über `httptest` + `coder/websocket`-Testclient.
- **Contra:**
  - Zweiter `http.Server` = zweite Port-Konfiguration und ein zweiter Rebind-Pfad (akzeptabel, `internal/httpserver` liefert das Muster).
  - Die Overlay-Runtime ist ein neues TypeScript-Paket ohne UI-Framework; Widget-Rendering muss handgerollt werden (bewusster Trade-off gegen Framework-Overhead).
  - Im Server-Modus liefert der Overlay-Server keine Medien, solange `host:fs` nicht per `--grant` freigegeben ist ([Code-ADR-0019](code/0019-host-rechte-in-der-startkonfiguration.md)). Ein Reverse-Proxy ändert daran nichts; er betrifft nur den Zugriff von einem anderen Rechner, den `overlay-listen` = `:8742` plus Doku-Notiz abdeckt.
  - YouTube/Clip-Embeds brauchen im Browser-Pane Internet (B15); das ist inhärent und kein Bug.

## Abwägungen

| Variante | Pro | Contra |
| --- | --- | --- |
| Browser-Pane im Web-Client (Web-Repo) | Kein zweiter Server | Zwei Zielgeräte in einer SPA; der Browser-Pane würde die komplette Verwaltungs-SPA laden, und das Web-Repo ist kein Teil dieses Builds |
| **Eigener HTTP-Server (gewählt)** | Isoliert, einfach, OBS-nativ (Q1), wiederverwendete Bundle-Pipeline | Zweiter Port, zweiter Rebind-Pfad |
| Externe Overlay-App | Getrennter Prozess | Zusätzliche Installation, Duplizität des Protokolls, keine gemeinsame Medienlieferung, gegen das 1-App-Ziel der Roadmap |

## Open Items

- ~~MP3-Decoding für den `local`-Audio-Sink (ADR-0020): in 7.3 entscheiden.~~ Erledigt 2026-10-27: WAV-only, der damals genannte Kandidat existiert nicht ([ADR-0020, Nachtrag 2026-10-27](0020-audio-ausgabe.md#nachtrag-2026-10-27-umsetzung-73), Punkt 1).
- Das OBS-Reconnect-Intervall (5 s) ist aus der MIU-Doku übernommen (Q25); Anpassbar über Config in späteren Phasen, falls nötig.
- Browser-Pane-URL-Dokumentation (OBS-Einrichtung) gehört in `docs/usage.md`, mit der Umsetzung von 7.1.
