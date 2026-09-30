# Spezifikationen

Dieses Verzeichnis beschreibt das **Verhalten** von streamcrew je Fachgebiet, in eigenen Worten und mit Quellenangabe. Die Spezifikationen sind die Grundlage der Implementierung. Implementiert wird aus ihnen, nicht aus dem Code des Originals ([ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md)).

## Regeln

Die Regeln setzen [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md) um und gelten für jede Spezifikation.

1. **Quellen, in dieser Reihenfolge:**
   1. offizielle Dokumentation von Mix It Up (<https://mixitup.bot/docs>)
   2. beobachtetes Verhalten der App bei normaler Nutzung
   3. der öffentliche Quellcode (`../mixitup`) als Hilfestellung für Randfälle, Abläufe und Datenformate
   4. für Plattformen und Dienste (Twitch, YouTube, Kick, OBS …) ausschließlich deren offizielle Dokumentation
2. **Eigene Worte:** Beschrieben wird das *Was*, nicht das *Wie*. Code und Texte werden weder zitiert noch übersetzt. Das gilt auch für Kommentare, UI-Texte und die Dokumentation von Mix It Up (alle Rechte bei Blazing Cacti).
3. **Quellenangabe:** Jede Regel lässt sich auf mindestens eine Quelle im Abschnitt „Quellen“ zurückführen:
   - Doku-Link
   - Beschreibung einer Beobachtung
   - Referenzstelle im Original als Pfad und Version, z. B. `MixItUp.Base/Services/CommandService.cs @ v1.8.200`, ohne Code-Auszug
4. **Zwei Schritte:** Erst lesen und spezifizieren, dann implementieren. Beim Implementieren ist nur die Spezifikation geöffnet, nicht der Originalcode.
5. **KI-Assistenten:** Eingabe für die Implementierung ist die Spezifikation. C#-Code wird nicht als Kontext übergeben, und Aufträge wie „übersetze diese Datei nach Go“ sind ausgeschlossen.
6. **Interop-Ausnahmen:** Namen und Formate, die für die Kompatibilität gleich sein müssen, dürfen genannt werden: die Namen der `$`-Identifier, Importformate sowie die Bedeutung von Event- und Rollentypen. Sie werden im Text mit **[Interop]** markiert. Sie stehen unter dem Vorbehalt der rechtlichen Prüfung und des geplanten ADRs zum Import von Mix-It-Up-Daten (ADR-Backlog in [`plan.md`](../plan.md), §12.1).
7. **Bewusste Abweichungen** vom Original stehen ausdrücklich im Abschnitt „Abweichungen vom Original“, jeweils mit Begründung.
8. **Nicht erlaubt:** dekompilieren (EULA), Blazing-Cacti-Dienste nutzen, Marken verwenden.

## Aufbau und Ablauf

- **Datei:** eine Datei pro Fachgebiet, `docs/spec/<bereich>.md` in Kleinbuchstaben mit Bindestrichen, Vorlage: [`TEMPLATE.md`](TEMPLATE.md).
- **Verhaltensregeln** tragen stabile IDs (`B1`, `B2` …), Quellen ebenfalls (`Q1`, `Q2` …). Tests verweisen auf die Verhaltens-IDs, z. B. im Testnamen oder als Kommentar. So lässt sich jede Regel bis zum Test verfolgen.
- **Status:**
  - `Entwurf`: in Arbeit
  - `Geprüft`: vollständig, Quellen belegt, bereit zur Umsetzung
  - `Umgesetzt`: mit Verweis auf PR oder Paket
- **Änderungen** an einer geprüften Spezifikation kommen in deren Änderungshistorie. Ändert sich das Verhalten, werden Code und Tests im selben PR angepasst.
- **Vor der Veröffentlichung** (Gate O) dienen die Quellenangaben als Grundlage des Herkunfts-Reviews.

## Index

| Spezifikation | Fachgebiet | Roadmap | Status |
|---|---|---|---|
| [`users-and-roles.md`](users-and-roles.md) | Nutzer, Plattform-Identitäten, Statistiken, Rollen und ihre Rangordnung | Phase 2.2 | Geprüft, Datenmodell umgesetzt |
| [`commands.md`](commands.md) | Datenmodell der Commands: Arten, Trigger, Gruppen, Anforderungen | Phase 2.2 | Geprüft, Datenmodell umgesetzt |
| [`counters-and-quotes.md`](counters-and-quotes.md) | Counter und Quotes | Phase 2.2 | Geprüft, Datenmodell umgesetzt |
| [`events.md`](events.md) | Ereigniskatalog: stabile Typnamen, Auslöseregeln | Phase 2.2 | Geprüft, Katalog umgesetzt |
| [`template.md`](template.md) | `$`-Identifier-Engine: Syntax, Auflösung, Kodierung, Ausdrücke, Abweichungen | Phase 3.1 | Geprüft, teilweise umgesetzt |
| `command-engine.md` | Command-Instanzen, Warteschlange, Sperrmodi, Pause, Verlauf | Phase 3 | geplant |
| `twitch-events.md` | Zuordnung der Twitch-Events und ihrer Identifier | Phase 4 | geplant |
| `moderation.md` | Filter, Strikes, Teilnahmeregeln | Phase 5 | geplant |
| `overlays.md` | Overlay-Items, Widgets, Protokoll | Phase 7 | geplant |
| `economy.md` | Währungen, Ränge, Inventar, Shop | Phase 8 | geplant |
| `games.md` | Spiel-Framework und Chatspiele | Phase 8 | geplant |

Neue Spezifikationen werden hier eingetragen; mit dem Anlegen der Datei wird der Eintrag zum Link.
