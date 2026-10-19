# Plan: 6.2.7 Bus-Lag-Reporter (drops/min)

## Kontext

Der Event-Bus wirft für träge Aboenten Events weg (`internal/event/bus.go`): pro Subscription zählt `dropped`, die `event.lag`-Enveloppe meldet sie an den Aboenten, und die interne Warn-Log ist auf 1 Minute rate-limitet — meldet aber den **Bestand**, nicht die **Rate**. Ziel: pro Aboent **drops/min** im Log, bei laufendem Lag.

## Entscheidung: Pull, nicht Push (B1)

Der Bus bekommt **keinen** Ticker, keine Fenster-Mathematik und keinen fremden Code im Publisher-Pfad. Stattdessen:

1. **Bus (minimal, ~15 Zeilen):**
   - `Subscription.total atomic.Uint64` — monoton wachsend, wird in `drop()` neben `dropped` inkrementiert und **nie** zurückgesetzt (im Gegensatz zu `dropped`, das bei Zustellung der `event.lag`-Enveloppe zur Null geht — wer es pollt, müsste sonst Differenzen über Zustellungen hinweg rechnen).
   - `type DropCounter interface { Name() string; Count() uint64 }` — wird von `*Subscription` erfüllt (Pointer-Identität = stabiler Key).
   - `func (b *Bus) DropCounters() []DropCounter` — Snapshot der aktiven Aboenten unter dem Read-Lock.
   - Die bestehende 1-Minuten-Warn-Log **bleibt** (deckt Bus-Nutzer ohne Reporter ab, z. B. Tests).
2. **Reporter (neues File `internal/event/lagreport.go`, ~50 Zeilen):**
   - `NewLagReporter(b *Bus, logger *slog.Logger)` mit `interval time.Duration` (Produktion: 1 Minute; unexportiert, Tests setzen es klein).
   - `Run(ctx)`: Ticker, pro Tick `DropCounters()` pollen, Differenz `Count() - letzter Stand` pro Aboent; **nur wenn > 0** eine Warn-Log: `subscription`, `drops` (Differenz im Fenster), `window` (Fensterlänge), `total` (monotoner Gesamtstand). Gestoppt durch den Run-Kontext.
   - Abgefallene (geschlossene) Aboenten werden aus dem Diff-Map entfernt.
   - **Kein** „wieder gesund“-Signal (bewusst weggelassen: zusätzlicher Zustand, niemand hat es verlangt).
3. **Wiring (`internal/app/app.go`, eine Zeile):** in `App.Run` neben den übrigen Hintergrund-Komponenten `go event.NewLagReporter(a.bus, component(a.logger, "event")).Run(runCtx)` — derselbe Log-Komponente wie der Bus selbst.

## Abgrenzung

- Kein API-Status-Zähler, kein TUI-Display (die `event.lag`-Enveloppe bleibt der Client-Weg).
- Keine Push-Closure im Bus (Kontrakt „muss schnell sein“ auf dem Publisher-Pfad unter Lock — nicht nötig, Pull ist kleiner).
- Doppellog der bestehenden Bus-Warnung in der Produktion: vertretbar (gleiche Information, andere Granularität); die interne Warn bleibt aus Test-Gründen.

## Tests (deterministisch, in-Prozess; Reporter-Test mit 10-ms-Fenster → < 1 s)

| Test | Datei | Deckt ab |
|---|---|---|
| `TestSubscriptionTotalCountsDropsMonotonic` | `internal/event/lag_total_test.go` | `total` zählt jeden Drop; bleibt nach Zustellung der `event.lag`-Enveloppe stehen (während `dropped` zurücksetzt); Burst mit vollem Buffer ist exakt zählbar. |
| `TestDropCountersListsActiveSubscriptions` | dito | Names, geschlossene Aboenten fallen aus dem Snapshot. |
| `TestLagReporterLogsDropsInTheWindow` | `internal/event/lagreport_test.go` | 10-ms-Fenster: deterministischer Burst → eine Warn-Log mit `drops`, `window`, `total`; ohne Drops: keine Log. |
| `TestLagReporterStopsWithTheContext` | dito | nach `cancel` kommt keine Log mehr. |

## Exit-Kriterien

- Bus ohne neue Goroutine und ohne fremden Code im Publisher-Pfad (Pull-Accessor nur)
- Reporter loggt drops/min je Aboent, nur bei > 0, stoppt sauber mit dem Core
- Race-Suite grün (atomic-Zähler), Lint 0 Issues
