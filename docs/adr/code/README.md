# Code-ADRs

Dieses Verzeichnis enthält Entscheidungen zum Code: Bibliotheken, Muster, Konventionen und Werkzeuge. Architekturentscheidungen liegen eine Ebene höher ([`../README.md`](../README.md)).

Es gelten dieselben Konventionen und dieselbe Vorlage ([`../TEMPLATE.md`](../TEMPLATE.md)). Die Nummerierung ist eigenständig und beginnt ebenfalls bei 0001.

## Index

| Nr. | Titel | Status | Datum |
|---|---|---|---|
| [0001](0001-go-toolchain-und-linting.md) | Go-Toolchain und Linting | Akzeptiert | 2026-09-28 |
| [0002](0002-dependency-injection.md) | Dependency Injection und Composition Root | Akzeptiert | 2026-09-29 |
| [0003](0003-fehler-und-logging.md) | Fehler und Logging | Akzeptiert | 2026-09-29 |
| [0004](0004-nebenlaeufigkeit-und-supervisor.md) | Nebenläufigkeit und Supervisor | Akzeptiert | 2026-09-29 |
| [0005](0005-konfiguration.md) | Konfiguration | Akzeptiert | 2026-09-29 |
| [0006](0006-teststrategie.md) | Teststrategie | Akzeptiert | 2026-09-29 |
| [0007](0007-circuit-breaker.md) | Circuit Breaker für externe Dienste | Akzeptiert | 2026-09-29 |
| [0008](0008-datenbankzugriff.md) | Datenbankzugriff: modernc SQLite, sqlc, goose | Akzeptiert | 2026-09-29 |
| [0009](0009-ids-und-zeit.md) | IDs und Zeit | Akzeptiert | 2026-09-29 |
| [0010](0010-polymorphe-serialisierung.md) | Polymorphe Serialisierung | Akzeptiert | 2026-09-29 |
| [0011](0011-event-bus.md) | Event-Bus | Akzeptiert | 2026-09-29 |
| [0012](0012-template-engine.md) | Template-Engine | Akzeptiert | 2026-09-29 |
| [0017](0017-klare-signale-statt-magischer-werte.md) | Klar definierte Signale statt magischer Werte | Vorgeschlagen | 2026-09-30 |

Die geplanten Code-ADRs stehen im ADR-Backlog von [`plan.md`](../../plan.md) (§12.2). Code-ADR-0017 bekam die erste Nummer hinter dem Backlog, weil Spezifikationen und Code schon auf die vorgesehenen Nummern 0013 bis 0016 verweisen.
