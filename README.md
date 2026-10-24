# streamcrew

[![CI](https://github.com/ripmav/streamcrew/actions/workflows/ci.yml/badge.svg)](https://github.com/ripmav/streamcrew/actions/workflows/ci.yml)

streamcrew ist ein Bot- und Automatisierungsservice für Livestreams: Chat-Commands, Ereignis-Automatisierung, Overlays, Wirtschaft und Spiele. Es ist eine Clean-Room-Umsetzung des Verhaltens von *Mix It Up* in Go — es wurde kein Quellcode des Originals gelesen; Grundlage sind seine öffentliche Dokumentation und beobachtetes Verhalten ([ADR-0001](docs/adr/0001-neuimplementierung-und-nutzung-des-originals.md)).

## Status

Meilenstein M1 ist erreicht (Phasen 1–5 abgeschlossen): Der Core führt Commands aus, und Commands aus YAML/JSON reagieren auf simulierte Chat-Nachrichten und Events — und auf echte Twitch-Ereignisse (Phase 4: Authentifizierung, Helix-Client, EventSub, Chat, Moderation, Titel/Kategorie, Channel Points, Bits, Emote-Katalog, zusätzliche Kanäle, Pro-Channel-Konfiguration).

Phase 6 (Agent-Plattform) ist fast vollständig umgesetzt, M2 (Headless-MVP) ist erreicht:

- **Event-Stream** (6.1): Der Core streamt Ereignisse (Chat, Channel, Stream, App, Log) als Server-Streaming-ConnectRPC-Call an die Frontends, mit Catch-up für nachverbindende Clients.
- **Strömungsgrößen** (6.2): Die Puffer- und Catch-up-Größen sind konfigurierbar (`--stream-buffer`, `--stream-catch-up`); überschuldete Abonnenten werden fair abgeworfen.
- **TUI** (6.3/6.4): `streamcrew tui` zeigt Dashboard, Chat, Events, Warteschlange, Commands, Nutzer und Logs live aus dem Event-Stream — lokal oder gegen einen entfernten Core (`--server`).
- **On-the-fly-Konfiguration** (6.5): Einstellungen wie `listen` und `log_level` wirken im laufenden Core ohne Neustart, per Konfigurationsdatei (Watcher) oder über die API (`ConfigService`, TUI-Settings-View).
- **Log-Stream** (6.6): Log-Zeilen sind Ereignisse; Clients abonnieren sie gefiltert nach Level, Plattform, Channel und Ereignisklasse über den Event-Stream.
- **Release MVP** (6.7): Auslieferung und Nutzerdokumentation sind offen.

Die vollständige Planung mit Abnahmekriterien steht in der [Roadmap](docs/roadmap.md).

## Dokumentation

- [Spezifikationen](docs/spec/): das Soll der Features — was streamcrew tun soll, bevor es umgesetzt ist.
- [ADRs](docs/adr/): Architekturentscheidungen, mit Kontext, Alternativen und Folgen.
- [Code-ADRs](docs/adr/code/): Entscheidungen auf Code- und Modul-Ebene (z. B. Typ-Registry, Event-Bus, HTTP-Client).
- [Roadmap](docs/roadmap.md): Meilensteine, Phasen und Abnahmekriterien.
- [Tests](docs/tests/): Test-Matrizen je Issue.

## Architektur

```
┌──────────────┐  ┌────────────────┐  ┌──────────────┐
│  CLI / TUI   │  │  Desktop-App   │  │  Web-UI      │
└──────┬───────┘  └───────┬────────┘  └──────┬───────┘
       │                  │                  │
       └──────────────────┼──────────────────┘
                          │  API (ConnectRPC, ADR-0010)
                 ┌────────┴────────┐
                 │   Headless Core │  ein Binary, läuft auf dem
                 └────────┬────────┘  Streaming-PC, optional
                          │           als Server-App (ADR-0003)
           ┌──────────────┼──────────────┐
           │              │              │
     ┌─────┴─────┐  ┌─────┴─────┐  ┌─────┴─────┐
     │  Twitch   │  │ (weitere  │  │  Mock     │
     │ (Phase 4) │  │ Plattformen│ │ (Tests,   │
     └───────────┘  └───────────┘  │  Demo)    │
                                   └───────────┘
```

Der **Headless Core** ist ein einzelnes Binary, das auf dem Streaming-PC läuft (kein Daemon, kein Root) und die gesamte Logik trägt: Events aus den Plattformen, das Command-Engine, die Actions, Persistenz und die API. Alle Frontends (CLI/TUI, Desktop-App, Web-UI) kommunizieren **ausschließlich** über die API mit dem Core — nie direkt mit Dateien oder der Datenbank ([ADR-0010](docs/adr/0010-api-protokoll.md)). Der Desktop-Modus lauscht dabei primär auf dem Unix-Domain-Socket `<data-dir>/core.sock` und hinterlegt Adresse, PID und Version in `runtime.json` sowie den API-Token für die Desktop-App in `desktop-token` (ADR-0005). Zum Launch wird nur **Twitch** unterstützt ([ADR-0004](docs/adr/0004-plattformumfang-zum-start.md)); die Architektur ist plattformoffen.

## Voraussetzungen

- **Go 1.27** zum Bauen der Entwicklungsversion.
- Für die Twitch-Anbindung: ein Twitch-Konto (Streamer), optional ein Bot-Konto, und eine Twitch-App (eingebaut oder eigene, [ADR-0024](docs/adr/0024-eingebaute-twitch-app.md)).

## Loslegen

```bash
# aus dem Quellverzeichnis
go build ./cmd/streamcrew   # -> ./streamcrew

./streamcrew doctor              # Umgebung prüfen
./streamcrew profile list        # Profile
./streamcrew profile use <id>    # aktives Profil
./streamcrew profile channels list
./streamcrew auth login twitch   # Streamer-Konto anmelden
./streamcrew auth status
./streamcrew serve --profile <id>
```

`serve` startet den Core bis SIGINT/SIGTERM, meldet sich über `/healthz` und `/readyz` und beendet sich sauber. Der Core legt beim ersten Start sein Datenverzeichnis an (siehe unten); `config.yaml` wird nicht automatisch erzeugt — eine leere Datei ist ein gültiger Startzustand.

## Betriebsmodi

`--mode` entscheidet über die Standard-Listenadresse und den Basissatz der Host-Rechte; die Capability-Richtlinie wird in der Codebasis zentral gepflegt, nicht pro Deployment konfiguriert ([ADR-0013](docs/adr/0013-sicherheitsmodell.md), [Code-ADR-0019](docs/adr/code/0019-host-rechte-in-der-startkonfiguration.md)).

| Modus | Standard-Listenadresse |
|---|---|
| `desktop` (Standard) | Unix-Socket `<data-dir>/core.sock` |
| `daemon` | `127.0.0.1:8740` |
| `server` | `:8740` (alle Interfaces) |

Capabilities je Modus (Standard, ohne `--grant`/`--revoke`):

| Capability | Bedeutung | `desktop`/`daemon` | `server` |
|---|---|---|---|
| `host:fs` | Dateien lesen/schreiben, nur unter freigegebenen Wurzeln | an | aus |
| `host:process` | Programme starten (externe Programme, Python) | an | aus |
| `host:input` | Tastatur, Maus, Hotkeys (über den Agent) | aus | aus |
| `host:audio` | Tonausgabe auf dem Rechner | an | aus |
| `net:outbound` | Anfragen an andere Rechner (WebRequest-Action) | an | an, mit SSRF-Schutz |
| `script` | JavaScript in einer Sandbox | an | an |

`--grant` und `--revoke` schalten Einzelrechte an/aus (wirksame Menge = Standard des Modus + `grant` − `revoke`), `--file-root NAME=DIR` öffnet zusätzliche Wurzeln für die File-Action, `--outbound-allow` öffnet in Server-Modus einzelne interne Ziele für Web-Anfragen (SSRF-Schutz, ADR-0013 Punkt 4).

## Datenverzeichnis

Standard: `~/.config/streamcrew` (Linux/macOS), `%AppData%\streamcrew` (Windows), über `--data-dir` setzbar. Layout:

```
<data-dir>/
├── config.yaml          # Konfigurationsdatei
├── secret.key           # Schlüsselsatz (ohne System-Keyring; Priorität: Umgebungsvariable > System-Keyring > Datei)
├── core.sock              # Unix-Socket (Desktop-Modus)
├── runtime.json           # Adresse, PID, Version des laufenden Cores
├── desktop-token          # API-Token der Desktop-App (Desktop-Modus, ADR-0005)
├── logs/streamcrew.log  # JSON-Logdatei mit Rotation
├── chat/<plattform>.jsonl    # optionales Chat-Protokoll
├── backups/             # Profile-Backups
└── profiles/
    ├── active           # ID des aktiven Profils
    └── <id>.db          # SQLite-Datenbank je Profil: Commands, Counter, Nutzer, Einstellungen, Accounts, Secrets
```

Die Profil-Datenbank ist eine SQLite-Datei ([Code-ADR-0008](docs/adr/code/0008-datenbankzugriff.md), [ADR-0012](docs/adr/0012-persistenz.md)); Secrets (Plattform-Tokens, App-Credentials) sind darin verschlüsselt, der Schlüssel kommt aus dem System-Keyring, der Umgebungsvariable oder `secret.key` ([ADR-0014](docs/adr/0014-oauth-und-app-credentials.md)).

## Konfiguration

Die Konfiguration besteht aus der **Konfigurationsdatei** (`config.yaml`) und den **Flags**; jedes Flag hat eine Umgebungsvariable. Flags übersteuern die Datei; `streamcrew config show` zeigt die effektive Konfiguration im Dateiformat, `streamcrew config path` die Pfade.

### Konfigurationsdatei

`config.yaml` im Datenverzeichnis (oder `--config FILE`). Fehlende Datei und leere Datei sind erlaubt. Bekannte Sektionen (jeweils auch als Flags):

```yaml
mode: daemon                # desktop, daemon, server
listen: 127.0.0.1:8740      # Adresse des HTTP-Servers
dev: false                  # pprof unter /debug/pprof/ (nur Loopback)
dry_run: false              # Plattform liest den Channel und loggt, statt zu handeln
shutdown_timeout: 15s       # Zeitlimit des kompletten Shutdowns
log_level: info             # debug, info, warn, error
log_component_level:        # Level einzelner Komponenten, z. B. supervisor: debug
  supervisor: debug
log_format: text            # text, json
log_file: true              # true: <data-dir>/logs/streamcrew.log, false: aus, Pfad: diese Datei
log_max_size: 10            # Rotationsgröße in MiB
log_max_files: 5            # Anzahl der gehaltenen Rotationsdateien
grant: []                   # zusätzlich aktivierte Capabilities
revoke: []                  # deaktivierte Capabilities
file_root: {}               # Verzeichnisse der File-Action, z. B. "scripts": "/opt/streams/scripts"
outbound_allow: []          # interne Ziele für Web-Anfragen in Server-Modus (IP, CIDR, Hostname)
cors_allow: []              # erlaubte CORS-Origin, z. B. "http://localhost:5173"
tls_cert: ""                # TLS-Zertifikat (PEM), zusammen mit tls_key
tls_key: ""                 # TLS-Privatschlüssel (PEM)
stream_buffer: 1024         # Ereignisse, die die Strömungs-Hub der Bus maximal schuldet
stream_catch_up: 256        # aktuelle Ereignisse für den Catch-up nachverbindeender Clients
twitch_eventsub: on         # EventSub-Adapter der Twitch-Plattform: an oder aus
twitch_irc: off             # IRC-Adapter der Twitch-Plattform: an oder aus; mindestens einer muss an sein
```

Änderungen der **Hot-Settings** wirken im laufenden Core, ohne Neustart: Der Watcher liest die Datei regelmäßig, wendet die Änderung an und meldet Fehlschläge im Log. Hot sind `listen` (+ `tls_cert`/`tls_key`), `cors_allow`, `dev`, `dry_run`, die `log_*`-Einstellungen (außer `log_file`, das nach dem Nachtrag 2026-10-24 nur beim Start gilt), `grant`, `revoke`, `file_root`, `outbound_allow`, `mode` und `shutdown_timeout`; `data_dir`, `profile`, `config`, `stream_buffer`, `stream_catch_up`, `twitch_eventsub` und `twitch_irc` brauchen einen Neustart. Dieselben Einstellungen lassen sich über die API setzen (`ConfigService`) — die TUI-Settings-View nutzt das ([Code-ADR-0021](docs/adr/code/0021-on-the-fly-konfiguration.md)). Rechte und sicherheitsrelevante Einstellungen (`grant`, `revoke`, `file_root`, `outbound_allow`, `mode`, `cors_allow`, `listen`, TLS, `dev`) darf die API nur einschränken, erweitern geht nur lokal in der Datei ([Nachtrag 2026-10-24](docs/adr/code/0021-on-the-fly-konfiguration.md#nachtrag-2026-10-24-die-api-erweitert-keine-rechte); die Umsetzung ist offen).

### Flags und Umgebungsvariablen

Alle Flags gelten für jeden Befehl; die dazugehörige Umgebungsvariable steht in Klamern.

| Flag | Bedeutung |
|---|---|
| `--config FILE` (`$STREAMCREW_CONFIG`) | Konfigurationsdatei (YAML); ohne Flag wird `config.yaml` im Datenverzeichnis gelesen, falls vorhanden |
| `--data-dir DIR` (`$STREAMCREW_DATA_DIR`) | Datenverzeichnis; Standard `~/.config/streamcrew` |
| `--profile ID` (`$STREAMCREW_PROFILE`) | Profil; Standard: das aktive Profil |
| `--mode MODE` (`$STREAMCREW_MODE`) | Betriebsmodus: `desktop` (Standard), `daemon`, `server` |
| `--listen HOST:PORT` (`$STREAMCREW_LISTEN`) | HTTP-Server-Adresse; Standard `127.0.0.1:8740`, in Server-Modus `:8740` |
| `--dev` (`$STREAMCREW_DEV`) | Entwicklermodus: pprof unter `/debug/pprof/`, erfordert Loopback-Adresse |
| `--cors-allow ORIGIN,...` (`$STREAMCREW_CORS_ALLOW`) | erlaubte CORS-Origin, wiederholbar; Standard: cross-origin abgelehnt |
| `--tls-cert FILE` / `--tls-key FILE` (`$STREAMCREW_TLS_CERT`/`$STREAMCREW_TLS_KEY`) | TLS des HTTP-Servers (PEM); Standard: plain HTTP |
| `--dry-run` (`$STREAMCREW_DRY_RUN`) | Plattform liest den Channel und loggt, statt zu handeln |
| `--shutdown-timeout DUR` (`$STREAMCREW_SHUTDOWN_TIMEOUT`) | Zeitlimit des Shutdowns, Standard `15s` |
| `--log-level LEVEL` (`$STREAMCREW_LOG_LEVEL`) | `debug`, `info`, `warn`, `error` |
| `--log-component-level COMP=LEVEL` (`$STREAMCREW_LOG_COMPONENT_LEVEL`) | Level einer Komponente, wiederholbar |
| `--log-format FMT` (`$STREAMCREW_LOG_FORMAT`) | Konsolenformat: `text`, `json` |
| `--log-file PATH` (`$STREAMCREW_LOG_FILE`) | `true`: `<data-dir>/logs/streamcrew.log`, `false`: aus, Pfad: diese Datei (JSON, mit Rotation) |
| `--log-max-size MIB` (`$STREAMCREW_LOG_MAX_SIZE`) | Rotationsgröße, Standard `10` |
| `--log-max-files N` (`$STREAMCREW_LOG_MAX_FILES`) | Rotationsdateien halten, Standard `5` |
| `--stream-buffer N` (`$STREAMCREW_STREAM_BUFFER`) | Puffer der Strömungs-Hub, Standard `1024` |
| `--stream-catch-up N` (`$STREAMCREW_STREAM_CATCH_UP`) | Catch-up-Größe, Standard `256` |
| `--twitch-eventsub on\|off` (`$STREAMCREW_TWITCH_EVENTSUB`) | EventSub-Adapter: Heimkanal und, ohne IRC-Adapter, die zusätzlichen Kanäle; Standard `on` |
| `--twitch-irc on\|off` (`$STREAMCREW_TWITCH_IRC`) | IRC-Adapter: zusätzliche Kanäle und, ohne EventSub-Adapter, der Heimkanal; Standard `off`. Mindestens ein Adapter muss an sein, sonst startet der Core nicht |
| `--grant CAP,...` (`$STREAMCREW_GRANT`) | Capability aktivieren, z. B. `host:fs`, wiederholbar |
| `--revoke CAP,...` (`$STREAMCREW_REVOKE`) | Capability deaktivieren, wiederholbar |
| `--file-root NAME=DIR` (`$STREAMCREW_FILE_ROOT`) | Verzeichnis der File-Action unter NAME, wiederholbar |
| `--outbound-allow TARGET,...` (`$STREAMCREW_OUTBOUND_ALLOW`) | ausgehende Web-Ziele in Server-Modus, wiederholbar |
| `--server URL` (`$STREAMCREW_SERVER`) | Core-Adresse für die API-Befehle, z. B. `http://127.0.0.1:8740` |
| `--token TOKEN` (`$STREAMCREW_TOKEN`) | API-Token für die API-Befehle |

`completion <shell>` erzeugt das Shell-Completion-Skript (`bash`, `zsh`, `fish`).

### Befehls-spezifische Flags (Auswahl)

| Befehl | Flags |
|---|---|
| `mock` | `-c/--commands FILE` (Commands-as-Code in die Profil-Kopie laden), `--script FILE` (Konsole aus Datei statt stdin), `--streamer LOGIN`, `--bot LOGIN` |
| `tui` | `--theme auto\|light\|dark` (`$STREAMCREW_THEME`) |
| `token create` | `--scope read,control,admin,overlay` (wiederholbar, Standard `admin`) |
| `chat send` | `--as LOGIN` (Mock-Plattform), `--channel LOGIN`, `--as-streamer` |
| `event simulate` | `--user LOGIN`, `--target LOGIN`, `--text TEXT` (für `chat.message`) |
| `user list` / `show` | `-o/--output text\|json`, `--platform PLATFORM`, `--prefix PREFIX`, `--stat STAT`, `--limit N` |
| `user set` | `--platform`, `--role ROLE`, `--title TITLE`, `--strikes N`, `--excluded` / `--no-excluded` |
| `command list` / `run` | `-o/--output`, `--kind KIND`, `--prefix PREFIX`, `--channel LOGIN` |

## Commands

### Commands as Code

Commands liegen als YAML- oder JSON-Dateien (`kind: command`) oder direkt in der Datenablage des Profils — das Profil ist die einzige Quelle der Wahrheit. Das Format speichert jede Eigenschaft der Engine (Trigger, Aktionen, Cooldowns, Zeitpläne, Sperrmodi, Abhängigkeiten) und ist so gestaltet, dass ein LLM es schreiben kann. `streamcrew command import` lädt sie in das Profil (atomar: alle Dokumente oder bei Fehlern keines), `export` schreibt sie heraus, `validate` prüft sie gegen das Profil, ohne es zu ändern ([Spezifikation](docs/spec/commands-as-code.md)).

```yaml
kind: command
name: meins
title: Mein Command
aliases: [me, m]
description: Das tut was
tags: [custom]
trigger:
  triggerType: chat
  commandNames: [meins, me]
action:
  actionType: chat
  message: "Hallo {user.name}"
schedule:
  cooldownSeconds: 10
  repeatSeconds: 0
  timeLimitSeconds: 0
lock:
  mode: self
  maxActive: 1
```

### JSON Schema

Das JSON Schema des Formats wird aus dem Code generiert — Schema und Umsetzung können nie auseinanderlaufen:

```bash
streamcrew schema export --dir /tmp/schema   # schema-commands.json + schema-events.json
```

### Ereignistypen

Events sind die Eingänge der Commands. Der fachliche Katalog (mit Plattform- und Quell-Varianten) steht in der [Spezifikation](docs/spec/events.md); die wichtigsten:

| Ereignis | Auslöser |
|---|---|
| `chat.message`, `chat.message.delete`, `chat.whisper` | Chat-Nachrichten, Löschung, Flüstern |
| `chat.user.join/leave/entrance/new/first_message/timeout/ban` | Chat-Benutzereignisse |
| `channel.stream.start` / `channel.stream.stop` | Stream-Start/Stopp |
| `channel.follow`, `channel.raid` | Follow, Raid |
| `channel.subscribe/resubscribe/subscription.gift/subscription.mass_gift` | Abos, Geschenk-Abos |
| `app.started`, `app.stopping` | Core-Lebenszyklus |
| `auth.login_completed/failed/action_required` | Login-Zustände |
| `log.line` | Log-Zeilen (für den Log-Stream) |

Die Plattform-Ereignisse der Twitch-Quelle (`twitch.bits.cheer`, `twitch.channel_points.redeem`, `twitch.hype_train.*`, `twitch.channel.update`, `twitch.goal.*`, `twitch.poll.*`, `twitch.prediction.*`, `twitch.shoutout.receive`, …) werden in diese fachlichen Typen übersetzt; der komplette Katalog mit Plattform- und Quell-Varianten steht in der [Spezifikation](docs/spec/events.md).

### Templates und Locales

`{...}`-Platzhalter in Actions werden aus dem Event-Kontext gefüllt (Begriff: *Template*). Die Template-Engine kennt Ausdrücke mit den Rechenfunktionen der Rechenbibliothek des Originals; Datum, Uhrzeit, Monats- und Wochentagsnamen folgen der Locale des Profils mit festen Mustern für `en-US`, `en-GB`, `de-DE`, `de-AT` und `de-CH` (`system` liest die Locale der Umgebung; Standard). Die Zahlen bleiben kanonisch, weil sie in Ausdrücke, Argumente und Aufrufe fließen ([Spezifikation](docs/spec/template.md), [ADR-0022](docs/adr/0022-internationalisierung.md)).

## CLI

`streamcrew` ist der einzige Einstieg: Core starten, Profile, Commands, Nutzer, Token, Chat, Events, Backups und die TUI. Befehle, die auf den Core zugreifen (`command …`, `user …`, `counter …`, `chat send`, `event simulate`), sprechen die API — lokal über `--server` (Standard: die lokale API) und `--token`.

| Befehl | Beschreibung |
|---|---|
| `serve` | Den Core starten, bis SIGINT/SIGTERM |
| `mock` | Core mit der Mock-Plattform gegen eine Profil-Kopie; die Konsole simuliert Chat, Events und den Stream |
| `tui` | Terminal-UI gegen lokalen oder entfernten Core |
| `profile list/create/rename/use/delete` | Profile verwalten |
| `profile channels list/add/remove/chat` | zusätzliche Kanäle des Profils (Add/Remove/Chat: Core muss gestoppt sein); `chat` wechselt den Chat-Adapter der zusätzlichen Kanäle zwischen `eventsub` (Standard) und `irc` |
| `auth login/status/logout` | Twitch-Konten anmelden (Authorization Code Flow, `--device-flow` als Fallback), anzeigen, abmelden |
| `command list/show/run/delete/enable/disable` | Commands des laufenden Cores über die API |
| `command import/export/validate` | Commands as Code ein-/ausladen, prüfen |
| `user list/show/set/merge` | Nutzer des Cores über die API; `merge` braucht gestoppten Core |
| `counter list/get/set/add/reset` | Zähler des Cores über die API |
| `chat send` | Chat-Nachricht des Cores senden (Mock: `--as`) |
| `event simulate` | Mock-Ereignis am Core auslösen |
| `token create/list/revoke` | API-Tokens verwalten; der Wert wird beim Erstellen einmalig angezeigt |
| `backup create/list/restore` | Profil-Backups (Restore: Core muss gestoppt sein) |
| `secret rotate` | neuen Profil-Schlüssel erstellen und alle Tokens neu verschlüsseln (gestoppter Core) |
| `config show/path` | effektive Konfiguration bzw. Pfade anzeigen |
| `schema export` | JSON-Schema des Command-Formats schreiben |
| `doctor` | die Umgebung des Cores prüfen |
| `diag bundle` | Diagnostics-Bundle für Bug-Reports schreiben; Secrets werden maskiert |
| `version` | Versionsinformationen |
| `completion <shell>` | Shell-Completion-Skript |

## Mock

`streamcrew mock` startet den Core in einem Prozess gegen eine **Kopie** des Profils (die am Ende verworfen wird); die Konsole liest Zeilen:

```
chat send --as <login> <text>   Chat-Nachricht dieses Nutzers simulieren
event simulate <typ> [user <login>] [target <login>]   Ereignis simulieren (z. B. channel.follow)
stream start [title]            Stream-Start
stream stop                     Stream-Stopp
user add <login>                Mock-Nutzer anlegen
help                            Hilfe
exit                            beenden
```

Flags: `-c/--commands` lädt Commands-as-Code in die Kopie, `--script` liest die Konsole aus einer Datei (für Regressionstests), `--streamer`/`--bot` legen die Mock-Konten fest. `event simulate` und `chat send` am laufenden Core sind die API-Pendants.

## TUI

`streamcrew tui` zeigt den Core live — lokal (Standard) oder per `--server URL --token TOKEN` entfernt. Die Views (Tabulatorschaltleiste, `Tab`/`Shift-Tab` bzw. Zifferntasten):

| View | Inhalt |
|---|---|
| `dashboard` | Status, Channel, Verbindungen |
| `chat` | Chat-Verlauf aus dem Event-Stream |
| `events` | Ereignis-Feed |
| `queue` | Command-Warteschlange |
| `commands` | Commands mit Status |
| `users` | Nutzer mit Rolle, Titel, Strikes |
| `logs` | Live-Log, Level-Filter (`l`) |
| `settings` | Hot-Settings ändern (ConfigService), wirkt sofort |

`--theme auto\|light\|dark` setzt das Farbschema.

## API

Der Core stellt eine **ConnectRPC**-API (Protobuf, `streamcrew.v1alpha1`) unter der Listenadresse bereit — die einzige Ansprechadresse für alle Frontends ([ADR-0010](docs/adr/0010-api-protokoll.md)). Protobuf-Definitionen liegen unter `api/proto/`, der generierte Code in `api/gen/`.

**Dienste:** `SystemService` (Status, Profile, Shutdown), `CounterService`, `SettingsService`, `UserService`, `ChatService`, `BackupService`, `ConfigService` (Get/Update der Hot-Settings), `AuthService`, `CommandService` und `StreamService` (Server-Streaming `Subscribe` mit Catch-up über `since` und Filter nach Ereignis-Typen-Klassen, Plattformen, Channels und Log-Level, dazu `SimulateEvent` und `AnswerPrompt`).

**Authentifizierung:** Bearer-Token im `Authorization`-Header. Tokens werden pro Profil erstellt:

```bash
streamcrew token create "Mein Token" --scope read,control   # Wert wird einmalig gezeigt
streamcrew token list
streamcrew token revoke <id-oder-name>
```

Scopes: `read`, `control`, `admin` (Standard), `overlay`. Solange kein Token gespeichert ist, ist die API offen (Bootstrap, nur Loopback-Standard laut [ADR-0003](docs/adr/0003-betriebsmodi.md)); sobald mindestens ein Token existiert, braucht **jeder** Request ein gültiges Bearer-Token mit dem für die Methode nötigen Scope. In `server`-Modus gehört das Erstellen eines Tokens deshalb zum Aufsetzen — der Loopback-Standard und die offene Bootstrap-Phase gelten dort nicht.

**Zugriff:**

- `--server URL --token TOKEN` an den API-Befehlen, oder die Umgebungsvariablen `STREAMCREW_SERVER`/`STREAMCREW_TOKEN`.
- CORS: `--cors-allow ORIGIN` (wiederholbar); ohne Eintrag werden Cross-Origin-Anfragen abgelehnt.
- TLS: `--tls-cert` + `--tls-key`.
- Health: `/healthz`, `/readyz`.
- Entwicklermodus: `--dev` zusätzlich `pprof` unter `/debug/pprof/` (nur Loopback).

**Beispiel** (Client-Seite, Go):

```go
base := streamcrewv1alpha1connect.NewSystemServiceClient(http.DefaultClient, "http://127.0.0.1:8740")
cl := base.WithHeader("Authorization", "Bearer "+token)
_, err := cl.Status(ctx, connect.NewRequest(&streamcrewv1alpha1.StatusRequest{}))
```

## Log

- **Konsole:** `--log-level`, `--log-format text|json`, `--log-component-level COMP=LEVEL`.
- **Datei:** `--log-file` (`true` → `<data-dir>/logs/streamcrew.log`, `false` → aus, Pfad → diese Datei), JSON-Zeilen, Rotation nach `--log-max-size` MiB auf `--log-max-files` Dateien.
- **User-Nachrichten und API-Antworten:** Im Debug-Log-Level oder mit `--dry-run` gehen zusätzlich alle User-Nachrichten (Chat, Whispers, mit vollem Text) in eine eigene Datei `<Log-Verzeichnis>/messages.json` (JSON, gleiche Rotation), und alle Antworten der APIs (Twitch/Helix, BetterTTV, FrankerFaceZ) landen mit ihrem Body (ab 64 KiB gekürzt) im normalen Log. Sicherheitsrelevante Werte — Token- und Secret-Felder im Body (`access_token`, `refresh_token`, …) und in URLs (`token=`, `code=`, …) — werden dabei durch `***` ersetzt; dieselbe Maskierung gilt für alle Log-Attribute mit secret-ahnendem Schlüssel (existierende `redact`-Logik). Beide werden beim Start entschieden; eine Hot-Änderung von `log_level` oder `dry_run` öffnet/schließt die Datei nicht.
- **Stream:** Log-Zeilen sind zugleich Ereignisse (`log.line`) und laufen in den Event-Stream; TUI und Clients abonnieren sie gefiltert nach Level, Plattform, Channel und Ereignisklasse ([Roadmap 6.6](docs/roadmap.md)).

Alle Log-Einstellungen sind Hot-Settings: Änderungen wirken ohne Neustart. Ausnahme: `log_file` gilt nach dem [Nachtrag 2026-10-24 zu Code-ADR-0021](docs/adr/code/0021-on-the-fly-konfiguration.md#nachtrag-2026-10-24-die-api-erweitert-keine-rechte) nur beim Start (die Umsetzung ist offen).

## Plattform: Twitch

- **Login:** `streamcrew auth login twitch` — Authorization Code Flow mit Loopback-Redirect, Device Code Flow per `--device-flow` ([ADR-0023](docs/adr/0023-twitch-login-authorization-code-flow.md)). Tokens und App-Credentials liegen **verschlüsselt** im Profil (Vault) und werden vor Ablauf erneuert.
- **App:** eingebauter Twitch-App-Client oder eigene (BYO) ([ADR-0024](docs/adr/0024-eingebaute-twitch-app.md)).
- **Verbindung:** EventSub-WebSocket mit Keepalive, Session-Reconnect und Deduplizierung; die Subscriptions werden je Channel am Soll-Zustand gehalten.
- **Funktionen:** Chat (senden, antworten, flüstern, löschen, leeren), Moderation, Titel/Kategorie, Channel Points, Bits, Emote-Katalog (Twitch, BetterTTV, FrankerFaceZ), Clips, Marker, Raid, Shoutout, Umfrage, Vorhersage, Belohnungen.
- **Zusätzliche Kanäle:** `streamcrew profile channels add <login>` — der Bot arbeitet in jedem Kanal, in dem sein Konto Moderator ist, ohne zweiten Login; jeder Kanal bekommt die eigene WebSocket-Verbindung und Subscriptions. Die Startkonfiguration `twitch_irc: on` (oder `--twitch-irc on`, Neustart nötig) lässt die zusätzlichen Kanäle stattdessen über den Twitch-IRC-Chat des Bot-Kontos laufen — ohne Mod-Voraussetzung, mit Whisper und Senden; der Mod-Status wird dann aus den Chat-Badges erkannt. Ist EventSub aus (`twitch_eventsub: off`), übernimmt der IRC-Adapter auch den Heimkanal — mindestens ein Adapter muss an sein, sonst startet der Core nicht. Details in der [Spezifikation](docs/spec/twitch-multi-channel.md).
- **Pro-Channel-Konfiguration:** ein Command trägt einen optionalen Channel-Scope (`channel`, leer = alle betriebenen Kanäle); Counter, Cooldowns, Währungen und Nutzer-Statistiken gelten profiltweit.

## Sicherheit

- **Capabilities** ([ADR-0013](docs/adr/0013-sicherheitsmodell.md)): Jede Funktion, die über den Core hinausgreift, braucht eine benannte Capability; Action- und Requirement-Typen nennen ihre Rechte im Descriptor, die Engine prüft sie vor der Ausführung und scheitert sonst mit klarer Meldung. Die Menge entsteht aus dem Betriebsmodus plus `--grant`/`--revoke` und kann ohne Neustart aus der Konfigurationsdatei nachgeladen werden ([Code-ADR-0019](docs/adr/code/0019-host-rechte-in-der-startkonfiguration.md)).
- **Tokens:** API-Tokens sind pro Profil, verschlüsselt in der Profil-Datenbank gespeichert, per `secret rotate` neu verschlüsselbar; ohne gespeichertes Token ist die API offen (nur Loopback-Standard), mit einem Token ist jeder Request authentifiziert und berechtigt.
- **Diagnose:** `diag bundle` sammelt Umgebungs-, Konfigurations- und Log-Informationen für Bug-Reports — Secrets und Token werden maskiert.

## Container

Das [Dockerfile](Dockerfile) baut ein multi-stage Image: CGO-freies statisches Binary, das in ein `scratch`-Image kopiert wird und als UID 65532 (`nobody`) läuft; ENTRYPOINT `/usr/local/bin/streamcrew`. `docker build .` baut das Image; die Flags werden als Argumente übergeben (z. B. `docker run -v $HOME/.config/streamcrew:/data streamcrew serve --data-dir /data`).

## Entwicklung

```bash
go run ./cmd/streamcrew mock            # Core gegen die Mock-Plattform
go run ./cmd/streamcrew serve           # Core mit dem aktiven Profil
go run ./cmd/streamcrew auth login twitch
go test ./...
go test -race ./...                     # Race-Detektor
golangci-lint run                       # Linting
go generate ./api/...                   # Protobuf/Connect-Code neu generieren (installiert die Plugins, buf generate)
```

Die CI (`ci.yml`) prüft `go fix`, `go vet`, `golangci-lint`, `govulncheck`, Lizenz-Checks, `buf lint`/`buf generate diff`, Tests mit Race-Detektor und kurzen Fuzz-Läufen (inkl. EventSub-Mock-Server), Cross-Builds für Linux/Windows/macOS (amd64/arm64) und einen Docker-Smoke-Test; auf macOS/Windows-Runnern laufen die Tests zusätzlich wöchentlich und auf Abruf.

### Code-Struktur

| Modul | Zweck |
|---|---|
| `cmd/streamcrew` | Binary-Einstieg; Parsee, Profil, Core und CLI-Befehle zusammenführen |
| `internal/cli` | CLI-Befehle (Kong): serve, mock, profile, auth, command, user, counter, chat, event, token, backup, config, schema, doctor, diag, tui, completion |
| `internal/app` | Orchestrierung: Core-Aufbau, API-Server, Hot-Settings, Mock-Konsole, Desktop-Runtime |
| `internal/api` | ConnectRPC-Server: die v1alpha1-Dienste, Token-Auth, CORS, Health, Event-Stream |
| `internal/tui` | Terminal-UI (Bubble Tea v2): Views, Event-Stream-Feed, Settings über ConfigService |
| `core` | Öffentliche Start-API des Cores (ADR-0006): `Run` und `Options`; `serve` und der Desktop-Self-Start nutzen dieselbe Funktion |
| `internal/config` | Konfigurationsdatei, Flags, View, Watcher (Hot-Settings) |
| `internal/domain/…` | Fachbegriffe: Command, Counter, Event, User, Platform |
| `internal/engine` | Command-Engine: Auslösen, Warteschlange mit Sperrmodi, Kind-Actions, Verlauf ([Spezifikation](docs/spec/command-engine.md)) |
| `internal/template`, `internal/expr` | Template-Engine und Ausdrucksauswertung mit exakten Dezimalzahlen |
| `internal/twitch`, `internal/helix` | Twitch-Anbindung: EventSub-WebSocket, Helix-Client, Multi-Channel |
| `internal/auth` | Plattform-Konten, Vault, Refresh-Loop, `auth`-Befehle |
| `internal/capability` | Capability-Modell (Rechte der Actions, Menge je Modus) |
| `internal/store` | SQLite-Datenbank je Profil (sqlc: Queries, Migrationen) |
| `internal/profile` | Profil-Verwaltung (Anlegen, Umbenennen, Aktiv, `.db`-Dateien) |
| `internal/vault` | Verschlüsselte Secrets: Schlüssel aus Keyring, Umgebungsvariable oder Datei |
| `internal/backup` | Profile-Backups (VACUUM INTO) und Zeitplan |
| `internal/event` | Ereignis-Modell, Katalog, Event-Bus ([Code-ADR-0011](docs/adr/code/0011-event-bus.md)) |
| `api/proto`, `api/gen` | Protobuf-Definitionen und generierter Connect-Code |

### Protobuf-Generierung

```bash
go generate ./api/...   # installiert die Plugins (Versionen in api/generate.go gepinnt) und führt `buf generate` aus
```

## Abhängigkeiten

| Abhängigkeit | Zweck |
|---|---|
| `github.com/alecthomas/kong` | CLI-Parser und Flags |
| `connectrpc.com/connect/v2` | ConnectRPC-Client und Server (API) |
| `google.golang.org/protobuf` | Protobuf-Runtim |
| `modernc.org/sqlite` | SQLite-Datenbank (reines Go, CGO-frei) |
| `github.com/pressly/goose/v3` | Datenbank-Migrationen |
| `charm.land/bubbletea/v2`, `charm.land/lipgloss/v2` | TUI |
| `github.com/coder/websocket` | EventSub-WebSocket |
| `github.com/santhosh-tekuri/jsonschema/v6` | JSON-Schema-Prüfung der Commands as Code |
| `github.com/cockroachdb/apd/v3` | exakte Dezimalzahlen in Ausdrücken |
| `github.com/zalando/go-keyring` | System-Keyring für den Schlüsselsatz |
| `github.com/sony/gobreaker/v2`, `github.com/sethvargo/go-retry`, `golang.org/x/time` | Circuit-Breaker, Wiederholung, Rate-Limits (Twitch-HTTP-Client) |
| `golang.org/x/oauth2` | OAuth-Flows (Twitch-Login) |
| `golang.org/x/text` | Unicode- und Locale-Hilfen |
| `go.yaml.in/yaml/v3` | YAML (Konfiguration, Commands as Code) |

## Lizenz

MIT — siehe [LICENSE](LICENSE).
