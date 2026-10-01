# Spezifikation: Counter und Quotes

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-10-01 |
| **Bezug** | Roadmap Phase 2.2 (Counter und Quotes), 5.6, 8.3; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md); Plan §5.5, §6.13, Anhang A.3, A.6 |
| **Umsetzung** | Datenmodell umgesetzt: `internal/domain/counter`, `internal/domain/quote`, Repositories in `internal/store`; Rücksetzen beim Start in `internal/app`. Identifier `$<name>` und `$<name>display` (B1, B4) als Quelle `template.CounterSource`; Abgleich mit eingebauten Identifiern (B7) in `Counter.CheckReserved`, aufgerufen, sobald sich Counter anlegen lassen (Actions 3.3, API Phase 6). Offen: Abruf und Format der Quotes per Identifier (B23, B24) mit Phase 3; vorgefertigte Quote-Commands (B22) mit Phase 5.6; Import (B25) |

## Zweck und Umfang

Beschreibt die Daten von Countern (benannte Zähler, etwa Tode im Spiel) und Quotes (gesammelte Zitate) sowie ihre Grundoperationen. Die Counter-Action, die vorgefertigten Quote-Commands und die Ausgabe über Identifier folgen in Phase 3 und 5.

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Counter | benannter Zahlenwert je Profil, der sich per Action ändern und per Identifier ausgeben lässt |
| Quote | ein gespeichertes Zitat mit Nummer, Text, Spiel bzw. Kategorie und Zeitpunkt |

## Verhalten

### Counter

| ID | Regel | Quellen |
|---|---|---|
| B1 | Ein Counter hat einen Namen und einen Wert. Der Name ist zugleich der Name seines Identifiers und deshalb eindeutig je Profil. **[Interop]** `$<name>` | Q6 |
| B2 | Operationen: um einen Betrag erhöhen oder verringern, auf einen Wert setzen, auf 0 zurücksetzen. | Q6 |
| B3 | Ein Counter kann beim Start des Cores auf 0 zurückgesetzt werden (Option je Counter). | Q6 |
| B4 | Der Wert lässt sich zusätzlich mit Tausendertrennzeichen formatiert ausgeben. **[Interop]** `$<name>display` | Q6 |
| B5 | Der Wert ist eine ganze Zahl (64 Bit). | Q6 (Beispiele mit ganzen Zahlen), A2 |
| B6 | Jede Änderung wird sofort gespeichert. | ADR-0012, A1 |
| B7 | Namen bestehen aus Buchstaben und Ziffern und dürfen nicht mit einem eingebauten Identifier kollidieren. | Q6, A3 |

### Quotes

| ID | Regel | Quellen |
|---|---|---|
| B20 | Eine Quote hat eine Nummer, einen Text, ein Spiel bzw. eine Kategorie und einen Zeitpunkt. | Q7 |
| B21 | Die Nummer ist je Profil eindeutig und wird beim Hinzufügen fortlaufend vergeben. | Q7 |
| B22 | Quotes lassen sich hinzufügen, ändern und löschen, in der Oberfläche und über vorgefertigte Chat-Commands. | Q7, QP (Anhang A.3: Quote, LastQuote, AddQuote, DeleteQuote) |
| B23 | Abrufbar sind eine bestimmte Quote, eine zufällige, die neueste und die Gesamtzahl. **[Interop]** `$quote<n>`, `$quoterandom`, `$quotelatest`, `$quotetotal` | Q7 |
| B24 | Die Ausgabe folgt einem einstellbaren Format; ohne Format zeigt sie Nummer, Text, Spiel und Zeitpunkt. Feldnamen im Format: **[Interop]** `$quotenumber`, `$quotetext`, `$quotegame`, `$quotedatetime` | Q7 |
| B25 | Quotes lassen sich aus Text- oder Tabellendateien importieren, Felder getrennt durch Komma oder Tabulator; die Spalten für Nummer, Text und Spiel wählt der Streamer. | Q7 |
| B26 | Wer eine Quote hinzugefügt hat, wird zusätzlich gespeichert, sofern bekannt. | A4 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B40 | Counter mit einem Namen, den es schon gibt | beim Anlegen abgelehnt | B1 |
| B41 | Quote wird gelöscht | ihre Nummer wird nicht neu vergeben; die übrigen Quotes behalten ihre Nummern | A5 |
| B42 | Import mit einer Nummer, die es schon gibt | die vorhandene Quote bleibt; der Import meldet den Konflikt | A5 |
| B43 | Überlauf beim Erhöhen eines Counters | Der Wert bleibt unverändert, die Action scheitert. | B5 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | Option „in Datei speichern“ schreibt den Wert in einen Ordner der Installation | Werte liegen in der Profildatenbank; eine Ausgabe als Datei, etwa für OBS-Textquellen, kann später als eigene Funktion kommen | ADR-0012; der Core hat keinen Installationsordner |
| A2 | Werttyp nicht dokumentiert | ganze Zahl (64 Bit) | Beispiele der Doku; zu prüfen (offene Frage) |
| A3 | Namensregeln nicht dokumentiert | Buchstaben und Ziffern, keine Kollision mit eingebauten Identifiern | eindeutige Auflösung in der Template-Engine (Phase 3) |
| A4 | nicht dokumentiert | Urheber der Quote wird gespeichert | Nachvollziehbarkeit; optional |
| A5 | Verhalten der Nummern nach dem Löschen nicht dokumentiert | keine Neuvergabe, keine Umnummerierung | Nummern werden im Chat zitiert und sollen stabil bleiben; zu prüfen |

## Akzeptanzkriterien

- [x] B1, B40: Counter-Namen sind je Profil eindeutig.
- [x] B2: Erhöhen, Setzen und Zurücksetzen verändern den gespeicherten Wert.
- [x] B3: Counter mit Rücksetz-Option stehen nach dem Start auf 0, andere behalten ihren Wert.
- [x] B21, B41: Nummern werden fortlaufend vergeben und nach dem Löschen nicht neu verwendet.
- [x] B23: zufällige, neueste und Gesamtzahl lassen sich abfragen (Integrationstest gegen SQLite).

## Offene Fragen

- B5/A2: Kann ein Counter im Original Nachkommastellen haben?
- B41/A5: Vergibt das Original nach dem Löschen einer Quote die Nummer neu oder nummeriert es um?
- B7: Welche Zeichen erlaubt das Original in Counter-Namen?
- B20: Welches Datumsformat nutzt `$quotedatetime` bei anderen Sprachen als Englisch?

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q6 | Doku | <https://mixitup.bot/docs/actions/counter-action> | Operationen, Optionen, Identifier; abgerufen 2026-09-29 |
| Q7 | Doku | <https://mixitup.bot/docs/quotes> | Felder, Identifier, Format, Import; abgerufen 2026-09-29 |
| QP | Projekt | [Plan](../plan.md) §5.5, Anhang A.3, A.6 | vorgefertigte Commands, Identifier-Familien |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-29 | Erstfassung (Entwurf) aus der offiziellen Doku und dem Plan, ohne Code des Originals |
| 2026-09-29 | Vom Projektinhaber geprüft und akzeptiert. Die offenen Fragen bleiben bis zur Prüfung am Original offen; bis dahin gilt das hier beschriebene Verhalten. |
| 2026-09-29 | Datenmodell umgesetzt. Festlegungen dabei: Counter-Namen bestehen aus 1 bis 64 ASCII-Buchstaben und -Ziffern, weil die Template-Engine Identifier nur aus diesen Zeichen liest, und sind unabhängig von der Schreibweise eindeutig (B1, B7). Bei einem Überlauf bleibt der Wert am Grenzwert; ob er gespeichert wird, entscheidet der Aufrufer (B43). Die neueste Quote ist die mit der höchsten Nummer (B23). Quotes haben zusätzlich eine UUID wie jede Entität (Code-ADR-0009); Nutzer sprechen sie über die Nummer an. |
| 2026-09-30 | Identifier der Counter umgesetzt (`internal/template`, `internal/domain/counter`). Festlegungen dabei: Tausendertrennzeichen nach Englisch (USA), bis das Profil eine Locale hat (B4; Spezifikation Templates, B41). Ein Name kollidiert (B7), wenn `$<name>` oder `$<name>display` mit einem eingebauten Identifier kollidiert (Spezifikation Templates, B12, B74). |
| 2026-10-01 | B43 geändert (Entscheidung des Projektinhabers): Bei einem Überlauf bleibt der Wert unverändert, statt am Grenzwert stehen zu bleiben, und die Action scheitert. Eine Änderung gilt damit ganz oder gar nicht, wie es [`actions.md`](actions.md), B42, für die Counter-Action festlegt; die Festlegung vom 2026-09-29 zu B43 entfällt. `counter.Counter.Add` lässt den Wert bei einem Überlauf unverändert. |
