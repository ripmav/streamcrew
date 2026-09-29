# Code-ADR-0003: Fehler und Logging

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | [ADR-0011](../0011-keine-telemetrie.md); Plan §6.21, §8, §11.1; Roadmap Phase 1.2 und 1.3; [Code-ADR-0001](0001-go-toolchain-und-linting.md), [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0004](0004-nebenlaeufigkeit-und-supervisor.md), [Code-ADR-0005](0005-konfiguration.md) |

## Kontext

- Plan §6.21 legt `log/slog` fest: Text oder JSON, Attribute wie `component`, `platform` und `command_id`, Ausgabe auf Konsole, in eine Datei mit Rotation und später in den Log-Stream der API. Die Rotation soll ein Code-ADR regeln (Plan §8).
- [ADR-0011](../0011-keine-telemetrie.md) schließt Telemetrie aus. Die Fehlersuche stützt sich deshalb auf lokale Logs und ein Diagnose-Paket. Die Logs müssen maschinenlesbar sein und dürfen keine Secrets enthalten.
- Der Core hält Tokens für Plattformen und Dienste. Eine versehentliche Ausgabe in Logs ist das naheliegendste Leck.
- Plan §11.1 verlangt Fehler-Wrapping mit `%w`, Prüfung mit `errors.Is` bzw. `errors.AsType` und kein `panic` zur Steuerung.

## Entscheidung

### Fehler

1. **Standardbibliothek:** `errors` und `fmt.Errorf("<vorgang>: %w", err)`; mehrere Fehler mit `errors.Join`. Kein `github.com/pkg/errors` und keine andere Fehlerbibliothek, keine Stacktraces in Fehlerwerten.
2. **Meldungen** sind englisch, klein geschrieben, ohne Satzzeichen am Ende und nennen den Vorgang, nicht das Scheitern: `open profile "main": permission denied`, nicht `failed to open profile.`
3. **Prüfbare Fehler:**
   - Sentinel-Fehler `ErrXxx` für Zustände, auf die Aufrufer reagieren
   - Fehlertypen `XxxError`, wenn Aufrufer Daten brauchen
   - Aufrufer prüfen mit `errors.Is` und `errors.AsType`, nie über den Text. `errname` und `errorlint` setzen das durch.
4. **Übersetzung an Schichtgrenzen:** Treiber- und Adapterfehler werden zu Domänenfehlern (z. B. `sql.ErrNoRows` → `ErrNotFound`), die API übersetzt Domänenfehler zentral in Connect-Codes (Phase 6).
5. **Behandeln oder zurückgeben, nie beides.** Geloggt wird einmal, an der Grenze, die den Fehler behandelt: der Supervisor für Runnables, die API- bzw. HTTP-Schicht für Anfragen, `main` für den Prozess.
6. **`panic`** nur für Programmierfehler beim Start (`Must…` mit konstanten Eingaben). Panics in Runnables fängt der Supervisor ab und behandelt sie als Fehler mit Stacktrace im Log ([Code-ADR-0004](0004-nebenlaeufigkeit-und-supervisor.md)). Panics in HTTP-Handlern fängt `net/http` ab; das Server-Log geht über slog.
7. **Exit-Codes** von `streamcrew`: `0` Erfolg, `1` Laufzeitfehler, `2` ungültige Nutzung oder Konfiguration. `main` gibt den Fehler einmal auf stderr aus. *Präzisiert am 2026-09-29: Das übernimmt `cli.Run` in `internal/cli`; `main` beendet den Prozess mit dem zurückgegebenen Code.*

### Logging

8. **Nur `log/slog`**, kein zap, zerolog oder logrus. Die Composition Root erzeugt den Logger aus der Konfiguration und reicht ihn als `*slog.Logger` weiter, mit dem Attribut `component` je Komponente ([Code-ADR-0002](0002-dependency-injection.md)).
   - Code ruft nie die globalen Funktionen (`slog.Info`, `slog.Default()`) auf.
   - `serve` setzt den Logger einmal mit `slog.SetDefault`, damit Ausgaben von Abhängigkeiten über das Paket `log` in denselben Handlern landen. `http.Server.ErrorLog` bekommt einen Adapter über `slog.NewLogLogger`.
9. **Stil**, geprüft durch `sloglint`:
   - statische, klein geschriebene englische Meldungen ohne Satzzeichen am Ende
   - Schlüssel in `snake_case`, als Schlüssel-Wert-Paare oder `slog.Attr`, nicht gemischt
   - die `…Context`-Varianten, wenn ein Kontext vorhanden ist
   - einheitliche Schlüssel: `component`, `error`, `platform`, `command_id`, `addr`, `path`, `duration`
10. **Level:**
    - `DEBUG`: Details für Entwickler, auch Nutzdaten wie Chat-Nachrichten
    - `INFO`: Lebenszyklus und Zustandswechsel
    - `WARN`: eingeschränkter Betrieb, Wiederholungsversuch
    - `ERROR`: eine Komponente ist ausgefallen und braucht Aufmerksamkeit

    Ein globales Level (`--log-level`) lässt sich je Komponente übersteuern (`--log-component-level supervisor=debug`). Ein Handler-Wrapper wertet dafür das Attribut `component` aus, das über `Logger.With` gesetzt wird.
11. **Ausgaben** über `slog.NewMultiHandler`:

    | Ziel | Format | Standard |
    |---|---|---|
    | Konsole (stderr) | Text, wahlweise JSON (`--log-format json`, für Container) | an |
    | Datei `<data-dir>/logs/streamcrew.log` | JSON Lines, damit das Diagnose-Paket die Logs auswerten kann | an; `--no-log-file` schaltet sie ab, etwa im Container |
    | Log-Stream der API | strukturiert | ab Phase 6 |

12. **Rotation, eigene kleine Implementierung** in `internal/logging`:
    - nach Größe (Standard 10 MiB, `--log-max-size`), mit einer festen Zahl alter Dateien (Standard 5, `--log-max-files`)
    - alte Dateien heißen `streamcrew-<UTC-Zeitstempel>.log`; keine Rotation nach Zeit, keine Kompression
    - Verzeichnis mit Rechten `0700`, Dateien mit `0600`
13. **Secrets werden maskiert**, zweistufig:
    - Jeder Handler maskiert über `ReplaceAttr` die Werte von Schlüsseln, die nach Secret aussehen: `token`, `secret`, `password`, `passwd`, `authorization`, `cookie`, `api_key`, `apikey`, `credential`, `private_key` als Bestandteil des Schlüssels. Ausgegeben wird `[REDACTED]`.
    - Der Typ `logging.Secret` maskiert sich selbst (`slog.LogValuer` und `fmt.Stringer`), unabhängig vom Schlüssel. Werte, die nie im Log erscheinen dürfen, tragen diesen Typ.
    - Request- und Response-Bodies werden nicht geloggt.
14. **Lint-Regeln** in `.golangci.yml` ergänzen: `sloglint` mit `no-global: all`, `context: scope`, `static-msg`, `msg-style: lowercased`, `key-naming-case: snake`.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `gopkg.in/natefinch/lumberjack.v2` für die Rotation | verbreitet und MIT-lizenziert, aber seit 2023 ohne Release und mit Funktionen, die nicht gebraucht werden (Kompression, Rotation nach Alter). Die benötigte Rotation nach Größe umfasst rund 150 Zeilen und lässt sich vollständig testen. |
| Rotation dem Betriebssystem überlassen (logrotate, journald) | gibt es auf dem Streaming-PC unter Windows und macOS nicht; das Diagnose-Paket braucht eine bekannte Log-Datei |
| Datei-Log als Text | für Menschen lesbarer, für das Diagnose-Paket und für Werkzeuge schlechter auswertbar; die Konsole bleibt Text |
| zap oder zerolog | schneller, aber eine Abhängigkeit mehr; slog genügt den Anforderungen und ist Standard |
| Stacktraces in Fehlern (z. B. `pkg/errors`) | archiviert; der Kontext aus dem Wrapping reicht, Stacktraces gibt es für Panics |

## Konsequenzen

**Positiv:**

- Einheitliche, maschinenlesbare Logs als Grundlage für das Diagnose-Paket (ADR-0011) ohne Telemetrie.
- Secrets sind doppelt abgesichert: über ihren Schlüssel und über ihren Typ.
- Keine zusätzliche Abhängigkeit für Logging und Rotation.

**Negativ und Risiken:**

- Die Maskierung über Schlüsselnamen ist eine Heuristik. Ein Secret unter einem harmlosen Schlüssel fällt nur auf, wenn es den Typ `logging.Secret` trägt.
- Die eigene Rotation muss gepflegt werden, etwa beim Verhalten unter Windows (Umbenennen offener Dateien).
- Die strengen `sloglint`-Regeln kosten beim Schreiben etwas Disziplin.

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen und den Index in [`README.md`](README.md) anpassen, erledigt 2026-09-29
- [x] `internal/logging` mit Handlern, Level je Komponente, Rotation und Maskierung umsetzen; `sloglint` konfigurieren (Roadmap Phase 1.3), erledigt 2026-09-29
- [ ] Log-Stream der API als weiteren Handler anbinden (Roadmap Phase 6)
- [ ] Diagnose-Paket liest die Datei-Logs ein (Roadmap Phase 6, ADR-0011)
