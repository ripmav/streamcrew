# Plan: 6.6 On-the-fly-Konfiguration (Server-Modus)

Roadmap: [6.6](../../roadmap.md#65-on-the-fly-konfiguration-server-modus). Ziel: Alle Starteinstellungen
(Flags, `STREAMCREW_*`-Env, `config.yaml`) lassen sich im **Server-Modus zur Laufzeit über die TUI** ändern.
Die TUI schreibt die Konfigurationsdatei; der Core nimmt die Änderung ohne Neustart in ≤ 1 s wahr (Watcher)
und wendet alle hot-capablen Einstellungen an.

## 1. Ist-Zustand (Audit)

Die Starteinstellungen kommen aus Flag > Env > Datei > Default (Code-ADR-0005). `config.Watcher`
(Code-ADR-0019, Punkt 5) vergleicht die Datei alle `ReloadInterval` (1 s) und wendet **nur**
`grant`, `revoke`, `file-root`, `outbound-allow` live an; jede andere geänderte Einstellung wird
nur als „applies after a restart“ geloggt. Vom Flag oder der Env fixierte Werte bleiben fix —
die Datei darf sie nicht ändern.

| Einstellung | hot heute? | hot nach 6.6? | Mechanismus |
|---|---|---|---|
| `grant`, `revoke`, `file-root`, `outbound-allow` | ja | ja | Watcher (Code-ADR-0019), unverändert |
| `log-level`, `log-component-level` | nein | **ja** | gemeinsame Level-Store, die jeder levelHandler pro Record liest |
| `log-format`, `log-file`, `log-max-size`, `log-max-files` | nein | **ja** | Ausgabe-Handler des Logger-Baums per Slot austauschen |
| `dry-run` | nein | **ja** | Dry-Run-Wrapper wird immer gebaut, per Flag umgeschaltet |
| `dev` | nein | **ja** | pprof-Routen bleiben gemountet, dispatchen per Flag |
| `cors-allow` | nein | **ja** | CORS-Check liest austauschbare Allowlist |
| `listen`, `tls-cert`, `tls-key` | nein | **ja** | Rebind: neuer Listener, sanfte Übergabe, alter Bind bleibt bei Fehlern |
| `mode` | nein | **ja** | Host-Rechte werden neu berechnet (Code-ADR-0019) |
| `shutdown-timeout` | nein | **ja** | Wert wird beim Shutdown aus einem gemeinsamen Ort gelesen |
| `data-dir`, `profile`, `config` | nein | **nein** | braucht Restart (offene DB, Logdateien, Prozessdateien) — dokumentiert |

## 2. Architektur

```
TUI (Ansicht 8: Einstellungen)
  │  GetConfig → Dokument + hot/fixed/restart-Listen
  │  UpdateConfig → neues YAML-Dokument
  ▼
API ConfigService (Admin, nur Server-Modus)
  ▼
app: Dokument validieren → config.yaml atomar schreiben
  ▼
config.Watcher (1-s-Polling, wie heute)
  ├─ Rechte → Live.Rights (unverändert)
  └─ neue Applies → Callbacks der App:
      Log → logging.Dynamic.Update
      dry-run → atomic.Bool
      dev / cors → httpserver
      listen/tls → httpserver.Rebind
      mode → Rechte neu berechnen
      shutdown-timeout → gemeinsamer Wert
```

Keine neue CLI-Oberfläche: Der TUI ist der einzige Pfad (Anforderung). In Daemon/Desktop-Modus
meldet die `ConfigService` „nur im Server-Modus“; die TUI zeigt das Dokument read-only.

## 3. 6.6.1 Hot-Apply-Engine + Code-ADR-0021 (PR 1)

### 3.1 `internal/logging` — hot Logger

- `LevelStore`: `sync.RWMutex` um Level + ComponentLevels; `Set(level, levels)`,
  `LevelFor(component)`, `Min()`.
- `levelHandler` hält die `*LevelStore` und den eigenen Component-Namen (wie heute aus
  `WithAttrs` erkannt) und liest das Level **pro `Enabled`-Aufruf** — Level-Änderungen sind
  damit sofort in allen Komponenten gültig.
- `Dynamic`: baut den Logger. Die Basis (MultiHandler aus Konsole + Datei) liegt hinter einem
  geteilten Slot (`*slog.Handler`); abgeleitete Handler (`WithAttrs`/`WithGroup`) referenzieren
  den Slot und leiten bei Bedarf neu ab. `Dynamic.Update(cfg)` baut die Basis neu (neue
  Rotationsdatei, neues Format), schließt die alte Datei und stellt den Slot um.
- `NewDynamic(cfg)` daneben zur `New` (die bleibt für Nicht-App-Nutzer und Tests).

### 3.2 `internal/dryrun` — umschaltbarer Wrapper

- `NewSwitchablePlatform(p, log, on *atomic.Bool)`: wie `NewPlatform`, aber `Chat()` und
  `Moderation()` delegieren bei `!on.Load()` an die innere Plattform; `Run`, `Status`,
  `Channel`, `Users` etc. wie heute.
- Umschaltbare `Ops`: Wrapper, der jede `twitchaction.Ops`-Methode je Aufruf an real oder dry
  delegiert.
- Die App baut den Wrapper **immer** (Flag initial aus `cfg.DryRun`), nicht nur bei Start.

### 3.3 `internal/httpserver` — Rebind, dev, CORS

- `Server` hält den aktiven `*http.Server` + `net.Listener` + `Config` hinter einem Mutex;
  `Run` wartet auf ctx.
- `Rebind(ctx, cfg)`: neuen Listener öffnen (TLS: Key-Pair vorher laden), neuen `http.Server`
  auf dem **gleichen** Handler starten, alten Server graceful shut down, dann Zustand tauschen.
  Jeder Fehler ⇒ alter Bind bleibt aktiv, Fehler zurück. `Addr()`/`Listening()` bleiben gültig.
- `Dev` wird zum `atomic.Bool`: pprof-Routen sind immer gemountet, antworten ohne Dev mit 404.
  `SetDev(bool)`.
- CORS: `CrossOriginProtection` erhält eine `func() []string` (Allowlist-Funktion);
  `Server.SetCorsAllow([]string)` tauscht sie.
- `ShutdownTimeout` wird beim Shutdown aus einer `func() time.Duration` gelesen.

### 3.4 `supervisor` / `engine` — Shutdown-Timeout

- `WithShutdownTimeoutFunc(func() time.Duration)` ergänzen (der starre Wert bleibt als Default);
  die App übergibt eine Funktion, die den aktuellen Wert liest.

### 3.5 `config.Watcher` — Applies

- `Watcher` erhält `Applies *ApplyFuncs` (nil ⇒ nur Rechte, wie heute — Desktop/Tests):
  ```go
  type ApplyFuncs struct {
      Log              func(LogConfig) error
      DryRun           func(bool)
      Dev              func(bool)
      CorsAllow        func([]string)
      Listen           func(addr, tlsCert, tlsKey string) error // Rebind
      Mode             func(Mode) error                         // Rechte neu berechnen
      ShutdownTimeout  func(time.Duration)
  }
  ```
- Pro Einstellung gilt die heute übliche Regel: nur anwenden, wenn `fromFile` (nicht per
  Flag/Env fixiert). `mode` fließt in die Rechte-Berechnung ein (neues Basis-Set + Grant/Revoke
  aus der Datei). Geänderte Werte der nicht-hot Settings landen wie heute in der
  restart-Liste; zusätzlich `data_dir`, `profile`, `config` immer.
- Jede Anwendung wird geloggt (`setting`, `value`, gekürzt); Fehlschlag ⇒ loggen, Zustand bleibt.

### 3.6 `internal/app` — Verdrahtung

- `newLogger` baut `logging.NewDynamic`; die App hält `*logging.Dynamic` + den Console-Writer.
- `dryrun.NewSwitchablePlatform` + umschaltbare Ops; `a.dryRun atomic.Bool`.
- `httpserver.Rebind`, `SetDev`, `SetCorsAllow`, Timeout-Funk; supervisor/engine Timeout-Funk.
- `ApplyFuncs` mit den App-Callbacks; Rebind aktualisiert zusätzlich die runtime-Datei.
- TLS/Listen-Änderung in Desktop-Modus (Unix-Socket-Default): erlaubt, funktioniert über denselben Pfad.

### 3.7 Code-ADR-0021

`docs/adr/code/0021-on-the-fly-konfiguration.md`: Status Akzeptiert; die Aufteilung
hot / restart / fix und die Gründe (DB-Handle, Logdateien, Prozess-Identität); Bezug zu
Code-ADR-0005 (Präzedenz bleibt; fixierte Werte bleiben fix) und Code-ADR-0019 (Watcher wird
vom Rechte- auf den Hot-Apply-Watcher erweitert).

### 3.8 Tests (PR 1)

- logging: Level-Änderung wirkt in abgeleiteten Komponenten ohne Neuaufbau; Format-/
  Rotations-Änderung wechselt die Ausgabe; alte Datei wird geschlossen.
- dryrun: Umschaltung an/aus leitet korrekt weiter (Chat + Moderation + Ops).
- httpserver: Rebind auf neuen Port (alter Port bleibt bis Übergabe), Rebind-Fehler lässt alten
  Bind; Dev-Toggle 404/200; CORS-Tausch.
- Watcher: Datei-Änderung ruft die Applies in der richtigen Reihenfolge auf; fixierte
  Einstellungen (fromFile=false) werden nicht angewendet; nicht-hot Änderungen landen in der
  restart-Liste; modus-Basis-Rechte werden neu berechnet.
- App-Test: Log-Level-Änderung über den Watcher ist in der App sichtbar; dry-run-Umschaltung
  ändert das Verhalten der Ops.

## 4. 6.6.2 `ConfigService` (PR 2)

### 4.1 Proto `api/proto/streamcrew/v1alpha1/config.proto`

```proto
service ConfigService {
  rpc Get(GetConfigRequest) returns (GetConfigResponse);
  rpc Update(UpdateConfigRequest) returns (UpdateConfigResponse);
}
message GetConfigRequest {}
message GetConfigResponse {
  string document = 1;    // gültige Konfiguration im Format der Konfigurationsdatei (YAML)
  repeated string hot = 2;      // gelten ohne Restart
  repeated string fixed = 3;    // per Flag/Env fixiert, live nicht änderbar
  repeated string restart = 4;  // gelten erst nach Restart
  bool live = 5;         // Live-Konfiguration verfügbar (Server-Modus + Datei)
}
message UpdateConfigRequest { string document = 1; }
message UpdateConfigResponse {
  repeated string applied = 1;   // sofort/gleichzeitig angewandt
  repeated string restart = 2;   // erst nach Restart
  string document = 3;           // das gespeicherte Dokument
}
```

`FileView` (und damit `config show`) ergänzt um `tls_cert`, `tls_key`, `cors_allow`
(`omitempty`), damit das Dokument vollständig ist.

### 4.2 Scope + Guard

- `scopes.go`: `ConfigService/Get` → Admin (Dokument enthält Pfade; Read reicht nicht,
  weil das Dokument Server-Einteile zeigt), `ConfigService/Update` → Admin.
- Guard: nur `Mode == server`; sonst `FailedPrecondition` („Live-Konfiguration ist nur im
  Server-Modus verfügbar“). `live` im Get-Response erlaubt der TUI das Read-only-Dokument.

### 4.3 `Update`-Validierung

- Dokument = vollständiges Dateiformat (Teilmengen erlaubt, wie heute `config.yaml`).
- Unbekannte Keys ⇒ Fehler (wie Datei-Laden).
- In das Startup-Cfg kopiert (Datei-Werte über Default, fixierte Werte bleiben) und `Resolve()`
  laufen; Fehler ⇒ Dokument wird **nicht** geschrieben.
- Atomares Schreiben: tmp + `Rename`, 0600. Bestehende Datei mit gleichem Inhalt ⇒ kein
  Write, `applied` leer.
- Antwort: `applied` = die geänderten hot-Einstellungen (der Watcher wendet sie an),
  `restart` = die geänderten nicht-hot (inkl. der immer-restart `data_dir`/`profile`/`config`).

## 5. 6.6.3 TUI-Ansicht 8 (PR 3)

- `views` um `"settings"`; Taste `8`; Hilfe erweitern.
- `settingsView`: Dokumentzeilen mit Annotation pro Einstellung: `⚡` hot, `⟳` Restart,
  `✋` fixiert; Kopfzeile mit `live`-Status. Nicht-Server-Modus: „read-only“.
- `onViewEnter`: `GetConfig`. `e`: Dokument in tmp-Datei, `$EDITOR` (wie Commands), danach
  `UpdateConfig`; Ergebniszeile (applied/restart/Fehler). `r`: neu laden.
- Keys in der Ansicht sind nur `e`, `r`, `Esc` (Zurück zum letzten?) — ansonsten wie die
  anderen Ansichten keine Eingabe.

### Tests

- In-process Core im Server-Modus mit `config.yaml`: `Get` zeigt das Dokument mit korrekten
  Listen; `Update` ändert `log_level`, der Watcher wendet an (über Log oder Status
  nachweisbar); `Update` mit unbekanntem Key wird abgelehnt und die Datei bleibt unangetastet.
- TUI: Ansicht rendert das Dokument; `r` lädt neu; nicht-Server-Modus zeigt read-only.

## 6. 6.6.4 Dokumentation (PR 4)

- `docs/usage.md`: Abschnitt „On-the-fly-Konfiguration (Server-Modus)“: TUI-Ansicht 8,
  was hot ist, was Restart braucht, fixierte Werte, `config show` bleibt der Befehl für die
  effektive Konfiguration.
- Roadmap-Checkboxen 6.6 abhaken; Historie-Einträge je PR.

## 7. Reihenfolge (Stack)

| PR | Inhalt | Branch |
|---|---|---|
| 1 | 6.6.1 Engine + ADR-0021 + Tests | `feat/onthefly-engine` |
| 2 | 6.6.2 ConfigService + Tests | `feat/onthefly-api` |
| 3 | 6.6.3 TUI-Ansicht 8 + Tests | `feat/onthefly-tui` |
| 4 | 6.6.4 Docs | `docs/onthefly-usage` |

Jeder PR: `go fix`, `gofmt`, `go vet`, `golangci-lint`, `go test` (inkl. Race), Test-Matrix
unter `docs/tests/I-6.6-*.md`.

## 8. Bewusst nicht enthalten

- Kein Profil-Wechsel zur Laufzeit (DB-Handle, Commands, Timer, Plattform würden neu gebaut —
  eigener Punkt, wenn gewünscht).
- Kein Live-Wechsel von `data-dir`/`config` (Prozess-Dateien).
- Keine GUI-Settings (Phase 7, Fyne).
- Kein Watcher via inotify: Polling bleibt (Cross-Platform, simple, 1 s ist gut genug).
