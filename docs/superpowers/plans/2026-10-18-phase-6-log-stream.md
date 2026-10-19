# Plan: 6.6 Log-Stream (read-only)

Roadmap: [6.6](../../roadmap.md#66-log-stream-read-only). Ziel: Die Log-Zeilen des Cores werden
Teil des Event-Streams. Der Core publiziert jedes Log-Record, das den Level-Filter passiert, als
`log`-Ereignis auf dem Bus. Die TUI (Logs-Ansicht) und die API (Abo-Filter) können dann auswählen,
ab welchem Level angezeigt wird. **Read-only**: TUI und API ändern nie das Log-Level des Cores —
das bleibt bei der On-the-fly-Konfiguration (6.5) bzw. der Konfigurationsdatei.

## 1. Ist-Zustand (Audit)

| Teil | Zustand |
|---|---|
| `LogEntry` im Stream | Existiert: `api/proto/streamcrew/v1alpha1/stream.proto` (Feld `Event.log = 9`) mit `level`, `component`, `message` — kein Produzent |
| TUI Logs-Ansicht | Existiert: Ansicht 7 rendert `log`-Events aus dem Abo (`internal/tui/model.go`, `feedView`); der Test `views_test.go:99` füttert ein synthetisches `LogEntry` |
| Abo-Filter | `EventFilter.kinds` filtert nach Typ-Präfix (inkl. „log“) — kein Level-Filter |
| Logger-Baum | `internal/logging`: `NewDynamic` → `levelHandler` (liest `LevelStore` pro Record) → `baseSlot`/`derivedSlot` → Ausgabe (Konsole/Datei). `Dynamic.Update` tauscht Ausgabe + Levels zur Laufzeit (6.5); `derivedSlot` delegiert an `baseSlot` |
| Bus | `internal/event`: nicht blockierend, volles Puffer → `event.lag`; jeder Typ muss im Katalog mit seiner Nutzlast angemeldet sein (`event.Register[T]`) |
| `component`-Attribut | `logging.ComponentKey = "component"`; jede Komponente bekommt den Logger per `With(ComponentKey, …)` |

Fehlt also nur: (a) ein Produzent, der Records als Bus-Events publiziert, (b) die zwei
Level-Filter auf der Konsumenten-Seite.

## 2. Architektur

```
slog.Record ── Level-Store (6.5, hot) ──► levelHandler
                                            │
                     ┌──────────────────────┼───────────────────────┐
                     ▼                      ▼                       ▼
               Konsole/Datei        Sink (neu, 6.6.1)          (weitere Sinks)
                                     │  event.New(Source{system, "log"},
                                     │  eventtype.Log, LogPayload{level, component, message})
                                     ▼
                                   Bus ──► StreamService.Subscribe
                                              │  (6.6.3: min_level filtert nur Typ "log")
                                              ▼
                                   TUI (6.6.2: Taste l filtert clientseitig)
```

Der Sink sitzt **hinter** der Level-Store: Was der Core nicht loggt (z. B. `log-level=warn`),
erreicht den Bus nicht — das Volumen ist damit von der 6.5-Konfiguration begrenzt. Der Sink
blockiert nie (Bus-Publish ist nicht blockierend; bei Überlast kommt `event.lag`).

## 3. 6.6.1 Core publiziert Log-Ereignisse (M, PR 1)

### 3.1 `internal/domain/eventtype` — der Typ `log`

- `Log event.Type = "log"` (Namensregel erfüllt: der Typ ist anwendungseigen wie die
  Authentifizierungsereignisse — **nicht** in `All()`, löst keine Event-Commands aus).
- `LogPayload struct { Level, Component, Message string }` — 1:1 die Felder von `LogEntry`.
- Der `component` kommt aus dem `component`-Attribut des Records (fehlend → `""`).
  Strukturierte Attribute (`slog.Attr` jenseits von `component`) bleiben unberücksichtigt —
  `LogEntry` hat dafür kein Feld; dokumentiert.

### 3.2 `internal/logging` — der Sink

- `Dynamic` bekommt `SetSink(h slog.Handler)`: registriert einen zusätzlichen Handler, der jedes
  Record erhält, das `levelHandler` durchlässt, parallel zur Ausgabe; überlebt `Update` (der
  Sink ist am `Dynamic`-Objekt, nicht am austauschbaren Basis-Handler).
- Einbaupunkt: `baseSlot.Handle` ruft nach `out().Handle` den Sink auf (nil → nichts).
  `derivedSlot` delegiert an `baseSlot` → Komponente-Logger sind automatisch dabei.
  `Enabled` bleibt beim Ausgangs-Handler (der Sink ändert die Level-Logik nicht).
- Der Sink-Handler im App-Paket (`internal/app`): liest aus dem Record
  `r.Level`, das `component`-Attribut, `r.Message` und publiziert
  `event.New(event.Source{Kind: event.SourceSystem, Name: "log"}, eventtype.Log, …)` auf `a.bus`.
  Publish-Fehler werden verworfen (Logging darf den Core nie verlangsamen oder brechen; der Bus
  meldet Überlast selbst als `event.lag`).

### 3.3 Verkabelung in `internal/app`

- Im Bus-Katalog anmelden wie die Authentifizierungsereignisse
  (`event.Register[eventtype.LogPayload](c, eventtype.Log)`) — der Katalog wird in `New` gebaut,
  der Sink hängt an, sobald der Bus existiert (nach `a.bus`-Erstellung, vor `Run`).
- Kein neuer `config`-Key: Ein/Aus folgt dem Bestehen des Bus-Abos; der Level-Filter ist der
  bestehende von 6.5.

### 3.4 Spezifikation

- `docs/spec/events.md`: der `log`-Typ wird wie die Authentifizierungsereignisse dokumentiert
  (Absatz neben dem Katalog, nicht in `All()`; Nutzlast-Tabelle `level`/`component`/`message`).

### 3.5 Tests

- `internal/logging`: Sink erhält ein Record hinter der Level-Store (unter `warn` bei
  `log-level=warn` kein `info`-Record); überlebt `Update`; `component`-Logger delegieren.
- `internal/app`: nach `Run` erscheint ein gelogtes Record als `log`-Event auf dem Bus
  (Abo + Timeout-Assertion).
- E2E über die API: `Subscribe` ohne Filter liefert das `log`-Event (das existierende TUI-Test-
  Muster mit dem in-process Core übernehmen).

## 4. 6.6.2 TUI: Log-Level-Auswahl in der Logs-Ansicht (S, PR 2)

- `model` bekommt `logMin slog.Level` mit Default `slog.LevelDebug` (= „alle“, da unter `debug`
  nichts kommt).
- Taste `l` in der Logs-Ansicht schaltet durch: `debug → info → warn → error → debug`.
- `handleEvent` (`case "log"`): nur annehmen, wenn `level ≥ logMin` (string → `slog.Level`
  via `config.ParseLevel`, wie in der App).
- Kopfzeile der Logs-Ansicht zeigt den aktiven Filter („logs ≥ warn“).
- Read-only: Die Taste verändert nur den Sicht-Filter des Models — kein API-Call, kein
  Config-Write.
- Tests: Events unter dem Filter-Level erscheinen nicht, darüber schon; Taste schaltet durch.

## 5. 6.6.3 API: Level-Filter im Abo (S, PR 3)

- `EventFilter` in `stream.proto` bekommt `string min_level = 2` („debug“/„info“/“warn“/“error“,
  leer = alle) — `buf generate`.
- Server-Seite (`internal/api/stream.go`): bei der Zustellung wird ein Event nur ausgelassen,
  wenn sein Typ `log` ist und sein `level` unter `min_level` liegt; alle anderen Typen gehen
  unverändert durch. Ungültiger Wert (keines der vier) → `InvalidArgument` auf dem Abo.
  (Leerer Filter wie heute = alle.)
- Read-only: Der Filter beschreibt nur, was geliefert wird; das Core-Level bleibt unberührt
  (6.5). Kein neuer RPC.
- Tests: Abo mit `min_level=warn` liefert `log`-Events ab warn, keine darunter; `chat.message`
  geht ungefiltert durch; leer = alle; ungültig = InvalidArgument.

## 6. 6.6.4 Dokumentation (S, PR 4)

- `docs/usage.md`: Logs-Ansicht (Taste `l`, Kopfzeile), `min_level` im Abo (Beispiel-Client-Call).
- Test-Matrizen `docs/tests/I-6.6.x-*.md` je Test-Datei-Änderung.

## 7. Abgrenzung / bewusst nicht dabei

- Kein RPC, der das Core-Log-Level ändert (das ist 6.5, Konfigurationsdatei).
- Keine strukturierten Attribute in `LogEntry` (kein Proto-Feld; bei Bedarf später).
- Kein Ring-Puffer/History: Vor Events vor dem Abo sind im Logfile (`log-file`), nicht im Stream.
- Kein eigener `log.*`-Typ-Baum (z. B. `log.line`): ein Typ `log` reicht, `component` trennt.

## 8. Exit-Kriterien

- Ein `slog.Info` einer beliebigen Komponente erscheint nach dem Abo als `log`-Event (TUI + API).
- TUI: `l` schaltet den Sicht-Filter, Kopfzeile zeigt ihn; der Core wird nicht verändert.
- API: `min_level` filtert nur `log`-Events; ungültiger Wert wird abgelehnt.
- `log-level=warn` im Core (6.5) → keine `debug`/`info`-Events im Stream.
