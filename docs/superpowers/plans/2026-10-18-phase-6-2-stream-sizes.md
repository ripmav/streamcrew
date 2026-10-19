# Plan: 6.2.6 Stream-Größen zur Startzeit

## Kontext

Die Stream-Pfade des Cores (API `StreamService`) haben zwei im Code verdrahtete Größen (`internal/app/api_stream.go`):

- **Bus-Abo-Buffer des Stream-Hubs** = 1024 (`event.WithBuffer(1024)`): wie viele Bus-Events der Hub der Umwandlung/Fan-out hinterherhängen darf, bevor der Bus wegwirft (`event.lag`).
- **Catch-up-Buffer** = 256 (`streamBufferSize`): wie viele der letzten Events ein neu abonnierender Client per `since` nachholen kann.

Buffer-Größen sind Go-Kanal-Kapazitäten: nach Anlage nicht veränderbar. Eine Laufzeit-Änderung würde ein Re-Subscribe mit Ereignislücke bedeuten — das wird bewusst **nicht** gemacht (Abgrenzung unten).

## Ziel

Beide Größen lassen sich **nur zur Startzeit** des Cores über die üblichen Kanäle festlegen: Flag (`--stream-buffer`, `--stream-catchup`), Env (`STREAMCREW_STREAM_BUFFER`, `STREAMCREW_STREAM_CATCHUP`) und Konfigurationsdatei (`stream-buffer`, `stream-catchup`). **Keine** API- oder TUI-Änderung zur Laufzeit; Defaults bleiben 1024/256.

## Entscheidungen

| Punkt | Entscheidung |
|---|---|
| Keys | `stream-buffer` (Default **1024**), `stream-catchup` (Default **256**) — eingebettete `StreamConfig` mit Präfix `stream-`, wie `LogConfig` |
| Validierung | beide ≥ 1, sonst Usage-Fehler in `Resolve` (wie `--shutdown-timeout`) |
| Startzeit-Strenge | Die Werte werden in `app.New` aus dem aufgelösten `config.Config` per Wertekopie in den `apiStream` übernommen. Der Hub liest sie nie vom live `a.cfg` (der Watcher ersetzt es bei Datei-Änderung) — eine Datei-Änderung wirkt also erst nach Neustart, auch wenn der Hub erst beim ersten Client startet |
| Hot-Klassifikation | Nicht in `Hot()` → TUI-Einstellungen zeigen automatisch ⟳ (restart), der Watcher meldet „restart erforderlich“. Kein Apply-Hook, kein API-Pfad |
| `core.Options` | spiegelt die beiden Felder (`StreamBuffer`, `StreamCatchUp`, 0 = Default) — Konvention von 6.2.4; `serveCmd` mappt sie weiter |
| Dateiresolver | neue Keys kommen automatisch über die kong-Tags (Präfix-Struktur wie `LogConfig`); keine Änderung nötig |
| Per-Client-Kanal | bleibt 256 (interner Backpressure-Hebel, bewusst nicht exposed — YAGNI) |
| Internes `DefaultBuffer` (256) | bleibt, das betrifft die internen Aboenten, nicht den Stream-Pfad |

## Umsetzung

1. `internal/config/config.go`: `StreamConfig{Buffer, CatchUp}` eingebettet als `Stream StreamConfig embed:"" prefix:"stream-"`, Defaults in den Kong-Tags, `check()` für ≥ 1.
2. `core/core.go`: `Options.StreamBuffer`/`StreamCatchUp`, Defaults in `Run`, Mapping in den `config.Config`.
3. `internal/cli/commands.go` (`serveCmd`): Mapping `cfg.Stream.*` → `Options`.
4. `internal/app/app.go`: `newAPIStream(a, cfg.Stream)`; `internal/app/api_stream.go`: `newStreamHub(a, streamCfg)` mit Feldern `busBuffer`/`catchUp`; `run` nutzt `event.WithBuffer(h.busBuffer)`, `publish` nutzt `h.catchUp`; `streamBufferSize`-Konstante weg.
5. Doku: `docs/usage.md` (Abschnitt „Logs im Stream“ um die zwei Keys ergänzen — Startzeit, ⟳), Roadmap 6.2.6 + History, Test-Matrix.

## Tests (alle in-Prozess, ohne Sleeps — Tempo-neutral)

| Test | Datei | Deckt ab |
|---|---|---|
| `TestResolve/…stream-buffer…`/`stream-catchup…` (0/negativ ⇒ Fehler; Defaults) | `internal/config/config_test.go` | Validierung + Defaults |
| `TestStreamHubCatchUpKeepsTheConfiguredSize` | `internal/app/api_stream_internal_test.go` | `catchUp=3`: 5 publizierte Events → Snapshot hat genau 3 (die letzten) |
| `TestStreamHubTakesTheConfiguredBusBuffer` | dito | `busBuffer` des Hubs = der Konfigurationswert (Whitebox-Wiring-Assert, ein Zeile) |

## Abgrenzung

- **Keine Laufzeit-Änderung**: kein Hot-Hook, kein RPC, keine TUI-Taste (Anforderung). Datei-Änderung ⇒ Watcher meldet restart; neu gelesen wird erst beim nächsten Start.
- **Kein Re-Subscribe-Mechanismus**: ein Kanal lässt sich nicht vergrößern; ein „Resize“ mit Lücke ist Komplexität ohne messbaren Nutzen (Ponytail).
- Per-Client-Fan-out-Kanal (256) und das interne `event.DefaultBuffer` bleiben unverändert.

## Exit-Kriterien

- `--stream-buffer`/`--stream-catchup` (Flag/Env/Datei) werden respektiert; ungültige Werte scheitern vor dem Start
- TUI-Einstellungen zeigen die neuen Keys mit ⟳; Watcher meldet bei Änderung „restart“
- Race-Suite grün, Lint 0 Issues, Defaults unverändert (1024/256)
