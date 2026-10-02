# Spezifikation: Commands (Datenmodell)

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-10-02 |
| **Bezug** | Roadmap Phase 2.2 (Commands), 3.2–3.5, 5.4; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md), [Code-ADR-0010](../adr/code/0010-polymorphe-serialisierung.md); Plan §5.2, §5.4, §6.8, §6.9; [`users-and-roles.md`](users-and-roles.md), [`events.md`](events.md) |
| **Umsetzung** | Datenmodell umgesetzt: `internal/domain/command`, Repository in `internal/store`; Cooldown-Gruppen als `command.CooldownGroup` (B33, Migration 0007); den Schalter „aktiv“ ändern `command.Service.SwitchCommand` und `SwitchGroup` für die Command-Action ([`actions.md`](actions.md), B34), mit B14 für die Trigger. Ausführung, Sperren, Prüfung der Anforderungen und fehlerhafte Verweise (B3, B5, B15, B21, B63) folgen mit `command-engine.md` (Phase 3), B6 mit den vorgefertigten Commands (Phase 5.6) |

## Zweck und Umfang

Beschreibt, was ein Command ist und welche Daten er trägt: Arten, Trigger, Gruppen, Freigaben, Anforderungen und die Liste der Actions. Das ist die Grundlage für Speicherung, API und Commands als Code.

Nicht Teil dieser Spezifikation, sondern von `command-engine.md` (Phase 3):

- wie Commands ausgeführt, in Warteschlangen gestellt und gegeneinander gesperrt werden
- wann Anforderungen geprüft und Kosten abgebucht werden, und welche Fehlermeldungen entstehen
- das Verhalten der einzelnen Action- und Requirement-Typen

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Command | benannte Folge von Actions, die unter bestimmten Bedingungen ausgeführt wird |
| Art | wodurch ein Command ausgelöst wird: Chat, Ereignis, Timer, Action-Gruppe und weitere |
| Trigger | Text im Chat, der einen Chat-Command auslöst |
| Action | ein einzelner Schritt, etwa eine Chatnachricht senden oder warten |
| Anforderung (Requirement) | Bedingung, die vor der Ausführung erfüllt sein muss, etwa Mindestrolle oder Cooldown |
| Gruppe | benannte Sammlung von Commands, etwa für gemeinsame Cooldowns oder Timer-Intervalle |
| Freigegeben (unlocked) | ein Command, der nicht auf andere Commands seiner Art wartet |

## Verhalten

### Gemeinsame Merkmale

| ID | Regel | Quellen |
|---|---|---|
| B1 | Jeder Command hat eine ID (UUIDv7), einen Namen, eine Art, einen Schalter „aktiv“, einen Schalter „freigegeben“, optional eine Gruppe, eine Menge von Anforderungen, eine geordnete Liste von Actions und eine Fehlerpolitik ([`command-engine.md`](command-engine.md), B71). | Q3, Q4 |
| B2 | Die Arten teilen den Großteil ihrer Merkmale; sie unterscheiden sich im Auslöser. Arten zum Start (P0): `chat`, `event`, `timer`, `action_group`. Später: nutzerspezifische Chat-Commands (P1), vorgefertigte Commands (Phase 5.6), Kanalpunkte (Phase 4), Spiele (Phase 8), Webhooks (Phase 10), Stream Pass (P2). | Q3, QP (Plan §5.2) |
| B3 | Ein inaktiver Command wird nie automatisch ausgelöst; von Hand gestartet (Test in der Oberfläche, API) kann er trotzdem werden. | Q3 („Play“ zum Testen), Q8 (Schalter aktiv/inaktiv) |
| B4 | Actions sind polymorphe Dokumente mit `type` und `schemaVersion`; ihre Reihenfolge ist die Ausführungsreihenfolge. Unbekannte Action-Typen bleiben erhalten und werden nicht ausgeführt. | Code-ADR-0010 |
| B5 | Freigegeben heißt: Der Command läuft sofort, auch wenn ein anderer Command derselben Sperrgruppe gerade läuft. Wie gesperrt wird, legt eine globale Einstellung fest (je Art, je Action-Art, visuell/akustisch, eine Sperre für alles, keine). | Q3 |
| B6 | Vorgefertigte Commands können nicht gelöscht werden, nur deaktiviert oder geändert. | Q3 („nutzererstellte Commands lassen sich löschen“) |

### Chat-Commands

| ID | Regel | Quellen |
|---|---|---|
| B10 | Ein Chat-Command hat einen oder mehrere Trigger. | Q4 |
| B11 | Standardmäßig ist ein Trigger ein einzelnes Wort, beginnt im Chat mit `!` und wird ohne Beachtung der Groß- und Kleinschreibung erkannt. Gespeichert wird der Trigger ohne `!`. | Q4 |
| B12 | Mehrere Trigger werden bei der Eingabe durch Leerzeichen getrennt; Trigger aus mehreren Wörtern durch Semikolon. Gespeichert wird eine Liste einzelner Trigger. | Q4 |
| B13 | Mit der Option „Platzhalter“ (Wildcard) wird ein Trigger überall in der Nachricht als ganzes Wort erkannt: Der Trigger `what` passt auf „what is going on?“, aber nicht auf ein Wort, das `what` nur enthält. | Q4 |
| B14 | Ein Trigger ist innerhalb aller aktiven Chat-Commands eindeutig, ohne Beachtung der Groß- und Kleinschreibung. | QP, A1 |
| B15 | Die auslösende Nachricht ist während der Ausführung als ganze verfügbar, samt Trigger. **[Interop]** `$message` | Q4 |

### Ereignis-, Timer- und Action-Gruppen-Commands

| ID | Regel | Quellen |
|---|---|---|
| B20 | Ein Ereignis-Command ist genau einem Ereignistyp aus dem Katalog zugeordnet ([`events.md`](events.md)); je Ereignistyp gibt es höchstens einen Ereignis-Command. | Q8, A2 |
| B21 | Ein Timer-Command hat keinen eigenen Auslöser; wann er läuft, bestimmen die Timer-Einstellungen bzw. das Intervall seiner Gruppe (B31). | Q5 |
| B22 | Ein Action-Gruppen-Command wird nur von anderen Commands, über die API oder von Hand gestartet. | QP (Plan §5.2) |

### Gruppen

| ID | Regel | Quellen |
|---|---|---|
| B30 | Eine Gruppe hat eine ID und einen eindeutigen Namen. Ein Command gehört zu höchstens einer Gruppe. | Q3, Q5 |
| B31 | Eine Gruppe kann ein eigenes Timer-Intervall haben. Timer-Commands in einer Gruppe mit Intervall laufen unabhängig von den globalen Timer-Einstellungen (Intervall und Mindestzahl an Nachrichten) nach dem Intervall der Gruppe. Ohne Intervall dient die Gruppe nur der Ordnung. | Q5 |
| B32 | Für gemeinsame Cooldowns gibt es eigene Cooldown-Gruppen (B33); die Gruppe eines Commands spielt dafür keine Rolle. | Q3, Q9 |
| B33 | Eine Cooldown-Gruppe hat eine ID, einen Namen, der ohne Rücksicht auf die Schreibweise eindeutig ist, und eine positive Dauer. Cooldowns der Arten `group` und `per_user_group` nennen sie und teilen sich mit allen Commands, die dieselbe Cooldown-Gruppe nennen, gleich welcher Gruppe und Art. Eine neue Dauer gilt für Cooldowns, die danach beginnen. Ein Cooldown, der eine Cooldown-Gruppe nennt, die es nicht gibt, ist beim Speichern ungültig. | Q9, A4 |

### Anforderungen

Die Anforderungen sind eine Menge von Einträgen je Art; jede Art kommt höchstens einmal vor. Sie sind polymorphe Dokumente wie die Actions (Code-ADR-0010).

| ID | Regel | Quellen |
|---|---|---|
| B40 | **Rolle:** eine Mindestrolle nach [`users-and-roles.md`](users-and-roles.md), B23. Ohne Angabe gilt `user`. | Q3, Q4 |
| B41 | **Cooldown:** eine von vier Arten: für alle (`standard`) und je Nutzer (`per_user`), jeweils mit eigener Dauer; für alle Commands einer Cooldown-Gruppe (`group`) und je Nutzer über die Cooldown-Gruppe (`per_user_group`), jeweils mit der Dauer der Gruppe (B33). | Q3, Q9 |
| B42 | **Währung:** Währung, Modus und Betrag: fester Betrag, der abgebucht wird (`required`); Mindestbetrag, den der Nutzer angibt (`minimum`); Betrag zwischen Minimum und Maximum (`range`). | Q3 |
| B43 | **Rang:** Rang und Vergleich: dieser oder höher, genau dieser, dieser oder niedriger. | Q3 |
| B44 | **Inventar:** Gegenstand und Mindestmenge, die bei der Ausführung abgebucht wird. | Q3 |
| B45 | **Argumente:** geordnete Liste mit Name, Typ, Pflicht oder optional und optional dem Namen eines Identifiers, unter dem der Wert verfügbar ist. | Q3 |
| B46 | **Schwelle:** Mindestzahl verschiedener Nutzer innerhalb eines Zeitfensters, bevor der Command läuft; optional läuft er dann für jeden dieser Nutzer einzeln. | Q3 |
| B47 | **Einstellungen:** die auslösende Chatnachricht nach der Ausführung löschen; den Command im Kontextmenü des Chats anbieten. | Q3 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B60 | Trigger mit Großbuchstaben gespeichert | Erkennung unabhängig von der Schreibweise; angezeigt wie eingegeben | B11 |
| B61 | Leere Trigger-Liste bei einem Chat-Command | beim Speichern abgelehnt | B10 |
| B62 | Gruppe wird gelöscht | ihre Commands verlieren die Gruppenzugehörigkeit und bleiben erhalten | B30 |
| B63 | Anforderung verweist auf eine gelöschte Währung, einen Rang oder Gegenstand | Command bleibt gespeichert, gilt aber als fehlerhaft und wird nicht ausgeführt, bis der Verweis repariert ist | B42–B44 |
| B64 | Eine Cooldown-Gruppe wird gelöscht | Commands, deren Cooldown sie nennt, bleiben gespeichert, gelten aber als fehlerhaft und werden nicht ausgeführt, bis eine andere gewählt ist ([`requirements.md`](requirements.md), B7) | B33 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | nicht belegt, ob doppelte Trigger erlaubt sind | Trigger eindeutig über aktive Chat-Commands | vorhersehbares Verhalten, klare Fehlermeldung beim Speichern; zu prüfen (offene Frage) |
| A2 | nicht belegt, ob mehrere Ereignis-Commands je Ereignis möglich sind | höchstens einer je Ereignistyp | entspricht der Oberfläche mit einem Schalter je Ereignis (Q8); zu prüfen |
| A3 | Commands in einer internen Einstellungsdatei | Commands als versionierte Dokumente in der Profildatenbank, als YAML exportierbar | Plan §6.9, ADR-0012 |
| A4 | Cooldown-Gruppen über ihren Namen; die Dauer stellt das Cooldown-Feld eines Commands ein und gilt danach für alle Commands mit diesem Namen (Q9) | Dauer an der Cooldown-Gruppe selbst, Verweis über die ID (B33) | eine Stelle für die Dauer; Umbenennen bricht keine Verweise; Entscheidung des Projektinhabers (2026-10-02) |

## Akzeptanzkriterien

- [x] B1, B4: Ein Command mit Actions unbekannten Typs wird gespeichert, geladen und unverändert zurückgeschrieben.
- [x] B11, B12: Trigger werden ohne `!` und als Liste gespeichert; Eingaben mit Leerzeichen und Semikolon werden richtig zerlegt.
- [x] B13: Tabellengetriebener Test der Wortgrenzen für Platzhalter-Trigger.
- [x] B14: Ein zweiter aktiver Chat-Command mit gleichem Trigger wird abgelehnt.
- [x] B20: Ein zweiter Ereignis-Command für denselben Typ wird abgelehnt.
- [x] B30, B62: Gruppennamen sind eindeutig; Löschen einer Gruppe lässt ihre Commands bestehen.
- [x] B40–B47: Jede Anforderungsart lässt sich speichern und laden (Golden Files, Code-ADR-0010).

## Offene Fragen

- B11: Gibt es im Original die Möglichkeit, einen Chat-Command ohne `!` auszulösen, außer über den Platzhalter?
- B13: Braucht ein Platzhalter-Trigger im Original trotzdem das `!`?
- B14/A1: Wie verhält sich das Original bei zwei Commands mit gleichem Trigger?
- B20/A2: Lassen sich im Original mehrere Commands für dasselbe Ereignis anlegen?
- B45: Welche Argumenttypen gibt es im Original (Text, Zahl, Nutzer …)?

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q3 | Doku | <https://mixitup.bot/docs/commands> | Arten, Sperren, Anforderungen, Cooldown-Arten; abgerufen 2026-09-29 |
| Q4 | Doku | <https://mixitup.bot/docs/chat/chat-commands> | Trigger, Trennzeichen, Platzhalter, `$message`; abgerufen 2026-09-29 |
| Q5 | Doku | <https://mixitup.bot/docs/timers> | Timer, Gruppen mit eigenem Intervall; abgerufen 2026-09-29 |
| Q8 | Doku | <https://mixitup.bot/docs/events> | Ereignis-Commands, Schalter je Ereignis; abgerufen 2026-09-29 |
| Q9 | Original (Hilfestellung) | `MixItUp.Base/Model/Requirements/CooldownRequirementModel.cs @ v1.8.200`, `MixItUp.Base/ViewModel/Requirements/CooldownRequirementViewModel.cs @ v1.8.200`, `MixItUp.Base/Model/Settings/SettingsV3Model.cs @ v1.8.200` | Cooldown-Gruppen mit eigenem Namen und einer Dauer je Name, unabhängig von der Ordnergruppe, auch für Shop-Artikel (B32, B33, A4); gelesen 2026-10-02 von einem eigenen Recherche-Agenten, der nur das Verhalten in eigenen Worten weitergab |
| QP | Projekt | [Plan](../plan.md) §5.2, §6.8, §6.9 | Command-Arten und Prioritäten, Commands als Code |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-29 | Erstfassung (Entwurf) aus der offiziellen Doku und dem Plan, ohne Code des Originals |
| 2026-09-29 | Vom Projektinhaber geprüft und akzeptiert. Die offenen Fragen bleiben bis zur Prüfung am Original offen; bis dahin gilt das hier beschriebene Verhalten. |
| 2026-09-29 | Datenmodell umgesetzt. Festlegungen dabei: Der Platzhalter gilt je Command für alle seine Trigger (B13); eine Wortgrenze liegt überall, wo nicht Buchstabe oder Ziffer auf Buchstabe oder Ziffer folgt. Ohne Platzhalter folgt auf `!` und Trigger das Ende der Nachricht oder ein Leerraum (B11). Trigger mit und ohne Platzhalter teilen sich die Eindeutigkeit (B14). Argumenttypen vorerst `text`, `number` und `user` (B45, offene Frage). Dauern in Anforderungen stehen als Go-Dauer, etwa `30s` (B41, B46; Code-ADR-0009). Gruppennamen sind unabhängig von Groß- und Kleinschreibung eindeutig (B30). |
| 2026-09-30 | B1 um die Fehlerpolitik ergänzt, die die akzeptierte Spezifikation [`command-engine.md`](command-engine.md) (B71) für jeden Command vorsieht: `continue` oder `abort`, ein Pflichtfeld ohne leeren Wert (Vorgabe des Projektinhabers: keine magischen Werte, Code-ADR-0017 vorgeschlagen); gespeichert in der Spalte `error_policy` (Migration 0005), bestehende Commands bekommen mit der Migration `continue`. |
| 2026-10-02 | Benannte Cooldown-Gruppen wie im Original (Entscheidung des Projektinhabers, Q9): Gemeinsame Cooldowns hängen nicht mehr an der Gruppe des Commands, sondern an eigenen Cooldown-Gruppen mit einer Dauer je Gruppe (B32, B33, B41, Randfall B64). Die Dauer steht an der Gruppe, der Verweis geht über die ID (A4). Die Cooldown-Anforderung ist jetzt in Version 2: `standard` und `per_user` haben eine Dauer, die Gruppen-Arten nennen eine Cooldown-Gruppe; gespeicherte Gruppen-Cooldowns der Version 1 verlieren ihre Dauer und nennen keine Gruppe, bis der Streamer eine wählt. Die offene Frage zu B41 ist geklärt. |
