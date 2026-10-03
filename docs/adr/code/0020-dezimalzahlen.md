# Code-ADR-0020: Dezimalzahlen mit `cockroachdb/apd`

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-10-03 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §8; Roadmap Phase 3.5; [`counters-and-quotes.md`](../../spec/counters-and-quotes.md) B4, B5, B8 und A2 in der Fassung vom 2026-10-02 (PR #102); [`actions.md`](../../spec/actions.md) B40–B43; [`template.md`](../../spec/template.md) B41, B50–B52; [Code-ADR-0008](0008-datenbankzugriff.md), [Code-ADR-0012](0012-template-engine.md), [Code-ADR-0017](0017-klare-signale-statt-magischer-werte.md), [Code-ADR-0018](0018-json-v2.md) |

## Kontext

- **Entscheidung vom 2026-10-02** (`counters-and-quotes.md`, B5 und A2, PR #102):
  - Der Wert eines Counters ist eine Dezimalzahl, auch mit Nachkommastellen wie 2,5, und wird exakt gerechnet: 0,1 + 0,2 ergibt 0,3.
  - Die Schrittweite darf ebenfalls Nachkommastellen haben (B8).
  - Typ, Wertebereich und Genauigkeit legt ein Code-ADR fest; das ist dieses.
  - Das Original rechnet mit Gleitkommazahlen.
- **Heute** sind Wert und Schrittweite ganze Zahlen mit 64 Bit (`internal/domain/counter`, Migration 0006). Die Counter-Action rechnet mit ganzen Beträgen ([`actions.md`](../../spec/actions.md), B40–B42).
- **Ausdrücke** rechnen mit `float64` ([Code-ADR-0012](0012-template-engine.md), `internal/expr`). Daran ändert sich nichts. Beträge der Counter-Action, etwa `$x / 3`, kommen deshalb als `float64` an.
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
   - apd meldet jede ungenaue Rechnung als Bedingung, die sich zum Fehler machen lässt. Es hat keinen änderbaren Zustand auf Paketebene und gibt Fehler statt `panic` zurück.
   - `lib/pq` in seiner `go.mod` nutzen nur seine Tests.
2. **Eigener Typ:** Das Paket `internal/decimal` kapselt apd im Typ `decimal.Decimal`.
   - Er wird als Wert benutzt: Methoden ändern ihn nicht, sondern liefern einen neuen Wert und, wo eine Rechnung scheitern kann, einen Fehler. Intern wird kein bestehender Wert verändert, damit Kopien sicher sind.
   - Der Nullwert ist 0.
   - Typen von apd erscheinen in keiner Schnittstelle anderer Pakete; ein Wechsel der Bibliothek bleibt auf `internal/decimal` beschränkt.
3. **Wertebereich ohne feste Zahl an Nachkommastellen** (Entscheidung des Projektinhabers): Ein Wert erfüllt drei Grenzen. Werte außerhalb lehnt das Lesen ab.
   - höchstens 34 gültige Stellen, so viele wie IEEE 754 decimal128;
   - Betrag kleiner als 10^34;
   - höchstens 34 Nachkommastellen.
4. **Exakt oder Fehler:** Jede Rechnung ist exakt.
   - Ein Ergebnis, das die Grenzen aus Punkt 3 nicht exakt einhält, ist ein Fehler. Nichts wird still gerundet ([Code-ADR-0017](0017-klare-signale-statt-magischer-werte.md)).
   - Der Rechen-Kontext von apd hat dafür 34 Stellen, und die Bedingungen „ungenau“, „Überlauf“, „Unterlauf“, „Division durch 0“ und „ungültig“ sind Fehler.
   - Für die Counter-Action heißt das wie beim Überlauf heute: Die Action scheitert, und der Wert bleibt unverändert (`actions.md`, B42).
5. **Werte aus Ausdrücken:**
   - **`float64` zu Dezimalzahl:** Ein `float64` wird mit seiner kürzesten Darstellung übernommen, die ihn eindeutig bezeichnet (`strconv` mit Genauigkeit −1). So wird 0.1 zu 0.1 und 10 / 3 zu 3.3333333333333335. Solche Werte haben höchstens 17 gültige Stellen. NaN und ±Unendlich sind Fehler.
   - **Dezimalzahl zu `float64`:** Ein Counter in einem Ausdruck liefert die nächste `float64` (`template.md`, B51). Werte mit mehr als 15 gültigen Stellen verlieren dabei Stellen; der gespeicherte Wert bleibt exakt.
6. **Text:**
   - **Schreiben:** Die kanonische Form hat keinen Exponenten, einen Punkt als Dezimaltrennzeichen, keine Nullen am Ende der Nachkommastellen und kein „−0“. Beispiele: `2.5`, `-0.3`, `100`.
   - **Lesen:** Erlaubt sind Dezimalzahlen wie Zahlen in Ausdrücken (`template.md`, B51), mit Vorzeichen, Nachkommastellen und Exponent, aber exakt. Nullen am Ende und ein Exponent sind beim Lesen erlaubt, die kanonische Form entfernt sie.
7. **Speicherung:** SQLite-Spalten mit Dezimalzahlen haben den Typ `TEXT` und die kanonische Form ([Code-ADR-0008](0008-datenbankzugriff.md)).
   - Gerechnet wird im Go-Code innerhalb der Schreibtransaktion, nicht in SQL. Nach Wert sortieren kann SQL diese Spalten nicht.
   - Bestehende ganze Werte bringt eine Migration in die kanonische Form.
8. **JSON und API:** Dezimalzahlen stehen als JSON-Text in der kanonischen Form, etwa `"2.5"`, nicht als JSON-Zahl. So verlieren Leser, die Zahlen als `float64` lesen, etwa JavaScript, keine Stellen.
   - Der Typ erfüllt dafür `encoding.TextMarshaler` und `encoding.TextUnmarshaler`, die `encoding/json/v2` nutzt ([Code-ADR-0018](0018-json-v2.md)).
   - In Protobuf gilt dasselbe mit einem Feld vom Typ `string`.
9. **Ausgabe nach der Locale** (`counters-and-quotes.md`, B4; `template.md`, B41):
   - `$<name>` zeigt den Wert exakt, mit dem Dezimaltrennzeichen der Locale.
   - `$<name>display` zeigt ganze Werte mit Tausendertrennzeichen, andere zusätzlich mit genau zwei Nachkommastellen. Gerundet wird dabei kaufmännisch: Die Hälfte rundet von 0 weg.
10. **Geltungsbereich:** Zuerst nutzen Counter den Typ: Wert, Schrittweite und die Beträge der Counter-Action. Ob weitere Bereiche ihn nutzen, etwa Währungen in Phase 8, entscheidet deren Spezifikation. Ausdrücke bleiben bei `float64`.
11. **Tests:**
    - Tabellentests für die Grenzen aus Punkt 3, für exakte Rechnungen wie 0,1 + 0,2 und für Fehler statt Rundung.
    - Lesen und Schreiben der kanonischen Form, auch als Fuzz-Test: Lesen der geschriebenen Form ergibt denselben Wert.
    - Übernahme aus `float64`.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `github.com/shopspring/decimal` | Kein Release seit April 2024. Globale, änderbare Einstellungen wie `DivisionPrecision` und `MarshalJSONWithoutQuotes`. Division durch 0 bricht mit `panic` ab, und Division rundet still. |
| `github.com/woodsbury/decimal128` | Technisch passend (Wertetyp, `encoding/json/v2`, keine Abhängigkeiten), aber die Pflege hängt an einer Person |
| `github.com/quagmt/udecimal` | Schneidet überzählige Stellen bei manchen Rechnungen still ab; die Pflege hängt im Wesentlichen an einer Person |
| eigenes Festkomma mit `int64` | Braucht eine feste Zahl an Nachkommastellen; der Projektinhaber hat sich gegen eine feste Grenze entschieden |
| `math/big.Rat` | Brüche statt Dezimalzahlen: 10/3 bleibt ein Bruch, die Ausgabe muss runden, und der Speicherbedarf wächst mit jedem Schritt |
| `float64` wie im Original | Nicht exakt: 0,1 + 0,2 ergibt 0,30000000000000004; widerspricht B5 |
| `govalues/decimal`, `ericlagergren/decimal` | Nicht mehr gepflegt: kein Commit seit Januar 2025 bzw. kein Release seit 2019 |

## Konsequenzen

**Positiv:**

- Counter rechnen exakt, und eine Rechnung, die nicht exakt geht, fällt als Fehler auf, statt still zu runden.
- Die Abhängigkeit ist gepflegt, steht unter derselben Lizenz und bleibt hinter `internal/decimal`.
- Werte gehen ohne Verlust durch Datenbank, JSON und API.

**Negativ und Risiken:**

- Eine neue Abhängigkeit; ihr Rechen-Kontext ist umständlicher als ganze Zahlen. `internal/decimal` verbirgt das.
- Dezimalspalten lassen sich in SQL nicht nach Wert sortieren. Ranglisten über Counter bräuchten eine eigene Lösung.
- In Ausdrücken haben Counter nur die Genauigkeit von `float64`. Ein Betrag aus einem Ausdruck kann bis zu 17 gültige Stellen mitbringen, etwa bei 10 / 3.
- Rechnen mit apd ist langsamer als mit `int64`. Für Counter spielt das keine Rolle.

**Folgearbeiten:**

- [ ] Paket `internal/decimal` mit Tests (Roadmap 3.5)
- [ ] Counter mit Dezimalzahlen: Migration der Spalten nach `TEXT`, `internal/domain/counter`, Counter-Action, Ausgabe nach Punkt 9 (Roadmap 3.5)
- [ ] Spezifikationen anpassen: `counters-and-quotes.md` (B5 mit dem Wertebereich, B43), `actions.md` (B40–B42, Randfall B220)
