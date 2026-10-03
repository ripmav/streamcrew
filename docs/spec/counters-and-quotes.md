# Spezifikation: Counter und Quotes

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-10-02 |
| **Bezug** | Roadmap Phase 2.2 (Counter und Quotes), 5.6, 8.3; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md); Plan §5.5, §6.13, Anhang A.3, A.6 |
| **Umsetzung** | Datenmodell umgesetzt: `internal/domain/counter`, `internal/domain/quote`, Repositories in `internal/store`; Rücksetzen beim Start in `internal/app`. Identifier `$<name>` und `$<name>display` (B1, B4) als Quelle `template.CounterSource`; Abgleich mit eingebauten Identifiern (B7) in `Counter.CheckReserved`, aufgerufen, wenn das Speichern eines Commands einen Counter anlegt, den eine Counter-Action nennt ([`actions.md`](actions.md), B41), später auch über die API (Phase 6). Ändern über die Counter-Action (`internal/action/values`). Schrittweite (B8) als `counter.Counter.Step`, Spalte `step` aus Migration 0006. Werte und Schrittweiten sind seit Roadmap 3.5 exakte Dezimalzahlen aus `internal/decimal` (B4, B5, B8; Code-ADR-0020), gespeichert als Text (Migration 0011). Offen: Abruf und Format der Quotes per Identifier (B23, B24) mit Phase 3; vorgefertigte Quote-Commands (B22) mit Phase 5.6; Import (B25). Werte mit Nachkommastellen (B4, B5, B8) und Namen mit Buchstaben aller Schriften (B7, Spalte `name_key` aus Migration 0012) seit Roadmap 3.5. |

## Zweck und Umfang

Beschreibt die Daten von Countern (benannte Zähler, etwa Tode im Spiel) und Quotes (gesammelte Zitate) sowie ihre Grundoperationen. Die Counter-Action, die vorgefertigten Quote-Commands und die Ausgabe über Identifier folgen in Phase 3 und 5.

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Counter | benannter Zahlenwert je Profil, der sich per Action ändern und per Identifier ausgeben lässt |
| Schrittweite | Betrag, um den ein Schritt den Wert eines Counters erhöht oder verringert |
| Quote | ein gespeichertes Zitat mit Nummer, Text, Spiel bzw. Kategorie und Zeitpunkt |

## Verhalten

### Counter

| ID | Regel | Quellen |
|---|---|---|
| B1 | Ein Counter hat einen Namen und einen Wert. Der Name ist zugleich der Name seines Identifiers und deshalb eindeutig je Profil. **[Interop]** `$<name>` | Q6 |
| B2 | Operationen: um einen Betrag oder um einen Schritt (B8) erhöhen oder verringern, auf einen Wert setzen, auf 0 zurücksetzen. | Q6, A6 |
| B3 | Ein Counter kann beim Start des Cores auf 0 zurückgesetzt werden (Option je Counter). | Q6 |
| B4 | Der Wert lässt sich zusätzlich formatiert ausgeben: ganze Werte mit Tausendertrennzeichen, andere zusätzlich mit genau zwei Nachkommastellen, beides nach der Locale ([`template.md`](template.md), B41). `$<name>` selbst zeigt den Wert ohne Tausendertrennzeichen, mit dem Dezimaltrennzeichen der Locale. **[Interop]** `$<name>display` | Q6, Q8 |
| B5 | Der Wert ist eine Dezimalzahl, auch mit Nachkommastellen wie 2,5, und wird exakt gerechnet: 0,1 + 0,2 ergibt 0,3. Er hat höchstens 34 gültige Stellen und 34 Nachkommastellen, sein Betrag liegt unter 10^34; ein Ergebnis mit mehr Stellen wird auf diese Grenzen gerundet, die Hälfte zur geraden Ziffer (Code-ADR-0020). | Q8, A2 |
| B6 | Jede Änderung wird sofort gespeichert. | ADR-0012, A1 |
| B7 | Namen bestehen aus 1 bis 64 Buchstaben und Ziffern im Sinne von Unicode, also auch Umlauten und anderen Schriften ([`template.md`](template.md), B1), und dürfen nicht mit einem eingebauten Identifier kollidieren. | Q6, Q8, A3 |
| B8 | Ein Counter hat eine Schrittweite, eine Zahl größer als 0, auch mit Nachkommastellen (B5). Ohne Angabe ist sie 1, auch für Counter, die es vor der Schrittweite gab. Ein Schritt erhöht oder verringert den Wert um die Schrittweite. | A6 |

### Quotes

| ID | Regel | Quellen |
|---|---|---|
| B20 | Eine Quote hat eine Nummer, einen Text, ein Spiel bzw. eine Kategorie und einen Zeitpunkt. | Q7 |
| B21 | Die Nummer ist je Profil eindeutig und wird beim Hinzufügen fortlaufend vergeben. | Q7 |
| B22 | Quotes lassen sich hinzufügen, ändern und löschen, in der Oberfläche und über vorgefertigte Chat-Commands. | Q7, QP (Anhang A.3: Quote, LastQuote, AddQuote, DeleteQuote) |
| B23 | Abrufbar sind eine bestimmte Quote, eine zufällige, die neueste und die Gesamtzahl. **[Interop]** `$quote<n>`, `$quoterandom`, `$quotelatest`, `$quotetotal` | Q7 |
| B24 | Die Ausgabe folgt einem einstellbaren Format; ohne Format zeigt sie Nummer, Text, Spiel und Zeitpunkt. `$quotedatetime` ist das Datum ohne Uhrzeit im kurzen Format der Locale ([`template.md`](template.md), B41). Feldnamen im Format: **[Interop]** `$quotenumber`, `$quotetext`, `$quotegame`, `$quotedatetime` | Q7 |
| B25 | Quotes lassen sich aus Text- oder Tabellendateien importieren, Felder getrennt durch Komma oder Tabulator; die Spalten für Nummer, Text und Spiel wählt der Streamer. | Q7 |
| B26 | Wer eine Quote hinzugefügt hat, wird zusätzlich gespeichert, sofern bekannt. | A4 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B40 | Counter mit einem Namen, den es schon gibt | beim Anlegen abgelehnt | B1 |
| B41 | Quote wird gelöscht | ihre Nummer wird nicht neu vergeben; die übrigen Quotes behalten ihre Nummern | A5 |
| B42 | Import mit einer Nummer, die es schon gibt | die vorhandene Quote bleibt; der Import meldet den Konflikt | A5 |
| B43 | Überlauf beim Erhöhen eines Counters: ein Ergebnis, dessen Betrag 10^34 erreicht | Der Wert bleibt unverändert, die Action scheitert. | B5 |
| B44 | Schrittweite 0 oder negativ | beim Anlegen und Ändern abgelehnt; der Counter bleibt, wie er war | B8 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | Option „in Datei speichern“ schreibt den Wert in einen Ordner der Installation | Werte liegen in der Profildatenbank; eine Ausgabe als Datei, etwa für OBS-Textquellen, kann später als eigene Funktion kommen | ADR-0012; der Core hat keinen Installationsordner |
| A2 | Gleitkommazahl; `$<name>` im Format der Regionseinstellung von Windows (Q8) | exakte Dezimalzahl, Ausgabe nach der Locale (B4, B5) | keine Rundungsfehler beim Hoch- und Runterzählen; Counter mit Nachkommastellen aus dem Original lassen sich übernehmen; Entscheidung des Projektinhabers (2026-10-02) |
| A3 | Unicode-Buchstaben und -Ziffern ohne Längengrenze, kleingeschrieben gespeichert, ohne Abgleich mit eingebauten Identifiern: Ein Counter `date` überschreibt `$date` und zerstört `$datetime` (Q8) | Unicode-Buchstaben und -Ziffern, höchstens 64, Schreibweise wie eingegeben, kein Name eines eingebauten Identifiers (B7) | eindeutige Auflösung in der Template-Engine (Phase 3); Entscheidung des Projektinhabers (2026-10-02) |
| A4 | nicht dokumentiert | Urheber der Quote wird gespeichert | Nachvollziehbarkeit; optional |
| A5 | keine Umnummerierung; eine neue Quote bekommt die höchste vorhandene Nummer plus 1, nach dem Löschen der höchsten also deren Nummer wieder; der Import vergibt einer Quote mit vorhandener Nummer still eine neue (Q8) | keine Neuvergabe, keine Umnummerierung (B41); der Import meldet den Konflikt (B42) | Nummern werden im Chat zitiert und sollen stabil bleiben; Entscheidung des Projektinhabers (2026-10-02) |
| A6 | Der Betrag steht in jeder Counter-Action (Q6). | Zusätzlich hat jeder Counter eine Schrittweite, voreingestellt 1 (B8); Actions erhöhen oder verringern um einen Schritt. | Entscheidung des Projektinhabers: Der Betrag eines Counters steht an einer Stelle statt in jeder Action. |

## Akzeptanzkriterien

- [x] B1, B40: Counter-Namen sind je Profil eindeutig.
- [x] B2: Erhöhen, Setzen und Zurücksetzen verändern den gespeicherten Wert.
- [x] B3: Counter mit Rücksetz-Option stehen nach dem Start auf 0, andere behalten ihren Wert.
- [x] B8, B44: Neue Counter haben die Schrittweite 1, bestehende erhalten sie bei der Migration; ein Schritt nutzt die gespeicherte Schrittweite; eine Schrittweite von 0 oder darunter wird abgelehnt (Integrationstest gegen SQLite).
- [x] B21, B41: Nummern werden fortlaufend vergeben und nach dem Löschen nicht neu verwendet.
- [x] B23: zufällige, neueste und Gesamtzahl lassen sich abfragen (Integrationstest gegen SQLite).
- [x] B4, B5, B8: Werte und Schrittweiten mit Nachkommastellen, exakt gerechnet, gespeichert und nach der Locale ausgegeben; bestehende Counter werden übernommen.
- [x] B7: Namen mit Umlauten und anderen Schriften, auch als Identifier in Templates.
- [ ] B24: `$quotedatetime` als kurzes Datum nach der Locale.

## Offene Fragen

Keine.

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q6 | Doku | <https://mixitup.bot/docs/actions/counter-action> | Operationen, Optionen, Identifier; abgerufen 2026-09-29 |
| Q7 | Doku | <https://mixitup.bot/docs/quotes> | Felder, Identifier, Format, Import; abgerufen 2026-09-29 |
| Q8 | Original (Hilfestellung) | `MixItUp.Base/Model/Settings/CounterModel.cs @ v1.8.200`, `MixItUp.Base/Model/Actions/CounterActionModel.cs @ v1.8.200`, `MixItUp.Base/Util/StringExtensions.cs @ v1.8.200`, `MixItUp.Base/Util/SpecialIdentifierStringBuilder.cs @ v1.8.200`, `MixItUp.Base/ViewModel/User/UserQuoteViewModel.cs @ v1.8.200`, `MixItUp.Base/Model/User/UserQuoteModel.cs @ v1.8.200` | Counter mit Nachkommastellen und ihre Ausgabe (B4, B5, A2), Namen mit Unicode-Buchstaben (B7, A3), Nummern der Quotes (A5), `$quotedatetime` als kurzes Datum (B24); gelesen 2026-10-02 von einem eigenen Recherche-Agenten, der nur das Verhalten in eigenen Worten weitergab |
| QP | Projekt | [Plan](../plan.md) §5.5, Anhang A.3, A.6 | vorgefertigte Commands, Identifier-Familien |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-29 | Erstfassung (Entwurf) aus der offiziellen Doku und dem Plan, ohne Code des Originals |
| 2026-09-29 | Vom Projektinhaber geprüft und akzeptiert. Die offenen Fragen bleiben bis zur Prüfung am Original offen; bis dahin gilt das hier beschriebene Verhalten. |
| 2026-09-29 | Datenmodell umgesetzt. Festlegungen dabei: Counter-Namen bestehen aus 1 bis 64 ASCII-Buchstaben und -Ziffern, weil die Template-Engine Identifier nur aus diesen Zeichen liest, und sind unabhängig von der Schreibweise eindeutig (B1, B7). Bei einem Überlauf bleibt der Wert am Grenzwert; ob er gespeichert wird, entscheidet der Aufrufer (B43). Die neueste Quote ist die mit der höchsten Nummer (B23). Quotes haben zusätzlich eine UUID wie jede Entität (Code-ADR-0009); Nutzer sprechen sie über die Nummer an. |
| 2026-09-30 | Identifier der Counter umgesetzt (`internal/template`, `internal/domain/counter`). Festlegungen dabei: Tausendertrennzeichen nach Englisch (USA), bis das Profil eine Locale hat (B4; Spezifikation Templates, B41). Ein Name kollidiert (B7), wenn `$<name>` oder `$<name>display` mit einem eingebauten Identifier kollidiert (Spezifikation Templates, B12, B74). |
| 2026-10-01 | B43 geändert (Entscheidung des Projektinhabers): Bei einem Überlauf bleibt der Wert unverändert, statt am Grenzwert stehen zu bleiben, und die Action scheitert. Eine Änderung gilt damit ganz oder gar nicht, wie es [`actions.md`](actions.md), B42, für die Counter-Action festlegt; die Festlegung vom 2026-09-29 zu B43 entfällt. `counter.Counter.Add` lässt den Wert bei einem Überlauf unverändert. |
| 2026-10-01 | B8, B44 und A6 ergänzt, B2 erweitert (Entscheidung des Projektinhabers): Jeder Counter hat eine Schrittweite, voreingestellt 1, und lässt sich um einen Schritt erhöhen oder verringern. Umgesetzt als `counter.Counter.Step` mit `Increment` und `Decrement`; `counter.New` legt Counter mit der Schrittweite 1 an. Migration 0006 gibt bestehenden Countern die Schrittweite 1. Festlegungen dabei: Die Schrittweite ist mindestens 1, damit ein Schritt immer in die genannte Richtung geht; nach oben begrenzt sie nur der Wertebereich (B5). Ein Überlauf durch einen Schritt verhält sich wie jeder andere (B43). |
| 2026-10-02 | Offene Fragen am Original geklärt (Q8) und entschieden (Entscheidungen des Projektinhabers). Geändert: Counter-Werte sind exakte Dezimalzahlen mit Ausgabe nach der Locale (B4, B5, A2), Schrittweiten damit auch (B8); Namen dürfen Unicode-Buchstaben und -Ziffern enthalten (B7, A3). Übernommen: `$quotedatetime` ist das kurze Datum (B24). Geblieben, mit dem Verhalten des Originals unter „Abweichungen“: keine Neuvergabe von Quote-Nummern und Konflikte beim Import (A5). |
| 2026-10-03 | Counter mit Dezimalzahlen umgesetzt (B4, B5, B8, B43; Code-ADR-0020). Festlegungen dabei: Wert und Schrittweite stehen als Text in der kanonischen Form in der Datenbank; bestehende ganze Zahlen behalten ihre Ziffern (Migration 0011), der Weg zurück schneidet Nachkommastellen ab und macht eine Schrittweite unter 1 zu 1. `$<name>` zeigt den Wert exakt, `$<name>display` einen ganzen Wert mit Tausendertrennzeichen, jeden anderen zusätzlich mit genau zwei Nachkommastellen, die Hälfte von 0 weg gerundet, etwa 1,234.50; bis zur Locale-Einstellung im Format von Englisch (USA). |
| 2026-10-03 | Namen mit Buchstaben aller Schriften umgesetzt (B1, B7, Roadmap 3.5). Festlegungen dabei: Die 64 Zeichen sind Unicode-Zeichen, keine Bytes. Zeichen, die einen Buchstaben verändern (Unicode-Kategorie M), zählen mit, können einen Namen aber nicht beginnen, wie in Tokens ([`template.md`](template.md), B1). Eindeutig ist ein Name in Kleinbuchstaben nach Unicode, Zeichen für Zeichen (`counter.Key`); die Spalte `name_key` hält diesen Schlüssel (Migration 0012), weil `COLLATE NOCASE` von SQLite nur ASCII kennt. Im JSON-Schema lautet das Muster `^[\p{L}\p{Nd}][\p{L}\p{M}\p{Nd}]{0,63}$`. |
