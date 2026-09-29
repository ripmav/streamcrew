# Code-ADR-0004: Nebenläufigkeit und Supervisor

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §6.1, §6.6, §11.1; Roadmap Phase 1.2 und 1.3; [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0003](0003-fehler-und-logging.md), [Code-ADR-0006](0006-teststrategie.md) |

## Kontext

- Plan §6.1: Jede Goroutine hat einen Besitzer. Ein Supervisor startet sie, ein `context` bricht sie ab, und beim Shutdown wird auf sie gewartet.
- Plan §6.6: Jede Verbindung (Plattform, Integration, Overlay-Server) ist ein Runnable mit Restart-Policy: exponentieller Backoff mit Jitter und Obergrenze, Status auf dem Bus. Der Shutdown läuft geordnet und mit Zeitlimits.
- Verbindungen zu Twitch, OBS und anderen Diensten brechen im Betrieb regelmäßig ab. Sie müssen sich selbst erholen, ohne den Prozess zu beenden, und ohne endlos im Takt zu wiederholen.
- `gosec` meldet `math/rand` und `math/rand/v2` als schwachen Zufall (G404), auch für Jitter.

## Entscheidung

1. **Zwei erlaubte Formen für Goroutinen:**
   - **Langlebige Hintergrundarbeit** ist ein Runnable, das beim Supervisor registriert ist.
   - **Kurzlebige Parallelität innerhalb einer Funktion** nutzt `sync.WaitGroup.Go` bzw. `golang.org/x/sync/errgroup`, sobald der erste Fehler die übrigen abbrechen soll. Die Funktion kehrt erst zurück, wenn alle Goroutinen beendet sind.

   Ein `go`-Statement ohne eine dieser Formen gibt es nicht; das Review prüft das.
2. **Runnable:** `Run(ctx context.Context) error`, dazu der Adapter `RunnableFunc`.
   - `Run` blockiert, bis die Arbeit beendet ist, und hinterlässt keine Goroutinen.
   - Nach Abbruch des Kontexts gibt `Run` `nil` zurück (sauber gestoppt), bei einem Ausfall einen Fehler.
   - `supervisor.Permanent(err)` markiert einen Fehler, den ein Neustart nicht behebt, z. B. einen belegten Port oder ungültige Zugangsdaten.
3. **Supervisor** im Paket `internal/supervisor`:
   - Runnables werden mit Namen vor dem Start registriert und in dieser Reihenfolge gestartet. Einmalige, blockierende Startschritte (Profil laden, Migrationen) erledigt die Composition Root vorher, nicht der Supervisor.
   - **Restart-Policy je Runnable:** `RestartOnFailure` (Standard: nach einem Fehler neu starten, nach `nil` beendet), `RestartAlways`, `RestartNever`.
   - **Backoff:** exponentiell ab 1 s mit Faktor 2 bis höchstens 1 min, mit Jitter: die Hälfte der Wartezeit fest, die andere Hälfte zufällig. Läuft ein Runnable mindestens 1 min stabil, beginnt der Backoff wieder bei 1 s. Der Zufall kommt aus `crypto/rand`, das `gosec` akzeptiert.
   - **Kritische Runnables** (Option `Critical`): Scheitert ein kritisches Runnable endgültig (permanenter Fehler oder `RestartNever`), fährt der Supervisor alles herunter und gibt den Fehler zurück. Beispiel: Der HTTP-Server kann seinen Port nicht belegen.
   - **Panics** in `Run` werden abgefangen, mit Stacktrace geloggt und wie ein Fehler behandelt.
   - **Status:** Eine Callback-Option meldet jeden Zustandswechsel mit Name, Zustand (`starting`, `running`, `backoff`, `stopped`, `failed`), Fehler und Anzahl der Neustarts. Die Composition Root leitet daraus die Bereitschaft (`/readyz`) ab. Ab Phase 2 gehen die Meldungen zusätzlich auf den Event-Bus.
4. **Geordneter Shutdown:**
   - Endet der Kontext von `Run`, stoppt der Supervisor die Runnables in umgekehrter Startreihenfolge. Jedes bekommt seinen eigenen Kontext, der abgebrochen wird; der Supervisor wartet, bis `Run` zurückkehrt.
   - Für den gesamten Shutdown gilt ein Zeitlimit (Standard 15 s, `--shutdown-timeout`). Ist es erreicht, bricht der Supervisor die übrigen Runnables gleichzeitig ab und gibt `ErrShutdownTimeout` mit den Namen der hängenden Runnables zurück. Der Prozess endet dann mit Exit-Code `1`.
5. **Signale:** `serve` beendet sich über `signal.NotifyContext` bei SIGINT und SIGTERM. Nach dem ersten Signal wird die Standardbehandlung wiederhergestellt, sodass ein zweites Strg+C den Prozess sofort beendet.
6. **Synchronisation:**
   - Mutex für gemeinsamen Zustand, Channels für Übergaben; den Channel schließt der Sender.
   - typisierte Atomics (`atomic.Bool`, `atomic.Int64`) statt der Funktionen auf rohen Feldern
   - kein `time.Sleep` zur Koordination; Timer über `time.NewTimer` bzw. den Kontext, Aufräumen über `context.AfterFunc`
7. **Tests** für Nebenläufigkeit und Zeitverhalten laufen in `testing/synctest`, in der CI zusätzlich mit Race-Detector ([Code-ADR-0006](0006-teststrategie.md)).

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `github.com/thejerf/suture` | ausgereifter Supervisor nach Erlang-Vorbild, aber eine Abhängigkeit mit mehr Konzepten als nötig (Baum aus Supervisoren, eigene Logger-Hooks) |
| `github.com/oklog/run` | startet und stoppt Gruppen, kennt aber keine Neustarts, keinen Backoff und keine Reihenfolge beim Stoppen |
| nur `errgroup` | der erste Fehler beendet alles; für Verbindungen, die sich erholen sollen, ungeeignet |
| `github.com/cenkalti/backoff` | eine Abhängigkeit für rund 20 Zeilen Code |
| Jitter mit `math/rand/v2` | von `gosec` als G404 gemeldet; eine Ausnahme bräuchte `//nolint`, `crypto/rand` kostet hier nichts |
| gemeinsamer Kontext für alle Runnables | einfacher, aber kein geordneter Shutdown: Plattformen würden gleichzeitig mit den Diensten beendet, die sie noch brauchen |

## Konsequenzen

**Positiv:**

- Kein Goroutine-Leck durch vergessene Hintergrundarbeit; jede Verbindung erholt sich mit begrenzter Last auf die Gegenstelle.
- Der Shutdown ist geordnet und zeitlich begrenzt; hängende Komponenten werden mit Namen gemeldet.
- Der Status jedes Runnables steht für Health-Checks, API und Frontends bereit.

**Negativ und Risiken:**

- Ein eigener Supervisor muss gepflegt und gut getestet werden.
- Es gibt keine Abhängigkeiten zwischen Runnables. Wartet eines auf ein anderes, muss es das selbst abbilden, etwa über den Status. Werden solche Fälle häufig, ergänzt ein neues Code-ADR die Reihenfolge.

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen und den Index in [`README.md`](README.md) anpassen, erledigt 2026-09-29
- [x] `internal/supervisor` umsetzen und in `internal/app` nutzen (Roadmap Phase 1.3), erledigt 2026-09-29
- [x] Statusmeldungen auf den Event-Bus legen (Roadmap Phase 2.3), erledigt 2026-09-29 als Ereignis `supervisor.status`
