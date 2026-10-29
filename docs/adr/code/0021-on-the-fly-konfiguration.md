# ADR-0021 (Code): On-the-fly-Konfiguration

- **Status:** Akzeptiert
- **Phase:** 6.5
- **Betrifft:** `internal/config`, `internal/logging`, `internal/httpserver`,
  `internal/dryrun`, `internal/supervisor`, `internal/engine`, `internal/app`
- **Bezug:** Code-ADR-0005 (Konfiguration), Code-ADR-0019 (Host-Rechte,
  Punkt 5: Watcher), Code-ADR-0003 (Betriebsmodi), ADR-0013 (Punkt 3:
  über die API nur einschränken; siehe [Nachtrag 2026-10-24](#nachtrag-2026-10-24-die-api-erweitert-keine-rechte))

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
    (`log-file` ist seit dem [Nachtrag 2026-10-24](#nachtrag-2026-10-24-die-api-erweitert-keine-rechte),
    Punkt 6, restart)
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
  - `log-file`: seit dem Nachtrag 2026-10-24, Punkt 6.
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

## Nachtrag 2026-10-24: Die API erweitert keine Rechte

Das Code-Review vom 2026-10-24 (V3, C14) hat einen Widerspruch zu
[ADR-0013](../0013-sicherheitsmodell.md), Punkt 3, gefunden: „Über die API
lassen sich Capabilities nur einschränken, nie erweitern.“ Diese
Entscheidung macht `grant`, `revoke`, `file-root`, `outbound-allow` und
`mode` hot, und `ConfigService/Update` schreibt die Datei, die der Watcher
anwendet. Ein Admin-Token reichte damit für Host-Rechte, etwa
`grant: [host:process]` oder ein Wechsel von `server` auf `daemon`. Heute
ist das nur deshalb nicht erreichbar, weil `serve` den File-Resolver nicht
lädt (C14).

Der Projektinhaber hat am 2026-10-24 entschieden. ADR-0013, Punkt 3, gilt
unverändert; diese Entscheidung passt sich an:

1. **Nur einschränken, gemessen am aktuellen Stand.** `ConfigService/Update`
   lehnt ein Dokument ab, das gegenüber den gerade wirksamen Werten erweitert.
   Maßstab sind die aktuell wirksamen Werte, nicht die beim Start. Was über die
   API eingeschränkt wurde, lässt sich nur lokal wieder freigeben. Als
   Erweiterung gilt:
   - **Rechte:** Die wirksame Rechtemenge (Basis des Modus, `grant`, `revoke`)
     ist keine Teilmenge der bisherigen. Das deckt neue `grant`-Einträge und
     entfernte `revoke`-Einträge ab.
   - **`file-root`:** eine neue Wurzel oder eine Wurzel, deren Verzeichnis
     nicht gleich der bisherigen Wurzel ist oder nicht darin liegt.
   - **`outbound-allow`:** ein Eintrag, der bisher nicht in der Liste steht.
   - **`mode`:** jeder Wechsel. Die API schreibt nur im Server-Modus, und jeder
     andere Modus hat eine größere Rechtebasis und keinen SSRF-Schutz.
2. **Dieselbe Regel für die übrigen sicherheitsrelevanten Einstellungen.** Als
   Erweiterung gilt:
   - **`cors-allow`:** ein Origin, der bisher nicht in der Liste steht.
   - **`listen`:** ein Host, der bisher nicht gebunden war, außer Loopback
     oder einem Unix-Socket. Ein anderer Port auf demselben Host ist keine
     Erweiterung.
   - **`overlay-listen`** (ergänzt 2026-10-29, [ADR-0019](../0019-overlay-architektur.md) §1):
     dieselbe Regel wie bei `listen`. Der Overlay-Server ist ohne Token nur
     bei reinem Loopback-Bind offen; ein Bind über Loopback hinaus verlangt
     zwar einen Token, gibt aber Fremden eine neue Angriffsfläche. Der Wert
     gilt erst nach einem Neustart; abgelehnt wird schon das Schreiben.
     Overlay-Tokens selbst sind keine Startkonfiguration, sondern API-Tokens
     mit dem Scope `overlay`.
   - **`tls-cert`, `tls-key`:** TLS entfernen, während der Bind über
     Loopback hinausreicht. Zertifikate tauschen bleibt erlaubt, weil das der
     Hauptfall ist.
   - **`dev`:** einschalten. Ausschalten ist erlaubt.
3. **Ganz oder gar nicht.** Ein Dokument mit einer Erweiterung wird ganz
   abgelehnt und nicht geschrieben. Die Antwort nennt die betroffenen
   Schlüssel mit dem Hinweis, dass sie nur lokal in der Konfigurationsdatei
   erweitert werden können.
4. **Nur der API-Pfad prüft.** Die Prüfung sitzt in `Watcher.Update`, nicht im
   Neuladen der Datei. Wer die Datei lokal ändert, hat lokalen Zugriff und
   darf weiter erweitern (ADR-0013, Punkt 3; Code-ADR-0019, Punkt 5).
   Fixierte Werte (Flag, Umgebungsvariable) bleiben wie bisher unberührt.
5. **Reihenfolge der Umsetzung.** Zuerst kommt die Prüfung mit Tests, auch für
   `mode` server → daemon. Erst danach wird `serve` verdrahtet (C14:
   `core.Run` lädt den File-Resolver), damit die Lücke nie offen ist.
6. **`log-file` gilt nur beim Start.** Wäre `log-file` über die API änderbar,
   könnte ein Admin-Token den Core Log-Zeilen in jede Datei schreiben lassen,
   auf die das Konto des Cores Schreibrechte hat. Darin stehen auch Inhalte,
   die Angreifer steuern, etwa Chat-Texte. Deshalb wechselt `log-file` von
   **hot** zu **restart**, für die Datei wie für die API: Pfad und Ein/Aus
   gelten erst nach einem Neustart, und der Watcher meldet eine Änderung als
   Restart. `log-format`, `log-max-size` und `log-max-files` bleiben hot.

Für die übrigen Einstellungen ändert dieser Nachtrag nichts.

Umgesetzt am 2026-10-25; siehe [Roadmap 6.5](../../roadmap.md#65-on-the-fly-konfiguration-server-modus).
