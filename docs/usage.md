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
Datenbanken des laufenden Prozesses), `overlay-listen`, `overlay-token`
(Overlay-Server, s. u.).

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

**Token:** Leer gelassen sind die Endpunkte offen — nur korrekt für
`127.0.0.1`. Wird der Server auf ein anderes Interface gebunden,
sollte `--overlay-token` (bzw. `overlay_token`, oder die
Umgebungsvariable `STREAMCREW_OVERLAY_TOKEN`) einen Wert tragen; dann
verlangen Seite, WebSocket und Medien-URLs den gleichen Wert als
Query-Parameter:

```
http://127.0.0.1:8742/overlay/default?token=<wert>
```

Der Token ist ein Credential: er steht nicht in der Ausgabe von
`streamcrew config show` und nicht in den Logs. Auch er gilt erst nach
einem **Neustart**.

**Medien:** Dateien aus `<data-dir>/profiles/<profil-id>/media/`
dient der Server unter `http://127.0.0.1:8742/media/media/<datei>` aus,
freigegebene `file-root`-Verzeichnisse zusätzlich unter
`/media/<wurzel>/<pfad>` — mit Range-Requests (Video/Audio-Seeking)
und korrekten MIME-Typen. Die Medienlieferung braucht das Recht
`host:fs`; im Server-Modus ohne `grant` antworten die Medien-URLs
deshalb mit 404. Pfade außerhalb der Wurzeln antworten ebenfalls 404
(nie 403).

**Ausfall:** Der Overlay-Server ist nicht-kritisch — sein Ausfall
beendet den Core nicht, die API bleibt erreichbar. Items und Widgets
dienen ab Roadmap 7.2 über Commands.

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
