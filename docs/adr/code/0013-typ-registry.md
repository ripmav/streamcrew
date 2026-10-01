# Code-ADR-0013: Typ-Registry für Actions

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-10-01 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §6.8, §6.9, §6.15, §8; Roadmap Phase 3.3 bis 3.5; [ADR-0013](../0013-sicherheitsmodell.md); [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0006](0006-teststrategie.md), [Code-ADR-0010](0010-polymorphe-serialisierung.md), [Code-ADR-0012](0012-template-engine.md), [Code-ADR-0017](0017-klare-signale-statt-magischer-werte.md), [Code-ADR-0018](0018-json-v2.md); Spezifikationen [`actions.md`](../../spec/actions.md), [`command-engine.md`](../../spec/command-engine.md) |

## Kontext

- Plan §6.9 sieht für jeden Action-Typ einen Descriptor vor: stabile Typ-ID, Schemaversion, Kategorie, i18n-Schlüssel, JSON-Schema der Konfiguration, UI-Hinweise und benötigte Capabilities. Aus dem Typkatalog der API rendern Frontends generische Editoren; Commands als Code (Roadmap 3.5) exportieren die Schemas.
- Code-ADR-0010 legt die Registry je Familie in `internal/polydoc` fest; die Typ-Registry baut darauf auf. Bisher registriert der Codec der Commands keinen Action-Typ, jede Action ist ein `command.UnknownAction`.
- Die Engine aus Phase 3.2 bietet `engine.Performer`, ein statisches `engine.TimeLimiter`, `engine.Container` (nur für die Sperren), `engine.WithVisualAudio`, `engine.ErrStop` und `engine.Run.Call`. Das Zeitlimit umfasst die ganze Action; Kind-Actions kennt die Engine nur für die Sperren.
- Die Spezifikation `actions.md` (geprüft 2026-10-01) verlangt mehr, als die Engine kann:
  - einen Schalter „aktiv“ je Action; eine inaktive wird samt Kind-Actions übersprungen und braucht keine Sperre (B1)
  - Zeitlimits, die erst beim Start feststehen, etwa beim Warten die Dauer aus einem Template plus 5 s; kein Limit für eine Action mit Kind-Actions als Ganzes, dafür eines je Kind-Action (B8)
  - die Fehlerpolitik auch in Kind-Actions und den Pfad einer Kind-Action im Verlauf, etwa `3.2` (B9)
  - Capabilities: Scheitern beim Ausführen, Warnung beim Speichern (B7)
  - Mengenangaben als Ausdrücke mit festem Bereich (B4), Namen von Ergebniswerten (B5), Verweise auf Commands, Gruppen und Counter, die das Speichern prüft (B31, B41)
  - endgültige Typ-IDs statt der Arbeitsnamen
- Kind-Actions sind selbst Dokumente der Familie. `internal/polydoc` dekodiert bisher nur flache Dokumente.
- Plan §8 nennt `github.com/google/jsonschema-go` als Kandidaten für JSON-Schema. Am 2026-10-01 liefen drei aktiv gepflegte Go-Bibliotheken gegen die offizielle JSON-Schema-Test-Suite (Stand 2026-09-21), mit Formatprüfung für die Format-Tests:

  | | `google/jsonschema-go` v0.4.3 | `santhosh-tekuri/jsonschema/v6` v6.0.3 | `kaptinlin/jsonschema` v0.9.10 |
  |---|---|---|---|
  | 2020-12, Pflichtteil | 1298/1301 | 1301/1301 | 1284/1301 |
  | draft-07, Pflichtteil | 929/929 | 929/929 | 925/929 |
  | 2020-12, optionale Tests | 120/162 | 147/162 | 117/162 |
  | 2020-12, Formatprüfung | 408/874, `format` wird nicht geprüft | 744/874 | 809/874 |
  | Entwürfe | 2020-12, 07 | 2020-12, 2019-09, 07, 06, 04 | 2020-12, 2019-09, 07, 06, 04 |
  | letztes Release, Commits seit April 2026 | 2026-04-17, 2 | 2026-06-28, 8 | 2026-09-13, 80 |
  | Abhängigkeiten | keine | `golang.org/x/text` | sieben direkte, darunter `goccy/go-yaml` und `go-json-experiment/json` |
  | Lizenz | MIT | Apache-2.0 | MIT |

  - `google/jsonschema-go` kennt keine eigenen Vokabulare, prüft `format` nicht und wird kaum weiterentwickelt.
  - `kaptinlin/jsonschema` scheitert im Pflichtteil: Ein leeres `enum` lässt jeden Wert zu, `content*` wird ohne Einstellung geprüft, und ein Fall von `$dynamicRef` schlägt fehl.
  - `santhosh-tekuri/jsonschema` besteht den ganzen Pflichtteil. Die optionalen Fehler kommen fast alle von Go-RE2 statt ECMA-262; die Engine lässt sich austauschen. Bei der Formatprüfung fehlen Randfälle bei `idn-hostname`, `hostname`, `uri-template`, `uri` und `email`. Eigene Vokabulare, Formatprüfung und die Ausgabeformate der Spezifikation sind vorhanden. Ein Datenmodell zum Bauen von Schemas bietet sie nicht.

## Entscheidung

1. **Typ-IDs:** Eine Typ-ID besteht aus Kleinbuchstaben und Ziffern, mehrere Wörter verbunden mit `_`, höchstens 40 Zeichen und ohne Punkt. Sie wird nie umbenannt und nie wiederverwendet. Eine Action mit mehreren Arten bleibt ein Typ; die Art steht im Feld `kind`, ihre Werte folgen derselben Schreibweise. Die P0-Typen:

   | Arbeitsname in `actions.md` | Typ-ID | Kategorie |
   |---|---|---|
   | `wait`, `random`, `group`, `repeat`, `conditional` | `wait`, `random`, `group`, `repeat`, `conditional` | `flow` |
   | `command` | `command` | `commands` |
   | `counter`, `specialidentifier` | `counter`, `special_identifier` | `values` |
   | `chat`, `platformmessage` | `chat`, `platform_message` | `chat` |
   | `webrequest` | `web_request` | `network` |
   | `moderation` | `moderation` | `moderation` |
   | `userlookup` | `user_lookup` | `users` |
   | `file`, `externalprogram` | `file`, `external_program` | `host` |

   Das Beispiel `chat.send` in Plan §6.9 und Code-ADR-0010 war ein Platzhalter. Neue Kategorien kommen mit ihren Typen, etwa `media` und `overlay` in Phase 7.

2. **Pakete:**
   - `internal/capability`: das Enum `Capability` mit den Namen aus ADR-0013 und die Menge der vorhandenen Capabilities, die die Composition Root aus Betriebsmodus und Startkonfiguration bildet.
   - `internal/action`: Descriptor, Kategorie, Registry und die gemeinsamen Feldtypen (Punkt 4); in `internal/action/schema` der Schema-Typ mit seinen Bausteinen (Punkt 6); in `internal/action/actiontest` der gemeinsame Konformitätstest (Punkt 9).
   - Ein Paket je Kategorie: `internal/action/flow`, `…/commands`, `…/values`, `…/chat`, `…/network`, `…/moderation`, `…/users` und `…/host`. Jedes liefert `Descriptors(…) []action.Descriptor` mit den Ports, die seine Typen brauchen. Die Ports sind kleine Schnittstellen im Paket selbst. Registriert wird in der Composition Root mit `action.NewRegistry`, nicht per `init()` (Code-ADR-0002).

3. **Descriptor und Registry:**

   ```go
   // Descriptor describes an action type for the registry, the type catalog
   // of the API and generic editors (plan §6.9).
   type Descriptor struct {
   	Type         string                  // stable type ID, e.g. "web_request"
   	Version      int                     // current schema version, from 1
   	Category     Category                // e.g. CategoryNetwork
   	Capabilities []capability.Capability // needed to run; empty for none
   	VisualAudio  bool                    // shares the lock "visual_audio" (command-engine.md B23)
   	Schema       schema.Schema           // configuration of the current version, with UI hints
   	New          func() command.Action   // a new action with the defaults for creating one
   	Decode       func(data []byte, opts json.Options) (command.Action, error)
   	Migrations   []polydoc.Migration     // Migrations[i] upgrades version i+1 to i+2
   }
   ```

   - Die Registry prüft jeden Descriptor beim Aufbau: Typ-ID nach Punkt 1 und eindeutig, Kategorie und Capabilities bekannt, ein Schema vorhanden, und das Dokument aus `New` besteht `Validate`. Ein Fehler hält den Start an. Ob das Schema selbst gültig ist, prüft der Konformitätstest (Punkt 9).

     *Umgesetzt am 2026-10-01 mit einer Auflösung zu Punkt 4: Felder ohne Voreinstellung, etwa die Dauer beim Warten, fehlen im Dokument aus `New`. Es kann `Validate` deshalb nicht bestehen. Die Registry prüft stattdessen, dass `New` eine Action des Typs liefert, die sich kodieren lässt und deren Felder im Schema stehen; aus ihr setzt sie die `default`-Werte des Schemas, sodass beide nicht auseinanderlaufen. Schema und `Validate` prüft der Konformitätstest an Beispieldokumenten. `Descriptor.Schema` ist ein Zeiger (`*schema.Schema`); `Descriptor.WithNew` setzt `New` und `Decode` aus einer Konstruktorfunktion.*
   - Sie liefert die Einträge für `command.NewCodec`, die Descriptors in fester Reihenfolge für den Typkatalog (API, Phase 6) und `schema export` (Roadmap 3.5), und sie setzt den Port der Engine um (Punkt 8).
   - Die i18n-Schlüssel folgen aus der Typ-ID und stehen deshalb nicht einzeln im Descriptor: `action.<typ>.name`, `action.<typ>.description`, `action.<typ>.field.<feld>`, `action.<typ>.kind.<art>` und `action.category.<kategorie>`. Der Typkatalog liefert sie ausgeschrieben mit. Die Texte kommen mit ADR-0022.
   - Anforderungen bekommen Descriptors derselben Form (Schema, UI-Hinweise, i18n), sobald der Typkatalog sie braucht (Roadmap 3.5). Capabilities und der Anschluss an die Engine betreffen nur Actions.

4. **Konfiguration einer Action:**
   - Eine Action ist ein Struct aus ihrer Konfiguration und einem unexportierten Verweis auf ihre Ports. `Decode` ist eine Closure der Composition Root, die die Ports einsetzt; JSON sieht nur die Konfiguration.
   - Jede Action hat das Feld `enabled`, den Schalter „aktiv“ (B1). Es kommt über das eingebettete `action.Common` mit dem Tag `json:",inline"`. Es ist kein Kopfschlüssel von `polydoc`, weil andere Familien wie Anforderungen und Settings-Sektionen keinen solchen Schalter haben.
   - Gemeinsame Feldtypen setzen die Regeln aus `actions.md` an einer Stelle um:
     - `action.Template`: Text mit `$`-Identifiern, gerendert erst beim Ausführen (B3). Die Kodierung wählt die Action je Ausgabeort (Code-ADR-0012).
     - `action.Amount`: eine Mengenangabe, in JSON eine Zahl (fester Wert) oder ein Text (Ausdruck nach `template.md`, B50–B52). Zu jedem Feld gehört ein `action.Range` aus Minimum, Maximum und der Angabe, ob nur ganze Zahlen gelten. Derselbe Wert geht ins Schema und gilt beim Ausführen (B4); feste Werte prüft schon das Speichern.
     - `action.ResultName`: der Name eines Ergebniswerts nach B5.
     - Verweise auf Commands und Gruppen als `id.ID`, auf Counter als Name (B31, B41).
     - Kind-Actions als `[]command.Action` (Punkt 5).
   - Arten sind ein Enum mit `Valid` (Code-ADR-0017, Punkt 5). Felder, die nur für einige Arten gelten, sind bei den übrigen nicht erlaubt; das prüfen das Schema (`oneOf` je Art) und `Validate`.
   - **Voreinstellungen und Pflichtfelder:** `New` setzt die Werte „beim Anlegen“ aus `actions.md`, etwa „warten“ bei der Command-Action und 30 s Zeitlimit beim externen Programm, und `enabled` auf an. Gespeicherte Dokumente enthalten immer alle Felder. Fehlen in handgeschriebenen Dokumenten (Commands als Code, Import) Felder, beginnt das Dekodieren mit den Werten aus `New`. Felder ohne Voreinstellung, etwa die Dauer beim Warten, stehen im Schema unter `required`; fehlen sie, lehnt das Dekodieren das Dokument ab. Das ist die eine Stelle, die fehlende Eingaben auflöst (Code-ADR-0017, Punkt 6).

5. **Kind-Actions im Dokument:**
   - `internal/polydoc` dekodiert und kodiert Felder vom Typ der Familie (`command.Action`, auch in Listen) über dieselbe Registry. Dafür reicht es typspezifische Funktionen von `encoding/json/v2` an `Decode` weiter: `json.WithUnmarshalers` mit `json.UnmarshalFromFunc`, beim Schreiben `json.WithMarshalers`. Unbekannte Kind-Actions bleiben wie auf oberster Ebene als `command.UnknownAction` erhalten.
   - Actions sind höchstens 16 Ebenen tief verschachtelt (`action.MaxDepth`). Tiefere Dokumente lehnen Dekodieren und Speichern ab. Die Grenze schützt Dekodierer, Engine und Sperren vor zu tiefer Rekursion durch fremde Dokumente.
   - `Children()` liefert alle Kind-Actions in fester Reihenfolge, bei der Bedingung erst die für „wahr“, dann die für „falsch“. Der Index darin ist die Position der Kind-Action im Pfad (B9).

6. **JSON-Schema:**
   - Die Schemas folgen dem Entwurf 2020-12. Sie entstehen ausdrücklich im Go-Code jedes Typs, aus Bausteinen in `internal/action/schema`, etwa für Template, Mengenangabe mit Bereich und Art. Sie werden nicht per Reflection aus den Structs abgeleitet. Die Bausteine nutzen dieselben Werte wie der Code, etwa `Range` und die Konstanten der Enums, damit Schema und Prüfung nicht auseinanderlaufen.
   - **Eigener Schema-Typ:** `schema.Schema` bildet nur die Schlüsselwörter ab, die die Bausteine brauchen, etwa `type`, `properties`, `required`, `additionalProperties`, `items`, `enum`, `const`, `minimum`, `maximum`, `oneOf`, `default` und `x-ui`. Kodiert wird mit `encoding/json/v2` (Code-ADR-0018). Weitere Schlüsselwörter kommen hinzu, wenn ein Baustein sie braucht.
   - **Prüfbibliothek:** `github.com/santhosh-tekuri/jsonschema/v6`, nur in Tests (Punkt 9). Sie kommt so nicht ins ausgelieferte Binary.
   - UI-Hinweise stehen am Feld als eigenes Schlüsselwort `x-ui`. Es ist ein geschlossenes Enum, zum Start mit `text`, `multiline`, `template`, `amount`, `expression`, `user`, `platform`, `command`, `group`, `counter`, `file_root`, `result_name` und `actions`. Weitere Werte kommen mit den Typen, die sie brauchen, etwa `color` für Overlays. Voreinstellungen stehen als `default`.

     *Umgesetzt am 2026-10-01 mit zwei weiteren Werten, die die Bausteine für Wahrheitswerte und Auswahllisten brauchen: `switch` und `choice`.*
   - **Schemas lesen auch Frontends in anderen Sprachen.** Deshalb gilt:
     - `format` ist nur ein Hinweis für Editoren. Was geprüft werden muss, steht in Enums, Bereichen und `pattern` und im Go-Code, weil Validatoren `format` verschieden oder gar nicht prüfen.
     - `pattern` kommt nur aus Konstanten in `internal/action/schema` und nutzt nur, was Go-RE2 und ECMA-262 gleich verstehen: Zeichenklassen, Quantoren, Gruppen ohne Namen und Anker, aber keine Rückverweise, kein Lookaround und keine Unicode-Klassen wie `\p{…}`.
   - Der Core prüft Dokumente mit Go-Code (Punkt 7), nicht gegen das Schema. Das Schema beschreibt sie für Editoren, die API und Commands als Code; die Tests halten beides gleich (Punkt 9).

7. **Prüfen beim Speichern:**
   - `Validate() error` jeder Action prüft ihre Konfiguration für sich: Arten, Pflichtfelder je Art, feste Mengenangaben im Bereich, Namen nach B5, Ausschlüsse wie „über das System öffnen“ zusammen mit „warten“ (B116), reguläre Ausdrücke ohne Identifier. Kind-Actions prüft `command.Command.Validate` mit.
   - Was nur mit gespeicherten Daten geht, prüft der Command-Service. Actions nennen über die Schnittstelle `action.Referrer` die Commands, Gruppen, Counter und freigegebenen Wurzeln, auf die sie verweisen, und über `action.ResultSetter` die Namen ihrer Ergebniswerte. Unbekannte Commands und Gruppen lehnt das Speichern ab (B31), fehlende Counter legt es an (B41), und Namen, die eingebaute Identifier verdecken, lehnt es ab (B5).
   - Fehlt im Betriebsmodus eine Capability eines Typs oder kennt die Startkonfiguration eine Wurzel nicht, speichert der Service trotzdem und gibt eine Warnung zurück (B7). Beides hängt vom Rechner ab, und Importe sollen erhalten bleiben. `command.Service.Save` gibt dafür ein Ergebnis mit Command und Warnungen zurück.

8. **Anschluss an die Engine:** Die Engine kennt weiter keine einzelnen Typen. Sie ändert sich so (Ergänzung zu `command-engine.md`, B22, B23, B60, B72):
   1. `engine.Performer` verlangt zusätzlich `Enabled() bool`. Eine inaktive Action läuft nicht, samt ihrer Kind-Actions, und zählt für keine Sperre (B1). Actions ohne `Performer`, also unbekannte Typen, behandelt die Engine wie bisher: übersprungen mit Warnung im Log, bei `per_action_type` mit eigener Sperre.
   2. **Zeitlimit als anhaltbarer Timer:** Das Limit einer Action gilt für ihre eigene Zeit. Während sie eine Kind-Action ausführt oder auf einen aufgerufenen Command wartet (`engine.Run.Call` mit `Wait`), steht ihr Timer; die Kind-Actions und die Actions des aufgerufenen Commands haben ihre eigenen Limits. Läuft der Timer ab, bricht er den Kontext der Action mit `engine.ErrTimeLimit` als Ursache ab; eine Deadline hat der Kontext deshalb nicht. Ohne andere Angabe gelten 60 s (`engine.DefaultTimeLimit`).
   3. `Run.LimitTo(d)` setzt das Limit der laufenden Action auf `d`, gemessen ab dem Aufruf. Warten und externes Programm rufen es nach dem Rendern auf, mit ihrer Dauer bzw. ihrem Zeitlimit plus 5 s (B8). So wird jedes Template nur einmal gerendert (B3). Das statische `engine.TimeLimiter` entfällt, weil kein Typ es braucht.
   4. `Run.PerformChild(ctx, i)` führt `Children()[i]` der laufenden Action aus, mit allem, was die Engine auf oberster Ebene tut: Schalter „aktiv“, Capability-Prüfung, eigenes Zeitlimit, Fehlerpolitik und Eintrag im Verlauf mit Pfad. Das Ergebnis ist ein benannter Ausgang (Code-ADR-0017, Punkt 3):
      - `engine.ChildNext`: Die Kind-Action ist gelaufen, wurde übersprungen oder ist bei der Fehlerpolitik `continue` gescheitert; der Container macht weiter.
      - `engine.ChildEnd`: Die Instanz endet, durch `exit`, einen Fehler bei `abort` oder einen Abbruch; der Container kehrt sofort mit `nil` zurück.

      Einen Fehler gibt es nur für einen ungültigen Index. Den Ausgang der Instanz bestimmt die Engine aus ihrem eigenen Zustand, nicht aus der Rückgabe des Containers.
   5. **Verlauf:** `engine.ActionError` bekommt statt der Position (`position`, eine Zahl) den Pfad (`path`), eine Liste ab 1, etwa `[3, 2]` (B9). Die Nutzlast der Ereignisse `command.instance.*` ändert sich damit; Verbraucher außerhalb des Cores gibt es noch nicht.
   6. Der Port `engine.ActionTypes` ersetzt `engine.WithVisualAudio`; `*action.Registry` setzt ihn um. Er sagt, welche Typen Bild oder Ton sind (B23) und welche Capabilities einem Typ im Betriebsmodus fehlen. Vor jeder Action, auch einer Kind-Action, prüft die Engine die Capabilities. Fehlt eine, scheitert die Action, ohne zu laufen, mit `engine.ErrCapability` und dem Namen der Capability (B7, ADR-0013 Punkt 1). Ohne den Port gilt keine Capability als vorhanden und kein Typ als Bild oder Ton.

      *Umgesetzt am 2026-10-01 als Pflichtparameter von `engine.New` statt als Option: Ohne die Registry weiß die Engine nicht, welche Capabilities ein Typ braucht, und kann deshalb keinen Standard anbieten, der „keine vorhanden“ bedeutet.*
   7. `Run.Path()` nennt den Pfad der laufenden Action. Typen mit Zustand über Ausführungen hinweg, etwa der Zufall mit Gedächtnis (B13), bilden ihren Schlüssel aus Command-ID, Änderungszeitpunkt des Commands und Pfad.
   8. Unverändert bleiben `engine.ErrStop` für `exit`, `engine.Container.Children()` für die Sperren und `engine.Run.Call` für die Command-Action.

9. **Tests:**
   - Der Konformitätstest aus `internal/action/actiontest` läuft für jeden Descriptor:
     - Das Schema besteht mit `santhosh-tekuri/jsonschema` die Prüfung gegen das Meta-Schema von 2020-12. `x-ui` ist dort als eigenes Vokabular registriert, sodass unbekannte Werte auffallen.
     - Das Dokument aus `New` lässt sich kodieren und dekodieren und besteht Schema und `Validate`. *Umgesetzt mit Beispieldokumenten statt `New` (siehe Vermerk zu Punkt 3); zusätzlich muss jede Eigenschaft des Schemas in einem gültigen Beispiel oder in `New` vorkommen.*
     - Die Felder des kodierten Dokuments und die Eigenschaften des Schemas stimmen überein.
     - Golden Files je Version in `testdata/` werden auf die aktuelle Version migriert (Code-ADR-0010, Punkt 8).
     - Beispieldokumente nimmt das Schema genau dann an, wenn `Validate` sie annimmt.
   - Das Verhalten jedes Typs wird gegen Fakes seiner Ports getestet, Zeit mit `testing/synctest` (Code-ADR-0006). Die Testnamen nennen die IDs aus `actions.md`.
   - Der Fuzz-Test von `internal/polydoc` deckt verschachtelte Dokumente und die Grenze der Tiefe ab.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Typ-IDs mit Punkt je Vorgang, etwa `chat.send`, `moderation.ban` | Aus 15 Typen würden über 50, und die Einstellungen einer Art wären über Typen verstreut. Anforderungen und Settings-Sektionen haben schon einfache IDs wie `cooldown`. |
| `github.com/google/jsonschema-go` (Kandidat aus Plan §8) für Datenmodell und Prüfung | kaum weiterentwickelt, ohne eigene Vokabulare und ohne Formatprüfung (Kontext). Ihr Datenmodell wäre die einzige Stärke; den Teil, den wir brauchen, deckt der eigene Typ ab. |
| `github.com/kaptinlin/jsonschema` | wird aktiv entwickelt, scheitert aber im Pflichtteil der Test-Suite, etwa beim leeren `enum` (Kontext), und bringt sieben direkte Abhängigkeiten mit |
| Schema aus den Structs ableiten (`jsonschema.For` von google, `invopop/jsonschema`) | Die Ableitung liest die Tags nach v1 und kennt `inline` nicht (Code-ADR-0018). Bereiche, Voreinstellungen, Arten und UI-Hinweise bräuchten eine eigene Sprache in Struct-Tags. Dazu käme Reflection im Produktionscode. |
| Schemas als JSON-Dateien neben dem Code | laufen ohne gemeinsame Konstanten mit dem Go-Code auseinander; Bereiche stünden doppelt |
| Go-Structs aus den Schemas generieren | ein weiterer Build-Schritt; generierter Code lässt sich schlecht um Methoden wie `Perform` und `Validate` ergänzen |
| Dokumente im Core gegen das Schema prüfen | Die Dokumente würden doppelt dekodiert, und Fehler kämen in zwei Formaten. Regeln wie gültige reguläre Ausdrücke oder Verweise braucht es ohnehin in Go. |
| Container führen ihre Kind-Actions selbst innerhalb ihres Limits aus (bisheriger Stand) | Eine lange Schleife scheiterte am Limit der ganzen Action (B8). Fehlerpolitik, Pfad, Schalter „aktiv“ und Capabilities müsste jeder Container selbst umsetzen. |
| Die Engine treibt Container über einen Iterator (`Next` liefert die nächsten Kind-Actions) | Das gibt dieselben Garantien wie `PerformChild`, verlangt aber mehr Typen und zerlegt einfache Schleifen in Zustandsautomaten. Für die Command-Action mit Warten passt es nicht. |
| statisches Zeitlimit (`TimeLimiter`) oder ein eigener Schritt, der nur das Limit berechnet | Die Dauer beim Warten kommt aus einem Template. Zweimal gerendert könnte `$randomnumber` zwei verschiedene Werte liefern, und B3 verlangt einen Rendervorgang je Action. |
| `enabled` als Kopfschlüssel von `polydoc` neben `type` und `schemaVersion` | gälte für alle Familien, auch für solche ohne Schalter |
| ein Paket je Typ | 15 Pakete für P0; `command` kollidierte mit `internal/domain/command`. Typen einer Kategorie teilen Ports und Hilfen. |
| ohne Grenze für die Tiefe | Ein fremdes Dokument könnte Dekodierer und Engine in sehr tiefe Rekursion treiben |

## Konsequenzen

**Positiv:**

- Ein Ort beschreibt jeden Action-Typ für Engine, Speichern, API, Editoren und Commands als Code.
- Die Engine setzt Schalter „aktiv“, Zeitlimits, Fehlerpolitik, Pfade und Capabilities für alle Typen gleich um; die Typen bleiben klein und lassen sich gegen Fakes testen.
- Bereiche und Enums stehen einmal im Code und gelten für Schema, Speichern und Ausführung.
- Fehlende Capabilities fallen beim Speichern als Warnung auf, nicht erst beim Stream.

**Negativ und Risiken:**

- Neue Abhängigkeit `github.com/santhosh-tekuri/jsonschema/v6`, nur in Tests. Sie hängt im Wesentlichen an einem Maintainer; fällt sie aus, lässt sie sich ersetzen, ohne dass sich Schemas oder Produktionscode ändern.
- Der eigene Schema-Typ muss mit jedem neuen Baustein wachsen und kann Schlüsselwörter falsch abbilden. Das fängt die Prüfung gegen das Meta-Schema im Konformitätstest ab.
- Die von Hand gebauten Schemas können vom Go-Code abweichen. Das fängt der Konformitätstest ab, aber nur für die Beispiele, die er bekommt.
- Änderungen an gemergtem Code aus Phase 3.2: `engine.Performer`, `engine.TimeLimiter`, `engine.WithVisualAudio` und das Feld `position` im Verlauf. `command-engine.md` bekommt dazu einen Eintrag in der Änderungshistorie.
- Weil der Kontext einer Action keine Deadline hat, kann sie ihr Limit nicht über `ctx.Deadline()` erfahren. Kein P0-Typ braucht das; ausgehende Anfragen setzen ihre eigenen Zeitlimits (B73).
- `command.Service.Save` liefert ein anderes Ergebnis; Aufrufer und Tests ändern sich mit.

**Folgearbeiten:**

- [x] Nach der Annahme Status setzen und den Index in [`README.md`](README.md) anpassen, erledigt 2026-10-01
- [x] Nach der Annahme Plan §2.4, §6.9 (Descriptor, Beispiel `chat.send`), §8 (JSON-Schema: eigener Typ, `santhosh-tekuri/jsonschema/v6` in Tests statt des Kandidaten `google/jsonschema-go`) und §12.2 anpassen und in Code-ADR-0010 den Vermerk **Ergänzt durch** setzen, erledigt 2026-10-01
- [x] Die Folgearbeit „Die Typ-Registry in Phase 3 auf `internal/polydoc` aufbauen“ in Code-ADR-0010 mit der Umsetzung abhaken, erledigt 2026-10-01
- [x] In `actions.md` die Arbeitsnamen durch die Typ-IDs aus Punkt 1 ersetzen und auf dieses ADR verweisen, erledigt 2026-10-01
- [x] In `command-engine.md` die Änderungen aus Punkt 8 mit der Umsetzung in der Änderungshistorie festhalten, erledigt 2026-10-01
- [ ] Umsetzen (Roadmap 3.3), bevor die einzelnen Typen kommen: `internal/capability`, `internal/action` mit Registry und Feldtypen, `internal/action/schema` mit Schema-Typ und Bausteinen, der Konformitätstest mit `santhosh-tekuri/jsonschema/v6`, verschachtelte Dokumente in `internal/polydoc`, die Erweiterungen der Engine aus Punkt 8 und das Speichern mit Verweisen, Namen und Warnungen
