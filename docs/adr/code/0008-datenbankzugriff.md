# Code-ADR-0008: Datenbankzugriff: modernc SQLite, sqlc, goose

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | [ADR-0012](../0012-persistenz.md); Plan §6.13, §8, §9.2; Roadmap Phase 2.1; [Code-ADR-0001](0001-go-toolchain-und-linting.md), [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0003](0003-fehler-und-logging.md), [Code-ADR-0006](0006-teststrategie.md), [Code-ADR-0009](0009-ids-und-zeit.md), [Code-ADR-0010](0010-polymorphe-serialisierung.md) |

## Kontext

- [ADR-0012](../0012-persistenz.md) legt SQLite je Profil fest. Der Core ist CGO-frei ([ADR-0003](../0003-betriebsmodi.md)) und wird für linux, windows und darwin auf amd64 und arm64 gebaut.
- SQLite erlaubt einen Schreiber zur Zeit. Im WAL-Modus lesen andere Verbindungen parallel weiter. Eine Transaktion, die erst liest und dann schreibt, scheitert unter Last mit `SQLITE_BUSY`, wenn sie nicht von Anfang an die Schreibsperre anfordert.
- Das Schema wächst über viele Phasen (Plan §6.13). Die Exit-Kriterien von Phase 2 verlangen Migrationen, die vorwärts und rückwärts getestet sind, und Integrationstests gegen eine echte SQLite.
- Plan §8 nennt als Kandidaten `modernc.org/sqlite`, `sqlc` und `goose`. Geprüft am 2026-09-29:
  - `modernc.org/sqlite` v1.60.0 (BSD-3-Clause): SQLite nach Go übersetzt, ohne CGO; Pragmas lassen sich validiert per DSN setzen.
  - `sqlc` v1.31.1 (MIT) baut ohne CGO und hat eine SQLite-Engine. Als Werkzeug zöge es rund 50 Module in die eigene `go.mod`.
  - `goose` v3.28.0 (MIT): Die `go.mod` von goose nennt viele Datenbanktreiber (ClickHouse, MSSQL u. a.). In den Build gelangen davon nur goose selbst, `mfridman/interpolate`, `sethvargo/go-retry` und `go.uber.org/multierr`.
  - modernc, goose und der Keyring zusammen machen ein gestripptes Binary rund 4,5 MB größer (gemessen am 2026-09-29).

## Entscheidung

1. **Treiber `modernc.org/sqlite`** über `database/sql`.
2. **Zwei Verbindungspools je Profildatenbank:**

   | Pool | Verbindungen | Einstellungen |
   |---|---|---|
   | Schreiben | 1 | `_txlock=immediate`: jede Transaktion holt die Schreibsperre sofort |
   | Lesen | `min(4, GOMAXPROCS)` | `_query_only=1` |

   - Beide Pools setzen per DSN `_journal_mode=WAL`, `_foreign_keys=1`, `_busy_timeout=5000` und `_synchronous=NORMAL`.
   - Nur die validierten Kurzschlüssel des Treibers werden verwendet, kein `_pragma`, das beliebiges SQL ausführen könnte.
   - Der Pfad wird als `file:`-URI kodiert, damit Sonderzeichen im Pfad nicht als Parameter gelesen werden.
3. **Typisierte Abfragen mit sqlc:**
   - SQL steht in `internal/store/queries/*.sql`, das Schema kommt aus den Migrationen. Konfiguration in `sqlc.yaml` im Repository-Root (Plan §9.2).
   - Der generierte Code liegt eingecheckt in `internal/store/sqlcgen`. Er entsteht mit `go generate ./internal/store/...` über `//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@<version> generate`.
   - Die Version ist wie bei `go-licenses` im Aufruf gepinnt ([Code-ADR-0001](0001-go-toolchain-und-linting.md)). Renovate hebt sie über den bestehenden Regex-Manager an, dessen Dateimuster auf Go-Dateien erweitert wird.
   - Eine `tool`-Direktive in `go.mod` wäre das übliche Mittel, brächte aber rund 50 Module von sqlc in die `go.mod` des Cores. Die lokale Konvention aus Code-ADR-0001 geht vor.
   - Die CI prüft mit `sqlc diff`, dass der eingecheckte Code zum SQL passt.
4. **Migrationen mit goose v3:**
   - SQL-Dateien `internal/store/migrations/NNNN_<name>.sql` mit fortlaufender Nummer und den Abschnitten `-- +goose Up` und `-- +goose Down`, eingebettet per `go:embed`.
   - Ausgeführt über die Provider-API von goose beim Öffnen der Datenbank auf dem Schreib-Pool, jede Migration in einer Transaktion.
   - Jede Migration hat einen Down-Teil. Ein Test führt alle Migrationen hoch, wieder herunter und erneut hoch.
   - Die höchste Migrationsnummer ist die Schemaversion (Backup-Manifest, Restore-Prüfung, [ADR-0012](../0012-persistenz.md)). Vor einer Migration einer bestehenden Datenbank legt der Core ein Backup an.
   - Eine Datenbank mit höherer Schemaversion, als die laufende Version kennt, wird nicht geöffnet.
5. **Tabellen:**
   - `STRICT`-Tabellen, damit SQLite die Spaltentypen durchsetzt. Unterstützt sqlc `STRICT` für eine Tabelle nicht, entfällt es dort, mit Kommentar im Schema.
   - IDs als `TEXT` (UUID), Zeitpunkte als `INTEGER` in Unix-Millisekunden UTC ([Code-ADR-0009](0009-ids-und-zeit.md)), Wahrheitswerte als `INTEGER` 0/1.
   - Polymorphe Dokumente als JSON in `TEXT`-Spalten ([Code-ADR-0010](0010-polymorphe-serialisierung.md)).
6. **Repositories:** `internal/store` implementiert die Interfaces, die die Konsumenten definieren ([Code-ADR-0002](0002-dependency-injection.md)).
   - Sie übersetzen zwischen sqlc-Zeilen und Domänentypen; sqlc-Typen verlassen `internal/store` nicht.
   - Schreibende Abläufe laufen in einer Transaktion auf dem Schreib-Pool über einen Helfer, der bei einem Fehler zurückrollt. Lesende Abfragen nutzen den Lese-Pool.

     *Ergänzt am 2026-10-03 (Entscheidung des Projektinhabers) für den Import von Commands als Code ([`commands-as-code.md`](../../spec/commands-as-code.md), B33): `Store.Atomically(ctx, fn)` gibt `fn` einen Store, dessen Lesen und Schreiben über eine Transaktion auf dem Schreib-Pool laufen; sie wird bestätigt, wenn `fn` ohne Fehler endet, sonst zurückgerollt. So gelingen mehrere Speichervorgänge der Services gemeinsam oder keiner. Lesen innerhalb dieser Transaktion nutzt nicht den Lese-Pool, damit es die Schreibvorgänge davor sieht. Ein Aufruf auf einem solchen Store läuft in derselben Transaktion; schließen lässt er sich nicht.*
   - Fehler werden an der Grenze übersetzt ([Code-ADR-0003](0003-fehler-und-logging.md)): `sql.ErrNoRows` zu `store.ErrNotFound`, Verletzungen von Eindeutigkeit und Fremdschlüsseln zu `store.ErrConflict`.
7. **Tests** laufen gegen eine echte SQLite in `t.TempDir()` ([Code-ADR-0006](0006-teststrategie.md)), ohne Build-Tags und ohne Fakes für die Datenbank.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `github.com/mattn/go-sqlite3` | braucht CGO; widerspricht ADR-0003 und erschwert Cross-Builds |
| `github.com/ncruces/go-sqlite3` | CGO-frei über WebAssembly (wazero), MIT, aktiv; noch vor Version 1.0 (v0.35) und weniger verbreitet als modernc |
| ein Pool mit einer Verbindung | am einfachsten, aber lange Schreibvorgänge blockieren alle Leser, etwa Ranglisten während eines Imports |
| handgeschriebene Abfragen ohne sqlc | keine Abhängigkeit, aber viel Boilerplate beim Scannen und Fehler erst zur Laufzeit; das Schema wächst über viele Phasen |
| `sqlx`, GORM, ent | sqlx spart nur das Scannen; GORM und ent sind ORMs mit eigener Abfragesprache und Laufzeitreflexion |
| sqlc als `tool`-Direktive | rund 50 Module von sqlc in der `go.mod` des Cores; Renovate-Updates und `go.sum` würden davon dominiert |
| eigener Migrator | rund 150 Zeilen ohne Abhängigkeit, aber Sperren, Down-Migrationen und Versionsverwaltung müssten selbst gebaut und getestet werden; goose ist erprobt und im Projekt `n8n-go` des Inhabers bewährt |
| `golang-migrate/migrate` | getrennte Dateien für Up und Down und ein schwererer Abhängigkeitsbaum durch die Quell- und Datenbanktreiber |
| Zeitstempel als Migrationsnummer | vermeidet Konflikte zwischen parallelen Branches, die es in einem Einpersonenprojekt kaum gibt; fortlaufende Nummern sind als Schemaversion lesbarer |

## Konsequenzen

**Positiv:**

- CGO-frei auf allen Zielplattformen; Leser und Schreiber behindern sich nicht.
- SQL bleibt SQL, der Go-Code dazu ist typisiert und generiert; Abweichungen fallen in der CI auf.
- Migrationen sind eingebettet, versioniert und in beide Richtungen getestet.

**Negativ und Risiken:**

- Abhängigkeiten: `modernc.org/sqlite` mit `modernc.org/libc` und weiteren modernc-Modulen (BSD-3-Clause), `github.com/pressly/goose/v3` (MIT) mit drei kleinen Modulen. Das Binary wächst um rund 4,5 MB.
- `go generate` braucht beim ersten Lauf Netz und rund 20 s, um sqlc zu bauen.
- Die SQLite-Engine von sqlc deckt nicht jede SQLite-Syntax ab. Seltene Abfragen, die sqlc nicht versteht, werden von Hand in `internal/store` geschrieben und getestet.
- Zwei Pools auf dieselbe Datei verdoppeln die Verbindungsverwaltung; das kapselt `internal/store`.

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen und den Index in [`README.md`](README.md) anpassen, erledigt 2026-09-29
- [x] `internal/store` mit Pools, Migrationen, Transaktions-Helfer und Fehlerübersetzung umsetzen; `sqlc.yaml` anlegen (Roadmap Phase 2.1), erledigt 2026-09-29
- [x] CI: `sqlc diff` im Checks-Job; Renovate-Regex-Manager um `go:generate`-Zeilen in Go-Dateien erweitern, erledigt 2026-09-29
- [ ] Das Code-ADR zur Codegenerierung (Phase 6: `buf`, esbuild) nimmt die Konventionen dieses ADRs für sqlc auf
