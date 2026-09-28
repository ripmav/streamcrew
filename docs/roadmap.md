# Roadmap: Go-Port von Mix It Up

| | |
|---|---|
| **Status** | Phase 0 läuft; Kernentscheidungen getroffen (ADR-0001 bis ADR-0011), Umsetzung noch nicht begonnen |
| **Stand** | 2026-09-27 |
| **Grundlage** | [`plan.md`](plan.md) (Architektur, Prioritäten, Risiken), [`starting.md`](starting.md), [`adr/`](adr/README.md) |
| **Aktuelle Phase** | Phase 0: Klärung und Projektstart (Gate) |

> **Name:** Das Projekt heißt vorerst `streamcrew` (Codename, [ADR-0008](adr/0008-codename-streamcrew.md)); Binary `streamcrew`. Der endgültige Name wird vor Gate O geprüft.

## Legende und Pflege

- **Checkboxen:** `[ ]` offen, `[x]` erledigt. Eine Aufgabe wird **sofort nach Abschluss abgehakt**; größere Änderungen an der Roadmap kommen in die Änderungshistorie am Ende.
- **Prioritäten:** wie in Plan §5.
  - P0: MVP (M2)
  - P1: Core 1.0 (M8)
  - P2: nach 1.0
  - P3: nur bei Bedarf

  Aufgaben ohne Kennzeichnung gehören zur Priorität ihrer Phase bzw. ihres Meilensteins.
- **Größen:**
  - S: bis 2 Tage
  - M: bis 1 Woche
  - L: bis 2 Wochen
  - XL: mehr als 2 Wochen, vor Beginn aufteilen
- **Aufwand je Phase:** Personenwochen (PW) für eine Person in Vollzeit. Das ist eine grobe Schätzung, die nach M1 neu kalibriert wird.
- **Gate:** Eine Phase mit Gate muss abgeschlossen sein, bevor die Folgephasen beginnen.
- **Neuimplementierung (ADR-0001):** Jedes fachliche Arbeitspaket beginnt mit einer Spezifikation in `docs/spec/`, die das Verhalten in eigenen Worten beschreibt und ihre Quellen nennt. Quellen sind die Doku, beobachtetes Verhalten und der Originalcode als Hilfestellung.
  - Implementiert wird aus der Spezifikation, nicht aus dem C#-Code.
  - KI-Assistenten bekommen keinen C#-Code und keine Übersetzungsaufträge.
- **Lizenz (ADR-0002):** Jede neue Quelldatei trägt `// SPDX-License-Identifier: MIT`.
- **ADR-Nummern:** ADR-0001 bis ADR-0011 existieren (siehe [`adr/`](adr/README.md)). Höhere Nummern sind vorläufige Backlog-Nummern aus Plan §12.1. Wer ein geplantes ADR anlegt, vergibt die nächste freie Nummer und passt die Verweise hier und im Plan an.
- **Pre-Commit-Checkliste:** gilt für jede Aufgabe mit Code (Plan §11.1):
  1. `go fix ./...`
  2. `gofmt -w .`
  3. `go vet ./...`
  4. `golangci-lint run ./...`
  5. `govulncheck ./...`
  6. `go test ./...`

---

## Überblick

### Abhängigkeiten der Phasen

```mermaid
flowchart LR
    P0["Phase 0<br/>Klärung und Start"] --> P1["Phase 1<br/>Fundament"]
    P1 --> P2["Phase 2<br/>Domäne und Persistenz"]
    P2 --> P3["Phase 3<br/>Engine und Mock"]
    P3 --> P4["Phase 4<br/>Twitch"]
    P3 --> P5["Phase 5<br/>Core-Services"]
    P4 --> P6["Phase 6<br/>API, CLI, TUI"]
    P5 --> P6
    P6 --> P7["Phase 7<br/>Overlays und Medien"]
    P6 --> P8["Phase 8<br/>Economy und Spiele"]
    P6 --> P9["Phase 9<br/>Weitere Plattformen"]
    P6 --> P11["Phase 11<br/>Dev-API, MCP, Webhooks"]
    P7 --> P10["Phase 10<br/>Integrationen"]
    P8 --> P12["Phase 12<br/>Härtung, Core 1.0"]
    P9 --> P12
    P10 --> P12
    P11 --> P12
    P6 --> D["Desktop D0 bis D7"]
    P6 --> W["Web W0 bis W7"]
    P9 -.-> R["Relay R0 bis R2 (optional)"]
    P6 -.-> O["Gate O<br/>Open-Sourcing"]
```

Gate O kann frühestens nach M2 stattfinden. Es schafft nur die Voraussetzungen: Öffentlich wird das Projekt erst, wenn der Projektinhaber die Repositories selbst umschaltet. Bis dahin bleibt alles privat.

### Meilensteine

| Meilenstein | Phasen | Exit-Kriterium | Aufwand (PW) | kumuliert |
|---|---|---|---:|---:|
| **M0** Projektstart | 0–1 | ADR-0001 bis -0008 akzeptiert; privates Repo mit `LICENSE`; CI und Docker-Image grün; `serve` startet und stoppt sauber | 2–3 | 2–3 |
| **M1** Engine mit Mock | 2–3 | Commands aus YAML reagieren auf simulierte Chat-Nachrichten und Events; Persistenz und Backups funktionieren | 8–11 | 10–14 |
| **M2** Headless-MVP (Twitch) | 4–6 | echter Twitch-Kanal ohne GUI betreibbar; API `v1alpha1`, CLI, TUI; Releases und Container-Image | 13–17 | 23–31 |
| **M3** Overlays und Medien | 7 | Alerts mit Bild, Ton und TTS in OBS; Widgets live; OBS-Steuerung | 5–7 | 28–38 |
| **M4** Economy und Community | 8 (P1) | Währung, Ränge, Inventar, 5 Spiele, Giveaways, Queue, Quotes | 8–11 | 36–49 |
| **M5** Multiplattform | 9 (P1) | Twitch, YouTube und Kick gleichzeitig mit gemeinsamen Commands | 8–11 | 44–60 |
| **M6** Integrationen Tier 1 | 10 (P1) | Spenden von Streamlabs, StreamElements und Ko-fi; Discord; Scripting | 4–6 | 48–66 |
| **M7** Offen und erweiterbar | 11 (P1) | Developer-API, MCP-Server, eingehende Webhooks, Command-Bundles | 3–4 | 51–70 |
| **M8** Core 1.0 | 12 | alle P1-Aufgaben erledigt; Security-, Last- und Chaos-Tests bestanden; API `v1` eingefroren | 3–4 | **54–74** |

### Statusübersicht

| Track / Phase | Status |
|---|---|
| Phase 0: Klärung und Projektstart | in Arbeit (ADR-0001 bis ADR-0011 akzeptiert, `LICENSE` angelegt) |
| Phase 1: Fundament | offen |
| Phase 2: Domäne und Persistenz | offen |
| Phase 3: Engine, Templates, Actions, Mock | offen |
| Phase 4: Twitch | offen |
| Phase 5: Core-Services | offen |
| Phase 6: API, CLI, TUI | offen |
| Phase 7: Overlays und Medien | offen |
| Phase 8: Economy, Community, Spiele | offen |
| Phase 9: Weitere Plattformen | offen |
| Phase 10: Integrationen, Scripting, Agent | offen |
| Phase 11: Developer-API, MCP, Webhooks, Import | offen |
| Phase 12: Härtung und Core 1.0 | offen |
| Desktop-Track D0–D7 | offen (ab M2) |
| Web-Track W0–W7 | offen (ab M2) |
| Relay-Track R0–R2 (optional) | offen |
| Gate O: Open-Sourcing | offen (frühestens nach M2) |

---

## Phase 0: Klärung und Projektstart (Gate)

| | |
|---|---|
| **Ziel** | Vorgehen, Lizenz und Organisation klären, bevor Code entsteht |
| **Voraussetzungen** | keine |
| **Aufwand** | ~1 PW |
| **ADRs** | 0001–0011 (akzeptiert), 0021 (rechtlicher Teil) |

### 0.1 Vorgehen und Recht

- [x] ADR-0001 „Neuimplementierung und Nutzung des Originals“ schreiben und entscheiden (S), erledigt 2026-09-27
- [x] Festlegen, ob `../mixitup` weiter gelesen werden darf: ja, als Hilfestellung nach den Regeln aus ADR-0001, erledigt 2026-09-27
- [x] Regeln aus ADR-0001 in [`docs/spec/README.md`](spec/README.md) übernommen, mit Vorlage [`docs/spec/TEMPLATE.md`](spec/TEMPLATE.md) für Spezifikationen mit Quellenangabe (S), erledigt 2026-09-28
- [ ] Rechtliche Einschätzung einholen (extern), spätestens vor Gate O, besser vor Phase 3. Themen:
  - BSL §2 und §3.1–3.5
  - EULA §1 (kein Reverse Engineering)
  - Nutzung des Codes als Hilfestellung (ADR-0001)
  - die MIT-Datei `MixItUp.Base/LICENSE.txt`
  - die Import-Funktion (Interoperabilität)
  - die Übernahme der `$`-Identifier-Namen
- [ ] Optional (Option D): Blazing Cacti um eine schriftliche Erlaubnis bitten und die Antwort dokumentieren (S)
- [ ] Rechtlichen Teil von ADR-0021 (Import, Identifier-Namen) entscheiden; danach in ADR-0001 unter „Interop-Ausnahmen“ den Link auf das neue ADR nachtragen (S)

### 0.2 Lizenz, Name, Marke

- [x] ADR-0002 Lizenz des Projekts: MIT (S), erledigt 2026-09-27
- [x] `LICENSE` mit dem offiziellen MIT-Text im Projektwurzelverzeichnis anlegen (S), erledigt 2026-09-27
- [x] [ADR-0008](adr/0008-codename-streamcrew.md) Codename `streamcrew` (englisch, beschreibend, ohne Anlehnung an Mix It Up), erledigt 2026-09-28
- [ ] Namensprüfung für den endgültigen Namen, spätestens vor Gate O, möglichst früher (S):
  - Markenrecherche (DPMA, EUIPO, USPTO)
  - Domain-, GitHub- und Paketnamen-Verfügbarkeit
- [x] Platzhalter für Projekt- und Binärnamen in `plan.md`, `roadmap.md` und den ADRs durch `streamcrew` ersetzt (S), erledigt 2026-09-28

### 0.3 Umfang

- [x] Kernfragen beantwortet (Vorgehen, Veröffentlichung, Lizenz, Betriebsmodus, Plattform zum Start), erledigt 2026-09-27
- [x] ADR-0003 Betriebsmodi: Streaming-PC primär, Server-Anwendung sekundär, erledigt 2026-09-27
- [x] ADR-0004 Plattformumfang zum Start: nur Twitch; der MVP-Umfang (M2) ist damit bestätigt, erledigt 2026-09-27
- [x] [ADR-0005](adr/0005-core-in-desktop-builds.md) Core in Desktop-Builds: Die App liefert den Core mit und startet ihn als eigenen Prozess, erledigt 2026-09-27
- [x] [ADR-0006](adr/0006-core-als-bibliothek-fuer-selbststart.md) Core als Bibliothek: schmale Start-API nur für den Selbststart, kein Betrieb im selben Prozess; ob mitgeliefert oder eingebunden, legt der Build-Prozess fest, erledigt 2026-09-27
- [x] [ADR-0007](adr/0007-release-artefakte-des-cores.md) Release-Artefakte: Jedes Core-Release enthält Binary und Bibliothek (Go-Modul als Quellarchiv), erledigt 2026-09-27
- [ ] Restliche offene Fragen aus Plan §15 beantworten: Import, Zielsysteme der Desktop-App, Kapazität, Web-Stack (S)
- [x] MVP-Umfang (P0, M2) wie geplant bestätigt; P1–P3 werden vor den jeweiligen Phasen überprüft (S), erledigt 2026-09-28

### 0.4 Organisation

- [x] Privates GitHub-Repository `ripmav/streamcrew` angelegt ([ADR-0009](adr/0009-repositories-und-hosting.md)); `LICENSE` im ersten Commit auf `main`, `docs/` per Pull Request (S), erledigt 2026-09-28
- [ ] `main` schützen und die Branch-Konvention festlegen (S). Branch-Schutz und Rulesets sind für private Repositories im aktuellen GitHub-Plan nicht verfügbar (geprüft 2026-09-28). Bis zur Veröffentlichung oder einem Plan-Upgrade gilt die Regel per Konvention (Arbeit nur auf Branches, Merge per Pull Request), optional abgesichert durch einen lokalen `pre-push`-Hook. Nach dem Merge löscht GitHub den Branch automatisch (Einstellung aktiv seit 2026-09-28).
- [x] ADR-Infrastruktur anlegen (S), erledigt 2026-09-27:
  - `docs/adr/README.md` (Index)
  - `docs/adr/TEMPLATE.md` (Kontext, Entscheidung, Alternativen, Konsequenzen, Status)
  - `docs/adr/code/README.md`
- [ ] Projekt-Board oder Issues mit den Phasen dieser Roadmap anlegen (S)

**Exit-Kriterien:**

- ADR-0001 bis ADR-0008 haben den Status „akzeptiert“.
- Das private Repository existiert, mit `LICENSE` ab dem ersten Commit.
- Produktivcode entsteht erst, wenn der Name bzw. Codename für den Modulpfad feststeht.

---

## Phase 1: Fundament (Toolchain, Skelett, CI)

| | |
|---|---|
| **Ziel** | lauffähiges, sauber beendbares Skelett mit vollständiger Qualitäts-Pipeline |
| **Voraussetzungen** | Phase 0 |
| **Aufwand** | 1–2 PW |
| **ADRs** | 0009, 0010, 0011 (bereits akzeptiert); Code-ADRs 0001–0005, 0014 |

### 1.1 Toolchain und Qualität

- [ ] `go.mod` mit `go 1.27` (installiert: go1.27.1) und Modulpfad `github.com/ripmav/streamcrew` anlegen (S)
- [ ] `.golangci.yml` im v2-Format anlegen (installiert: 2.13.2); die Linter-Auswahl in Code-ADR-0001 festhalten (S)
- [ ] `goheader`-Linter für den SPDX-Header `// SPDX-License-Identifier: MIT` konfigurieren (ADR-0002) (S)
- [ ] Pre-Commit-Checkliste als Skript oder Taskfile umsetzen (S)
- [ ] CI-Pipeline mit GitHub Actions aufsetzen (M):
  - Lint, `go test -race`, `govulncheck`
  - Lizenzprüfung der Abhängigkeiten (`github.com/google/go-licenses`): nur MIT-kompatible Lizenzen
  - Linkprüfung für Markdown in `docs/` (z. B. `lychee`), damit Verweise zwischen Dokumenten und ADRs nicht ins Leere zeigen
  - kurze Fuzz-Läufe
  - Build-Matrix linux/windows/darwin × amd64/arm64
- [ ] Dependency-Updates automatisieren (Renovate oder Dependabot) (S)
- [x] Claude-Code-Review nur auf `@claude`-Erwähnung in Pull Requests, mit Fortschritts- und Ergebniskommentar (`.github/workflows/claude.yml`; kein automatisches Review, keine Issues) (S), erledigt 2026-09-28
- [ ] Claude-Review so korrigieren, dass das Review tatsächlich läuft. Ursache laut Diagnose: Das Werkzeug `Skill` wurde verweigert, über das Claude Code den Plugin-Befehl ausführt. Es ist jetzt gezielt für `code-review:code-review` freigegeben; der Nachweis im nächsten Review-Lauf steht aus (S)
- [ ] `GOPRIVATE=github.com/ripmav/*` und CI-Token für den Zugriff auf private Repositories und Release-Artefakte einrichten ([ADR-0009](adr/0009-repositories-und-hosting.md)) (S)

### 1.2 Architekturentscheidungen

- [x] [ADR-0009](adr/0009-repositories-und-hosting.md) Repositories und Hosting: drei Repositories auf GitHub (privat), CI mit Actions, Releases in GitHub Releases; vorgezogen, erledigt 2026-09-28
- [x] [ADR-0010](adr/0010-api-protokoll.md) API-Protokoll: ConnectRPC mit Protobuf, inklusive lokalem Transport zum Core-Prozess; vorgezogen, erledigt 2026-09-28
- [x] [ADR-0011](adr/0011-keine-telemetrie.md) Keine Telemetrie; Fehlersuche über lokale Logs und Diagnose-Paket; vorgezogen, erledigt 2026-09-28
- [ ] Code-ADRs schreiben (M):
  - 0001 Toolchain und Linting
  - 0002 Dependency Injection
  - 0003 Fehler und Logging
  - 0004 Nebenläufigkeit und Supervisor
  - 0005 Konfiguration
  - 0014 Teststrategie

### 1.3 Skelett

- [ ] `cmd/streamcrew/main.go` mit kong und den Unterkommandos `serve`, `version`, `config show|path`, `doctor` (S)
- [ ] `internal/config` (M):
  - Flags, Umgebungsvariablen (`STREAMCREW_…`), optionale Konfigurationsdatei
  - Datenverzeichnis (`os.UserConfigDir`, `--data-dir`, portabler Modus)
  - Betriebsmodus
- [ ] `internal/app` (M):
  - Composition Root
  - Supervisor: Runnables mit Restart-Policy und Backoff mit Jitter
  - `signal.NotifyContext`, geordneter Shutdown mit Zeitlimits
- [ ] Logging mit `log/slog` (S):
  - Text oder JSON, Level pro Komponente
  - Dateiausgabe mit Rotation
  - Maskierung von Secrets
- [ ] HTTP-Grundserver mit dem stdlib-Routing: `/healthz`, `/readyz`, `pprof` nur im Dev-Modus (S)
- [ ] `time/tzdata` einbinden (S)

### 1.4 Dokumentation und Container

- [ ] `README.md` (Ziel, Status, Build, Lizenzhinweis MIT) und `CONTRIBUTING.md` (Konventionen, Pre-Commit, ADR-Prozess, Herkunftsregeln aus ADR-0001) (S)
- [ ] Dockerfile: Multi-Stage, CGO-frei, non-root, minimales Laufzeit-Image, aktuelle Basis-Images (S)
- [ ] Docker-Smoke-Test: `docker build`, `docker run … version`, `serve` mit Healthcheck (S)

**Exit-Kriterien (M0):**

- `streamcrew serve` startet, meldet sich gesund und beendet sich auf SIGINT/SIGTERM sauber.
- Die CI ist grün.
- Das Docker-Image ist gebaut und getestet.

---

## Phase 2: Domäne und Persistenz

| | |
|---|---|
| **Ziel** | Datenmodell, Speicher, Profile, Backups, Event-Bus und Secrets |
| **Voraussetzungen** | Phase 1 |
| **Aufwand** | 3–4 PW |
| **ADRs** | 0012, 0020 (Entwurf); Code-ADRs 0006, 0007, 0008, 0009, 0015 |

### 2.1 Speicher

- [ ] Code-ADR-0006 Datenbankzugriff: `modernc.org/sqlite`, `sqlc`, `goose`; Code-ADR-0015 Codegenerierung (S)
- [ ] ADR-0012 Persistenz: SQLite je Profil, Backups (S)
- [ ] `internal/store` (M):
  - Verbindung mit WAL, `foreign_keys` und `busy_timeout`
  - eingebettete Migrationen
  - Transaktions-Helfer
  - Repository-Implementierungen; die Interfaces liegen beim Konsumenten
- [ ] Profile: anlegen, auflisten, wechseln, umbenennen, löschen; Sperrdatei gegen Doppelstart (M)
- [ ] Backups (M):
  - `VACUUM INTO` → ZIP mit Manifest (App- und Schemaversion)
  - Zeitplan (täglich, wöchentlich, monatlich) und Aufbewahrung
  - Restore mit Versionsprüfung
  - `backup create|list|restore`

### 2.2 Domänenmodell

- [ ] Code-ADR-0007 IDs und Zeit: UUIDv7, injizierbare Uhr, `testing/synctest` (S)
- [ ] Nutzer: Nutzer, Plattform-Identitäten, Statistiken, Titel, Notizen, Ausschlüsse (M)
- [ ] Rollenmodell: plattformneutrale Rollen mit Rangordnung plus plattformspezifische Rollen; Semantik „erfüllt Mindestrolle“ (Plan Anhang A.7) (M)
- [ ] Commands (M):
  - Arten, Trigger
  - Gruppen inkl. Gruppen-Timer-Intervall
  - aktiv/unlocked
  - Requirements-Set
  - polymorphe Actions
- [ ] Code-ADR-0008 polymorphe Serialisierung: `type`-Diskriminator, `schemaVersion`, `encoding/json/v2`, Migrationen je Typversion (M)
- [ ] Datenmodell für Counter und Quotes (S)
- [ ] Settings-Sektionen, typisiert und versioniert: allgemein, Chat, Commands, Moderation, Overlay, Zeit/Locale, Backups (M)
- [ ] Event-Modell (M):
  - Umschlag (ID, Zeit, Quelle, Typ, Nutzlast)
  - Katalog der Event-Typen als stabile Strings
  - Zuordnungstabelle zu den numerischen IDs des Originals für den späteren Import

### 2.3 Event-Bus

- [ ] Code-ADR-0009 Event-Bus (S)
- [ ] Typisierter In-Process-Bus mit Abonnements, Filtern, Puffern und Lag-Erkennung für langsame Abonnenten (M)

### 2.4 Secrets und Sicherheit

- [ ] `internal/secret` (M):
  - AES-256-GCM
  - Schlüssel aus OS-Keyring, Key-Datei (0600) oder Umgebungsvariable
  - Schlüsselrotation
- [ ] ADR-0020 Sicherheitsmodell, Entwurf: Betriebsmodi × Capabilities (Plan §6.15) (S)

**Exit-Kriterien:**

- Migrationen sind vorwärts und rückwärts getestet.
- Die Repositories sind durch Integrationstests gegen eine echte SQLite abgedeckt.
- Der Backup/Restore-Roundtrip-Test ist grün.
- Ein Test belegt, dass Tokens nie im Klartext in der Datenbank stehen.

---

## Phase 3: Engine (Templates, Commands, Actions, Requirements, Mock)

| | |
|---|---|
| **Ziel** | plattformneutrale Ausführung von Commands, testbar ohne Live-Plattform |
| **Voraussetzungen** | Phase 2 |
| **Aufwand** | 5–7 PW |
| **ADRs** | 0022; Code-ADRs 0010, 0011, 0016 |

### 3.1 Template-Engine

- [ ] Spezifikation `docs/spec/template.md` (S):
  - Syntax und Auflösungsreihenfolge
  - Regel „längster Präfix“
  - Kodierung
  - bewusste Abweichungen vom Original
- [ ] Code-ADR-0010 Template-Engine (S)
- [ ] Tokenizer und Resolver-Registry (statisch, Muster, dynamische Namen); bedarfsgesteuerte Auflösung mit `context`; Cache pro Rendervorgang (M)
- [ ] Kodierungsmodi: Text, URL, HTML, JSON (S)
- [ ] Identifier-Familien für das MVP (M):
  - Nutzer, Ziel, Streamer, Bot
  - Argumente
  - Datum und Zeit in der Profil-Zeitzone
  - Zufall, Stream, Counter, letzte Ereignisse
  - Command-Name, Plattform
- [ ] Ausdrücke mit `expr-lang/expr`: Rechnen, Vergleiche (S)
- [ ] Golden-Tests und Fuzz-Targets (S)

### 3.2 Command-Engine

- [ ] Spezifikation `docs/spec/command-engine.md`: Zustände, Sperrmodi, Pause, Verlauf (S)
- [ ] Instanzen mit Zustandsmaschine (Pending → Running → Completed/Failed/Canceled) (S)
- [ ] Warteschlange mit fünf Sperrmodi, Unlocked-Commands, Pause/Fortsetzen, eigene Pause für Entrance-Commands (L)
- [ ] Abbrechen über `context`, Replay, Verlauf als Ringpuffer, Ereignisse `command.instance.*` (M)
- [ ] Runner-Parameter pro Nutzer, Auflösung des Ziel-Nutzers, Rekursions- und Zyklenschutz, Zeitlimits je Action (M)
- [ ] Nebenläufigkeitstests mit `testing/synctest` und `-race` (M)

### 3.3 Action-Framework und P0-Actions

- [ ] Code-ADR-0011 Typ-Registry (M): Descriptor mit
  - Typ-ID, Version, Kategorie, i18n-Schlüsseln
  - JSON-Schema und UI-Hinweisen
  - benötigten Capabilities
- [ ] Capability-Prüfung je Betriebsmodus: Warnung beim Speichern, Verweigerung bei Ausführung (S)
- [ ] `chat`: senden, antworten, flüstern; als Bot oder Streamer (S)
- [ ] `wait`, `random`, `group`, `repeat` (S)
- [ ] `conditional`: Vergleiche, Und/Oder, `expr`-Ausdrücke, Verzweigungen (M)
- [ ] `command`: ausführen mit oder ohne Warten, aktivieren/deaktivieren, Gruppe schalten (S)
- [ ] `counter`: setzen, addieren, zurücksetzen (S)
- [ ] `specialidentifier`: lokale und globale Werte setzen, Ausdrücke (S)
- [ ] `webrequest`: Methode, Header, Body; JSON-Pfade in Identifier; SSRF-Schutz im Server-Modus (M)
- [ ] `moderation`: Timeout, Bann, Entbannen, Mod/VIP, Nachricht löschen, Chat leeren (S)
- [ ] `platformmessage`, `userlookup` (S)
- [ ] `file`: lesen, schreiben, anhängen, Zeile lesen; nur unter freigegebenen Wurzeln via `os.Root` (S)
- [ ] `externalprogram`: nur mit `host:process`; Timeout; Ausgabe in Identifier (S)

### 3.4 Requirements

- [ ] Set-Validierung und -Ausführung, Fehlermeldungen (i18n), Fehler-Cooldowns (global, pro Command, aus) (M)
- [ ] Rolle, Cooldown (pro Nutzer, global, Gruppe), Argumente, Einstellungen, z. B. Auslösenachricht löschen (M)
- [ ] Threshold: Mindestanzahl Nutzer im Zeitfenster (S) (P1)

### 3.5 Commands als Code und Typkatalog

- [ ] Code-ADR-0016 YAML-Bibliothek (S)
- [ ] Typkatalog als JSON-Schema exportieren (`schema export` → `schemas/`) (S)
- [ ] YAML/JSON-Format (`apiVersion`, `kind`, `metadata`, `spec`) mit Import, Export und Validierung: `command validate|import|export` (M)

### 3.6 Mock-Plattform und Event-Grundlagen

- [ ] `internal/platform/mock`: simulierte Nutzer, Chat und Events; Ausgaben ins Log und auf den Bus (M)
- [ ] `event simulate` und `chat send --as <nutzer>`, nur im Mock- bzw. Dev-Modus (S)
- [ ] Event-Service, Grundlage (M):
  - Event → Event-Command
  - generische plattformneutrale Events
  - Einmal-Events pro Nutzer
  - Deduplizierung
- [ ] Trigger-Erkennung: `!`-Präfix, Wildcards, längster Treffer, Argumente inkl. Anführungszeichen (M)
- [ ] ADR-0022 Internationalisierung; Grundgerüst mit EN und DE (S)

**Exit-Kriterien (M1):**

- Beispiel-Commands aus YAML reagieren auf simulierte Chat-Nachrichten und Events der Mock-Plattform.
- Die Testabdeckung von `engine`, `template` und `requirement` liegt bei mindestens 80 %.
- Die Fuzz-Targets laufen in der CI.

---

## Phase 4: Twitch

| | |
|---|---|
| **Ziel** | vollständige Twitch-Anbindung für Streamer- und Bot-Konto |
| **Voraussetzungen** | Phase 3 (parallel zu Phase 5 möglich) |
| **Aufwand** | 5–6 PW |
| **ADRs** | 0013; Code-ADRs 0012, 0013 |

### 4.1 Authentifizierung

- [ ] Twitch-App registrieren (öffentlicher Client, Device Code Flow) (S)
- [ ] ADR-0013 OAuth und App-Credentials, inkl. BYO-Option für alle Plattformen (S)
- [ ] `internal/auth` (M):
  - Device Code Flow mit `golang.org/x/oauth2`
  - verschlüsselter Token-Speicher
  - Refresh nach Ablaufzeit
  - Scope-Abgleich: fehlende Scopes führen zu „Anmeldung erforderlich“
  - Widerruf beim Abmelden
- [ ] Streamer- und Bot-Konto; `auth login twitch [--bot]`, `auth status`, `auth logout` (S)
- [ ] Auth-Aufforderungen (URL, Code, Ablauf) als Ereignisse für Frontends (S)

### 4.2 Helix-Client

- [ ] Code-ADR-0012 HTTP-Client: Retry mit Backoff, Rate-Limit-Header, Paginierung, typisierte Fehler (S)
- [ ] Endpunkte (L):
  - Users, Channels (lesen/aktualisieren), Streams
  - Chat: Nachricht senden, löschen, Einstellungen, Ankündigung, Shoutout
  - Moderation: Bann/Timeout/Entbannen, Mods, VIPs
  - Follower, Abos, Kategorien
  - EventSub-Subscriptions

### 4.3 EventSub

- [ ] Code-ADR-0013 WebSocket-Bibliothek (S)
- [ ] WebSocket-Client (L):
  - Welcome-Nachricht, Keepalive-Überwachung
  - `session_reconnect` ohne Eventverlust
  - Revocation
  - Deduplizierung per `message_id`
- [ ] Subscription-Manager (M):
  - Sollzustand nach Plan Anhang A.2
  - Abgleich mit bestehenden Subscriptions
  - Versionen, Bedingungen, Limits
- [ ] Abbildung auf kanonische Events und das Chat-Modell: Fragmente, Emotes, Cheermotes, Badges, Antworten, Shared Chat (L)

### 4.4 Funktionen

- [ ] Spezifikation `docs/spec/twitch-events.md`: Event-Zuordnung und Identifier je Event (S)
- [ ] Chat senden, löschen, leeren; Moderation; Titel und Kategorie setzen (S)
- [ ] Event-Commands (M):
  - Stream Start/Stop, Follow, Raid (ein- und ausgehend)
  - Abo, Resub, Geschenk, Massengeschenk
  - Cheer, Channel Points, Hype Train, Werbung, Shoutout, Ziele, Charity
  - Moderationsereignisse, Umfragen, Vorhersagen
- [ ] Channel-Points-Commands: Belohnung ↔ Command, Einlösung abschließen oder erstatten (M)
- [ ] Bits-Commands mit Schwellen und Bereichen (S) (P1)
- [ ] Twitch-Action: Clip, Stream-Marker, Umfrage/Vorhersage, Werbung, Raid, Shoutout, Belohnungen verwalten (L)
- [ ] Custom Power-Ups als Command-Typ (S) (P2)
- [ ] Emote-Kataloge für Twitch, BetterTTV und FrankerFaceZ mit Cache (M) (P1)

### 4.5 Tests

- [ ] Fixtures aus der offiziellen Doku bzw. eigenen Test-Streams (anonymisiert) und Contract-Tests (M)
- [ ] Integrationstests gegen den Mock-EventSub-Server der Twitch CLI (M)
- [ ] Reconnect- und Chaos-Tests: Verbindungsabbruch, Keepalive-Timeout, Session-Reconnect (S)

**Exit-Kriterien:**

- Ein Test-Stream läuft mindestens vier Stunden mit Streamer- und Bot-Konto.
- Chat-, Event- und Channel-Points-Commands sowie die Moderation funktionieren.
- Erzwungene Reconnects verlaufen ohne Eventverlust.

---

## Phase 5: Core-Services

| | |
|---|---|
| **Ziel** | Chat-Pipeline, Nutzer, Timer und Moderation auf Produktionsniveau |
| **Voraussetzungen** | Phase 3 (parallel zu Phase 4 möglich) |
| **Aufwand** | 4–5 PW |
| **ADRs** | keine neuen |

### 5.1 Chat

- [ ] Pipeline: normalisieren → Nutzer auflösen → Moderation → Trigger → Folge-Events (erste Nachricht, erster Join) → Verlauf (M)
- [ ] Senden über Bot oder Streamer, Aufteilen langer Nachrichten, Rate-Limits (`golang.org/x/time/rate`), Whisper (S)
- [ ] Chatverlauf im Speicher (Ringpuffer je Plattform) und optionales Chat-Protokoll auf Platte (S)

### 5.2 Nutzer

- [ ] Aktive Nutzer (Join, Leave, Aktivität), Watchtime (nur während live), Statistiken wie Nachrichten, Commands und Tags (M)
- [ ] Rollen plattformübergreifend, Regular-Regel (Watchtime-Schwelle), Follow- und Abo-Daten bei Bedarf mit Cache (M)
- [ ] Konten verknüpfen und Nutzer zusammenführen (M) (P1)
- [ ] Ausschlüsse von Bots und Streamer aus Ranglisten und Zufallsauswahl (S)

### 5.3 Events, Feed, Statistik

- [ ] Letzte Ereignisse (Follower, Abo, Raid, Cheer, Spende), Event-Feed für Frontends, Session-Statistiken (M)

### 5.4 Timer

- [ ] Timer-Commands und -Gruppen: Intervall, Mindestanzahl Chatnachrichten, nur live, zufällige oder feste Reihenfolge (M)

### 5.5 Moderation

- [ ] Spezifikation `docs/spec/moderation.md` (S)
- [ ] Wortfilter und Bannwörter mit Wildcards, Links, Großbuchstaben/Satzzeichen/Emotes (absolut oder prozentual), Ausnahmen je Rolle (M)
- [ ] Strikes mit Folge-Commands je Stufe; Maßnahmen: löschen, Timeout, Bann (S)
- [ ] Teilnahmeregeln: Chat erst ab Follow-Dauer, Watchtime oder Rolle (S) (P1)

### 5.6 Vorgefertigte Commands und Counter

- [ ] P0-Teilmenge (M):
  - Commands-Liste, Uptime, Followage
  - Titel und Kategorie anzeigen und setzen
  - Command hinzufügen, ändern, deaktivieren
- [ ] Counter-Service mit Identifiern, persistiert (S)
- [ ] Nutzerspezifische Chat-Commands (S) (P1)

**Exit-Kriterien:** E2E-Tests mit der Mock-Plattform decken Chat-Pipeline, Timer, Moderation und Watchtime (simulierte Live-Session) ab.

---

## Phase 6: API, CLI, TUI (→ M2 Headless-MVP)

| | |
|---|---|
| **Ziel** | vollständige Bedienung ohne GUI über API, CLI und TUI; erste Releases |
| **Voraussetzungen** | Phasen 4 und 5 |
| **Aufwand** | 4–6 PW |
| **ADRs** | 0023, [ADR-0010](adr/0010-api-protokoll.md) (API), [ADR-0011](adr/0011-keine-telemetrie.md) (Diagnose-Paket), [ADR-0006](adr/0006-core-als-bibliothek-fuer-selbststart.md) (Start-API), [ADR-0007](adr/0007-release-artefakte-des-cores.md) (Release-Artefakte); Code-ADR 0015 (buf ergänzen) |

### 6.1 API-Vertrag

- [ ] `buf` einrichten (lint, breaking, generate); Code-ADR-0015 um `buf` ergänzen (S)
- [ ] Protos `v1alpha1` gemäß Plan §6.14 (L):
  - `SystemService`, `AuthService`, `StreamService`, `ChatService`
  - `CommandService` inkl. Typkatalog
  - `UserService`, `CounterService`, `SettingsService`, `BackupService`
- [ ] Ereignisstrom (Server-Streaming) mit Filtern und Wiederaufsetzen ab den letzten N Ereignissen (M)
- [ ] Prompt-Mechanismus (Information, Bestätigung, Eingabe) mit Antwort-RPC (S)
- [ ] Agent-Schnittstelle skizzieren (nur Vertrag, Umsetzung Phase 10), damit `v1` sie später aufnehmen kann (S)

### 6.2 API-Server

- [ ] ConnectRPC-Handler als dünne Schicht über den Application Services (L)
- [ ] Zugriffsschutz (M):
  - API-Tokens mit Scopes (`read`, `control`, `admin`, `overlay`)
  - `token create|list|revoke`
  - `http.CrossOriginProtection`, CORS-Allowlist
- [ ] Bindung je Betriebsmodus (Loopback als Standard), optional TLS (S)
- [ ] Öffentliches Paket `core` mit schmaler Start-API `Run(ctx, Options)` als Hülle um `internal/app`; `streamcrew serve` nutzt dieselbe Funktion ([ADR-0006](adr/0006-core-als-bibliothek-fuer-selbststart.md)) (S)
- [ ] Lokaler Desktop-Modus ([ADR-0005](adr/0005-core-in-desktop-builds.md)) (M):
  - Core lauscht auf Unix-Socket bzw. Loopback
  - Laufzeitdatei mit Adresse, PID und API-Version
  - Token im Datenverzeichnis (0600)
  - Versions-Handshake und Shutdown-Aufruf

### 6.3 CLI

- [ ] Unterkommandos gemäß Plan §7.2 im MVP-Umfang, `--output json`, Remote-Verbindung (`--server`, `--token`) (M)
- [ ] Shell-Completion (S)
- [ ] `streamcrew diag bundle`: Diagnose-Paket mit Logs, Versionen und Konfiguration, Secrets maskiert ([ADR-0011](adr/0011-keine-telemetrie.md)) (S)

### 6.4 TUI

- [ ] Grundgerüst mit Bubble Tea v2 (M):
  - Layout, Tastenkürzel, Hilfe
  - helles und dunkles Theme
  - Verbindung zu lokalem oder entferntem Core
- [ ] Ansichten (L):
  - Dashboard
  - Chat: lesen, senden, moderieren
  - Event-Feed
  - Command-Queue: pausieren, abbrechen, wiederholen
  - Commands: Liste, Suche, Ausführen, An/Aus, Bearbeiten im `$EDITOR`
  - Nutzer, Logs, Login-Flow
- [ ] TUI-Tests mit dem Test-Harness für Bubble Tea v2 (S)

### 6.5 Release MVP

- [ ] ADR-0023 Release und Distribution (S)
- [ ] Release-Pipeline mit beiden Artefakten am selben Release ([ADR-0007](adr/0007-release-artefakte-des-cores.md)) (M):
  - `goreleaser`: Binaries für alle Zielplattformen, Container-Image, Beispiel für eine systemd-Unit
  - Go-Modul als Quellarchiv im Proxy-Layout (`.zip`, `.mod`, `.info`), z. B. mit `golang.org/x/mod/zip`
  - Prüfsummen und SBOM für alle Artefakte
  - Prüfschritt: ein Testprogramm per `GOPROXY=file://…` gegen das Quellarchiv bauen
- [ ] Nutzerdokumentation (M):
  - Schnellstart: Twitch-Login, erster Command als YAML
  - Betriebsmodi: Streaming-PC als Standard, Server-Anwendung mit Reverse Proxy, TLS und Token (ADR-0003)
  - Sicherheitshinweise

**Exit-Kriterien (M2 Headless-MVP):**

- Ein Twitch-Kanal lässt sich ohne GUI betreiben: Login, Commands aus YAML, Event-Reaktionen, Channel Points, Timer, Basis-Moderation, Counter, Backups.
- Release-Artefakte und das Container-Image stehen bereit.
- Der Server-Modus läuft im Container mit Token-Authentifizierung und abgeschalteten Host-Capabilities (ADR-0003).
- Die TUI nutzt ausschließlich die API.

---

## Phase 7: Overlays und Medien

| | |
|---|---|
| **Ziel** | OBS-Browserquellen mit Items und Widgets, Ton, TTS und OBS-Steuerung |
| **Voraussetzungen** | Phase 6 |
| **Aufwand** | 5–7 PW (P1); 3–5 PW zusätzlich für P2-Widgets |
| **ADRs** | 0018, 0019 |

### 7.1 Overlay-Server und Runtime

- [ ] Spezifikation `docs/spec/overlays.md` und ADR-0018: Architektur und Protokoll (S)
- [ ] HTTP- und WebSocket-Server, mehrere Endpunkte (eine URL je Browserquelle), optionales Token, Wiederverbinden der Clients (M)
- [ ] Neu geschriebene Overlay-Runtime in TypeScript, gebündelt mit der Go-API von esbuild per `go generate` und per `go:embed` ausgeliefert (L):
  - Pakete, Positionen, Ebenen
  - Ein- und Ausblend-Animationen, Batching
  - Sandbox-iframes für eigenes HTML
- [ ] Dateien aus freigegebenen Verzeichnissen (`os.Root`), Range-Requests (`http.ServeContent`), korrekte MIME-Typen (S)

### 7.2 Items und Widgets (P1)

- [ ] Items: Text, Bild, Video, Ton, HTML, YouTube, Twitch-Clip (M)
- [ ] Widgets: Label, Ziel/Fortschritt, Timer, Event-Liste, Chat (L)
- [ ] Overlay-Action (Item zeigen; Widget aktualisieren, zeigen oder verbergen) mit Positions- und Animationsschema für Editoren (M)

### 7.3 Medien

- [ ] ADR-0019 Audio-Ausgabe (S)
- [ ] Audio-Sinks: Overlay als Standard; lokale Ausgabe mit Geräteauswahl per Build-Tag, P1 wegen des Streaming-PCs als Hauptbetriebsort (ADR-0003); Agent-Schnittstelle vorbereitet (M)
- [ ] Sound-Action mit Lautstärke und Ausgabe (S)
- [ ] Desktop-Variante des Core-Binaries (Build-Tag für lokale Audioausgabe) als Release-Artefakt für die Desktop-Pakete ([ADR-0005](adr/0005-core-in-desktop-builds.md)) (S)
- [ ] TTS-Schnittstelle und Anbieter: Browser-TTS im Overlay, Google Cloud TTS, Azure Speech, lokal Piper (M)
- [ ] TextToSpeech-Action (S)

### 7.4 OBS Studio

- [ ] OBS-Integration mit `goobs` (obs-websocket v5) (M):
  - Szenen, Quellen, Filter
  - Stream und Aufnahme, Replay-Buffer
  - OBS-Events
  - StreamingSoftware-Action

### 7.5 Weitere Widgets (P2/P3)

- [ ] Persistenter Timer, Stream Boss, Abspann, Rangliste (M) (P2)
- [ ] Glücksrad, Umfrage, Emote-Effekt, persistenter Emote-Effekt, Game Queue (L) (P2)
- [ ] Discord Reactive Voice (M) (P3)

**Exit-Kriterien (M3):**

- Eine Browserquelle zeigt Alerts für Follow, Abo und Cheer mit Bild, Ton und TTS.
- Ein Ziel-Widget aktualisiert sich live.
- Ein Command wechselt die OBS-Szene.

---

## Phase 8: Economy, Community, Spiele

| | |
|---|---|
| **Ziel** | Währungen, Ränge, Inventar, Community-Funktionen und Chatspiele |
| **Voraussetzungen** | Phase 6 |
| **Aufwand** | 8–11 PW (P1); 4–7 PW zusätzlich für P2 |
| **ADRs** | keine neuen |

### 8.1 Währungen und Ränge

- [ ] Spezifikation `docs/spec/economy.md` (S)
- [ ] Währungen (L):
  - Zuwachs pro Intervall für aktive Zuschauer, Mindestaktivität
  - Boni nach Rolle und Ereignis (Follow, Abo …)
  - Obergrenze, Reset-Intervalle
- [ ] Ränge: Schwellen, Rang-auf- und Rang-ab-Commands, Identifier für Rang, nächsten Rang und Position (M)
- [ ] Ranglisten per SQL (`$top…`) (S)
- [ ] Consumables-Action: hinzufügen, abziehen, setzen, übertragen; für einzelne Nutzer, alle oder eine Rolle (M)
- [ ] Requirements Währung und Rang (S)

### 8.2 Inventar und Shop

- [ ] Inventare und Items (Kauf- und Verkaufspreis, Höchstmenge), Shop-Commands, Inventar-Requirement (M)
- [ ] Stream Pass (M) (P2)
- [ ] Redemption Store (M) (P2)

### 8.3 Community

- [ ] Quotes: hinzufügen, löschen, zufällig, letztes, nach Nummer; mit Spiel und Datum (S)
- [ ] Giveaways (M):
  - Teilnahme per Command, Mehrfachlose, Rollenbedingungen
  - Laufzeit, Ziehung, Gewinnerbestätigung
- [ ] Game Queue: beitreten, verlassen, Position; Vorrang für Abonnenten; GameQueue-Action (M)
- [ ] Restliche vorgefertigte Commands: Quote-, Giveaway- und Alters-Commands, Nutzertitel, Konto verknüpfen (M)

### 8.4 Spiele

- [ ] Spezifikation `docs/spec/games.md` (S)
- [ ] Spiel-Framework (L):
  - Einsatz, Mindest- und Höchsteinsatz
  - Sammelphase
  - Wahrscheinlichkeiten und Auszahlungen
  - Status- und Ergebnis-Commands
- [ ] Welle 1: Roulette, Slot Machine, Duel, Heist, Trivia (L) (P1)
- [ ] Welle 2: Bet, Bid, Coin Pusher, Hangman, Hitman, Hot Potato, Lock Box, Russian Roulette, Spin, Steal, Treasure Defense, Volcano, Word Scramble (XL, je Spiel S–M) (P2)

**Exit-Kriterien (M4):**

- Die Währung verhält sich über eine simulierte Acht-Stunden-Session korrekt (Zuwachs, Boni, Ränge).
- Fünf Spiele sind spielbar.
- Giveaways, Queue und Quotes sind produktiv nutzbar.

---

## Phase 9: Weitere Plattformen

| | |
|---|---|
| **Ziel** | YouTube und Kick als vollwertige Plattformen; Multiplattform-Betrieb |
| **Voraussetzungen** | Phase 6; Go/No-Go je Plattform nach M2 (ADR-0004) |
| **Aufwand** | 8–11 PW (P1); 4–6 PW zusätzlich für Velora und VPZone (P3) |
| **ADRs** | 0014, 0015 |

### 9.0 Entscheidung

- [ ] Go/No-Go für YouTube und Kick nach M2 treffen und in einem ADR festhalten. Bei No-Go wandert die Plattform ins Backlog, und M5 entfällt bzw. schrumpft. (S)

### 9.1 Multiplattform

- [ ] Standardplattform, Senden an alle oder bestimmte Plattformen, Plattformfilter in Commands und Requirements, Rollen-Mapping (M)
- [ ] Gemeinsamer Webhook-Eingang: Routing, Signaturprüfung als Schnittstelle, Deduplizierung. Er wird von Kick, Diensten (Phase 10) und Webhook-Commands (Phase 11) genutzt. (M)

### 9.2 YouTube

- [ ] ADR-0015 Chat-Streaming; Anleitung für eigene Google-Cloud-Credentials (S)
- [ ] OAuth: Loopback + PKCE, Server-Callback, Einfügen des Codes als Fallback (M)
- [ ] Chat-Empfang (L):
  - Livestream-Erkennung
  - gRPC-Client für `liveChatMessages.streamList`, generiert aus `stream_list.proto`
  - quota-schonender Polling-Fallback
- [ ] Chat senden und löschen, Timeout und Bann, Mitgliedschaften, Super Chats und Super Stickers, Jewels (L)
- [ ] YouTube-Action, Events, Quota-Überwachung (M)

### 9.3 Kick

- [ ] ADR-0014 Eingehende Webhooks und Relay (S):
  - Server-Modus mit Reverse Proxy
  - Tunnel-Anleitung
  - Relay optional
- [ ] OAuth 2.1 + PKCE; Anleitung für eine eigene Kick-App (M)
- [ ] Webhook-Empfang: Kick-Signaturprüfung, Deduplizierung, erneutes Abonnieren nach automatischer Kündigung durch Kick (M)
- [ ] REST-Client (L):
  - Chat senden, Moderation, Kanal aktualisieren, Belohnungen
  - Events: Follow, Abos, Geschenke, Belohnungen, Kicks, Livestream-Status
- [ ] Kick-Action sowie Kick-Channel-Points- und Kick-Kicks-Commands (M)

### 9.4 Velora und VPZone (P3)

- [ ] Offizielle API-Dokumentation sichten, Aufwand schätzen, Go/No-Go (S)
- [ ] Adapter umsetzen (je L), falls Go

**Exit-Kriterien (M5):**

- Ein gleichzeitiger Stream auf Twitch, YouTube und Kick läuft mit gemeinsamen Commands und gemeinsamer Währung.
- Plattformübergreifend verknüpfte Nutzer werden korrekt zusammengeführt.

---

## Phase 10: Integrationen, Scripting, Agent

| | |
|---|---|
| **Ziel** | wichtigste Dienste anbinden, Skripte ermöglichen, Host-Fähigkeiten für Remote-Betrieb |
| **Voraussetzungen** | Phase 7; für Webhook-Dienste den Webhook-Eingang aus 9.1, sonst hier bauen |
| **Aufwand** | 4–6 PW (P1); 8–12 PW für P2 (Tier 2, Agent); 4–6 PW für P3 (Tier 3) |
| **ADRs** | 0016 |

### 10.1 Grundlagen

- [ ] Integrations-Registry: Konfigurationsschema, Status, Secrets, OAuth für Dienste, Anbindung an den Webhook-Eingang (M)
- [ ] Spike Socket.IO-Client (Kandidat `zishang520/socket.io`) gegen Streamlabs (S)
- [ ] Gemeinsames Spendenmodell (Betrag, Währung, Nachricht, Quelle) mit generischem Spenden-Event (S)

### 10.2 Tier 1 (P1)

- [ ] Discord: Webhook-Nachrichten, optional Bot (M)
- [ ] Streamlabs: Spenden (M)
- [ ] StreamElements: Spenden (M)
- [ ] Ko-fi per Webhook: Tipps, Mitgliedschaften, Shop (S)

### 10.3 Scripting (P1)

- [ ] ADR-0016 Scripting (S)
- [ ] `goja`-Sandbox (M):
  - Zeitlimit
  - kein Datei- oder Netzzugriff außer über freigegebene Funktionen
  - API für Parameter, Identifier und Chat
- [ ] Script-Action mit Tests (S)

### 10.4 Tier 2 (P2)

- [ ] VTube Studio, Voicemod, SAMMI, Lumia Stream, Streamlabs Desktop, Meld Studio (je S–M)
- [ ] Tiltify, Patreon, Fourthwall, Throne, TipeeeStream (je S–M)
- [ ] Streamloots und Crowd Control, jeweils mit eigenem Command-Typ (je M)
- [ ] PixelChat, IFTTT, Pulsoid, serielle Geräte mit `go.bug.st/serial` (je S)
- [ ] TTS: Amazon Polly, ElevenLabs (je S)
- [ ] 7TV-Emotes (S)

### 10.5 Tier 3 (P3)

- [ ] PolyPop, XSplit, T.I.T.S., VTS Pog, Veadotube, VConnect, RahiTuber, Mtion Studio (je S–M)
- [ ] DonorDrive, JustGiving, TreatStream, Rainmaker, Pally (je S)
- [ ] TTS Monster, Uberduck, ResponsiveVoice (je S)
- [ ] Alejo-Pronomen, Musik-Player (je S)

### 10.6 Agent (P2)

- [ ] Agent-Protokoll in der API umsetzen, Unterkommando `streamcrew agent`. Auch die Desktop-App nutzt es für Hotkeys und Eingabe ([ADR-0005](adr/0005-core-in-desktop-builds.md)). (M)
- [ ] Capabilities: Tastatur/Maus, globale Hotkeys, lokales Audio, externe Programme. Die Umsetzung ist plattformspezifisch, CGO bleibt auf den Agent beschränkt. (L)
- [ ] Hotkey-Konfiguration und Zuordnung zu Commands (S)

**Exit-Kriterien (M6 = Tier 1 + Scripting):**

- Spenden von Streamlabs, StreamElements und Ko-fi lösen Commands aus.
- Beim Stream-Start geht eine Discord-Nachricht raus.
- Die Script-Action ist produktiv nutzbar.

---

## Phase 11: Developer-API, MCP, Webhooks, Import

| | |
|---|---|
| **Ziel** | Öffnung für Drittanbieter und Migrationspfad |
| **Voraussetzungen** | Phase 6; Webhook-Eingang aus 9.1 |
| **Aufwand** | 3–4 PW (P1); 4–6 PW zusätzlich für P2 (Import) |
| **ADRs** | 0021 (Umsetzung) |

### 11.1 Offene Schnittstellen (P1)

- [ ] Developer-API (REST per Transcoding oder eigene Handler) mit generierter OpenAPI-Dokumentation (M)
- [ ] MCP-Server mit `modelcontextprotocol/go-sdk` (M):
  - Werkzeuge für Status, Chat, Commands, Counter, Währung, Inventar, Quotes, Ranglisten und Nutzer
  - Annotationen (read-only, destruktiv)
  - Token-Authentifizierung
- [ ] Webhook-Command-Typ: Endpunkt je Webhook, Secret oder Signatur, JSON-Pfade als Identifier (M)
- [ ] Command-Bundles teilen: Export und Import als Datei oder URL, optional signiert (S)

### 11.2 Import (P2)

- [ ] Nutzerimport aus CSV und XLSX (S)
- [ ] ADR-0021 Import von Mix-It-Up-Daten, Umsetzung auf Basis der Rechtsgrundlage aus Phase 0 (S)
- [ ] Importer für `.miubackup`, `.miu3` und `.db3` (XL, vor Beginn aufteilen):
  - Typ-Mapping (`$type` → Typ-ID, numerische Event-IDs → Event-Strings)
  - Commands, Actions, Requirements, Nutzer, Währungen, Quotes, Counter
  - Overlays, soweit abbildbar
  - Importbericht mit allem, was nicht übernommen wird (C#-Skripte, Tokens)
- [ ] Testdaten für den Import aus einer eigenen Testinstallation erzeugen, nicht aus fremden Beständen (S)
- [ ] Kompatibilitätsfassade für die Developer-API (Pfade `/api/v2/…`), nur nach rechtlicher Prüfung (M) (P3)

**Exit-Kriterien (M7):**

- Steuerung über die Developer-API (Stream-Deck-artig), über MCP-Clients und über eingehende Webhooks funktioniert.
- Command-Bundles lassen sich austauschen.

---

## Phase 12: Härtung und Core 1.0

| | |
|---|---|
| **Ziel** | Stabilität, Sicherheit, Performance, vollständige Dokumentation; API `v1` |
| **Voraussetzungen** | alle P1-Aufgaben der Phasen 8–11 |
| **Aufwand** | 3–4 PW |
| **ADRs** | 0020 (final) |

- [ ] Security-Review (M):
  - Prüfumfang: Capabilities, API-Authentifizierung, SSRF, Pfadzugriffe, Secrets, Abhängigkeiten
  - Threat Model in `docs/security.md`
  - ADR-0020 finalisieren
- [ ] Lasttests mit der Mock-Plattform (z. B. 100 Chatnachrichten/s, 100.000 Nutzer), Profiling mit `pprof`, Speicherbudget festlegen (M)
- [ ] Chaos-Tests: Netzabbrüche, Datenbanksperren, Absturz während eines Schreibvorgangs; Wiederanlauf und Backup-Verifikation (M)
- [ ] Upgrade-Test: Datenbanken aus M2 lassen sich auf 1.0 migrieren (S)
- [ ] API `v1` einfrieren, `buf breaking` verpflichtend machen, Deprecation-Policy festlegen (S)
- [ ] Dokumentation vervollständigen: Nutzer- und Betriebshandbuch, API-Referenz, Spezifikationen; EN und DE vollständig (L)
- [ ] Release-Prozess: Changelog, signierte Artefakte, SBOM, optionaler Update-Hinweis über den Release-Feed (M)

**Exit-Kriterien (M8 Core 1.0):**

- Alle P1-Aufgaben sind erledigt, und es gibt keine offenen kritischen Findings.
- Last-, Chaos- und Upgrade-Tests sind grün.
- API `v1` ist veröffentlicht.

---

## Desktop-Track (Repo `streamcrew-desktop`)

| | |
|---|---|
| **Ziel** | Desktop-App mit Fyne; startet den mitgelieferten Core oder verbindet sich remote; zugleich Agent |
| **Voraussetzungen** | M2 (API `v1alpha1`); jede Stufe braucht die entsprechenden Core-Funktionen |
| **Aufwand** | 16–26 PW |
| **ADRs** | [ADR-0005](adr/0005-core-in-desktop-builds.md) (Core als eigener Prozess), [ADR-0006](adr/0006-core-als-bibliothek-fuer-selbststart.md) (Selbststart, Build-Varianten), [ADR-0007](adr/0007-release-artefakte-des-cores.md) (Core aus dem Release); eigene ADRs im Desktop-Repo nach derselben Konvention |

### D0: Setup und Machbarkeit (1–2 PW)

- [ ] Repository, `go.mod` (Go 1.27), Fyne v2.8, CI, `fyne-cross`-Builds für Windows, macOS und Linux (M)
- [ ] Spike: generisches Formular aus dem JSON-Schema des Typkatalogs und verschachtelter Action-Editor (Baum, Umordnen). Das Ergebnis wird als ADR im Desktop-Repo festgehalten. (M)
- [ ] Spike: Emote-Darstellung im Chat (statisch und animiert) (S)
- [ ] Spike Paketierung (M):
  - beide Build-Varianten („mitgeliefert“, „eingebunden“) auf allen Zielplattformen bauen
  - vergleichen: Signatur und Notarisierung (macOS), Paketgröße, Virenscanner; in der Variante „eingebunden“ initialisiert der Core-Prozess keine Oberfläche und erzeugt kein Dock-Symbol
  - Aufwand und Stolpersteine dokumentieren
- [ ] Build-Varianten im Desktop-Build umsetzen: Build-Tag für die Startroutine, Taskfile/CI-Matrix; die Standardvariante je Plattform in der Build-Konfiguration festlegen, ohne ADR ([ADR-0006](adr/0006-core-als-bibliothek-fuer-selbststart.md)) (S)
- [ ] Core-Artefakte aus dem Release der in `go.mod` festgelegten Version beziehen und gegen die Prüfsummen prüfen: das Binary für „mitgeliefert“, die Bibliothek für „eingebunden“, offline per `GOPROXY=file://…` ([ADR-0007](adr/0007-release-artefakte-des-cores.md)) (S)

### D1: Verbindung und Grundgerüst (3–4 PW)

- [ ] Lokaler Modus ([ADR-0005](adr/0005-core-in-desktop-builds.md)) (L):
  - einen laufenden Core über Laufzeitdatei und Health-Check finden
  - sonst den Core losgelöst starten, je nach Build-Variante das mitgelieferte Binary oder sich selbst mit verstecktem Unterkommando ([ADR-0006](adr/0006-core-als-bibliothek-fuer-selbststart.md))
  - Versions-Handshake, Neustart mit Backoff, Statusanzeige
  - Einstellung „Core mit der App beenden“
- [ ] Remote-Modus und Profilwahl (S)
- [ ] Login-Flows (Browser öffnen, Device-Code anzeigen) und Prompt-Dialoge (S)
- [ ] Navigation, helles und dunkles Theme, i18n EN/DE, Systemtray, Autostart (M)

### D2: Dashboard und Chat (2–3 PW)

- [ ] Status und Stream-Infos; Titel und Kategorie bearbeiten (S)
- [ ] Mehrplattform-Chat mit Emotes, Moderationsmenü, Nutzer-Popup (L)
- [ ] Event-Feed und Command-Queue (M)

### D3: Commands (3–5 PW)

- [ ] Listen je Command-Art, Suche, Gruppen, Aktivieren (M)
- [ ] Generischer Action- und Requirement-Editor aus dem Typkatalog; Spezialeditoren für Conditional und Overlay (L–XL)
- [ ] Testlauf mit Test-Identifiern, Import und Export (S)

### D4: Nutzer, Economy, Community (2–3 PW)

- [ ] Nutzerliste und -details, Währungen, Ränge, Inventar, Shop, Giveaways, Queue, Quotes, Spiele-Einstellungen (L)

### D5: Overlays (2–3 PW)

- [ ] Endpunkte, Widget-Verwaltung, Positionierung (L)
- [ ] Vorschau im externen Browser; Fyne hat keine eingebaute Web-Ansicht, der Stand wird in D0 geprüft (S)

### D6: Integrationen und Einstellungen (1–2 PW)

- [ ] Integrationsseiten aus dem Schema, Einstellungen, Backups, Logs (M)

### D7: Agent und Release (2–3 PW)

- [ ] Agent-Fähigkeiten über das Agent-Protokoll (Phase 10.6): globale Hotkeys, Tastatur- und Mausaktionen. Lokales Audio spielt der Core selbst. (L)
- [ ] Paketierung je Plattform in der gewählten Build-Variante („mitgeliefert“ mit zwei Binaries oder „eingebunden“ mit einem), Signierung (Windows, macOS), Notarisierung (macOS), Update-Hinweis (M)

**Exit-Kriterien:** Die Desktop-App deckt alle P1-Funktionen des Cores ab, lokal mit mitgeliefertem Core und remote. Ein Absturz oder das Schließen der App beendet den Core nicht, außer der Nutzer wünscht es. Die Pakete für die Zielsysteme sind signiert.

---

## Web-Track (Repo `streamcrew-web`)

| | |
|---|---|
| **Ziel** | Weboberfläche für Server- und LAN-Betrieb, später auch für Moderatoren |
| **Voraussetzungen** | M2 (API `v1alpha1`) |
| **Aufwand** | 16–20 PW |
| **ADRs** | 0017 im Core-Repo; weitere im Web-Repo |

### W0: Entscheidung und Setup (1–2 PW)

- [ ] ADR-0017 Web-Technologie; Kandidaten: TypeScript-SPA (Svelte 5 oder React), Go + templ + htmx, Go-WASM (S)
- [ ] Repository, Build, TS-Client aus den Protos (`@connectrpc/connect-web`), CI (M)
- [ ] Auslieferung festlegen: statisch vom Core unter `/ui` (optional eingebettet) oder separat hinter einem Reverse Proxy (S)

### W1: Grundgerüst (2 PW)

- [ ] Token-Login, Layout, Navigation, Theme, i18n EN/DE (M)
- [ ] Verbindungsstatus, Prompt-Dialoge, Login-Flows der Plattformen (M)

### W2: Dashboard und Chat (2 PW)

- [ ] Status, Stream-Infos, Mehrplattform-Chat mit Emotes und Moderation, Event-Feed, Queue (L)

### W3: Commands (3–4 PW)

- [ ] Generische Editoren aus dem Typkatalog, Umordnen per Drag-and-drop, Spezialeditoren (L–XL)
- [ ] Testlauf, Import und Export (S)

### W4: Overlays (2–3 PW)

- [ ] Visueller Overlay-Editor mit Live-Vorschau im iframe (dieselbe Runtime wie in OBS) (L)

### W5: Nutzer, Economy, Community (2 PW)

- [ ] Nutzer, Währungen, Ränge, Inventar, Shop, Giveaways, Queue, Quotes, Spiele (L)

### W6: Integrationen, Einstellungen, Mehrbenutzer (2–3 PW)

- [ ] Integrationsseiten aus dem Schema, Einstellungen, Backups, Logs (M)
- [ ] Moderatoren-Zugänge mit eingeschränkten Token-Scopes (M) (P2)

### W7: Qualität und Release (2 PW)

- [ ] Playwright-E2E für die kritischen Pfade (M)
- [ ] Barrierefreiheit nach WCAG 2.2 AA, Performance-Budget (M)
- [ ] Container- bzw. Static-Hosting-Artefakte (S)

**Exit-Kriterien:** Die Weboberfläche deckt alle P1-Funktionen des Cores ab, die E2E-Tests sind grün und die Barrierefreiheit ist geprüft.

---

## Relay-Track (optional, Repo `streamcrew-relay`)

| | |
|---|---|
| **Ziel** | Webhooks für Desktop-Nutzer ohne öffentliche URL (Kick, Spendendienste) |
| **Voraussetzungen** | ADR-0014 (Phase 9) |
| **Aufwand** | 2–3 PW |

### R0: Entscheidung

- [ ] In ADR-0014 festlegen, ob der Relay gebaut wird und wer ihn betreibt (selbst gehostet oder vom Projekt) (S)

### R1: MVP

- [ ] Webhook-Empfang für Kick und Dienste mit Signaturprüfung (M)
- [ ] Weiterleitung per WebSocket an registrierte Cores (Token pro Core), keine Speicherung von Nutzdaten (M)

### R2: Betrieb

- [ ] Container-Image, Rate-Limits, Monitoring, Datenschutzhinweise, Self-Hosting-Doku (M)

---

## Gate O: Open-Sourcing (Veröffentlichung)

| | |
|---|---|
| **Ziel** | Voraussetzungen schaffen, damit der Projektinhaber die Repositories rechtlich und organisatorisch abgesichert öffentlich schalten kann |
| **Voraussetzungen** | frühestens M2; Namensprüfung für `streamcrew` abgeschlossen (Backlog ADR-0024); Zeitpunkt entscheidet der Projektinhaber |
| **Aufwand** | 1–2 PW, dazu externe Rechtsprüfung |
| **ADRs** | 0001, 0002; neues ADR zu den Beitragsregeln (DCO oder CLA) |

### O.1 Recht und Herkunft

- [ ] Rechtliche Prüfung abgeschlossen: BSL §3.1–3.5, Urheberrecht (abgeleitetes Werk), Marken, Import-Funktion (extern)
- [ ] Herkunfts-Review (M):
  - Alle Spezifikationen haben einen Quellennachweis.
  - Stichproben des Go-Codes werden mit dem Original verglichen: keine übernommene Struktur, keine Bezeichner ohne Interop-Grund, keine Kommentare oder Texte.
  - Alle Assets und Texte sind eigenständig.
- [ ] Namensprüfung abgeschlossen und endgültigen Namen als ADR festgehalten (Backlog ADR-0024): `streamcrew` bestätigen oder umbenennen; Logo prüfen (S)
- [ ] Git-Historie geprüft: keine Secrets, keine kopierten Fremdinhalte; bei Bedarf bereinigt (S)

### O.2 Lizenz und Community

- [ ] SPDX-Header in allen Quelldateien vorhanden; `NOTICE` mit den Drittkomponenten aus `go.mod` erzeugt (S)
- [ ] Beitragsregeln entscheiden (DCO oder CLA) und in `CONTRIBUTING.md` festhalten (S)
- [ ] `CODE_OF_CONDUCT.md`, `SECURITY.md` (Meldeweg für Sicherheitslücken), Issue- und PR-Vorlagen (M)
- [ ] README für die Öffentlichkeit: Ziel, Status, Abgrenzung zu Mix It Up (keine Verbindung, keine Marken) (S)

### O.3 Veröffentlichung

- [ ] Der Projektinhaber schaltet die Repositories selbst öffentlich; weder die CI noch Dritte oder KI-Assistenten tun das (S)
- [ ] Nach dem Umschalten Branch-Schutz und Security-Features der Plattform aktivieren (S)
- [ ] Erstes öffentliches Release mit Changelog (S)

**Exit-Kriterien:**

- Der Projektinhaber hat die Repositories öffentlich geschaltet.
- Rechtliche Prüfung und Herkunfts-Review sind dokumentiert, ohne offene Befunde.

---

## Querschnittsaufgaben (laufend)

Diese Punkte gelten dauerhaft und werden nicht abgehakt:

- Sichtbarkeit (ADR-0001, ADR-0009): Alle Repositories bleiben privat. Öffentlich schaltet sie ausschließlich der Projektinhaber.
- Herkunft (ADR-0001): Jede Spezifikation nennt ihre Quellen. Code aus `../mixitup` wird nie übernommen oder übersetzt, auch nicht per KI-Assistent.
- Lizenz (ADR-0002): Neue Dateien tragen den SPDX-Header, neue Abhängigkeiten sind MIT-kompatibel.
- ADRs werden geschrieben, **bevor** eine Entscheidung umgesetzt wird. Der Index in `docs/adr/README.md` wird gepflegt.
- Dokumentation (README, `docs/`, Code-Kommentare) wird mit jeder Änderung aktualisiert.
- Roadmap: Checkboxen abhaken, Statusübersicht und Änderungshistorie pflegen, Aufwände nach jedem Meilenstein kalibrieren.
- Sicherheit: `govulncheck`, Dependency-Updates, keine Secrets in Repo und Logs. Findings ohne Fix-Version werden gemeldet, nicht ignoriert.
- i18n: Neue Texte entstehen immer auf Englisch und Deutsch.
- Tests: Neue Features kommen nur mit Tests; der Race-Detector läuft in der CI.
- Performance-Budgets im Blick behalten: Startzeit, Speicher, Latenz von Chat-Nachricht zu Command-Start.
- Plattform-Changelogs (Twitch, YouTube, Kick, OBS) regelmäßig auf Abkündigungen prüfen.

---

## Backlog: später oder nicht geplant

**P3 (nur bei Bedarf):**

- Velora und VPZone
- Integrationen aus Tier 3
- Discord Reactive Voice
- Musik-Player, Alejo-Pronomen
- Kompatibilitätsfassade für die Developer-API

**Ideen ohne Priorität:**

- Mehrere gleichzeitig aktive Profile
- Plugin-System für Integrationen (z. B. per WASM)
- Geteilter Katalog für Command-Bundles
- Mobile Moderations-App auf Basis der Web-API

**Nicht geplant (Plan §4.3):**

- Nachbau der WPF-Oberfläche
- C#-Skripte
- Funktionen, die Blazing-Cacti-Server brauchen
- Mixer, Trovo, Glimesh, Facebook, Twitter, OvrStream, InfiniteAlbum
- Overlay v1/v2
- Inoffizielle Schnittstellen (Edge-TTS, TikTok-TTS, Kick-Pusher)
- SaaS-Betrieb für Dritte

---

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-27 | Erstfassung auf Basis von `starting.md` und dem Audit von Mix It Up v1.8.200 |
| 2026-09-27 | Entscheidungen eingearbeitet: Neuimplementierung mit Code als Hilfestellung (ADR-0001), MIT (ADR-0002), Streaming-PC primär und Server sekundär (ADR-0003), Start nur mit Twitch (ADR-0004). ADR-Backlog umnummeriert (Entstehungsreihenfolge), Gate O ergänzt, lokale Audioausgabe auf P1, Go/No-Go für YouTube und Kick nach M2. |
| 2026-09-27 | Tote ADR-Verweise in ADR-0001 bis ADR-0003 korrigiert. Verweis-Konvention in `adr/README.md` festgehalten, Backlog-Nummern als vorläufig gekennzeichnet, Linkprüfung für die CI ergänzt. |
| 2026-09-27 | ADR-0005 akzeptiert: Die Desktop-App liefert den Core mit und startet ihn als eigenen Prozess. Backlog-Nummern ab 0006 um eins verschoben; API-ADR auf das Protokoll reduziert; Bibliotheksfrage als Backlog-Eintrag ergänzt; Desktop-Track angepasst (Prozessverwaltung, Paketierungs-Spike, Agent). |
| 2026-09-27 | ADR-0006 akzeptiert: Core zusätzlich als Bibliothek mit schmaler Start-API, nur für den Selbststart (macOS: ein Binary), kein Betrieb im selben Prozess. Der Backlog-Eintrag zur Bibliothek entfällt; die übrigen Backlog-Nummern ab 0006 sind um eins verschoben. Phase 6 und Desktop-Track (D0, D1, D7) angepasst. |
| 2026-09-27 | ADR-0006 berichtigt: Die Auslieferungsart (mitgeliefert oder eingebunden) legt der Build-Prozess fest; die zuvor notierte feste Zuordnung zu Plattformen war eine Fehlinterpretation. Desktop D0, D1 und D7 angepasst. |
| 2026-09-27 | ADR-0007 akzeptiert: Jedes Core-Release enthält Binary und Bibliothek (Go-Modul als Quellarchiv, da Go keine vorkompilierten Go-Bibliotheken einbindet). Desktop-Builds beziehen den Core aus dem Release, statt ihn selbst zu bauen. Backlog-Nummern ab 0007 um eins verschoben. Phase 6 (Release-Pipeline) und Desktop D0 angepasst. |
| 2026-09-28 | ADR-0008 akzeptiert: Codename `streamcrew`. Platzhalter in Plan, Roadmap und ADRs ersetzt; Namensprüfung als Aufgabe vor Gate O (Backlog ADR-0024). |
| 2026-09-28 | ADR-0009 bis ADR-0011 akzeptiert: drei Repositories auf GitHub (privat, Actions, Releases), API mit ConnectRPC und Protobuf, keine Telemetrie. MVP-Umfang bestätigt. Backlog-Nummern angepasst (Datenschutz-Eintrag wurde ADR-0011; bisherige 0011–0019 um eins verschoben). Phase 0/1 abgehakt, Diagnose-Paket (Phase 6) und `GOPRIVATE`/CI-Token (Phase 1) ergänzt. |
| 2026-09-28 | Präzisiert: Das Projekt bleibt privat, bis der Projektinhaber es selbst öffentlich schaltet; Gate O ist Voraussetzung, nicht Auslöser. ADR-0001, ADR-0008 und ADR-0009 mit Präzisierungsvermerk angepasst; Gate O und Querschnittsaufgaben ergänzt; die Frage nach dem Zeitpunkt ist damit beantwortet. |
| 2026-09-28 | Privates Repository `ripmav/streamcrew` angelegt: `LICENSE` als erster Commit auf `main`, Dokumente per Pull Request. Branch-Schutz im aktuellen GitHub-Plan für private Repos nicht verfügbar; Roadmap-Aufgabe entsprechend ergänzt. |
| 2026-09-28 | Claude-Code-Workflows umgestellt: Claude reagiert nur auf `@claude` in Pull Requests und führt dann ein Review aus; das automatische Review bei jedem PR und die Reaktion auf Issues entfallen. Plan §11.3 ergänzt. |
| 2026-09-28 | `docs/spec/README.md` (Regeln, Ablauf, Index) und `docs/spec/TEMPLATE.md` angelegt. Automatisches Löschen gemergter Branches in ADR-0009, Plan §11.1 und Roadmap 0.4 dokumentiert. |
| 2026-09-28 | Claude-Review nachgebessert (PR #5): `gh`-Werkzeuge für den Review-Befehl freigegeben; jede `@claude`-Anfrage bekommt einen Fortschrittskommentar, der mit dem Ergebnis aktualisiert wird, auch ohne Befunde. |
| 2026-09-28 | Claude-Review-Diagnose (PR #6): Im zweiten Test wurde erneut ein Werkzeugaufruf verweigert, das Review lief also nicht wirklich. Das Log zeigt jetzt die Namen der benutzten und verweigerten Werkzeuge; der Ergebniskommentar meldet dann „unvollständig“ statt „keine Befunde“. |
| 2026-09-28 | Ursache gefunden (Diagnose in PR #7): Claude Code führt den Plugin-Befehl über das Werkzeug `Skill` aus, und genau dieses war nicht freigegeben. Freigabe gezielt nur für `Skill(code-review:code-review)`; die Diagnose zeigt jetzt auch Skill-Namen. |
