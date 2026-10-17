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

Zusätzliche Kanäle, in denen der Bot wirken soll (Bot braucht dort
Moderator-Rechte):

```bash
streamcrew profile channels add <login>   # Core gestoppt
```

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
