# streamcrew — Bedienungsanleitung

Kurzanleitung für den Betrieb des Cores: Schnellstart auf dem Streaming-PC,
die Betriebsarten (Daemon, Server hinter Reverse Proxy, TUI) und die
Sicherheitshinweise. Die vollständige Befehlsreferenz steht im
[README](../README.md); die Architektur in [docs/plan.md](plan.md).

## Schnellstart (Streaming-PC)

Ziel: ein Twitch-Kanal läuft ohne GUI — Login, erster Command aus YAML,
Start des Cores.

```bash
# 1. Profil anlegen (einmal)
streamcrew profile create "Mein Kanal" --id demo

# 2. Streamer-Konto im Browser anmelden (einmal pro Kanal)
streamcrew auth login twitch
# → URL im Browser öffnen, Twitch-App autorisieren, Loopback-Redirect
#   schließt den Login ab. Ohne eigene App-Credentials nutzt der Login
#   die eingebaute "StreamCrew"-App (ADR-0024): keine Einrichtung nötig.

# 3. Bot-Konto anmelden (einmal; das Bot-Konto braucht die Scopes
#    für Moderation und Chat)
streamcrew auth login twitch --bot

# 4. Beide Konten prüfen
streamcrew auth status

# 5. Erster Command als YAML (Commands als Code, spec/commands-as-code.md)
mkdir -p ~/commands
cat > ~/commands/hug.yaml <<'YAML'
apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata: {name: Hug}
spec:
  triggers: [!hug]
  actions:
    - {type: chat, kind: message, message: "$userdisplayname umarmt $arg1!"}
YAML

# 6. Gegen eine Profil-Kopie prüfen und übernehmen
streamcrew command validate ~/commands/hug.yaml
streamcrew command import ~/commands/hug.yaml

# 7. Core starten
streamcrew serve
```

Der Core lauscht jetzt auf `127.0.0.1:8740` (Loopback, ADR-0003). Im Chat
funktioniert `!hug <name>`; `streamcrew tui` zeigt Dashboard, Chat,
Event-Feed, Command-Queue, Commands, Nutzer und Logs. `Strg+C` beendet den
Core geordnet.

Zusätzliche Kanäle, in denen der Bot wirken soll:

```bash
streamcrew profile channels add <login>   # Core gestoppt
streamcrew profile channels list           # Kanäle auflisten
```

Zwei Chat-Adapter betreiben die Kanäle — gewählt über die
Startkonfiguration (`--twitch-eventsub`/`--twitch-irc`, Flag, Env,
Konfigurationsdatei; **nur zum Start**, Neustart bei Änderung):

- **EventSub** (`twitch_eventsub: on`, Standard): der Heimkanal läuft
  über seine EventSub-Session; sind beide Adapter an, laufen die
  zusätzlichen Kanäle über EventSub und verlangen den Mod-Status des
  Bots — ohne Mod-Status wird der Kanal nicht betrieben (eigene
  EventSub-Verbindung und Subscriptions pro Kanal).
- **IRC** (`twitch_irc: on`): die zusätzlichen Kanäle laufen über den
  Twitch-IRC-Chat des Bot-Kontos — ohne Mod-Voraussetzung (ohne Mod
  wird `mod_missing` gewarnt, der Kanal läuft trotzdem), mit Whispers
  an den Bot und Senden; der Mod-Status wird aus den Chat-Badges
  erkannt. Ist EventSub aus, übernimmt die IRC-Session zusätzlich den
  Heimkanal (dessen Events tragen keinen Kanal-Stempel) und der
  Stream-State kommt aus einem Helix-Poll statt der
  `stream.online`/`stream.offline`-Events.

**Mindestens ein Adapter muss an sein** — sind beide aus, startet der
Core nicht und meldet den Fehler. Sollen alle Kanäle ausschließlich
über IRC laufen (z. B. weil der EventSub-Endpunkt auf der eigenen IP
blockiert ist):

```yaml
twitch_eventsub: off
twitch_irc: on
```

Im IRC-Modus sendet der Bot in den zusätzlichen Kanälen als Bot-Konto;
ist der Chat nur für Follower/Abonnenten offen und der Bot nicht
entsprechend berechtigt, weist der Twitch-Server die Sendung ab (NOTICE
in den Logs). Der IRC-Modus braucht das Bot-Konto; ohne Bot-Konto
bleiben die Kanäle unbetreut (Warnung im Log).

Hinweis: Der Bot-Login verlangt mit dem IRC-Adapter zusätzlich den
Scope `chat:edit`; ein älterer Bot-Login zeigt das in
`streamcrew auth status` an und muss einmal neu angemeldet werden
(`streamcrew auth login twitch --bot`).

## Betriebsarten

### 1. Streaming-PC (Standard: Daemon-Modus)

`streamcrew serve` (Modus `daemon`, Standard). Der Core läuft neben
Streamsoftware und OBS, die API liegt nur auf Loopback, Frontends (TUI,
Desktop-App, Web) verbinden sich lokal. Für die Nutzung mit TUI genügt:

```bash
streamcrew tui
```

Die TUI findet einen lokalen Core über die `runtime.json` im
Datenverzeichnis (Adresse und, im Desktop-Modus, den Unix-Socket) und
nutzt den Desktop-Token automatisch.

### 2. Server-Anwendung (Server-Modus)

Der Server-Modus betreibt den Core auf einem Server: API auf alle
Schnittstellen, Host-Rechte auf `net:outbound` und `script` reduziert,
keine lokalen Dateien, keine Prozesse, kein Audio (ADR-0003).

```yaml
# config.yaml auf dem Server
mode: server
listen: 0.0.0.0:8740
tls_cert: /etc/streamcrew/tls/cert.pem
tls_key: /etc/streamcrew/tls/key.pem
```

**Access Control:** Die API ist fail-closed (ADR-0013 §5): ohne Token lehnt
jede Anfrage mit 401 ab. Im Server-Modus entsteht der erste Token mit
`streamcrew token create --scope <scope>` (Scopes: read, control, admin,
overlay; Default admin; der Wert wird genau einmal gezeigt) — oder beim
Erststart aus `STREAMCREW_TOKEN` (Umgebungsvariable oder
`--bootstrap-token`), die dann als Admin-Token gespeichert wird und danach
ignoriert wird. Im Daemon-Modus legt der Core beim ersten Start einen
lokalen Admin-Token in `<data-dir>/api.token` an, dessen Wert
`STREAMCREW_TOKEN` bzw. `--bootstrap-token` vorgibt, wenn gesetzt,
sonst der Core selbst erzeugt; CLI und TUI lesen ihn ohne
zusätzliches `--token`. Jeder Client setzt den Token:

```bash
streamcrew --server https://meinstream.example:8740 --token <TOKEN> counter list
```

**Reverse Proxy:** TLS kann auch der Reverse Proxy enden (nginx, Caddy,
Traefik) — dann `tls_cert`/`tls_key` weglassen und den Proxy auf
`http://127.0.0.1:8740` zeigen. Der Proxy muss `Upgrade`/`Connection`
für die Streaming-Endpunkte der API durchreichen (ConnectRPC nutzt
Long-Polling, bleibt aber HTTP/1.1-Streaming-fähig). Beispiel (Caddy):

```
meinstream.example {
	reverse_proxy 127.0.0.1:8740
}
```

**systemd:** `packaging/systemd/streamcrew.service` aus dem Release mit
`EnvironmentFile` für Token und Secret-Key (beide nie auf der
Kommandozeile, ADR-0005):

```ini
# /etc/systemd/system/streamcrew.env (0600, root)
STREAMCREW_TOKEN=…
STREAMCREW_SECRET_KEY=…
```

### 3. Container

Das Release enthält das Container-Image (linux/amd64, linux/arm64) als
OCI-Tar (das Registry-Publish steht noch aus, ADR-0025); lokal lädt man
ihn mit `docker load` und taggt das Image. Im Container läuft der Core
standardmäßig im Server-Modus mit Daten in `/data` (Volume) und
JSON-Logs auf stderr:

```bash
docker load -i streamcrew-linux-amd64-arm64.tar   # Image-Tar aus dem Release
docker tag <manifest-referenz> streamcrew
docker run -d --name streamcrew -v streamcrew-data:/data \
  -e STREAMCREW_TOKEN=… -e STREAMCREW_SECRET_KEY=… \
  streamcrew
```

Der Smoke-Test `scripts/docker-smoke.sh` prüft Image-Build,
Non-Root-Betrieb, `/healthz`, `/readyz` und sauberes Beenden.

## On-the-fly-Konfiguration (Server-Modus)

Alle Starteinstellungen, die ein laufender Prozess anwenden kann, lassen
sich im Server-Modus ohne Neustart ändern — über die TUI. Die TUI
(`streamcrew tui`) zeigt in der Ansicht **Einstellungen** (Taste `8`)
das aktuelle Dokument der Konfigurationsdatei. Jede Einstellung trägt
eine Markierung:

| Markierung | Bedeutung |
|---|---|
| ⚡ | **hot**: gilt sofort, ohne Neustart (Watcher, ~1 s) |
| ⟳ | **Restart**: gilt erst nach Neustart |
| ✋ | **fixiert**: per Flag (`--…`) oder Umgebungsvariablen
  (`STREAMCREW_…`) gesetzt — der Wert aus der Datei wird nicht
  angewendet |

**Ändern:** `e` öffnet das Dokument im Editor (`$EDITOR`/`$VISUAL`);
nach dem Speichern wird es validiert und atomar geschrieben, der Core
wendet die hot-Einstellungen an (die Ergebniszeile zeigt, was angewandt
wurde und was einen Restart braucht). `r` lädt neu. Im Daemon- und
Desktop-Modus ist die Ansicht read-only.

**Hot:** `grant`, `revoke`, `file-root`, `outbound-allow`,
`log-level`, `log-component-level`, `log-format`, `log-max-size`,
`log-max-files`, `dry-run`, `dev`, `cors-allow`, `listen` (Rebind),
`tls-cert`, `tls-key`, `mode`, `shutdown-timeout`.

**Nur einschränken:** Rechte und sicherheitsrelevante Einstellungen
(`grant`, `revoke`, `file-root`, `outbound-allow`, `mode`, `cors-allow`,
`listen`, `tls-cert`/`tls-key`, `dev`) lassen sich über die TUI bzw. die
API nur einschränken, gemessen am aktuell wirksamen Stand. Ein Dokument,
das sie erweitert, wird ganz abgelehnt und die Antwort nennt die
betroffenen Schlüssel. Erweitern geht nur lokal in der
Konfigurationsdatei ([Code-ADR-0021, Nachtrag 2026-10-24](adr/code/0021-on-the-fly-konfiguration.md#nachtrag-2026-10-24-die-api-erweitert-keine-rechte)).

**Nur beim Start:** `log-file` (Pfad und Ein/Aus) gilt nach demselben
Nachtrag nur nach einem Neustart, auch bei einer Änderung in der Datei.
Sonst könnte ein Admin-Token Log-Zeilen in beliebige Dateien schreiben
lassen.

**Restart:** `data-dir`, `profile`, `config` (offene Dateien und
Datenbanken des laufenden Prozesses), `overlay-listen` (Overlay-Server,
s. u.), `audio-sink`, `tts-provider`, `tts-piper-model` (Sound und TTS,
s. u.), `obs-host`, `obs-port`, `obs-password` (OBS-Verbindung, s. u.).

**Beispiele:**

```yaml
# Log-Ebene zur Fehlersuche anheben, ohne Neustart:
log_level: debug

# dry run, um Änderungen zu prüfen, bevor sie real laufen:
dry_run: true

# Zertifikate tauschen, ohne die laufenden Streams zu unterbrechen:
tls_cert: /etc/streamcrew/tls/cert.pem
tls_key: /etc/streamcrew/tls/key.pem
```

Ein ungültiges Dokument (unbekannter Key, ungültiger Wert) wird
abgelehnt, ohne die laufende Konfiguration zu verändern.

## Overlay (OBS-Browserquellen)

Der Core hostet einen eigenen HTTP-Server für OBS-Browserquellen
(Roadmap 7.1, ADR-0019): Jede Browserquelle lädt genau eine URL, die die
eingebettete Overlay-Runtime ausliefert — HTML, JavaScript und die
Medienauslieferung über einen einzigen Server, kein zweites Tool.

**OBS-Einrichtung:** In OBS eine Browserquelle anlegen mit der URL

```
http://127.0.0.1:8742/overlay/default
```

(Auffrischrate 30 ist gut, der Hintergrund der Quelle bleibt
transparent.) Der Endpunkt `default` existiert immer; weitere benannte
Endpunkte mit eigener URL stehen in der Profil-Sektion `overlay`.

**Port und Bindung:** Der Default ist `127.0.0.1:8742` (Daemon- und
Desktop-Modus) bzw. `:8742` (Server-Modus, alle Interfaces — z. B. für
OBS auf einem anderen Rechner per Reverse-Proxy). `--overlay-listen`
bzw. `overlay_listen` in der Konfigurationsdatei ändert die Adresse;
die Änderung gilt erst nach einem **Neustart**, und die Browserquellen
müssen nach einem Portwechsel neu geladen werden. Ein Unix-Socket ist
keine Option (`--overlay-listen` lehnt es ab).

**Token:** Die Endpunkte nutzen die API-Tokens des Profils (ADR-0013
§5, ADR-0019 §1). Für OBS legst du einen Token mit dem Scope
`overlay` an:

```
streamcrew token create overlay --scope overlay
```

Seite, WebSocket und Medien-URLs verlangen ihn als Query-Parameter:

```
http://127.0.0.1:8742/overlay/default?token=<wert>
```

Der Scope `overlay` öffnet nur den Overlay-Server — nie die API
(sonst 403). Nur ein Token, das den Scope `overlay` trägt, öffnet die
Endpunkte; ein Token mit nur `read` oder `admin` nicht, damit kein
starker Token in einer OBS-URL landen muss. Ohne Token sind die
Endpunkte nur im Desktop- und Daemon-Modus bei reinem Loopback-Bind
und einem `Host`-Header, der einen Loopback-Namen nennt, erreichbar —
eine fremde Website, deren Name auf `127.0.0.1` zeigt (DNS-Rebinding),
behält ihren eigenen `Host` und bekommt 401. Im Server-Modus, bei
einem ungültigen oder widerrufenen Token antworten die Endpunkte
ebenfalls mit 401. Ein Widerruf wirkt sofort: offene
WebSocket-Verbindungen eines widerrufenen Tokens werden beim nächsten
Senden geschlossen. Der Token-Wert wird nur einmal angezeigt, steht
nicht in `config show` und nicht in den Logs.

**Medien:** Dateien aus `<data-dir>/profiles/<profil-id>/media/`
dient der Server unter `http://127.0.0.1:8742/media/media/<datei>` aus,
freigegebene `file-root`-Verzeichnisse zusätzlich unter
`/media/<wurzel>/<pfad>` — mit Range-Requests (Video/Audio-Seeking)
und korrekten MIME-Typen. Die Medienlieferung braucht das Recht
`host:fs`; im Server-Modus ohne `grant` antworten die Medien-URLs
deshalb mit 404. Pfade außerhalb der Wurzeln antworten ebenfalls 404
(nie 403).

**Items (Roadmap 7.2):** Eine Aktion des Typs `overlay` mit dem Kind
`item` zeigt ein Item auf einem Endpunkt. Es gibt die Kinds `text`,
`image`, `video`, `audio`, `html`, `youtube`, `clip` und `timer`;
Position (simple/percent/pixels/random), Ebene und Ein-/Ausblend-
Animation je Item, eine Anzeigedauer (`duration`; leer = das Item
verschwindet nach 10 s, Ton- und Video-Items laufen dagegen bis zum
Wiedergabe-Ende, und ein Clip-Item nach einem Nutzer zeigt die Dauer
des Clips) und die Item-Inhalte selbst. Ein Clip-Item nennt eine URL
oder einen Nutzer — bei einem Nutzer zeigt das Item den neuesten Clip
des Kanals aus der letzten Woche (Helix listet Clips nach Aufrufzahlen;
ohne Clip im Fenster ist das ein Fehler). Ton- und Video-Items melden
ihr Wiedergabe-Ende der Engine: In
Begrüßungen halten sie damit den Medien-Abstand
([`command-engine.md`](spec/command-engine.md), B43) bis zum Ende des
Items. Die Aktion ist statisch `visual_audio` — auch ein reines
Text-Item wartet in einer Begrüßung auf seine Reihe (Abweichung von
[overlays.md](spec/overlays.md) B39, die den Status nur für Ton und
Video vorsieht).

**Widgets (Roadmap 7.2):** Widgets stehen als Definitionen in der
Profil-Sektion `overlay` (je Widget `id`, `kind`, Position, Sichtbarkeit
und eine Konfiguration) und werden von einem eigenen Dienst bedient. Es
gibt die fünf Kinds `label`, `goal`, `timer`, `event_list` und `chat`:

- `label` zeigt Text (Template) oder Werte: `viewer_count`,
  `chatter_count`, `total_followers`, `total_subscribers`,
  `latest_follower`, `latest_subscriber`, `latest_raid`, sowie
  `date` und `time` (die Runtime rendert diese clientseitig pro Sekunde
  in Locale und Zeitzone des Profils). Der Text wird bei Veränderung
  neu gesendet; Werte, die die Plattform nicht meldet, haben keinen
  Text. `total_followers` und `total_subscribers` stehen bis Phase 9
  die Zähler der Stream-Sitzung.
- `goal` zeigt den Fortschritt mit Segmenten (`cumulative` oder
  `individual`) und einem optionalen Automatik-Reset (täglich oder
  wochenmonatlich, in der Zeitzone des Profils). Follow, Abo, Raid und
  Bits wachsen das Ziel; der Stand bleibt im Profil erhalten. Ohne
  erreichtes Segment veröffentlicht der Core `overlay.goal.progress`,
  bei einem erreichten Segment `overlay.goal.segment_completed` —
  beides als Bus-Ereignis, auf das Ereignis-Commands reagieren. Negativ
  addierte Beträge klemmen den Fortschritt bei null.
- `timer` zählt eine Dauer (Format wie bei den Timer-Items) runter oder
  hoch; die Aktionen `add_to_timer` (negativ erlaubt) und
  `pause_timer` (pausiert bzw. setzt fort; ohne laufenden State startet
  der Timer) steuern ihn live.
- `event_list` zeigt die letzten Feed-Ereignisse (Follow, Abo, Resub,
  Geschenke, Raids, Cheers — filterbar) plus dynamische Einträge, die
  die Aktion `add_to_event_list` hinzufügt (Nutzer der Ausführung und
  Text).
- `chat` zeigt die Chat-Nachrichten der gewählten Plattformen, mit
  optionaler Verzögerung und Ablaufzeit. Gelöschte Nachrichten
  verschwinden über die Plattform-Nachrichten-ID; bei Sperre oder Bann
  eines Nutzers verschwinden seine Nachrichten.

Die Aktionen des Typs `overlay` mit dem Kind `widget` wirken auf diese
Widgets: `show`/`hide` (Sichtbarkeit), `update` (Zustandsdaten; beim
`timer` eine neue Dauer oder ein Reset), `add_to_goal` (Betrag, negativ
erlaubt), `add_to_timer`, `pause_timer` und `add_to_event_list` — und
auf die 7.5-Widgets: `add_to_credits` (Sektion in `data`, Eintrag als
`text`), `play_credits`, `spin_wheel` (der User der Ausführung kommt
mit dem Ereignis) und `damage_stream_boss` (Betrag, negativ erlaubt;
`data.force` übergeht den Self-Heal). Die 7.5-Widgets sind unter
[Weitere Widgets (Roadmap 7.5)](#weitere-widgets-roadmap-75)
dokumentiert.

**Ausfall:** Der Overlay-Server ist nicht-kritisch — sein Ausfall
beendet den Core nicht, die API bleibt erreichbar.

## Sound und Sprachsynthese (Roadmap 7.3)

Sound und Text-to-Speech (TTS) sind Command-Aktionen der Typen `sound`
und `text_to_speech` (spec [overlays.md](spec/overlays.md) B40, B41,
ADR-0020). Beide sind `visual_audio` und halten in Begrüßungen den
Medien-Abstand der Engine ([`command-engine.md`](spec/command-engine.md)
B43).

**Sound (B40):** Die Aktion `sound` spielt eine Datei
(`file`) aus `<data-dir>/profiles/<profil-id>/media/` mit der Lautstärke
`volume` (0–100, Default 100) auf dem Sink `sink` (Default:
`audio-sink`):

- `overlay` (Default): zeigt die Datei als Ton-Item auf dem
  Default-Endpunkt — die Browserquelle spielt sie ab, das Core braucht
  kein Recht.
- `local`: spielt auf dem Audiogerät des Hosts — der Build braucht das
  Tag `localaudio` (die Release-Binaries `streamcrew-desktop` haben es)
  und das Core das Recht `host:audio` (Desktop- und Daemon-Modus haben
  es, Server-Modus nur per `grant`). Ohne Tag oder Recht ist die
  Ausführung ein klarer Fehler (B40). Die Wahl des Geräts ist nicht
  möglich, es gilt das System-Defaultgerät (ADR-0020, offener Punkt).
- `agent`: vorbereitet für den Agent-Passthrough (Phase 9.6), ein
  klarer Fehler bis dahin.

Unterstützt sind 16-Bit-PCM-WAV-Dateien; andere Formate (MP3, 24-Bit,
float32) lehnt die Aktion mit klarem Fehler ab (Shortcut, ADR-0020,
offener Punkt). `waitForFinish` hält die Aktion bis zum
Wiedergabe-Ende und meldet es der Engine; ohne sie endet die Aktion
beim Start der Wiedergabe, und die Engine zählt den Medien-Abstand
vom Ende der Aktion.

**TTS (B41):** Die Aktion `text_to_speech` spricht einen Text
(`text`, Template) mit dem Provider `provider` (Default:
`tts-provider`):

- `browser` (Default): die Web Speech API der Overlay-Runtime spricht
  den Text in der Browserquelle — ohne Key und ohne Recht, nur mit dem
  Sink `overlay` (ein anderer Sink ist ein klarer Fehler).
- `google`: REST-API, Key in der Umgebungsvariablen
  `STREAMCREW_TTS_GOOGLE_KEY`, Recht `net:outbound`.
- `azure`: REST-API, Key in `STREAMCREW_TTS_AZURE_KEY`, Region in
  `STREAMCREW_TTS_AZURE_REGION`, Recht `net:outbound`.
- `piper`: externer Prozess `piper`, Modell in `tts-piper-model`, Recht
  `host:process`. Die Tonhöhe (`pitch`) ignoriert piper (Shortcut,
  ADR-0020, offener Punkt).

`voice`, `volume` (0–100), `rate` und `pitch` (0 oder 1 = normal)
werden an den Provider durchgereicht. Die Cloud-Keys liest das Core zum
Aufruf aus der Umgebung (ein gesetzter Key gilt ohne Neustart) und
niemals in Logs; ohne gesetzten Key ist die Ausführung ein klarer
Fehler (B54). Die erzeugten Audiodateien landen unter
`<media-dir>/generated/` und werden wie Sound-Dateien abgespielt
(`sink` wie bei `sound`). `waitForFinish` ist standardmäßig aus.

**Release:** Das Release enthält neben dem Core-Binary eine
Desktop-Variante `streamcrew-desktop` mit dem Tag `localaudio`
(linux, windows, darwin; amd64, arm64) — die Variante für den
Streaming-PC mit lokaler Tonwiedergabe. Die Wiedergabe selbst prüft die
CI nicht (kein Audiogerät auf den Runnern).

## OBS Studio (Roadmap 7.4)

Der Core verbindet sich mit OBS Studio über dessen obs-websocket-v5-
Schnittstelle (eigener Client auf `coder/websocket`, Code-ADR-0015;
[overlays.md](spec/overlays.md) B42–B45):
Er liest den Zustand und die Ereignisse der Verbindung und führt die
Operationen der `streaming_software`-Aktion darauf aus.

**Verbindung (B43):** Die Verbindung läuft ab Core-Start mit
`obs-host` (Default `127.0.0.1`), `obs-port` (Default `4455`) und
`obs-password` (leer = ohne Passwort) — alle drei gelten **nur zum
Start** (⟳, Neustart bei Änderung). Ist beim Start kein OBS
erreichbar oder das Passwort falsch, läuft der Core ohne OBS-Verbindung
weiter: eine info-Logzeile, kein Retry (B43). Eine danach abgerissene
Verbindung verbindet sich alle 5 s neu; ein Zustand, der sich seit der
letzten Verbindung nicht geändert hat, publiziert beim Reconnect
nichts. Im Server-Modus hütet der Netguard die Verbindung wie alle
ausgehenden Verbindungen — `--outbound-allow` öffnet das `host:port`
gezielt.

**Ereignisse (B44):** Diese OBS-Ereignisse laufen als Bus-Ereignisse,
unabhängig davon, wer die Änderung herbeiführt; startende und stoppende
Zwischenzustände werden ignoriert, jede Änderung publiziert einmal:

- `obs.stream.started` / `obs.stream.stopped`
- `obs.recording.started` / `obs.recording.stopped` (Dateipfad)
- `obs.replay.saved` (Dateipfad)
- `obs.scene.changed` (aktuelle und vorherige Szene)
- `obs.transition.started` / `obs.transition.ended` (Übergangsname)
- `obs.source.visibility_changed` (Szene, Quelle, sichtbar)
- `obs.filter.visibility_changed` (Quelle, Filter, sichtbar)

**StreamingSoftware-Action (B42):** Der Typ `streaming_software`
steuert die OBS-Verbindung; ohne aktive Verbindung ist jede Ausführung
ein klarer Fehler des Aktions-Schritts (die Fehlerpolitik des Commands
greift), kein Stillstand (B55):

| Kind | Mitglieder | Wirkung |
|---|---|---|
| `set_scene` | `scene` | Setzt die aktuelle Programm-Szene. |
| `start_stream` / `stop_stream` | — | Startet bzw. stoppt den Stream-Output. |
| `start_recording` / `stop_recording` | — | Startet bzw. stoppt den Aufnahme-Output. |
| `save_replay` | — | Speichert den Replay-Buffer; der Pfad kommt mit `obs.replay.saved`. |
| `set_source` | `source` plus mindestens eines von `visible`, `x`, `y` | Sichtbarkeit und/oder Position einer Quelle der aktuellen Szene (Rest des Transform bleibt, wie er ist). |
| `set_filter` | `input`, `filter`, `show` | Sichtbarkeit eines Filters einer Quelle. |

## Weitere Widgets (Roadmap 7.5)

Neun weitere Widget-Kinds (spec [overlays.md](spec/overlays.md)
B29–B37) im selben Dienst wie die 7.2-Widgets — Definition in der
Profil-Sektion `overlay`, Zustand zum Teil im Profil persistiert
(`overlay_widget_state`):

- `persistent_timer` (B29): zählt permanent ab und bleibt im Profil
  (Stream-a-thons). Startdauer `initial`, Anzeigeformat `format`
  (B17). Zeit ändert sich über `add_to_timer` (negativ erlaubt) und
  Ereignis-Beträge `event_amounts` (Sekunden pro Ereignistyp, z. B.
  `{"channel.follow": 60}`; multiplikative Beträge ab Phase 9).
  Ausgeblendet wird der Timer eingefroren und setzt sichtbar fort;
  `reset_on_enable` setzt ihn beim erneuten Einblenden auf den
  Anfangswert, `disable_on_completion` blendet ihn bei Erreichen von 0
  aus, `allow_adjustment_while_paused` lässt die Ereignis-Beträge auch
  den eingefrorenen Timer erreichen. Der Core veröffentlicht
  `overlay.timer.adjusted` (geänderter Betrag) und
  `overlay.timer.completed`.
- `stream_boss` (B30): ein Boss mit Health (Stand im Arbeitsspeicher).
  Schaden über `damage_stream_boss` (negativ = Heilung; `force`
  übergeht den Self-Heal) oder Ereignis-Beträge `event_amounts`
  (z. B. `{"channel.follow": 10}`). Schädigt der Boss sich selbst,
  heilt er mit `self_heal_multiplier` (Default 1). Nach einem Kill
  würfelt der Core einen neuen Boss aus den letzten Feed-Nutzern; die
  neue Health ist `base_health` plus `kill_bonus_health` plus
  Overkill mit `overkill_bonus_multiplier`, und `compound` (Default
  an) bildet sie zum neuen Maximum. Ereignisse:
  `overlay.boss.damage`/`overlay.boss.heal` (Details `health`,
  `amount` — die vorzeichenbehaftete Health-Änderung) und
  `overlay.boss.new`.
- `end_credits` (B31): sammelt, solange das Widget sichtbar ist,
  `chatters`, `new_followers`, `new_subscribers`, `resubscribers`,
  `gifted_subscriptions`, `all_subscriptions`, `raids`, `twitch_bits`
  plus eigene `custom_sections`. Gesperrte und gebannte Nutzer
  verschwinden aus allen Sektionen, der Stream-Start leert sie.
  `add_to_credits` (Sektion in `data` plus `text`) ergänzt einen
  Eintrag, `play_credits` startet die Wiedergabe (die Runtime zeigt
  die Liste; eine Roll-Animation folgt).
- `leaderboard` (B32): die Top-N einer Statistik (`stat`
  `watch_time`, `messages` oder `commands`, `count` Default 10) aus
  den Nutzer-Statistiken; aktualisiert sich bei den Ereignissen, die
  die Statistik bewegen.
- `wheel` (B33): Räder mit Outcomes; `equal_probability` (Default)
  oder je Outcome `probability` (dezimal, Summe 100). Je Outcome
  `modifier` (ganze Prozentpunkte, Summe 0): trifft die Landung ein
  Outcome mit negativem Modifier, verschieben sich alle
  Wahrscheinlichkeiten um die Modifier; mit positivem werden sie
  zurückgesetzt. `spin_wheel` würfelt serverseitig, die Runtime
  dreht (4 s) und der Core veröffentlicht `overlay.wheel.spun`
  (Outcome, User) — das Outcome-Command läuft als Ereignis-Command
  auf diesem Bus-Ereignis.
- `poll` (B34): zeigt die aktive Twitch-Umfrage oder -Vorhersage
  live. Der Core erfasst die Umfragen und Vorhersagen, die die
  `twitch`-Aktion (Helix) erzeugt, und meldet ihren Status an die
  sichtbaren Widgets; ein (Re-)Connect der Runtime bekommt den Stand
  im State-Sync. Es gibt keine Bus-Events dafür — die Katalog-Typen
  `twitch.poll.*`/`twitch.prediction.*` bleiben verworfen
  ([twitch-events.md](spec/twitch-events.md) B22).
- `persistent_emote_effect` (B35): löst einen Emote-Effekt aus, wenn
  ein Emote-Code `combo_count`-mal (Default 3) innerhalb von
  `combo_timeframe` Sekunden (Default 10) im Chat kommt
  (`ignore_duplicates` zählt pro Nachricht einmal). Die Codes löst
  der Core über den Emote-Katalog auf; unbekannte fallen weg. Effekte
  `rain`, `burst`, `random`, Dauer `duration`, Anzahl pro Emote
  `per_emote_shown`, Obergrenze `max_shown` (0 = keine).
- `game_queue` (B36): eine Queue je Profil, persistiert in
  `overlay_widget_state` (fester Schlüssel). `join`/`leave`
  (Phase 8.3, Chatspiele) ändern sie und der Core veröffentlicht
  `gamequeue.joined`/`gamequeue.left`/`gamequeue.position`
  (Position ab 1); das Widget zeigt die Liste. Größe `max_size`
  (Default 10).
- `discord_voice` (B37): listet die Besetzung eines konfigurierten
  Discord-Voice-Kanals (`discord-voice-channel`, nur zum Start ⟳).
  Der Discord-Token kommt **nur** aus der Umgebungsvariablen
  `STREAMCREW_DISCORD_TOKEN` (kein Config-Key, ADR-0019 §9); ohne
  Token oder Kanal läuft der Service inaktiv (info-Log). Beim
  Beitreten und Verlassen eines Nutzers veröffentlicht der Core
  `discord.voice.joined`/`discord.voice.left` (Name, Kanal) und
  aktualisiert die sichtbaren Widgets. **Beschränkung (A3):** der
  Core tritt dem Voice-Kanal nicht bei, daher erkennt er weder
  Sprechen noch Stummschaltung — die Besetzung ist der Umfang von
  7.5.

Das Item-Kind `emote_effect` der `overlay`-Aktion (B35) zeigt eine
Liste von Emotes mit Effekt: `emotes` (Namen, mind. 1, templatebar),
`effect` (`rain`, `burst` oder `random`, Default `random`),
`width`/`height`, `perEmoteShown` (Default 1) und `maxShown`
(0 = keine Obergrenze). Die Namen löst der Core über den
Emote-Katalog auf; `allowEmoji` lässt unbekannte Namen als Text
stehen (z. B. Emoji), sonst fallen sie weg — ohne einen auflösbaren
Emote ist das Item ein klarer Fehler. Die Anzeigedauer ist die
`duration` des Items.

## Logs im Stream (read-only)

Die Log-Zeilen des Cores sind Teil des Live-Streams: Jede Zeile, die den
Level-Filter der Logging-Konfiguration durchlässt, erscheint als
`log.line`-Ereignis neben dem Terminal- und Datei-Ausgang.

**TUI:** Die Ansicht **Logs** (Taste `7`) zeigt die Log-Zeilen des
Streams. Die Taste `l` schaltet den Anzeigefilter durch
`debug → info → warn → error → debug` (Default `debug` = alles); die
Kopfzeile zeigt den aktiven Filter (z. B. „Logs ≥ warn“). Der Filter ist
rein clientseitig — er ändert nichts am Core, dessen Log-Level bleibt bei
der On-the-fly-Konfiguration.

**API:** Das Abo (`StreamService.Subscribe`) nimmt den selben Filter
serverseitig als `min_level` im `EventFilter` an: „debug“, „info“,
„warn“ oder „error“ (leer = alle). Gefiltert werden nur die Log-Events;
alle anderen Ereignistypen gehen ungefiltert durch. Auch hier gilt:
read-only, es gibt keinen RPC, der das Log-Level des Cores ändert.

```go
// Beispiel: nur Warnungen und Fehler abonnieren
stream, err := streamClient.Subscribe(ctx, connect.NewRequest(
	&v1alpha1.SubscribeRequest{
		Filter: &v1alpha1.EventFilter{MinLevel: "warn"},
	}))
```

Hinweis: Der Stream ist kein Logfile-Ersatz — er trägt nur die Events
seit dem Abo (optional mit `since` für den Puffer), und bei Überlast
wirft der Bus ab (`event.lag`). Die vollständige Historie liegt in der
Logdatei (`log-file`).

**Größen der Stream-Pfade (Startzeit):** Zwei Puffergrößen lassen sich
über die üblichen Kanäle festlegen — Flag, Umgebungsvariable oder
Konfigurationsdatei (`--stream-buffer`, `--stream-catchup`,
`STREAMCREW_STREAM_BUFFER`, `STREAMCREW_STREAM_CATCHUP`). Beide gelten
**nur zum Start** des Cores (⟳, Neustart bei Änderung; kein Hot-Apply,
kein API- oder TUI-Pfad):

| Schlüssel | Default | Bedeutung |
|---|---|---|
| `stream-buffer` | `1024` | Wie viele Events der Stream-Hub dem Event-Bus gegenüber schulden darf, bevor der Bus für ihn wegwirft (`event.lag`). |
| `stream-catchup` | `256` | Wie viele der letzten Events ein neu abonnierender Client per `since` nachholen kann. |

**Chat-Adapter der Twitch-Plattform (Startzeit):** `twitch-eventsub`
und `twitch-irc` (`--twitch-eventsub`/`--twitch-irc`,
`STREAMCREW_TWITCH_EVENTSUB`/`STREAMCREW_TWITCH_IRC`) schalten die
zwei Adapter an oder aus; Details oben. Beide gelten **nur zum Start**
(⟳, Neustart bei Änderung; kein Hot-Apply, kein API- oder TUI-Pfad).

| Schlüssel | Default | Bedeutung |
|---|---|---|
| `twitch_eventsub` | `on` | EventSub-Adapter: Heimkanal und, ohne IRC-Adapter, die zusätzlichen Kanäle. |
| `twitch_irc` | `off` | IRC-Adapter: zusätzliche Kanäle und, ohne EventSub-Adapter, der Heimkanal. |

## Sicherheitshinweise

1. **Loopback ist der Standard.** Der Daemon-Modus lauscht nur auf
   `127.0.0.1:8740`. Wer die API von außen erreichen will, nimmt den
   Server-Modus mit TLS und Token — nicht `--listen` auf einer
   öffentlichen Adresse im Daemon-Modus.
2. **Token ist Macht.** Ein API-Token mit Scope `admin` kann Commands
   ändern und den Core steuern. Token erzeugen mit kleinstem Scope
   (`token create --scope …`), nie in Shell-Historie, CI-Logs oder
   Backups im Klartext; `diag bundle` enthält bewusst nur Namen und
   Scopes, nie Werte. Im Zweifel `token revoke`.
3. **Secret-Key schützen.** `STREAMCREW_SECRET_KEY` (oder der
   Keyring-Schlüssel) entschlüsselt die Twitch-Tokens aller Profile im
   Datenverzeichnis. Wer Key **und** Datenverzeichnis hat, hat die
   Twitch-Konten. Deshalb: Datenverzeichnis mit `0700`, Key in
   `EnvironmentFile` mit `0600`, `secret rotate` nach Verdacht auf
   Offenlegung.
4. **Server-Modus minimiert Host-Rechte.** Im Server-Modus sind
   `host:fs`, `host:process` und `host:audio` abgeschaltet
   (Code-ADR-0019); Web-Requests erreichen keine internen Netze, es sei
   denn `--outbound-allow` öffnet sie gezielt. Im Daemon-Modus bewusst
   `--grant host:fs --file-root …` nur für Verzeichnisse freigeben, die
   die Datei-Action lesen darf.
5. **Keine Telemetrie.** Der Core sendet nur an die Dienste, die er
   betreibt (Twitch, Emote-Kataloge), und an nichts anderes
   (ADR-0011). Ausgehende Ziele im Server-Modus lassen sich mit
   `--outbound-allow` und der Doku der Rechte nachvollziehen.
6. **Logs maskieren Secrets.** Passwörter, Tokens und App-Secrets
   erscheinen nicht in den Logs (Code-ADR-0003); `diag bundle` packt
   Logs, Konfiguration ohne Secrets und Pfade für einen Supportfall.
7. **Backups sind sensitiv.** Ein Profil-Backup enthält die
   verschlüsselten Twitch-Tokens. Backups wie Secret-Key schützen
   (verschlüsselter Speicher, keine Synchronisationsdienste im Klartext).
