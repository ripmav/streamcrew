# ADR-0009: Repositories und Hosting

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-28 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | [`starting.md`](../starting.md) (Desktop und Web als eigene Projekte); [ADR-0007](0007-release-artefakte-des-cores.md), [ADR-0008](0008-codename-streamcrew.md); Plan §9, §11.3; Roadmap Phasen 0, 1 und Gate O |
| **Ergänzt durch** | [Code-ADR-0001](code/0001-go-toolchain-und-linting.md): CI-Workflows und Abhängigkeits-Updates mit Renovate (kein Dependabot) |

## Kontext

- Laut `starting.md` gehört das CLI-Frontend zum Core, Desktop-Client und Weboberfläche entstehen als separate Projekte.
- Die Repositories bleiben privat, bis der Projektinhaber sie selbst öffentlich schaltet; Voraussetzung ist Gate O ([ADR-0001](0001-neuimplementierung-und-nutzung-des-originals.md)). Die Veröffentlichung soll ohne Umzug möglich sein.
- Releases enthalten Binary und Bibliothek des Cores ([ADR-0007](0007-release-artefakte-des-cores.md)). Die Desktop-Builds beziehen den Core daraus.

## Entscheidung

1. **Drei Repositories**, bei Bedarf ein viertes:

   | Repository | Inhalt |
   |---|---|
   | `streamcrew` | Core mit CLI/TUI, API-Vertrag (`api/proto`), generiertem Go-Client (`api/gen`), öffentlichem Start-Paket `core` und Overlay-Runtime |
   | `streamcrew-desktop` | Desktop-App (Fyne) |
   | `streamcrew-web` | Weboberfläche |
   | `streamcrew-relay` | optional: Webhook-Relay, nur falls das geplante ADR zu eingehenden Webhooks ihn vorsieht |

2. **Hosting auf GitHub** unter `github.com/ripmav/…`:
   - Alle Repositories bleiben privat, bis der Projektinhaber sie selbst öffentlich schaltet. Weder die CI noch Dritte oder KI-Assistenten ändern die Sichtbarkeit. Ein Umzug ist dafür nicht nötig.
   - CI läuft mit **GitHub Actions**, Releases und ihre Artefakte liegen in **GitHub Releases**.
3. **Abhängigkeiten zwischen den Repositories** laufen nur über versionierte Releases des Cores:
   - Die Desktop-App bindet den Core über `go.mod` ein (Modul `github.com/ripmav/streamcrew`) und bezieht die Artefakte aus dem passenden Release (ADR-0007).
   - Die Weboberfläche erzeugt ihren TypeScript-Client aus den Protobuf-Dateien eines Core-Tags.
4. **Private Module:**
   - Lokal und in der CI gilt `GOPRIVATE=github.com/ripmav/*`.
   - Die CI der abhängigen Repositories bekommt einen Token mit Leserechten auf das Core-Repository und seine Release-Artefakte, z. B. ein fein granularer Token oder eine GitHub App.
5. **Arbeitsweise:**
   - `main` ist geschützt, gearbeitet wird auf Branches nach dem Schema `<präfix>/<kurzbeschreibung>`.
   - Abhängigkeits-Updates laufen automatisiert (Renovate oder Dependabot).
   - Nach dem Merge löscht GitHub den Branch automatisch (Repository-Einstellung, aktiv seit 2026-09-28).

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Monorepo | übergreifende Änderungen in einem Schritt, aber gemeinsame Historie und CI für Go- und Web-Code; widerspricht dem Bild „separate Projekte“ |
| Zwei Repositories (Go und Web) | weniger Versionsabstimmung zwischen Core und Desktop, aber vermischt Core- und Desktop-Releases |
| Forgejo/Gitea selbst gehostet | mehr Betriebsaufwand; zum Open-Sourcing wäre ein Umzug oder eine Spiegelung nötig |
| GitLab privat | funktional gleichwertig; GitHub passt besser zu den übrigen Werkzeugen (goreleaser, Renovate, `gh`) |

## Konsequenzen

**Positiv:**

- Entspricht `starting.md`: klare Grenzen, eigene Release-Zyklen je Projekt.
- Veröffentlichung durch Umschalten der Sichtbarkeit, ohne Umzug.
- Releases sind die einzige Schnittstelle zwischen den Repositories; das passt zu ADR-0007.

**Negativ und Risiken:**

- Änderungen über Repository-Grenzen hinweg brauchen abgestimmte Releases.
- Private Module brauchen `GOPRIVATE` und Tokens in der CI.
- Für private Repositories sind die Minuten von GitHub Actions begrenzt, und macOS-Runner zählen mehrfach. Das ist bei Desktop-Builds zu beobachten.

**Folgearbeiten:**

- [x] `streamcrew` privat angelegt: `LICENSE` im ersten Commit auf `main`, `docs/` per Pull Request (2026-09-28). `streamcrew-desktop` und `streamcrew-web` folgen mit ihren Tracks.
- [ ] Branch-Schutz für `main` einrichten (Roadmap 0.4). Für private Repositories ist er im aktuellen GitHub-Plan nicht verfügbar (geprüft 2026-09-28); bis dahin gilt die Regel per Konvention, festgehalten in [`CONTRIBUTING.md`](../../CONTRIBUTING.md) (2026-09-29). Der technische Schutz folgt nach dem Umschalten auf öffentlich (Gate O, O.3).
- [ ] `GOPRIVATE` und CI-Token für den Zugriff zwischen den Repositories (Roadmap Phase 1)

---

*Präzisierung (2026-09-28): Zuvor hieß es „privat bis Gate O“. Öffentlich wird das Projekt aber nur, wenn der Projektinhaber es selbst umschaltet. Gate O ist die Voraussetzung dafür, nicht der Auslöser.*
