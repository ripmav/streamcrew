# Code-ADR-0005: Konfiguration

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | [ADR-0003](../0003-betriebsmodi.md), [ADR-0010](../0010-api-protokoll.md); Plan §6.5, §6.21, §7.2, §8; Roadmap Phase 1.2 und 1.3; [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0003](0003-fehler-und-logging.md) |

## Kontext

- Plan §6.21: Die Startkonfiguration kommt über `kong` aus Flags, Umgebungsvariablen `STREAMCREW_*` und einer optionalen Konfigurationsdatei. Laufzeiteinstellungen liegen in der Profildatenbank und sind über die API änderbar.
- Datenverzeichnis: `os.UserConfigDir()`, alternativ `--data-dir` oder ein portabler Modus.
- [ADR-0003](../0003-betriebsmodi.md): Die Standardwerte sind für den Streaming-PC ausgelegt; den Server-Modus wählt man ausdrücklich. Im Container kommt die Konfiguration üblicherweise aus Umgebungsvariablen.
- kong wertet von Haus aus Konfigurationsdateien **vor** Umgebungsvariablen aus: Eine Datei würde eine Umgebungsvariable überschreiben. Unbekannte Schlüssel in einer Datei ignoriert kong stillschweigend.
- Commands als Code (Phase 3) werden als YAML geschrieben. Eine YAML-Bibliothek braucht das Projekt deshalb ohnehin; bisher war ihre Wahl für Phase 3 geplant (ADR-Backlog in Plan §12.2).
- `gopkg.in/yaml.v3` ist archiviert. Die YAML-Organisation pflegt den Nachfolger unter `go.yaml.in/yaml/v3` (MIT für die aus libyaml portierten Dateien, sonst Apache-2.0).\*

## Entscheidung

1. **kong ist die einzige Quelle der Startkonfiguration.**
   - Die Struktur `config.Config` in `internal/config` trägt die kong-Tags und gilt als globale Flags für alle Unterkommandos.
   - Nur `cmd/streamcrew` parst. Danach ergänzt `Config.Resolve` die vom Modus abhängigen Standardwerte und prüft alles. Das Ergebnis geht als Wert an die Composition Root ([Code-ADR-0002](0002-dependency-injection.md)).
   - Außerhalb von `internal/config` und `cmd/streamcrew` liest kein Code Umgebungsvariablen. `forbidigo` meldet `os.Getenv`, `os.LookupEnv` und `os.Environ`. *Ergänzt am 2026-09-29, Entscheidung des Projektinhabers: Ausgenommen sind Testdateien (`_test.go`), damit Golden Files per Umgebungsvariable neu geschrieben werden können ([Code-ADR-0006](0006-teststrategie.md)).*
2. **Vorrang:** Flag vor Umgebungsvariable vor Konfigurationsdatei vor Standardwert.
   - Umgebungsvariablen heißen wie das Flag mit Präfix: `--data-dir` → `STREAMCREW_DATA_DIR` (`kong.DefaultEnvars`).
   - Die Reihenfolge von kong (Datei vor Umgebung) dreht ein eigener Resolver um: Ist die Umgebungsvariable eines Flags gesetzt, liefert die Datei keinen Wert.
3. **Konfigurationsdatei:** optional, im YAML-Format, gelesen mit `go.yaml.in/yaml/v3`.
   - Schlüssel sind die Flag-Namen in `snake_case`, z. B. `log_level`; Maps wie `log_component_level` sind verschachtelte YAML-Objekte. `streamcrew config show` gibt die wirksame Konfiguration genau in diesem Format aus.
   - Unbekannte Schlüssel sind ein Fehler, damit Tippfehler auffallen.
   - Pfad: `--config` bzw. `STREAMCREW_CONFIG`. Ist er gesetzt, muss die Datei existieren, und nur sie wird gelesen. Sonst wird `<Standard-Datenverzeichnis>/config.yaml` gelesen, falls vorhanden.
   - YAML erlaubt Kommentare und ist dasselbe Format wie später die Commands als Code.
   - **Die YAML-Bibliothek gilt damit für das ganze Projekt**, auch für Commands als Code. Der geplante Backlog-Eintrag „YAML-Bibliothek“ entfällt. Phase 3 prüft nur, ob die Bibliothek dort genügt, etwa bei Fehlermeldungen mit Zeile und Spalte; wenn nicht, löst ein neues Code-ADR diesen Punkt ab.
4. **Pfade:**
   - Datenverzeichnis: `--data-dir` bzw. `STREAMCREW_DATA_DIR`. Standard ist `os.UserConfigDir()/streamcrew`, also z. B. `~/.config/streamcrew`, `%AppData%\streamcrew` oder `~/Library/Application Support/streamcrew`.
   - **Portabler Modus:** Liegt neben dem Binary eine Datei `streamcrew.portable`, ist das Standard-Datenverzeichnis `<Verzeichnis des Binarys>/streamcrew-data`. Eine ausdrückliche Angabe mit `--data-dir` gilt weiterhin.
   - Logs liegen unter `<data-dir>/logs`. `serve` legt fehlende Verzeichnisse mit den Rechten `0700` an.
5. **Betriebsmodus** ([ADR-0003](../0003-betriebsmodi.md)): `--mode desktop|daemon|server`, Standard `daemon`. Vom Modus hängen Standardwerte ab, etwa die Adresse des HTTP-Servers: `127.0.0.1:8740` für `desktop` und `daemon`, `:8740` für `server`. Der Port ist frei gewählt; `--listen` ändert ihn. Den lokalen Transport per Unix-Socket ([ADR-0010](../0010-api-protokoll.md)) ergänzt Phase 6.
6. **Einstellungen in Phase 1:**

   | Flag | Standard | Zweck |
   |---|---|---|
   | `--config` | siehe oben | Konfigurationsdatei |
   | `--data-dir` | siehe oben | Datenverzeichnis |
   | `--mode` | `daemon` | Betriebsmodus |
   | `--listen` | abhängig vom Modus | Adresse des HTTP-Servers (`/healthz`, `/readyz`, später die API) |
   | `--dev` | aus | Entwicklermodus: `pprof` unter `/debug/pprof/`, nur mit einer Loopback-Adresse erlaubt |
   | `--shutdown-timeout` | `15s` | Zeitlimit für den gesamten Shutdown |
   | `--log-level`, `--log-component-level` | `info` | Level global und je Komponente ([Code-ADR-0003](0003-fehler-und-logging.md)) |
   | `--log-format` | `text` | Format der Konsole: `text` oder `json` |
   | `--[no-]log-file`, `--log-max-size`, `--log-max-files` | an, 10 MiB, 5 | Datei-Log und Rotation |

7. **Secrets** werden nie als Flag übergeben, weil Flags in der Prozessliste und im Shell-Verlauf stehen. Sie kommen aus Umgebungsvariablen oder Dateien (`--…-file`) und werden in `config show` maskiert. In Phase 1 gibt es noch keine.
8. **Prüfung:** Alle Fehler werden gesammelt (`errors.Join`) und nennen Flag bzw. Umgebungsvariable. Ungültige Konfiguration beendet `streamcrew` mit Exit-Code `2`.
9. **Unterkommandos:** `config show` gibt die wirksame Konfiguration als YAML aus, mit `--output json` als JSON für Skripte. `config path` nennt die Konfigurationsdatei, das Datenverzeichnis und das Log-Verzeichnis. `config validate` aus Plan §7.2 folgt in Phase 6.
10. **Laufzeiteinstellungen** gehören nicht in die Startkonfiguration. Sie liegen ab Phase 2 in der Profildatenbank und werden über die API geändert.

*\* Redaktionell berichtigt am 2026-09-29: Die Lizenz von `go.yaml.in/yaml/v3` war zunächst nur als Apache-2.0 angegeben. Die Entscheidung ändert sich dadurch nicht.*

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| JSON-Datei über `kong.JSON` | ohne Abhängigkeit, aber ohne Kommentare und unbequem von Hand zu pflegen; unbekannte Schlüssel würden ignoriert |
| TOML-Datei (`BurntSushi/toml`, `pelletier/go-toml/v2`) | gut lesbar, aber ein zweites Format neben dem YAML der Commands als Code |
| `github.com/goccy/go-yaml` | MIT-lizenziert, gute Fehlermeldungen mit Position; seltener gepflegt als der offizielle Nachfolger (letztes Release Januar 2026) |
| `github.com/alecthomas/kong-yaml` | hängt am archivierten `gopkg.in/yaml.v3`; der eigene Resolver ist ohnehin nötig, um den Vorrang und die Prüfung unbekannter Schlüssel umzusetzen |
| Vorrang wie in kong (Datei vor Umgebung) | widerspricht der Erwartung im Container, dass Umgebungsvariablen eine mitgelieferte Datei übersteuern |
| `spf13/viper` bzw. `koanf` | eigene Konfigurationsschicht neben kong mit doppelter Definition der Flags; viper bringt viele Abhängigkeiten mit |
| Daten nach XDG getrennt (`XDG_DATA_HOME`, `XDG_STATE_HOME`) | unter Linux sauberer, aber mehrere Verzeichnisse erschweren Backups und den portablen Modus; Plan §6.21 sieht ein Verzeichnis vor |

## Konsequenzen

**Positiv:**

- Eine Definition je Einstellung für Flag, Umgebungsvariable, Datei und Hilfetext.
- Vorhersehbarer Vorrang, auch im Container; Tippfehler in der Datei fallen sofort auf.
- Kommentierbare Konfigurationsdatei im selben Format wie die Commands als Code; die YAML-Frage für Phase 3 ist damit geklärt.

**Negativ und Risiken:**

- Eine Abhängigkeit mehr: `go.yaml.in/yaml/v3` (MIT und Apache-2.0, beide in der Allowlist). Ihre `NOTICE`-Datei gehört zu den Drittkomponenten, die vor Gate O in die eigene `NOTICE` kommen.\*
- YAML hat Fallstricke bei der Typerkennung, etwa `no` oder `on`, die je nach YAML-Version als Bool oder als Text gelten. Die Werte gehen deshalb durch die Typprüfung von kong; ein falscher Typ scheitert mit einer Meldung, die den Schlüssel nennt.
- Der eigene Resolver hängt an kongs Resolver-Schnittstelle und muss bei kong-Updates mitgeprüft werden; Tests sichern den Vorrang ab.
- Unter Linux liegen Daten und Logs im Konfigurationsverzeichnis (`~/.config`), nicht unter `~/.local/share`.

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen und den Index in [`README.md`](README.md) anpassen, erledigt 2026-09-29
- [x] `internal/config` und die Unterkommandos `config show|path` umsetzen; `forbidigo` für Umgebungsvariablen konfigurieren (Roadmap Phase 1.3), erledigt 2026-09-29
- [x] Backlog-Eintrag „YAML-Bibliothek“ in Plan §12.2 und Roadmap Phase 3 streichen bzw. durch die Prüfung der Bibliothek für Commands als Code ersetzen, erledigt 2026-09-29
- [ ] Unix-Socket als lokalen Transport und `config validate` ergänzen (Roadmap Phase 6)
