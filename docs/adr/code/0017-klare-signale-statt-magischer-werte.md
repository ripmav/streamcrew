# Code-ADR-0017: Klar definierte Signale statt magischer Werte

| | |
|---|---|
| **Status** | Vorgeschlagen |
| **Datum** | 2026-09-30 |
| **Entscheidung durch** | … (Vorgabe des Projektinhabers vom 2026-09-30; Abnahme des ADR ausstehend) |
| **Bezug** | Plan §11.1; [ADR-0010](../0010-api-protokoll.md); [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0003](0003-fehler-und-logging.md), [Code-ADR-0005](0005-konfiguration.md), [Code-ADR-0010](0010-polymorphe-serialisierung.md) |

## Kontext

- In der ersten Fassung des Auslösens der Command-Engine (PR #50) hieß eine Ablehnung mit leerem Text „dem Nutzer nichts sagen“, und eine leere Liste von Durchläufen hieß „noch nichts zu tun“. Beides war mehrdeutig: Die Schwelle ließ sich auf beide Arten ausdrücken, und ein vergessener Text wäre unbemerkt still geblieben.
- Das Muster steckt auch anderswo, meist als Nullwert, der etwas anderes bedeutet als sich selbst: 0 für „Standardwert“, `nil` gegenüber einer leeren Liste für „vom Aufrufer übernehmen“, ein leerer Text für „Systemzeitzone“, ein `nil`-Logger für „verwerfen“, ein unbekannter Enum-Wert, der still auf den Standard fällt.
- Go empfiehlt, den Nullwert eines Typs nutzbar zu machen. Für Typen wie einen leeren Puffer ist das sinnvoll. In Schnittstellen führt es aber zu Werten mit zwei Bedeutungen, die nur die Dokumentation auseinanderhält.
- Die Kommunikation des Cores lesen auch Programme in anderen Sprachen, die Go-Konventionen nicht kennen: die API nach ADR-0010, die Ereignisse auf dem Bus, gespeicherte Dokumente und Dateiformate wie Commands als Code.

## Entscheidung

Die Regeln gelten für jede Schnittstelle im Projekt: exportierte Go-APIs, Ports zwischen Paketen, Ereignis-Nutzlasten, API-Nachrichten, gespeicherte Dokumente und Einstellungen, Konfiguration, Kommandozeile und Dateiformate.

1. **Ein Wert steht nur für sich selbst.** Kein Wert aus dem Wertebereich eines Feldes, Parameters oder Rückgabewerts trägt eine Sonderbedeutung. Es gibt also keinen leeren Text für „still“, keine 0 für „Standardwert“, kein `nil` gegenüber leerer Liste für „übernehmen“, keine -1 für „unbegrenzt“ und keinen leeren Text für „Systemeinstellung“. Zahlen behalten ihre natürliche Bedeutung: Eine Dauer von 0 ist keine Zeit, eine Anzahl von 0 ist keine.
2. **Nicht vorhanden heißt nur: gibt es nicht.** Optionale Daten dürfen fehlen, als `nil`-Zeiger, als Null-ID oder als leerer Name, wo ein leerer Name ungültig ist; in JSON fehlt dann das Feld. Das Fehlen bedeutet ausschließlich, dass es das Ding nicht gibt, etwa keinen Nutzer bei einem Timer oder keine Gruppe bei einem Command. Es bedeutet nie „nimm stattdessen X“ oder „tu Y“. Hängt ein Verhalten an der Wahl, bekommt die Wahl ein eigenes Feld oder einen eigenen Enum-Wert, oder der Aufrufer löst sie auf, bevor er den Wert übergibt. Ob ein Feld gilt, darf ein Enum desselben Typs bestimmen, zum Beispiel der Zustand einer Instanz für ihren Endzeitpunkt.
3. **Ausgänge haben Namen.** Hat eine Operation mehrere normale Ausgänge, gibt sie einen Ergebnistyp zurück: ein Enum für den Ausgang, etwa `queued`, `waiting` oder `rejected`, und für die Daten jedes Ausgangs eigene Felder. Normale Ausgänge sind keine Fehler, auch wenn nichts passiert.
4. **Fehler sind nur Fehler.** Ein `error` heißt: Die Operation konnte nicht entscheiden oder nicht ausgeführt werden. Er ist ein Sentinel (`ErrXxx`) oder ein typisierter Fehler (`XxxError`) nach Code-ADR-0003 und lässt sich mit `errors.Is` oder `errors.AsType` prüfen, nie am Text.
5. **Enums sind geschlossen.** Ein Enum ist ein eigener Typ mit Konstanten und einer Prüfung (`Valid`). Der leere Wert ist kein Mitglied, außer er ist als eigene Konstante mit eigener Bedeutung definiert. Unbekannte Werte werden abgelehnt, nicht auf einen Standard abgebildet. Dokumente unbekannten Typs bleiben nach Code-ADR-0010 als Platzhalter erhalten; das ist ein definierter Ausgang, kein Rückfall.
6. **Standardwerte werden ausdrücklich gesetzt.** Voreinstellungen stehen als Konstanten oder Default-Funktionen (etwa `settings.DefaultCommands()`) und werden gesetzt, bevor ein Wert benutzt wird. Die Prüfung lehnt fehlende Pflichtwerte ab. Beim Option-Pattern (Code-ADR-0002) ist das Weglassen der Option der ausdrückliche Weg zum dokumentierten Standard; eine Option mit `nil` bedeutet nicht „aus“, dafür gibt es eine eigene Option. Eingaben von außen, etwa nicht gesetzte Flags oder Felder einer Datei, löst genau eine Stelle in ausdrückliche Werte auf (Code-ADR-0005), bevor der Rest des Cores sie sieht.
7. **Durchsetzung.** Das Review prüft jede neue und geänderte Schnittstelle darauf. Bestehender Code wird umgestellt (Folgearbeiten). Eine Ausnahme braucht die Zustimmung des Projektinhabers und steht mit Begründung am Code.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Nullwert als Standard, wie in der Go-Standardbibliothek üblich (`http.Server` mit Timeout 0 für „keiner“) | bequem, aber jeder Aufrufer muss die Doppelbedeutung kennen; ein vergessenes Feld fällt nicht auf |
| alle Ausgänge außer dem Erfolg als Fehler | vermischt Alltag mit Fehlern; Aufrufer prüfen Sentinels für normale Fälle, und echte Fehler gehen darin unter |
| `nil`-Zeiger für jedes Optionale, auch mit Rückfall | `nil` wäre wieder ein Sonderwert, sobald ein Rückfall daran hängt; erlaubt bleibt `nil` nur für „gibt es nicht“ |
| eigener generischer Option-Typ (`Optional[T]`) | Go hat keinen; ein eigener wäre ungewohnt und kollidiert mit JSON; Zeiger für Fehlen und ausdrückliche Felder für Wahlen genügen |

## Konsequenzen

**Positiv:**

- Schnittstellen erklären sich selbst; die Ausgänge einer Operation lassen sich vollständig aufzählen und testen.
- Fehler in Zulieferern, etwa ein vergessener Text oder ein falscher Enum-Wert, fallen bei der Prüfung auf, statt still ein anderes Verhalten auszulösen.
- Frontends und Werkzeuge in anderen Sprachen bekommen eindeutige Nachrichten.

**Negativ und Risiken:**

- mehr Typen und Felder; der Nullwert eines Structs ist öfter ungültig und muss vor Gebrauch gefüllt werden
- Bestehender Code und manche Festlegungen müssen geändert werden, darunter eine Entscheidung des Projektinhabers (leere Zeitzone für die Systemzeitzone, Code-ADR-0009).
- Die Grenze zwischen „gibt es nicht“ (erlaubt) und „nimm stattdessen“ (nicht erlaubt) verlangt im Review Aufmerksamkeit.

**Folgearbeiten:**

- [ ] Nach der Annahme Status setzen und den Index in [`README.md`](README.md) anpassen
- [ ] Command-Engine (Phase 3.2, PR #48 bis #51) vor dem Merge umstellen: Ergebnistypen für die Anforderungen, das Auslösen und die Aufrufe; ausdrückliche Fehlerpolitik, Zeitlimits, Argumente von Aufrufen, Einstellungen und Zielnutzer
- [ ] Bestehenden Code umstellen, jeweils mit Tests und angepasster Spezifikation:
  - `settings.Time.TimeZone`: leer heißt Systemzeitzone (Code-ADR-0009); braucht eine neue Entscheidung des Projektinhabers und eine neue Version der Sektion
  - `template.Scope` (Phase 3.1, PR #42 bis #45): `ArgDelimiter` leer heißt `|`, `Location` `nil` heißt UTC, `Target` `nil` heißt auslösender Nutzer, `ArgsText` leer heißt „Argumente mit Leerzeichen“
  - `template.StreamFamily` und `template.UserFamily`: ein `nil`-Port heißt „keine Werte“
  - `platform.Name.ProfileURL`: leerer Text für „keine Adresse“
  - `role.Role.Rank`: 0 für eine unbekannte Rolle
  - `polydoc.Registry.Version`: 0 für einen unbekannten Typ
  - `logging.Config`: `Console` `nil` und `File` leer schalten die Ausgabe ab
  - `supervisor.New`, `event.NewBus`, `httpserver.New`, `backup.NewScheduler`: ein `nil`-Logger verwirft; bei `httpserver.New` meldet ein `nil`-`ready` immer bereit
  - `app.WithKeyring(nil)`: überspringt den Schlüsselbund
  - `backup.Request.Kind`: leer heißt `manual`
  - `config.Config.Profile`: leer heißt „aktives Profil“, erst `profile.Manager.Resolve` löst es auf
