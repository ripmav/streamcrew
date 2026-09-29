# Code-ADR-0010: Polymorphe Serialisierung

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §6.8, §6.9, §8, §10; Roadmap Phase 2.2, 3.3 bis 3.5; [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0005](0005-konfiguration.md), [Code-ADR-0006](0006-teststrategie.md), [Code-ADR-0008](0008-datenbankzugriff.md) |

## Kontext

- Commands enthalten Listen verschiedenartiger Actions und Requirements, dazu kommen Trigger, Settings-Sektionen, Overlay-Widgets und später Integrationen: jeweils viele Typen hinter einer gemeinsamen Schnittstelle (Plan §6.9).
- Plan §6.9 sieht JSON-Dokumente mit `type`-Diskriminator und `schemaVersion` vor, Migrationen je Typversion und die Kodierung mit `encoding/json/v2`.
- `encoding/json/v2` ist in go1.27.1 noch nur mit `GOEXPERIMENT=jsonv2` verfügbar (geprüft 2026-09-29). Plan §6.9 nahm das Gegenteil an.
- Dieselben Dokumente erscheinen als Commands als Code in YAML (Plan §6.9, `go.yaml.in/yaml/v3` nach [Code-ADR-0005](0005-konfiguration.md)), in der API und später beim Import.
- Typen ändern sich über die Versionen: Felder kommen hinzu, werden umbenannt oder bekommen eine andere Bedeutung. Gespeicherte Commands müssen trotzdem weiter laufen.
- Eine Datenbank kann Typen enthalten, die die laufende Version nicht kennt, etwa nach einem Downgrade oder wenn ein Typ entfernt wurde. Solche Daten dürfen beim nächsten Speichern nicht verloren gehen.

## Entscheidung

1. **Flaches Dokument mit Diskriminator:** Jedes polymorphe Objekt ist ein JSON-Objekt mit `type` (stabile Typ-ID, z. B. `chat.send`), `schemaVersion` (ganze Zahl ab 1) und den Feldern seines Typs auf derselben Ebene:

   ```json
   {"type": "chat.send", "schemaVersion": 1, "message": "Hallo $username!", "asBot": true}
   ```

   Dasselbe Objekt in YAML hat dieselben Schlüssel. Fehlt `schemaVersion` in handgeschriebenem YAML, gilt die aktuelle Version des Typs.
2. **Feldnamen in lowerCamelCase**, wie im JSON der Protobuf-API ([ADR-0010](../0010-api-protokoll.md)) und im Beispiel für Commands als Code (Plan §6.9). Die Startkonfiguration bleibt davon unberührt; ihre Schlüssel folgen den Flag-Namen (Code-ADR-0005).
3. **Registry je Familie** (Actions, Requirements, Trigger, Settings-Sektionen, Overlay-Widgets …) im kleinen generischen Paket `internal/polydoc`:
   - Ein Eintrag nennt Typ-ID, aktuelle Version, die Dekodierfunktion der aktuellen Version und die Migrationen der älteren Versionen.
   - Registriert wird ausdrücklich in der Composition Root über Listenfunktionen der Pakete, z. B. `action.Builtins()`, nicht per `init()` ([Code-ADR-0002](0002-dependency-injection.md)).
   - Die Typ-Registry mit Descriptors, JSON-Schemas und Capabilities (Phase 3, ADR-Backlog in Plan §12.2) baut auf diesen Einträgen auf.
4. **Dekodieren in drei Schritten:**
   1. `type` und `schemaVersion` lesen.
   2. Ältere Versionen Schritt für Schritt migrieren, `vN → vN+1`. Eine Migration arbeitet auf dem JSON-Objekt (`map[string]any`) und ist eine reine Funktion.
   3. In die Go-Struktur der aktuellen Version dekodieren, mit `DisallowUnknownFields`, damit Tippfehler in handgeschriebenen Commands auffallen.
5. **Unbekannte Typen und zu neue Versionen** werden nicht verworfen. Sie werden als `polydoc.Unknown` mit dem unveränderten JSON gehalten und beim Speichern unverändert zurückgeschrieben. Ausgeführt werden sie nicht; die Engine meldet sie als deaktiviert mit Grund.
6. **Kodieren:** immer in der aktuellen Version, mit `type` und `schemaVersion`. Migrierte Dokumente werden in der Datenbank erst beim nächsten Speichern neu geschrieben; ein Massenlauf ist nicht nötig.
7. **`encoding/json` (v1)**, bis `encoding/json/v2` ohne `GOEXPERIMENT` verfügbar ist. Das Format ist davon unabhängig. Der Wechsel betrifft nur `internal/polydoc` und die Typen selbst und bekommt dann ein ergänzendes Code-ADR, das die abweichenden Standards von v2 bewertet, etwa bei ungültigem UTF-8 und doppelten Schlüsseln.
8. **Tests:** Für jede Version eines Typs gibt es Golden Files in `testdata/`: das gespeicherte Dokument der alten Version und das erwartete Ergebnis nach der Migration ([Code-ADR-0006](0006-teststrategie.md)). Die Dekodierung bekommt einen Fuzz-Test, weil sie Eingaben von außen verarbeitet.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Hülle mit verschachtelter Konfiguration (`{"type": …, "config": {…}}`) | eine Ebene mehr in YAML und API ohne Mehrwert; das flache Format entspricht dem Beispiel in Plan §6.9 |
| .NET-artiges `$type` mit Typnamen aus dem Code | koppelt das Format an Paket- und Typnamen; ein Umbenennen im Code bräche gespeicherte Daten |
| Protobuf `Any` oder `oneof` in der Datenbank | binär und schwer von Hand zu bearbeiten; Commands als Code brauchen ohnehin ein lesbares Format |
| Migrationen per SQL beim Schema-Update | polymorphe JSON-Dokumente lassen sich in SQL kaum sicher umbauen; die Migration je Typ in Go ist testbar |
| unbekannte Typen beim Lesen verwerfen | Datenverlust nach einem Downgrade oder bei entfernten Typen |
| `GOEXPERIMENT=jsonv2` für Release-Builds | experimentelle API kann sich noch ändern; alle Build-Werkzeuge (goreleaser, CI, Renovate) müssten das Flag setzen |
| Registrierung per `init()` | widerspricht Code-ADR-0002 und dem Linter `gochecknoinits` |

## Konsequenzen

**Positiv:**

- Ein Format für Datenbank, API, YAML und Import; lesbar und von Hand bearbeitbar.
- Gespeicherte Commands überstehen Versionswechsel, und nichts geht durch unbekannte Typen verloren.
- Die Wahl der JSON-Bibliothek lässt sich später ändern, ohne das Format zu ändern.

**Negativ und Risiken:**

- Jede inkompatible Änderung eines Typs braucht eine Migration mit Golden Files.
- `map[string]any` in Migrationen ist untypisiert; Fehler zeigen erst die Tests.
- `DisallowUnknownFields` macht ältere Versionen des Cores streng gegenüber neueren Dokumenten; das fangen Punkt 5 und die Versionsnummer ab.

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen, den Index in [`README.md`](README.md) anpassen und Plan §6.9 zu `encoding/json/v2` berichtigen, erledigt 2026-09-29
- [x] `internal/polydoc` mit Registry, Migrationskette, `Unknown` und Fuzz-Test umsetzen (Roadmap Phase 2.2), erledigt 2026-09-29
- [ ] Golden Files für die Versionen der ersten echten Typen (Roadmap Phase 3); der Mechanismus selbst ist mit Erwartungen im Test abgedeckt
- [ ] Die Typ-Registry in Phase 3 auf `internal/polydoc` aufbauen
