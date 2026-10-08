# Architecture Decision Records

Dieses Verzeichnis enthält die Architekturentscheidungen des Projekts. Entscheidungen zum Code liegen in [`code/`](code/README.md).

## Konventionen

Aus [`starting.md`](../starting.md):

- Entscheidungen werden als ADR festgehalten, auch Entscheidungen zum Code.
- ADRs sind Markdown-Dateien der Form `docs/adr/NNNN-<titel>.md`, Code-ADRs der Form `docs/adr/code/NNNN-<titel>.md`.
- ADRs werden fortlaufend nummeriert, beginnend bei 0001.

Ergänzend:

- Nummern werden in Entstehungsreihenfolge vergeben und nie wiederverwendet.
- Der Titel im Dateinamen ist klein geschrieben, mit Bindestrichen und ohne Umlaute (ä → ae).
- Vorlage: [`TEMPLATE.md`](TEMPLATE.md).
- Mögliche Status:
  - Vorgeschlagen
  - Akzeptiert
  - Abgelöst durch ADR-NNNN
  - Verworfen
- Akzeptierte ADRs werden inhaltlich nicht umgeschrieben. Ändert sich eine Entscheidung, entsteht ein neues ADR, das das alte ablöst.
- Präzisiert ein neues ADR ein bestehendes nur, ohne es abzulösen, bekommt das bestehende im Kopf den Vermerk **Ergänzt durch** mit Link. Sein Status bleibt „Akzeptiert“.
- Jedes neue ADR wird im Index unten eingetragen.
- **Verweise:**
  - ADRs verweisen nur auf existierende ADRs, und zwar als relativen Link.
  - Noch nicht geschriebene Entscheidungen werden beim Thema genannt, mit Hinweis auf den ADR-Backlog in [`plan.md`](../plan.md) (§12). Eine Nummer bekommen sie dabei nicht.
  - Sobald das ADR existiert, darf der Link redaktionell nachgetragen werden.
- **Backlog-Nummern:** Die Nummern im ADR-Backlog und in der Roadmap sind vorläufig. Wer ein geplantes ADR anlegt, vergibt die nächste freie Nummer und passt die Verweise in `plan.md` und `roadmap.md` an.

## Index

| Nr. | Titel | Status | Datum |
|---|---|---|---|
| [0001](0001-neuimplementierung-und-nutzung-des-originals.md) | Neuimplementierung und Nutzung des Originals | Akzeptiert | 2026-09-27 |
| [0002](0002-lizenz-des-projekts.md) | Lizenz des Projekts: MIT | Akzeptiert | 2026-09-27 |
| [0003](0003-betriebsmodi.md) | Betriebsmodi: Streaming-PC primär, Server-Anwendung sekundär | Akzeptiert | 2026-09-27 |
| [0004](0004-plattformumfang-zum-start.md) | Plattformumfang zum Start: Twitch | Akzeptiert | 2026-09-27 |
| [0005](0005-core-in-desktop-builds.md) | Core in Desktop-Builds: mitgeliefert und als eigener Prozess gestartet | Akzeptiert | 2026-09-27 |
| [0006](0006-core-als-bibliothek-fuer-selbststart.md) | Core als Bibliothek für den Selbststart der Desktop-App | Akzeptiert | 2026-09-27 |
| [0007](0007-release-artefakte-des-cores.md) | Release-Artefakte des Cores: Binary und Bibliothek | Akzeptiert | 2026-09-27 |
| [0008](0008-codename-streamcrew.md) | Codename `streamcrew` | Akzeptiert | 2026-09-28 |
| [0009](0009-repositories-und-hosting.md) | Repositories und Hosting | Akzeptiert | 2026-09-28 |
| [0010](0010-api-protokoll.md) | API-Protokoll: ConnectRPC mit Protobuf | Akzeptiert | 2026-09-28 |
| [0011](0011-keine-telemetrie.md) | Keine Telemetrie | Akzeptiert | 2026-09-28 |
| [0012](0012-persistenz.md) | Persistenz: SQLite je Profil, Backups und Secrets | Akzeptiert | 2026-09-29 |
| [0013](0013-sicherheitsmodell.md) | Sicherheitsmodell (Entwurf) | Akzeptiert | 2026-09-29 |
| [0014](0014-oauth-und-app-credentials.md) | OAuth und App-Credentials | Abgelöst durch ADR-0023 | 2026-10-07 |
| [0022](0022-internationalisierung.md) | Internationalisierung | Akzeptiert | 2026-10-02 |
| [0023](0023-twitch-login-authorization-code-flow.md) | Twitch-Login per Authorization Code Flow | Akzeptiert | 2026-10-08 |

Die geplanten ADRs stehen im ADR-Backlog von [`plan.md`](../plan.md) (§12).
