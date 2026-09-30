# Code-ADR-0012: Template-Engine

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §6.10, §8, §11.2; Roadmap Phase 3.1; [Spezifikation `template.md`](../../spec/template.md); [ADR-0001](../0001-neuimplementierung-und-nutzung-des-originals.md), [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0003](0003-fehler-und-logging.md), [Code-ADR-0006](0006-teststrategie.md), [Code-ADR-0009](0009-ids-und-zeit.md) |

## Kontext

- Fast jede Action gibt Text aus: Chatnachrichten, Overlays, Web-Requests, Dateien. Diese Texte enthalten `$`-Identifier, die beim Ausführen mit Werten gefüllt werden. Die Spezifikation [`template.md`](../../spec/template.md) legt das Verhalten fest: Tokens, längster Präfix, Rangfolge der Quellen, unbekannte Identifier bleiben stehen, keine erneute Auswertung, Kodierung je Ausgabeort, Ausdrücke.
- Das Original ersetzt per Textersetzung nacheinander (Plan §3). Eingesetzter Zuschauertext kann dabei von späteren Ersetzungen erfasst werden. streamcrew soll das verhindern (Plan §6.10).
- Es gibt Hunderte Identifier. Die meisten entstehen aus wenigen Subjekten (auslösender Nutzer, Ziel, Streamer, Bot, n-tes Argument, Zufallsnutzer, Platz n einer Rangliste) und einem gemeinsamen Satz an Eigenschaften.
- Manche Werte sind teuer, etwa das Follow-Alter über die Plattform-API; sie dürfen nur bei Bedarf entstehen.
- Die Namen stehen unter dem Interop-Vorbehalt (Spezifikation, Zweck und Umfang): Sie sollen sich nach der rechtlichen Einschätzung an einer Stelle ändern lassen.
- Plan §8 nennt `expr-lang/expr` als Kandidaten für Ausdrücke. Geprüft am 2026-09-29: Version 1.17.8, MIT-Lizenz, keine eigenen Abhängigkeiten. Die Bibliothek begrenzt die Knotenzahl eines Ausdrucks, hat ein Speicherbudget, lässt eingebaute Funktionen abschalten und kennt die Potenz mit `^` und `**`.

## Entscheidung

1. **Paket `internal/template`** mit zwei Schritten:
   - `Parse` zerlegt ein Template einmal in Text- und Token-Stücke. Ein Token ist `$` mit der längsten Folge aus Buchstaben, Ziffern und `:`, für die Suche kleingeschrieben.
   - `Render` setzt die Werte eines Kontexts ein und schreibt in einen `strings.Builder`. Die eingesetzten Werte werden nie erneut gelesen; damit ist die erneute Auswertung (Spezifikation B5) ausgeschlossen, statt verboten.
   - Geparste Templates sind unveränderlich und dürfen gleichzeitig gerendert werden. Actions parsen beim Laden des Commands, nicht bei jeder Ausführung.
2. **Auflösung beim Rendern:** Für jedes Token wird zur Laufzeit des Renderns der längste Präfix gesucht, weil sich dynamische Namen (Counter, lokale und globale Werte) zwischen zwei Durchläufen ändern.
   - Eingebaute Namen stehen in einem Präfixbaum, der beim Aufbau der Registry entsteht.
   - Muster (`arg<n>text`, `randomnumber<min>:<max>`, `unicode<n>` …) sind kleine Parser-Funktionen, die ein Token prüfen und die Länge ihres Treffers und ihre Zahlen liefern; keine regulären Ausdrücke.
   - Dynamische Quellen fragt die Engine über ein kleines Interface nach bekannten Namen.
   - Unter allen Quellen gewinnt der längste Treffer; bei gleicher Länge die Rangfolge der Spezifikation (B10, B11).
3. **Registry und Resolver:**
   - Eine `Registry` enthält die eingebauten Identifier und Muster. Sie wird in der Composition Root aus Listenfunktionen der Familien gebaut, z. B. `template.UserFamily()`, nicht per `init()` ([Code-ADR-0002](0002-dependency-injection.md)).
   - Ein Resolver ist eine Funktion `func(ctx context.Context, s *Scope) (Value, bool, error)`. `false` heißt „kein Wert“; der Identifier bleibt dann stehen (Spezifikation B4).
   - **Präfix-Familien** werden als Subjekt mal Eigenschaft registriert: Ein Subjekt liefert einen Nutzer (auslösender Nutzer, Ziel, Streamer, Bot, `arg<n>user`, Zufallsnutzer, Platz einer Rangliste), eine Eigenschaft liest einen Wert von ihm. So entstehen die Kombinationen ohne Aufzählung, und neue Subjekte oder Eigenschaften kommen mit einer Zeile dazu.
   - Die Namen jeder Familie stehen gesammelt in einer Datei je Familie mit dem Vermerk **[Interop]** (Interop-Vorbehalt).
4. **Scope je Rendervorgang:** Der `Scope` hält den Kontext des Durchlaufs (Nutzer, Ziel, Argumente, Ereigniswerte, lokale Werte), die Ports zu den Daten (Nutzer, Stream-Zustand, Counter, Zeitzone und Locale des Profils) als kleine Interfaces der Konsumentenseite und einen Cache.
   - Jeder Identifier wird je Rendervorgang höchstens einmal aufgelöst (B21); Ausnahmen, etwa `randomnumber`, markiert ihr Eintrag als nicht zwischenspeicherbar.
   - Ein Zufallsnutzer wird je Subjekt einmal gewählt und im Scope gehalten (B22).

   *Präzisiert am 2026-09-30: Ports zu Daten, die für das ganze Profil gelten (Stream-Zustand, Nutzer), bekommen die Familien als Parameter beim Aufbau der Registry, etwa `template.StreamFamily(states)`. Der Scope hält die Daten des Durchlaufs und die Zeitzone; `Scope.Memo` hält, was mehrere Identifier eines Rendervorgangs teilen. Counter sind eine dynamische Quelle (`template.CounterSource`).*
   - Gerendert wird der Reihe nach in einer Goroutine; ein Resolver bekommt den `context.Context` des Durchlaufs und muss Abbruch und Zeitlimit beachten (B24). Gleichzeitiges Vorladen teurer Werte ist eine spätere Optimierung, wenn Messungen sie begründen.
5. **Fehler:** Liefert ein Resolver einen Fehler, bleibt der Identifier stehen, und die Engine loggt eine Warnung mit Identifier und Fehler, ohne Werte aus dem Kontext ([Code-ADR-0003](0003-fehler-und-logging.md)). Nur ein abgebrochener Kontext beendet das Rendern mit Fehler.
6. **Kodierung:** `Render` bekommt eine Kodierung (`Text`, `URL`, `HTML`, `JSON`) und wendet sie nur auf eingesetzte Werte an (B30, B31):
   - `URL`: `url.QueryEscape`, danach `+` durch `%20` ersetzt; so bleibt nur `A–Z a–z 0–9 - . _ ~` unmaskiert, und der Wert passt in Pfad wie Query.
   - `HTML`: `html.EscapeString`.
   - `JSON`: Inhalt eines JSON-Strings ohne Anführungszeichen, mit `encoding/json` ohne HTML-Maskierung.

   *Präzisiert am 2026-09-30: `JSON` nutzt `jsontext.AppendQuote` aus `encoding/json/jsontext` (Go 1.27, ohne `GOEXPERIMENT` verfügbar; siehe die Berichtigung in [Code-ADR-0010](0010-polymorphe-serialisierung.md)). Es maskiert kein HTML und ersetzt ungültiges UTF-8 durch U+FFFD, statt abzubrechen.*
7. **Werte:** `Value` trägt den Text und optional eine Zahl. Datum, Uhrzeit und Zeitspannen formatiert die Engine mit Zeitzone und Locale des Profils (B40–B42); bis zur Locale-Einstellung mit den Formaten des Originals. Die Formate liegen in `internal/template`, nicht verstreut in den Resolvern.
8. **Ausdrücke mit `expr-lang/expr`** im Paket `internal/expr`:
   - Beim Parsen eines Ausdrucks wird jedes Token durch eine Variable ersetzt (`v0`, `v1` …); `expr` kompiliert den so entstandenen Text einmal. Beim Auswerten gehen die aufgelösten Werte als Variablen hinein: Zahlen als `float64`, sonst als Text (B51). Werte werden nie Teil des Ausdruckstexts.
   - `expr.DisableAllBuiltins()` mit einer Freigabeliste der Funktionen, die Rechnen und Runden brauchen; `expr.MaxNodes` begrenzt die Größe, das Speicherbudget der VM die Auswertung (B52).
   - Das Ergebnis ist eine Zahl, ein Wahrheitswert oder Text; Fehler gehen an die Action.

   *Präzisiert am 2026-09-30: Ein Prüfer auf dem Syntaxbaum lässt nur zu, was die Spezifikation in B50 nennt, und lehnt etwa Arrays, Zugriffe auf Felder, Bereiche und weitere Funktionen ab. Alle Zahlen sind `float64`, auch die Zahlen im Ausdruck selbst; `%` ersetzt ein Patch durch `math.Mod`, weil `expr` den Rest nur für ganze Zahlen kennt. Die Variablen sind beim Kompilieren nicht typisiert (`AllowUndefinedVariables`), ihre Namen prüft der Prüfer. Text in Anführungszeichen mit Identifiern wird als Ganzes zu einer Text-Variablen. Alle Identifier eines Ausdrucks löst ein Rendervorgang auf (`template.Engine.RenderEach`, B21). Ergebnisse, die keine endliche Zahl sind, gelten als Fehler.*
9. **Tests** ([Code-ADR-0006](0006-teststrategie.md)): Golden Files mit Template, Kontext und Ausgabe je Familie in `internal/template/testdata`; tabellengetriebene Tests für Rangfolge, Kodierung und Randfälle; `testing/synctest` für Datum, Zeit und Uptime; Fuzz-Tests für `Parse` und `Render` (keine Panics, ohne bekannte Identifier gleich der Eingabe) und für Ausdrücke; ein Benchmark für das Rendern.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Textersetzung nacheinander wie im Original | ersetzt eingesetzte Werte erneut (Template-Injection), hängt von der Reihenfolge ab und durchsucht den Text je Identifier einmal |
| reguläre Ausdrücke je Identifier oder für alle Tokens | dieselben Probleme wie oben bzw. schwer lesbare Muster für Hunderte Namen; die Regel des längsten Präfixes ist mit einem Präfixbaum einfacher und schneller |
| `text/template` der Standardbibliothek | andere Syntax (`{{…}}`), nicht kompatibel zu bestehenden Commands und zum Import |
| alle Kombinationen aus Subjekt und Eigenschaft einzeln registrieren | Hunderte fast gleiche Einträge; neue Subjekte oder Eigenschaften müssten überall nachgetragen werden |
| Werte beim Parsen statt beim Rendern auflösen | dynamische Namen und Werte ändern sich zwischen Durchläufen; ein geparstes Template würde veralten |
| Identifier als Text in Ausdrücke einsetzen wie im Original | ein Argument wie `1)+(2` würde Teil des Ausdrucks |
| `google/cel-go` für Ausdrücke | stark und sicher, aber mit Protobuf- und ANTLR-Abhängigkeiten deutlich schwerer; die Syntax passt weniger zu einfachen Rechnungen |
| eine JavaScript-Engine (`goja`) | großer Umfang und größere Angriffsfläche für eine Aufgabe, die Rechnen und Vergleiche braucht |
| `Knetic/govaluate` | seit Jahren nicht gepflegt |
| eigener Ausdrucksparser | Aufwand für Parser, Typen, Grenzen und Tests, den `expr` bereits erprobt mitbringt |

## Konsequenzen

**Positiv:**

- Zuschauertext kann keine Identifier auslösen; die Kodierung je Ausgabeort schützt Overlays und Web-Requests.
- Teure Werte entstehen nur bei Bedarf und je Rendervorgang einmal.
- Präfix-Familien halten die Registry klein; die Namen unter Interop-Vorbehalt liegen je Familie an einer Stelle.
- Ausdrücke sind begrenzt und bekommen Werte, keinen Code.

**Negativ und Risiken:**

- Neue Abhängigkeit `github.com/expr-lang/expr` (MIT, ohne eigene Abhängigkeiten). Ihre Syntax weicht von Jace ab; importierte Rechnungen brauchen eine Abbildung (Roadmap 10.2).
- Die Regel des längsten Präfixes über dynamische Namen kann überraschen, wenn ein Counter wie der Anfang eines eingebauten Identifiers heißt; das Anlegen solcher Namen wird abgelehnt (Spezifikation B12).
- Randfälle der Kompatibilität (Plan §13, R9) zeigen sich erst mit echten Commands; Golden Files und Fuzzing fangen sie ein.
- Die Engine rendert nacheinander; viele teure Werte in einem Template addieren ihre Laufzeiten, bis ein Vorladen nötig wird.

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen, den Index in [`README.md`](README.md) und das ADR-Backlog in Plan §12.2 anpassen, erledigt 2026-09-29
- [x] Kern von `internal/template` umsetzen (Roadmap 3.1), erledigt 2026-09-30
- [x] Familien für Argumente, Nachricht, Datum und Zeit, Zufallszahlen, Stream, Counter, Ereigniswerte, Command-Name und Plattform, erledigt 2026-09-30
- [x] Nutzer-Familien mit Zufallsnutzer (Roadmap 3.1), erledigt 2026-09-30
- [x] `internal/expr` umsetzen (Roadmap 3.1); `expr-lang/expr` 1.17.8 in `go.mod` aufgenommen, Lizenzprüfung grün, erledigt 2026-09-30
- [ ] Die Special-Identifier-Action (Roadmap 3.3) setzt lokale und globale Werte über die Quellen aus Punkt 2
- [ ] Namen und Eigenschaften nach der rechtlichen Einschätzung (Gate O, O.1) bestätigen oder austauschen
