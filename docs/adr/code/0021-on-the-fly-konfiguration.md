# ADR-0021 (Code): On-the-fly-Konfiguration

- **Status:** Akzeptiert
- **Phase:** 6.5
- **Betrifft:** `internal/config`, `internal/logging`, `internal/httpserver`,
  `internal/dryrun`, `internal/supervisor`, `internal/engine`, `internal/app`
- **Bezug:** Code-ADR-0005 (Konfiguration), Code-ADR-0019 (Host-Rechte,
  Punkt 5: Watcher), Code-ADR-0003 (Betriebsmodi)

## Kontext

Der Core startet mit einer Startkonfiguration aus Flag, Umgebungsvariablen und
Konfigurationsdatei (Code-ADR-0005). Auf einem Server (VPS, Container) will man
den laufenden Core aber nicht neu starten, um z. B. die Log-Ebene zu ändern,
einen Dry Run zu schalten, die TLS-Zertifikate zu tauschen oder die
Bind-Adresse zu verschieben. Code-ADR-0019, Punkt 5, hat dafür den Watcher
etabliert: Er vergleicht die Konfigurationsdatei im Sekundentakt und wendet
Änderungen der Host-Rechte ohne Neustart an.

Phase 6.5 (On-the-fly-Konfiguration) macht alle Starteinstellungen, die ein
laufender Prozess anwenden kann, hot: Der TUI (einziger Pfad, Server-Modus)
schreibt die Konfigurationsdatei über die `ConfigService`, der Watcher wendet
die Änderung an.

## Entscheidung

### Aufteilung in hot, restart und fix

Jede Starteinstellung ist genau eines von dreien:

- **hot** — der laufende Core wendet die neue Datei-Werte ohne Neustart an:
  - `log-level`, `log-component-level`: gemeinsame `logging.LevelStore`, die
    jeder level-Handler pro Record liest; die Änderung wirkt sofort in allen
    Komponenten.
  - `log-format`, `log-file`, `log-max-size`, `log-max-files`: das `logging.Dynamic`
    baut die Ausgabe-Handler (Konsole, Rotationsdatei) neu und stellt den
    geteilten Slot des Logger-Baums um; die alte Datei wird geschlossen.
  - `dry-run`: die Plattform läuft immer hinter dem schaltbaren
    `dryrun.NewSwitchablePlatform`-Wrapper, die Twitch-Operationen hinter
    `dryrun.NewSwitchOps`; der Wrapper delegiert je Aufruf an die echte oder
    die aufzeichnende Seite.
  - `dev`: die pprof-Routen bleiben gemountet und dispatchen auf ein
    `atomic.Bool`; ohne Developer-Modus antworten sie mit 404.
  - `cors-allow`: der CORS-Check liest eine austauschbare Allowlist
    (`atomic.Value`).
  - `listen`, `tls-cert`, `tls-key`: `httpserver.Server.Rebind` öffnet einen
    neuen Listener (TLS: Key-Pair vorher laden), dient darauf und schaltet den
    alten Server sanft ab. Jeder Fehler davor lässt den alten Bind aktiv.
    Der Request-Kontext des alten Servers endet mit ihm, damit offene Streams
    beim Rebind schließen.
  - `mode`: ändert die Basis der Host-Rechte; der Watcher berechnet die Rechte
    wie bisher neu, ein eigener Apply ist nicht nötig.
  - `shutdown-timeout`: Supervisor, Engine und HTTP-Server lesen den Wert beim
    Shutdown aus einer Funktion (`WithShutdownTimeoutFunc` /
    `SetShutdownTimeoutFunc`), die der Watcher aktualisiert.
  - `grant`, `revoke`, `file-root`, `outbound-allow`: bereits hot
    (Code-ADR-0019, Punkt 5), unverändert.
- **restart** — gilt erst nach Neustart und wird als solcher gemeldet:
  - `data-dir`: offene Datenbank- und Log-Dateien des Prozesses.
  - `profile`: offene Profil-Datenbank, Commands, Timer und Plattform sind an
    das laufende Profil gebunden.
  - `config`: der Pfade der Datei selbst.
- **fix** — vom Flag oder der Umgebungsvariablen gesetzt; die Datei darf den
  Wert nicht ändern (Präzedenz von Code-ADR-0005 bleibt): Der Watcher wendet
  für sie keine Datei-Werte an und meldet das beim Start.

### Mechanik

- `config.Watcher` erhält `config.ApplyFuncs` (Log, DryRun, Dev, CorsAllow,
  Listen, ShutdownTimeout). Er rechnet die Datei auf die effektive
  Konfiguration (Basis + Datei-Werte der `fromFile`-Einstellungen), validiert
  sie per `Resolve()` und ruft nur für geänderte Einstellungen die Applies.
  Fehlschlag eines Applies lässt den vorherigen Zustand und wird geloggt.
  Ohne `ApplyFuncs` bleibt das Rechte-only-Verhalten von Code-ADR-0019
  (Desktop/Tests).
- `app.App` hält die laufenden Werte unter `runtimeMu` (`cfgView`) und liefert
  die Applies: Logger-Austausch über `logging.Dynamic.Update`, Dry-Run- und
  Shutdown-Timeout per `atomic`, Dev/CORS/Listen über `httpserver.Server`
  (Rebind schreibt danach die Runtime-Datei neu).

### Grenzen

- `mode` ohne explizites `listen` verschiebt den Bind nicht (der
  Modus-Default der Adresse gilt nur ohne Listen-Wert); wer den Bind mit dem
  Modus verschieben will, setzt `listen` mit.
- Der Watcher bleibt Polling (1 s) statt inotify: cross-Plattform, einfach,
  und eine Sekunde Latenz ist für Konfigurationen gut genug.
- Die Validierung eines neuen Dokuments läuft über `Resolve()` auf Basis +
  Datei-Werte; erst ein gültiges Dokument wird geschrieben und angewendet.

## Folgen

- **Positive:**
  - Server-Betrieb ohne Neustart für alle betriebsrelevanten Einstellungen
    (Logging, Dry Run, TLS/Bind, CORS, Rechte, Timeout).
  - Ein Mechanismus (Datei + Watcher) für alle Einstellungen; der TUI ist ein
    gewöhnlicher Datei-Writer über die `ConfigService`.
  - Die Präzedenz Flag > Env > Datei bleibt bestehen; fixierte Werte bleiben
    nachweislich fix.
- **Negative / Kosten:**
  - Der Logger-Baum leitet abgeleitete Handler pro Record neu ab (Slot);
    vernachlässigbar bei Log-Ausgaben.
  - `Rebind` hält zwei Listener kurzzeitig nebeneinander; offene Verbindungen
    auf dem alten Bind werden beim Rebind geschlossen (Streams verbinden
    neu).
  - Drei nicht-hotte Einstellungen (`data-dir`, `profile`, `config`) müssen in
    jeder Antwort und in der TUI als „Restart“ gekennzeichnet werden.
