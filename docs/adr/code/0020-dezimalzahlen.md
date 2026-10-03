# Code-ADR-0020: Exakte Dezimalzahlen mit `cockroachdb/apd`

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-10-03 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Ersetzt in Punkt 8 von [Code-ADR-0012](0012-template-engine.md) die Bibliothek für Ausdrücke; Plan §6.10, §8; Roadmap Phase 3.5; [`template.md`](../../spec/template.md) B41, B50–B53; [`counters-and-quotes.md`](../../spec/counters-and-quotes.md) B4, B5, B8 und A2 in der Fassung vom 2026-10-02 (PR #102); [`actions.md`](../../spec/actions.md) B4, B40–B43; [`requirements.md`](../../spec/requirements.md) B33; [Code-ADR-0008](0008-datenbankzugriff.md), [Code-ADR-0017](0017-klare-signale-statt-magischer-werte.md), [Code-ADR-0018](0018-json-v2.md) |

## Kontext

- **Counter:** Die Entscheidung vom 2026-10-02 (`counters-and-quotes.md`, B5 und A2, PR #102) lautet:
  - Der Wert eines Counters ist eine Dezimalzahl, auch mit Nachkommastellen wie 2,5, und wird exakt gerechnet: 0,1 + 0,2 ergibt 0,3.
  - Die Schrittweite darf ebenfalls Nachkommastellen haben (B8).
  - Typ, Wertebereich und Genauigkeit legt ein Code-ADR fest.
  - Das Original rechnet mit Gleitkommazahlen.
- **Heute rechnet alles mit Gleitkomma:**
  - Ausdrücke rechnen mit `float64`, ausgewertet von `expr-lang/expr` (Code-ADR-0012, Punkt 8, `internal/expr`).
  - Zahlen in Templates, Mengenangaben der Actions und Argumente vom Typ `number` tragen ihren Zahlenwert als `float64`.
  - Wert und Schrittweite der Counter sind ganze Zahlen mit 64 Bit.
- **Warum ein Dezimaltyp nur für Counter nicht genügt:** Die Beträge der Counter-Action sind Ausdrücke. Ein Betrag wie `0.1 + 0.2` wäre schon ungenau (0,30000000000000004), bevor der Counter ihn übernimmt.
- **Vorgabe des Projektinhabers (2026-10-03):** Es wird so früh wie möglich nur noch mit apd gerechnet. Ein Ausdruck wird schon beim Lesen in Dezimalzahlen zerlegt, nicht erst mit `float64` ausgerechnet.
- **Die Standardbibliothek** hat keinen Dezimaltyp: `math/big.Float` rechnet binär, `math/big.Rat` mit Brüchen. Der Vorschlag `imath/decimal` ([golang/go#81886](https://github.com/golang/go/issues/81886)) wurde am 2026-09-30 geschlossen.
- **Geprüfte Bibliotheken** (Stand 2026-10-03, GitHub-API):

  | Bibliothek | Letztes Release | Commits seit Okt. 2025 | Art | Lizenz |
  |---|---|---|---|---|
  | `github.com/cockroachdb/apd/v3` | v3.2.3, 2026-03-23 | 10, gepflegt von Cockroach Labs | beliebig genau, nach „General Decimal Arithmetic“ wie Pythons `decimal` | Apache-2.0 |
  | `github.com/woodsbury/decimal128` | v1.5.0, 2026-09-06 | 5, eine Person | IEEE 754 decimal128, 34 Stellen | 0BSD |
  | `github.com/quagmt/udecimal` | v1.10.1, 2026-06-12 | 2, im Wesentlichen eine Person | Festkomma, bis 19 Nachkommastellen | BSD-3-Clause |
  | `github.com/shopspring/decimal` | v1.4.0, 2024-04-12 | 6, ohne neues Release | beliebig genau | MIT |
  | `github.com/govalues/decimal` | v0.1.36, 2025-01-18 | 0 | Festkomma, 19 Stellen | MIT |
  | `github.com/ericlagergren/decimal` | v3.3.1, 2019-01-02 | 0 | beliebig genau | BSD-3-Clause |

## Entscheidung

1. **Bibliothek:** Dezimalzahlen rechnet `github.com/cockroachdb/apd/v3` (Entscheidung des Projektinhabers).
   - Begründung für die Abhängigkeit: Die Standardbibliothek hat keinen Dezimaltyp. apd ist von den Kandidaten am besten gepflegt und steht unter derselben Lizenz wie streamcrew.
   - apd hat keinen änderbaren Zustand auf Paketebene und gibt Fehler statt `panic` zurück.
   - `lib/pq` in seiner `go.mod` nutzen nur seine Tests.
2. **Eigener Typ:** Das Paket `internal/decimal` kapselt apd im Typ `decimal.Decimal`.
   - Er wird als Wert benutzt: Methoden ändern ihn nicht, sondern liefern einen neuen Wert und, wo eine Rechnung scheitern kann, einen Fehler. Intern wird kein bestehender Wert verändert, damit Kopien sicher sind.
   - Der Nullwert ist 0.
   - Typen von apd erscheinen in keiner Schnittstelle anderer Pakete; ein Wechsel der Bibliothek bleibt auf `internal/decimal` beschränkt.
3. **Alle Zahlen sind Dezimalzahlen** (Vorgabe des Projektinhabers):
   - Gemeint sind die Zahlen in Ausdrücken, die Zahlenwerte von Templates, die Mengenangaben der Actions, Argumente vom Typ `number` und `integer` sowie Wert und Schrittweite der Counter.
   - `float64` kommt in diesen Wegen nicht mehr vor.
   - Wo ein Go-Typ eine andere Zahl verlangt, etwa `time.Duration` oder `int`, wandelt der Code ausdrücklich um und prüft den Bereich. Wo eine Stelle eine ganze Zahl braucht, scheitert die Action an Nachkommastellen (`actions.md`, B4).
4. **Genauigkeit und Wertebereich:** Eine Zahl hat höchstens 34 gültige Stellen, so viele wie IEEE 754 decimal128, und höchstens 34 Nachkommastellen; ihr Betrag ist kleiner als 10^34.
   - Eine feste Zahl an Nachkommastellen gibt es nicht (Entscheidung des Projektinhabers).
5. **Exakt, wo es geht; sonst auf 34 Stellen gerundet** (Entscheidung des Projektinhabers):
   - **Exakt:** Jede Rechnung ist exakt, solange ihr Ergebnis in die Grenzen aus Punkt 4 passt: 0,1 + 0,2 ergibt 0,3.
   - **Gerundet:** Ein Ergebnis mit mehr Stellen wird auf 34 gültige Stellen und höchstens 34 Nachkommastellen gerundet, die Hälfte zur geraden Ziffer wie in IEEE 754. Das betrifft etwa 10 / 3 (3,333…, 34 Stellen), Wurzeln und Logarithmen. Ebenso wird ein Text mit mehr Stellen beim Lesen gerundet.
   - **Fehler** ([Code-ADR-0017](0017-klare-signale-statt-magischer-werte.md)):
     - ein Betrag ab 10^34;
     - Division durch 0;
     - Rechnungen ohne Ergebnis, etwa die Wurzel oder der Logarithmus einer negativen Zahl.

     NaN und ±Unendlich gibt es nicht.
   - **Counter:** Für die Counter-Action heißt das wie beim Überlauf heute: Ein Fehler lässt die Action scheitern, und der Wert bleibt unverändert (`actions.md`, B42).
6. **Ausdrücke mit eigenem Auswerter** in `internal/expr` (Entscheidung des Projektinhabers). Er ersetzt `expr-lang/expr` und damit Punkt 8 von Code-ADR-0012, soweit es um die Bibliothek geht.
   - **Lesen:** Ein eigener Lexer und Parser liest die Sprache aus `template.md`, B50 und B53. Zahlen liest er exakt aus dem Text des Ausdrucks.
   - **Werte statt Code bleibt:** Identifier liefern Werte, nie Ausdruckstext (B51). Ein Wert, der wie eine Zahl aussieht, wird zur Dezimalzahl, nach denselben Regeln wie eine Zahl im Ausdruck.
   - **Grenzen (B52):** Größe und Tiefe des Ausdrucks beim Lesen; Rechenschritte beim Auswerten.
   - **Funktionen aus B53 in Dezimal:** Winkelfunktionen und ihre Umkehrungen rechnet `internal/decimal` selbst, mit Reihenentwicklung und Schutzstellen (Entscheidung des Projektinhabers). Das Ergebnis wird nach Punkt 5 gerundet; Winkel sind im Bogenmaß. Logarithmen, Wurzeln und Potenzen kommen aus apd. Die Konstanten `e` und `pi` haben 34 Stellen.
   - **Ergebnis:** eine Dezimalzahl, ein Wahrheitswert oder Text, wie bisher.
7. **Text:**
   - **Schreiben:** Die kanonische Form hat keinen Exponenten, einen Punkt als Dezimaltrennzeichen, keine Nullen am Ende der Nachkommastellen und kein „−0“. Beispiele: `2.5`, `-0.3`, `100`.
   - **Lesen:** Erlaubt sind Dezimalzahlen wie Zahlen in Ausdrücken (`template.md`, B51), mit Vorzeichen, Nachkommastellen und Exponent. Nullen am Ende und ein Exponent sind beim Lesen erlaubt, die kanonische Form entfernt sie.
8. **Speicherung:** SQLite-Spalten mit Dezimalzahlen haben den Typ `TEXT` und die kanonische Form ([Code-ADR-0008](0008-datenbankzugriff.md)).
   - Gerechnet wird im Go-Code innerhalb der Schreibtransaktion, nicht in SQL. Nach Wert sortieren kann SQL diese Spalten nicht.
   - Bestehende ganze Werte bringt eine Migration in die kanonische Form.
9. **JSON und API:** Dezimalzahlen, die streamcrew schreibt, stehen als JSON-Text in der kanonischen Form, etwa `"2.5"`, nicht als JSON-Zahl. So verlieren Leser, die Zahlen als `float64` lesen, etwa JavaScript, keine Stellen.
   - Der Typ erfüllt dafür `encoding.TextMarshaler` und `encoding.TextUnmarshaler`, die `encoding/json/v2` nutzt ([Code-ADR-0018](0018-json-v2.md)).
   - Mengenangaben in Action-Dokumenten bleiben eine JSON-Zahl oder ein Ausdruck (`actions.md`, B4); eine JSON-Zahl wird exakt aus ihrem Text gelesen.
   - In Protobuf gilt dasselbe wie in JSON, mit einem Feld vom Typ `string`.
10. **Ausgabe nach der Locale** (`counters-and-quotes.md`, B4; `template.md`, B41):
    - `$<name>` zeigt den Wert exakt, mit dem Dezimaltrennzeichen der Locale.
    - `$<name>display` zeigt ganze Werte mit Tausendertrennzeichen, andere zusätzlich mit genau zwei Nachkommastellen. Gerundet wird dabei kaufmännisch: Die Hälfte rundet von 0 weg.
    - Andere Zahlen in Templates erscheinen in der kanonischen Form.
11. **Tests:**
    - Tabellentests für Grenzen und Rundung: exakt bei 0,1 + 0,2, gerundet bei 10 / 3, Fehler ab 10^34.
    - Die Funktionen aus B53 gegen Referenzwerte mit mehr als 34 Stellen.
    - Fuzz-Tests: Lesen und Schreiben der kanonischen Form (die gelesene geschriebene Form ergibt denselben Wert) sowie Parser und Auswerter der Ausdrücke (kein `panic`, Grenzen halten).

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Dezimalzahlen nur für Counter, Ausdrücke weiter mit `float64` | Beträge aus Ausdrücken wären vor der Übernahme schon ungenau; widerspricht der Vorgabe, so früh wie möglich mit apd zu rechnen |
| `expr-lang/expr` behalten, mit Dezimalzahlen per AST-Patch und `expr.Operator` | Der Parser liefert Zahlen im Ausdruck als `float64`, unäres Minus lässt sich nicht überladen, und jeder Operator ginge über Reflection |
| Jede ungenaue Rechnung ist ein Fehler | Dann scheitert schon `10 / 3`, ebenso jede Wurzel und jeder Logarithmus |
| Winkelfunktionen über `float64` | nur etwa 16 gültige Stellen und eine Ausnahme vom Grundsatz; der Projektinhaber hat sich für Dezimal entschieden |
| `github.com/shopspring/decimal` | Kein Release seit April 2024. Globale, änderbare Einstellungen wie `DivisionPrecision` und `MarshalJSONWithoutQuotes`. Division durch 0 bricht mit `panic` ab. |
| `github.com/woodsbury/decimal128` | Technisch passend (Wertetyp, `encoding/json/v2`, keine Abhängigkeiten), aber die Pflege hängt an einer Person |
| `github.com/quagmt/udecimal` | Schneidet überzählige Stellen bei manchen Rechnungen still ab; die Pflege hängt im Wesentlichen an einer Person |
| eigenes Festkomma mit `int64` | Braucht eine feste Zahl an Nachkommastellen; der Projektinhaber hat sich gegen eine feste Grenze entschieden |
| `math/big.Rat` | Brüche statt Dezimalzahlen: 10/3 bleibt ein Bruch, Wurzeln und Logarithmen gehen gar nicht, und der Speicherbedarf wächst mit jedem Schritt |
| `float64` wie im Original | Nicht exakt: 0,1 + 0,2 ergibt 0,30000000000000004; widerspricht B5 |
| `govalues/decimal`, `ericlagergren/decimal` | Nicht mehr gepflegt: kein Commit seit Januar 2025 bzw. kein Release seit 2019 |

## Konsequenzen

**Positiv:**

- Rechnungen mit Dezimalzahlen sind exakt, wo es geht. Gerundet wird nur, was sich in 34 Stellen nicht darstellen lässt, und dann nach einer festen Regel.
- Templates, Ausdrücke, Actions und Counter haben eine gemeinsame Zahl; Werte gehen ohne Verlust durch Datenbank, JSON und API.
- `expr-lang/expr` entfällt. Der eigene Auswerter kennt genau die Sprache der Spezifikation und muss keine weiteren Teile einer fremden Sprache abschalten.
- Die Abhängigkeit apd ist gepflegt, steht unter derselben Lizenz und bleibt hinter `internal/decimal`.

**Negativ und Risiken:**

- Mehr eigener Code: Parser und Auswerter der Ausdrücke sowie Winkelfunktionen. Fuzz-Tests und Referenzwerte sichern sie ab.
- Rechnen mit apd ist langsamer als mit `float64`. Ausdrücke und Counter sind klein; ein Benchmark prüft das Rendern weiter.
- Dezimalspalten lassen sich in SQL nicht nach Wert sortieren. Ranglisten über Counter bräuchten eine eigene Lösung.
- Der Umbau berührt viele Pakete: `internal/expr`, `internal/template`, die Actions mit Mengenangaben, die Bedingung, die Argumente und die Counter.

**Folgearbeiten** (Roadmap 3.5, je ein PR):

- [ ] Paket `internal/decimal` mit Rundung, Grenzen, Text, den Funktionen aus B53 und Tests
- [ ] Eigener Auswerter in `internal/expr` auf Dezimalzahlen; Zahlen in `internal/template`; `expr-lang/expr` aus `go.mod` entfernen
- [ ] Mengenangaben der Actions, Bedingung, Argumente vom Typ `number` und `integer` auf Dezimalzahlen
- [ ] Counter mit Dezimalzahlen: Migration der Spalten nach `TEXT`, `internal/domain/counter`, Counter-Action, Ausgabe nach Punkt 10
- [ ] Spezifikationen anpassen: `template.md` (B50–B53: Zahlen exakt, Rundung, Grenzen), `counters-and-quotes.md` (B5 mit dem Wertebereich, B43), `actions.md` (B4, B40–B42, Randfall B220), `requirements.md` (B33)
