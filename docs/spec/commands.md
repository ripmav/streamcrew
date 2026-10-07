# Spezifikation: Commands (Datenmodell)

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-10-04 |
| **Bezug** | Roadmap Phase 2.2 (Commands), 3.2–3.5, 5.4; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md), [Code-ADR-0010](../adr/code/0010-polymorphe-serialisierung.md); Plan §5.2, §5.4, §6.8, §6.9; [`users-and-roles.md`](users-and-roles.md), [`events.md`](events.md) |
| **Umsetzung** | Datenmodell umgesetzt: `internal/domain/command`, Repository in `internal/store`; Cooldown-Gruppen als `command.CooldownGroup` (B33, Migration 0007); den Schalter „aktiv“ ändern `command.Service.SwitchCommand` und `SwitchGroup` für die Command-Action ([`actions.md`](actions.md), B34), mit B14 für die Trigger. Ausführung, Sperren, Prüfung der Anforderungen und fehlerhafte Verweise (B3, B5, B15, B21, B63) folgen mit `command-engine.md` (Phase 3), B6 mit den vorgefertigten Commands (Phase 5.6). Der Schalter „`!` voranstellen“ und die Option „Platzhalter“ stehen als Trigger-Art `command.TriggerMode` im Datenmodell (`exclamation`, `literal`, `wildcard`; B11, B13, Migration 0009), Trigger mit Schreibweise und ihre Eindeutigkeit (B14) in `command.TriggerKey`, der Vergleich einer Nachricht mit einem Trigger in `command.MatchTrigger`. Die Erkennung (B16) in `command.TriggerIndex` und `command.Service.Recognize`, die Argumente einer Nachricht in `command.SplitArgs` (Roadmap 3.6); eindeutige Namen (B7) mit `command.NameKey` und der Spalte `name_key` (Migrationen 0013 und 14) seit Roadmap 3.5 |

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
| B7 | Der Name eines Commands ist ohne Rücksicht auf die Schreibweise eindeutig, auch über die Arten hinweg und bei Buchstaben jenseits von ASCII; ein Command darf die Schreibweise seines eigenen Namens ändern. Daran erkennen Commands als Code einen Command (`commands-as-code.md`, B22). | Entscheidung des Projektinhabers |
| B8 | Einen Command, eine Command-Gruppe oder eine Cooldown-Gruppe, auf die ein anderer Command verweist, kann man nicht löschen: über eine Command-Action ([`actions.md`](actions.md), B31, B34) oder einen Cooldown (B33). Das Löschen scheitert und nennt die verweisenden Commands mit der Action oder dem Cooldown; einen Command kann man stattdessen deaktivieren (B3). Ein Verweis eines Commands auf sich selbst zählt nicht. Dass ein Command zu einer Gruppe gehört, ist kein solcher Verweis (B62). | Entscheidung des Projektinhabers |

### Chat-Commands

| ID | Regel | Quellen |
|---|---|---|
| B10 | Ein Chat-Command hat einen oder mehrere Trigger. | Q4 |
| B11 | Ein Chat-Command hat den Schalter „`!` voranstellen“, Standard an. Ist er an, beginnt ein Trigger im Chat mit `!`; gespeichert wird er ohne `!`. Ist er aus, gilt der Trigger wörtlich, ohne Präfix (`hallo`) oder mit einem eigenen (`?hallo`, auch `!hallo`). Standardmäßig ist ein Trigger ein einzelnes Wort. Im Datenmodell sind Schalter und Platzhalter (B13) zusammen die Trigger-Art eines Chat-Commands, ein Pflichtfeld: `exclamation` (Schalter an), `literal` (Schalter aus) oder `wildcard` (Platzhalter, für den der Schalter nicht gilt). | Q4, Q10 |
| B12 | Mehrere Trigger werden bei der Eingabe durch Leerzeichen getrennt; Trigger aus mehreren Wörtern durch Semikolon. Gespeichert wird eine Liste einzelner Trigger. | Q4 |
| B13 | Mit der Option „Platzhalter“ (Wildcard) wird ein Trigger überall in der Nachricht als ganzes Wort erkannt, ohne `!` und ohne Beachtung der Groß- und Kleinschreibung: Der Trigger `what` passt auf „what is going on?“ und auf „What?“, aber nicht auf ein Wort, das `what` nur enthält. Wortgrenzen sind Anfang und Ende der Nachricht und jedes Zeichen, das kein Buchstabe und keine Ziffer ist. Ein `!` gehört nur dazu, wenn es im Trigger steht. Die Argumente sind der Text nach dem Treffer. | Q4, Q10, A5 |
| B14 | Trigger beachten die Groß- und Kleinschreibung: `!Hallo` und `!hallo` sind verschiedene Trigger und können verschiedene Commands auslösen. Innerhalb aller aktiven Chat-Commands ist ein Trigger in genau dieser Schreibweise eindeutig, so wie er im Chat steht: `!hallo` ohne Schalter ist derselbe Trigger wie `hallo` mit Schalter; Platzhalter-Trigger, die die Schreibweise nie beachten (B13), sind unter den Platzhalter-Triggern ohne Beachtung der Schreibweise eindeutig. | QP, A1 |
| B15 | Die auslösende Nachricht ist während der Ausführung als ganze verfügbar, samt Trigger. **[Interop]** `$message` | Q4 |
| B16 | Erkennung einer Chatnachricht: Ein Trigger passt, wenn die Nachricht mit ihm beginnt, samt `!` bei eingeschaltetem Schalter (B11), und danach endet oder Leerraum folgt. Zuerst zählen Treffer in exakter Schreibweise; von ihnen gewinnt der längste, sodass bei `!a` und `!a b` die Nachricht „!a b c“ den Trigger `!a b` mit dem Argument „c“ auslöst. Gibt es keinen exakten Treffer, zählen Treffer ohne Beachtung der Schreibweise: Der längste gewinnt, wenn er eindeutig ist; sonst löst die Nachricht nichts aus, mit einem Eintrag im Log. Erst wenn kein solcher Trigger passt, werden die Platzhalter-Trigger geprüft (B13); von ihnen gewinnt der, der am weitesten vorn in der Nachricht steht, bei gleicher Stelle der längere. Je Nachricht läuft höchstens ein Command. | Q10, A1 |

### Ereignis-, Timer- und Action-Gruppen-Commands

| ID | Regel | Quellen |
|---|---|---|
| B20 | Ein Ereignis-Command ist genau einem Ereignistyp aus dem Katalog zugeordnet ([`events.md`](events.md)); je Ereignistyp gibt es höchstens einen Ereignis-Command. | Q8 |
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
| B45 | **Argumente:** geordnete Liste mit Name, Typ (`text`, `number`, `integer` oder `user`, [`requirements.md`](requirements.md), B33), Pflicht oder optional und optional dem Namen eines Identifiers, unter dem der Wert verfügbar ist. | Q3, Q9 |
| B46 | **Schwelle:** Mindestzahl verschiedener Nutzer innerhalb eines Zeitfensters, bevor der Command läuft; optional läuft er dann für jeden dieser Nutzer einzeln. | Q3 |
| B47 | **Einstellungen:** die auslösende Chatnachricht nach der Ausführung löschen; den Command im Kontextmenü des Chats anbieten. | Q3 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B60 | Trigger `Hug` gespeichert, Nachricht „!hug“ | löst aus, solange es keinen Trigger `hug` gibt; angezeigt wie eingegeben | B14, B16 |
| B61 | Leere Trigger-Liste bei einem Chat-Command | beim Speichern abgelehnt | B10 |
| B62 | Gruppe wird gelöscht | ihre Commands verlieren die Gruppenzugehörigkeit und bleiben erhalten; nennt eine Command-Action eines Commands die Gruppe, scheitert das Löschen (B8) | B8, B30 |
| B63 | Anforderung verweist auf eine gelöschte Währung, einen Rang oder Gegenstand | Command bleibt gespeichert, gilt aber als fehlerhaft und wird nicht ausgeführt, bis der Verweis repariert ist | B42–B44 |
| B64 | Eine Cooldown-Gruppe, die ein Cooldown nennt, soll gelöscht werden | Das Löschen scheitert und nennt die Commands (B8). Fehlerhaft ist ein Gruppen-Cooldown nur noch ohne Cooldown-Gruppe, etwa nach der Migration von Version 1 ([`requirements.md`](requirements.md), B7). | B8, B33 |
| B65 | Trigger `Hallo` und `hallo` an zwei Commands, Nachricht „!HALLO“ | löst nichts aus, weil beide ohne Schreibweise passen; Eintrag im Log | B16 |
| B66 | Schalter „`!` voranstellen“ aus, Trigger `?hallo`, Nachrichten „?hallo welt“ und „!?hallo“ | die erste löst mit dem Argument „welt“ aus, die zweite nicht | B11, B16 |
| B67 | Ein Platzhalter-Trigger `hallo` und ein normaler Trigger `hallo`, Nachricht „!hallo“ | der normale Trigger gewinnt | B16 |
| B68 | Gespeicherte Commands mit Namen, die sich nur in der Schreibweise unterscheiden, beim Wechsel auf eindeutige Namen | Der älteste behält seinen Namen, die übrigen bekommen das erste freie Suffix „ (2)“, „ (3)“ und so fort. | B7 |
| B69 | Ein Command ruft nur sich selbst auf und soll gelöscht werden | Das Löschen gelingt; der Verweis geht mit dem Command. | B8 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | Erkennung ohne Beachtung der Schreibweise; beim Speichern sind nur exakt gleiche Trigger aktiver Commands verboten, sodass bei `!Hallo` und `!hallo` still der zuletzt gespeicherte gewinnt; bei Mehrwort-Triggern gewinnt der kürzeste Treffer, `!a b` neben `!a` ist unerreichbar (Q10) | Schreibweise unterscheidet Trigger, exakte Treffer vor eindeutigen ohne Schreibweise, mehrdeutige lösen nichts aus; der längste Treffer gewinnt (B14, B16) | Verschieden geschriebene Trigger können verschiedene Commands auslösen, und kein Command ist still unerreichbar; eine automatische Großschreibung am Handy trifft trotzdem; Entscheidung des Projektinhabers (2026-10-02) |
| A3 | Commands in einer internen Einstellungsdatei | Commands als versionierte Dokumente in der Profildatenbank, als YAML exportierbar | Plan §6.9, ADR-0012 |
| A4 | Cooldown-Gruppen über ihren Namen; die Dauer stellt das Cooldown-Feld eines Commands ein und gilt danach für alle Commands mit diesem Namen (Q9) | Dauer an der Cooldown-Gruppe selbst, Verweis über die ID (B33) | eine Stelle für die Dauer; Umbenennen bricht keine Verweise; Entscheidung des Projektinhabers (2026-10-02) |
| A5 | Platzhalter-Trigger nur zwischen Leerraum: „what?“ und „(what)“ passen nicht (Q10) | Wortgrenzen auch an Satz- und Sonderzeichen (B13) | Fragen und Klammern sind im Chat üblich; Entscheidung des Projektinhabers (2026-10-02) |

## Akzeptanzkriterien

- [x] B1, B4: Ein Command mit Actions unbekannten Typs wird gespeichert, geladen und unverändert zurückgeschrieben.
- [x] B7, B68: Ein zweiter Command mit einem Namen in anderer Schreibweise wird abgelehnt, auch jenseits von ASCII; gespeicherte doppelte Namen bekommen beim Wechsel ein Suffix.
- [x] B11, B12: Trigger werden mit dem Schalter ohne `!` und als Liste gespeichert; Eingaben mit Leerzeichen und Semikolon werden richtig zerlegt.
- [x] B13: Tabellengetriebener Test der Wortgrenzen für Platzhalter-Trigger.
- [x] B14: Ein zweiter aktiver Chat-Command mit gleichem Trigger wird abgelehnt (bis 2026-10-02 ohne Beachtung der Schreibweise).
- [x] B11, B14: Schalter „`!` voranstellen“; Trigger, die sich nur in der Schreibweise unterscheiden, an verschiedenen Commands; Platzhalter-Trigger eindeutig ohne Schreibweise.
- [x] B16, B60, B65–B67: Erkennung mit exakten und eindeutigen Treffern, längstem Treffer, eigenem Präfix und Platzhaltern zuletzt, als Tabellentest mit Fuzzing. Erledigt 2026-10-04: `TestRecognize`, `FuzzRecognize`.
- [x] B20: Ein zweiter Ereignis-Command für denselben Typ wird abgelehnt.
- [x] B30, B62: Gruppennamen sind eindeutig; Löschen einer Gruppe lässt ihre Commands bestehen.
- [x] B8, B62, B64, B69: Löschen scheitert, solange ein anderer Command auf Command, Gruppe oder Cooldown-Gruppe verweist, und nennt ihn; Selbstbezug und Gruppenzugehörigkeit hindern nicht. Erledigt 2026-10-04: `command.Service.Delete`, `DeleteGroup` und `DeleteCooldownGroup` liefern `command.InUseError`.
- [x] B40–B47: Jede Anforderungsart lässt sich speichern und laden (Golden Files, Code-ADR-0010).

## Offene Fragen

Keine.

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q3 | Doku | <https://mixitup.bot/docs/commands> | Arten, Sperren, Anforderungen, Cooldown-Arten; abgerufen 2026-09-29 |
| Q4 | Doku | <https://mixitup.bot/docs/chat/chat-commands> | Trigger, Trennzeichen, Platzhalter, `$message`; abgerufen 2026-09-29 |
| Q5 | Doku | <https://mixitup.bot/docs/timers> | Timer, Gruppen mit eigenem Intervall; abgerufen 2026-09-29 |
| Q8 | Doku | <https://mixitup.bot/docs/events> | Ereignis-Commands, Schalter je Ereignis; abgerufen 2026-09-29 |
| Q9 | Original (Hilfestellung) | `MixItUp.Base/Model/Requirements/CooldownRequirementModel.cs @ v1.8.200`, `MixItUp.Base/Model/Requirements/ArgumentsRequirementModel.cs @ v1.8.200`, `MixItUp.Base/ViewModel/Requirements/CooldownRequirementViewModel.cs @ v1.8.200`, `MixItUp.Base/Model/Settings/SettingsV3Model.cs @ v1.8.200` | Cooldown-Gruppen mit eigenem Namen und einer Dauer je Name, unabhängig von der Ordnergruppe, auch für Shop-Artikel (B32, B33, A4); Argumenttypen Nutzer, Ganzzahl, Dezimalzahl und Text (B45); gelesen 2026-10-02 von einem eigenen Recherche-Agenten, der nur das Verhalten in eigenen Worten weitergab |
| Q10 | Original (Hilfestellung) | `MixItUp.Base/Model/Commands/ChatCommandModel.cs @ v1.8.200`, `MixItUp.Base/ViewModel/Commands/ChatCommandEditorWindowViewModel.cs @ v1.8.200`, `MixItUp.Base/Services/ChatService.cs @ v1.8.200` | Schalter für das `!` je Command, Platzhalter-Trigger ohne `!` und ohne Schreibweise (B11, B13); doppelte Trigger, kürzester Mehrwort-Treffer, Grenzen nur an Leerraum (A1, A5); gelesen 2026-10-02 von einem eigenen Recherche-Agenten, der nur das Verhalten in eigenen Worten weitergab |
| QP | Projekt | [Plan](../plan.md) §5.2, §6.8, §6.9 | Command-Arten und Prioritäten, Commands als Code |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-29 | Erstfassung (Entwurf) aus der offiziellen Doku und dem Plan, ohne Code des Originals |
| 2026-09-29 | Vom Projektinhaber geprüft und akzeptiert. Die offenen Fragen bleiben bis zur Prüfung am Original offen; bis dahin gilt das hier beschriebene Verhalten. |
| 2026-09-29 | Datenmodell umgesetzt. Festlegungen dabei: Der Platzhalter gilt je Command für alle seine Trigger (B13); eine Wortgrenze liegt überall, wo nicht Buchstabe oder Ziffer auf Buchstabe oder Ziffer folgt. Ohne Platzhalter folgt auf `!` und Trigger das Ende der Nachricht oder ein Leerraum (B11). Trigger mit und ohne Platzhalter teilen sich die Eindeutigkeit (B14). Argumenttypen vorerst `text`, `number` und `user` (B45, offene Frage). Dauern in Anforderungen stehen als Go-Dauer, etwa `30s` (B41, B46; Code-ADR-0009). Gruppennamen sind unabhängig von Groß- und Kleinschreibung eindeutig (B30). |
| 2026-09-30 | B1 um die Fehlerpolitik ergänzt, die die akzeptierte Spezifikation [`command-engine.md`](command-engine.md) (B71) für jeden Command vorsieht: `continue` oder `abort`, ein Pflichtfeld ohne leeren Wert (Vorgabe des Projektinhabers: keine magischen Werte, Code-ADR-0017 vorgeschlagen); gespeichert in der Spalte `error_policy` (Migration 0005), bestehende Commands bekommen mit der Migration `continue`. |
| 2026-10-02 | Benannte Cooldown-Gruppen wie im Original (Entscheidung des Projektinhabers, Q9): Gemeinsame Cooldowns hängen nicht mehr an der Gruppe des Commands, sondern an eigenen Cooldown-Gruppen mit einer Dauer je Gruppe (B32, B33, B41, Randfall B64). Die Dauer steht an der Gruppe, der Verweis geht über die ID (A4). Die Cooldown-Anforderung ist jetzt in Version 2: `standard` und `per_user` haben eine Dauer, die Gruppen-Arten nennen eine Cooldown-Gruppe; gespeicherte Gruppen-Cooldowns der Version 1 verlieren ihre Dauer und nennen keine Gruppe, bis der Streamer eine wählt. Die offene Frage zu B41 ist geklärt. |
| 2026-10-02 | B45: neuer Argumenttyp `integer` neben `text`, `number` und `user` (Entscheidung des Projektinhabers; Einzelheiten in [`requirements.md`](requirements.md), B33). Die offene Frage zu B45 ist geklärt. |
| 2026-10-02 | Offene Fragen am Original geklärt (Q10) und entschieden (Entscheidungen des Projektinhabers). Übernommen: der Schalter „`!` voranstellen“ je Chat-Command (B11) und Platzhalter-Trigger ohne `!` und ohne Schreibweise (B13). Geändert: Trigger beachten die Schreibweise, mit exakten Treffern vor eindeutigen ohne Schreibweise (B14, A1); neue Regel B16 für die Erkennung mit dem längsten Treffer; Randfälle B60 und B65–B67. Geblieben, mit dem Verhalten des Originals unter „Abweichungen“: Wortgrenzen der Platzhalter an Satzzeichen (A5). Übereinstimmend: höchstens ein Ereignis-Command je Ereignistyp (B20, die Zeile A2 entfällt). |
| 2026-10-03 | Schalter, Platzhalter und Schreibweise im Datenmodell umgesetzt (Roadmap 3.5). Festlegungen dabei: Der Schalter „`!` voranstellen“ und die Option „Platzhalter“ sind zusammen die Trigger-Art `triggerMode` mit den Werten `exclamation`, `literal` und `wildcard` (B11, Entscheidung des Projektinhabers): So gibt es keine Kombination ohne Wirkung, und die Art ist ein Pflichtfeld ohne Standard über einen leeren Wert (Code-ADR-0017). Die Oberfläche kann sie weiter als zwei Schalter zeigen. Gespeicherte Chat-Commands werden zu `wildcard` oder `exclamation` (Migration 0009). Nur mit `exclamation` verliert ein Trigger bei der Eingabe ein führendes `!`; wörtliche und Platzhalter-Trigger behalten es (B11, B13). Eindeutig ist ein Trigger so, wie er im Chat steht (B14): `!hallo` ohne Schalter kollidiert mit `hallo` mit Schalter. Innerhalb eines Commands gilt dasselbe, er darf also `Hug` und `hug` zugleich haben. |
| 2026-10-03 | B7 und Randfall B68 neu (Entscheidung des Projektinhabers, `commands-as-code.md`, B22): Command-Namen sind ohne Rücksicht auf die Schreibweise eindeutig. Festlegungen dabei: Verglichen wird in Kleinbuchstaben nach Unicode, Zeichen für Zeichen (`command.NameKey`); die Spalte `name_key` mit eindeutigem Index hält den Schlüssel. SQLite kann Kleinbuchstaben nur für ASCII bilden, deshalb füllt eine Migration in Go (Nummer 14, nach der SQL-Migration 0013 mit der Spalte) den Schlüssel und benennt doppelte Namen um. |
| 2026-10-04 | B8 und Randfall B69 neu, B62 und B64 angepasst (Entscheidung des Projektinhabers): Commands, Command-Gruppen und Cooldown-Gruppen, auf die ein anderer Command verweist, lassen sich nicht löschen, nur Commands deaktivieren. So entstehen keine Verweise auf gelöschte Objekte mehr; ein Command mit gelöschter Cooldown-Gruppe wird nicht mehr fehlerhaft, und der Export scheitert nicht mehr daran ([`commands-as-code.md`](commands-as-code.md), B64). Ein Verweis auf sich selbst zählt nicht. |
| 2026-10-04 | B8 umgesetzt. Festlegungen dabei: Der Fehler `command.InUseError` (passt zu `command.ErrInUse`) nennt jeden verweisenden Command mit den Stellen, etwa „action 2.1“ oder „cooldown“, und bei Commands den Hinweis, sie stattdessen auszuschalten. Geprüft wird beim Löschen über den Command-Service; Actions unbekannter Typen kennen ihre Verweise nicht und zählen deshalb nicht. |
| 2026-10-04 | B16 umgesetzt (Roadmap 3.6). Festlegungen dabei: Mehrdeutig ist ein Treffer nur, wenn die gleich guten Treffer zu verschiedenen Commands gehören; ein Command mit `Hug` und `hug` löst also aus. Ein mehrdeutiger Treffer lässt auch die Platzhalter nicht mehr zählen, weil schon ein Trigger passt (B65). Inaktive Commands zählen bei der Erkennung nicht (B3, B14). Platzhalter vergleichen Zeichen für Zeichen mit einfacher Unicode-Faltung wie die anderen Trigger. Der Text nach dem Trigger beginnt ohne den trennenden Leerraum. Er zerfällt an Leerraum in Argumente; Text in doppelten Anführungszeichen ist ein Argument ohne die Zeichen, auch leer oder mit Leerraum. Ein Anführungszeichen öffnet nur am Anfang eines Arguments und schließt nur vor Leerraum oder am Ende; jedes andere, auch eines, das nie geschlossen wird, gehört zum Argument. Der Command-Service hält die Trigger und die Ereignis-Commands nach jeder Änderung über ihn neu bereit, statt sie für jede Nachricht aus dem Store zu lesen. |
