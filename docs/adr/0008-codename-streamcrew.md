# ADR-0008: Codename `streamcrew`

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-28 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | [ADR-0001](0001-neuimplementierung-und-nutzung-des-originals.md) (keine Marken von Mix It Up); Plan §9; Roadmap Phase 0 und Gate O |

## Kontext

- Bevor Code entsteht, braucht das Projekt einen Namen: für den Go-Modulpfad, das Binary, die Repositories und die Umgebungsvariablen. Einen Modulpfad später zu ändern ist mühsam, weil alle Importe betroffen sind.
- Anforderungen an den Namen:
  - englisch
  - er beschreibt, was die Software tut
  - keine Anlehnung an „Mix It Up“ oder „Mixie“ ([ADR-0001](0001-neuimplementierung-und-nutzung-des-originals.md))
- Das Projekt bleibt privat, bis der Projektinhaber es selbst öffentlich schaltet; Voraussetzung ist Gate O. Die Markenprüfung für den endgültigen Namen kann bis dahin warten.

## Entscheidung

1. **Codename für die private Phase: `streamcrew`.** Gemeint ist die Crew hinter dem Stream: Chatbot, Commands, Alerts, Overlays und Integrationen, die im Hintergrund zusammenarbeiten.
2. **Verwendung:**

   | Stelle | Wert |
   |---|---|
   | Go-Modul (Core) | `github.com/ripmav/streamcrew` (Hosting: [ADR-0009](0009-repositories-und-hosting.md)) |
   | Binary und CLI | `streamcrew` |
   | Präfix für Umgebungsvariablen | `STREAMCREW_` |
   | Öffentliches Start-Paket ([ADR-0006](0006-core-als-bibliothek-fuer-selbststart.md)) | `github.com/ripmav/streamcrew/core` |
   | Protobuf-Pakete der API | `streamcrew.v1alpha1`, später `streamcrew.v1` |
   | Commands als Code | `apiVersion: streamcrew/v1alpha1` |
   | Repositories | `streamcrew` (Core), `streamcrew-desktop`, `streamcrew-web`, optional `streamcrew-relay`; Aufteilung: [ADR-0009](0009-repositories-und-hosting.md) |

3. **Vor Gate O** folgt die Prüfung für den endgültigen Namen: Markenrecherche (DPMA, EUIPO, USPTO), Domains, GitHub- und Paketnamen.
   - Ist `streamcrew` frei, wird er der endgültige Name.
   - Andernfalls wird das Projekt vor der Veröffentlichung umbenannt.
   - Beides hält ein eigenes ADR fest (ADR-Backlog, Plan §12.1).

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `stagehand` | sehr passend, aber bereits Name eines bekannten Open-Source-Projekts |
| `cuemaster`, `streamdirector` | ebenfalls beschreibend; `streamcrew` wurde bevorzugt |
| `showrunner` | von anderen Produkten bereits verwendet |
| Endgültigen Namen sofort festlegen | verzögert den Start; die Markenprüfung ist erst vor der Veröffentlichung nötig |

## Konsequenzen

**Positiv:**

- Der Code kann mit stabilem Modulpfad und Binary-Namen starten.
- Der Name ist verständlich und beschreibt den Zweck.

**Negativ und Risiken:**

- Ein beschreibender, generischer Begriff ist markenrechtlich schwächer und kollidiert eher mit bestehenden Namen.
- Scheitert die Prüfung vor Gate O, muss umbenannt werden: Modulpfad, Importe, Repositories, Binary und Umgebungsvariablen. Je früher die Prüfung stattfindet, desto günstiger.

**Folgearbeiten:**

- [x] Platzhalter für Projekt- und Binärnamen in Plan, Roadmap und ADRs durch `streamcrew` ersetzt (2026-09-28)
- [ ] Namensprüfung (Marken, Domains, Paketnamen) vor Gate O, möglichst früher
- [x] Repository als `ripmav/streamcrew` angelegt (2026-09-28)
