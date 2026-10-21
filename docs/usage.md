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
streamcrew profile channels list           # Kanäle und aktiver Adapter
streamcrew profile channels chat irc       # Chat-Adapter wechseln (Core gestoppt)
```

Zwei Chat-Adapter betreiben die zusätzlichen Kanäle — der Home-Kanal
läuft immer über EventSub und ist vom Adapter unbetroffen:

| Adapter | Mod-Rechte des Bots im Kanal | Anmerkung |
|---|---|---|
| `eventsub` (Standard) | **erforderlich** — ohne Mod-Status wird der Kanal nicht betrieben | eigene EventSub-Verbindung und Subscriptions pro Kanal |
| `irc` | keine Voraussetzung (ohne Mod wird `mod_missing` gewarnt, der Kanal läuft trotzdem) | der Chat der zusätzlichen Kanäle läuft über den Twitch-IRC-Chat des Bot-Kontos; zusätzlich Whispers an den Bot; Mod-Status aus den Chat-Badges |

Der Adapter wird nur beim Core-Start gelesen — `profile channels chat`
verlangt einen gestoppten Core und greift beim nächsten Start. Im
IRC-Modus sendet der Bot in den zusätzlichen Kanälen als Bot-Konto;
ist der Chat nur für Follower/Abonnenten offen und der Bot nicht
entsprechend berechtigt, weist der Twitch-Server die Sendung ab (NOTICE
in den Logs). Der IRC-Modus braucht das Bot-Konto; ohne Bot-Konto
bleiben die zusätzlichen Kanäle unbetreut.

Hinweis: Der Bot-Login verlangt mit dem IRC-Adapter zusätzlich den
Scope `chat:write:bot`; ein älterer Bot-Login zeigt das in
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

**Access Control:** Die API verlangt einen Bearer-Token
(`streamcrew token create --scope <scope>` erzeugt ihn (Scopes: read, control, admin, overlay; Default admin); der Wert wird genau einmal gezeigt). Jeder Client setzt ihn:

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

Das Release enthält ein mehrplatformiges Image (linux/amd64,
linux/arm64). Im Container läuft der Core standardmäßig im Server-Modus
mit Daten in `/data` (Volume) und JSON-Logs auf stderr:

```bash
docker run -d --name streamcrew -v streamcrew-data:/data \
  -e STREAMCREW_TOKEN=… -e STREAMCREW_SECRET_KEY=… \
  ghcr.io/ripmav/streamcrew:v1.0.0
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
`log-level`, `log-component-level`, `log-format`, `log-file`,
`log-max-size`, `log-max-files`, `dry-run`, `dev`, `cors-allow`,
`listen` (Rebind), `tls-cert`, `tls-key`, `mode`, `shutdown-timeout`.

**Restart:** `data-dir`, `profile`, `config` (offene Dateien und
Datenbanken des laufenden Prozesses).

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
