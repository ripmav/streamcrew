# ADR-0012: Persistenz: SQLite je Profil, Backups und Secrets

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | [ADR-0003](0003-betriebsmodi.md), [ADR-0011](0011-keine-telemetrie.md); Plan §6.12, §6.13; Roadmap Phase 2.1 und 2.4; [Code-ADR-0005](code/0005-konfiguration.md), [Code-ADR-0008](code/0008-datenbankzugriff.md), [Code-ADR-0009](code/0009-ids-und-zeit.md) |

## Kontext

- Der Core verwaltet Commands, Nutzer, Währungen, Einstellungen, Tokens und vieles mehr (Plan §6.13). Alles liegt beim Nutzer; es gibt keine Server des Projekts ([ADR-0011](0011-keine-telemetrie.md)).
- Er läuft auf dem Streaming-PC und als Server-Anwendung ([ADR-0003](0003-betriebsmodi.md)), ohne separaten Datenbankdienst. Der Core ist CGO-frei.
- Ein Nutzer kann mehrere Profile haben, etwa für verschiedene Kanäle. Genau eines ist aktiv (Plan §6.13).
- Streamer verlieren ungern Konfiguration: Backups müssen einfach, konsistent und automatisch sein.
- Tokens von Plattformen und Diensten sind die heikelsten Daten. Sie dürfen nicht im Klartext auf der Platte oder in Backups liegen (Exit-Kriterium von Phase 2).

## Entscheidung

1. **Eine SQLite-Datenbank je Profil:** `<data-dir>/profiles/<profil-id>.db`.
   - Die Profil-ID ist ein unveränderlicher Kurzname aus `a-z`, `0-9` und `-` (höchstens 32 Zeichen), abgeleitet beim Anlegen. Der Anzeigename steht in der Datenbank und lässt sich ändern, ohne die Datei umzubenennen.
   - Jede Änderung landet sofort in einer Transaktion; ein periodisches Speichern gibt es nicht.
   - Laufzeiteinstellungen liegen als typisierte, versionierte Sektionen in der Profildatenbank und werden über die API geändert ([Code-ADR-0005](code/0005-konfiguration.md), Punkt 10).
   - Zugriff, Treiber und Migrationen regelt [Code-ADR-0008](code/0008-datenbankzugriff.md).
2. **Profile:**
   - Beim ersten Start legt der Core das Profil `default` an.
   - Welches Profil aktiv ist, steht in `<data-dir>/profiles/active` (eine Zeile mit der Profil-ID). `--profile` bzw. `STREAMCREW_PROFILE` übersteuert das für einen Start.
   - Unterkommandos: `profile list|create|rename|use|delete`. `use` und `delete` wirken auf einen gestoppten Core; bei laufendem Core wechselt später die API das Profil (Phase 6).
   - `delete` legt vorher ein Backup an.
3. **Sperre gegen Doppelstart:** Der Core hält für seine Laufzeit eine Betriebssystem-Dateisperre auf `<data-dir>/streamcrew.lock`, unter Linux und macOS mit `flock`, unter Windows mit `LockFileEx` (über `golang.org/x/sys`).
   - Die Sperre gilt für das ganze Datenverzeichnis, nicht je Profil: Zwei Cores auf demselben Verzeichnis würden sich auch bei Logs und Laufzeitdateien stören.
   - In der Datei stehen PID und Startzeit, damit die Fehlermeldung eines zweiten Starts den laufenden Prozess nennen kann. Nach einem Absturz gibt das Betriebssystem die Sperre frei; eine veraltete Sperre gibt es nicht.
   - Unterkommandos, die das Profil schreiben (`profile …`, `backup restore`), nehmen dieselbe Sperre und scheitern, solange ein Core läuft.
4. **Backups:**
   - **Format:** eine ZIP-Datei `<data-dir>/backups/<profil-id>-<UTC-Zeitstempel>.zip` mit `profile.db` und `manifest.json`. Das Manifest enthält Formatversion, App-Version, Schemaversion, Profil-ID und -Name, Zeitpunkt und SHA-256 der Datenbank.
   - **Konsistenz:** `VACUUM INTO` erzeugt einen konsistenten, kompakten Schnappschuss, während der Core weiterläuft.
   - **Zeitplan:** täglich, wöchentlich und monatlich, mit Aufbewahrung je Stufe (Standard: 7 täglich, 4 wöchentlich, 12 monatlich), einstellbar je Profil. Der Zeitplan läuft als Runnable im Supervisor, in der Zeitzone des Profils.
   - **Restore:** nur bei gestopptem Profil. Geprüft werden Prüfsumme und Schemaversion: Ein Backup mit neuerem Schema als dem der laufenden Version wird abgelehnt, ein älteres beim nächsten Öffnen migriert. Vor dem Restore sichert der Core den aktuellen Stand als Backup.
   - **Unterkommandos:** `backup create|list|restore`.
5. **Secrets im Ruhezustand:**
   - Tokens und andere Secrets liegen in der Profildatenbank, verschlüsselt mit AES-256-GCM. Jeder Wert hat eine eigene Nonce; die ID des Datensatzes geht als zusätzliche authentifizierte Daten ein, damit sich verschlüsselte Werte nicht zwischen Datensätzen vertauschen lassen.
   - **Schlüssel:** ein Schlüssel je Datenverzeichnis, gesucht in dieser Reihenfolge:
     1. Umgebungsvariable `STREAMCREW_SECRET_KEY` (Base64), für Container und Server
     2. Schlüsselbund des Betriebssystems über `github.com/zalando/go-keyring`, Standard auf dem Streaming-PC
     3. Datei `<data-dir>/secret.key` mit den Rechten `0600`, wenn es keinen Schlüsselbund gibt, etwa auf einem Server ohne Desktop; der Core warnt dann im Log
     
     Fehlt ein Schlüssel, erzeugt ihn der Core beim ersten Start am ersten verfügbaren Ort aus Stufe 2 oder 3. Stufe 1 legt er nie selbst an.
   - **Rotation:** Jeder verschlüsselte Wert trägt die ID seines Schlüssels. `secret rotate` erzeugt einen neuen Schlüssel und verschlüsselt alle Werte in einer Transaktion neu; danach wird der alte Schlüssel entfernt.
   - **Backups** enthalten die Secrets nur verschlüsselt, der Schlüssel liegt nicht im Backup. Ein Restore auf einem anderen Rechner braucht deshalb den Schlüssel: `secret export-key` und `secret import-key` folgen mit Phase 6. Ohne Schlüssel bleibt das Backup nutzbar; nur die Anmeldungen müssen neu erfolgen.
6. **Kein ORM und keine zweite Datenbank:** Globale Daten, die kein Profil brauchen, sind Dateien im Datenverzeichnis (Konfiguration, aktives Profil, Sperre, Logs). API-Tokens gehören zum Profil (Plan §6.13).

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Eine Datenbank für alle Profile | einfachere Abfragen über Profile hinweg, aber Backups, Restore und Löschen eines Profils werden aufwendig, und ein Fehler in einem Profil gefährdet alle |
| JSON- oder YAML-Dateien wie im Original | ohne Transaktionen und Indizes; Ranglisten und Statistiken bei vielen Nutzern werden langsam (Plan §13, R17) |
| Eingebettete Key-Value-Speicher (bbolt, Badger, Pebble) | keine Abfragesprache, Ranglisten und Filter müssten von Hand gebaut werden; Backups und Werkzeuge sind weniger verbreitet |
| PostgreSQL oder ein anderer Datenbankdienst | ein Dienst mehr auf dem Streaming-PC; für den Hauptbetriebsmodus unverhältnismäßig |
| Profil-Datei nach dem Anzeigenamen benennen | Umbenennen hieße Datei, Backups und Verweise umbenennen |
| Sperrdatei mit PID ohne Betriebssystem-Sperre | bleibt nach einem Absturz liegen und braucht eine unsichere Prüfung, ob die PID noch lebt |
| Sperre per `PRAGMA locking_mode=EXCLUSIVE` | schützt nur die Datenbank, nicht Logs und Laufzeitdateien; die Fehlermeldung eines zweiten Starts wäre unverständlich |
| Backups per Dateikopie oder Online-Backup-API | Dateikopie ist im WAL-Modus nicht konsistent; `VACUUM INTO` liefert in einem Schritt eine konsistente, kompakte Datei |
| Secrets unverschlüsselt, geschützt nur über Dateirechte | Backups und kopierte Datenverzeichnisse gäben Tokens preis |
| Schlüssel aus einem Passwort beim Start | verhindert den unbeaufsichtigten Start, etwa mit dem Rechner oder als Dienst |

## Konsequenzen

**Positiv:**

- Ein Profil ist eine Datei: leicht zu sichern, zu kopieren und zu löschen.
- Backups laufen ohne Unterbrechung und lassen sich vor dem Einspielen prüfen.
- Tokens sind in Datenbank und Backups verschlüsselt; ein kopiertes Datenverzeichnis ohne Schlüssel gibt sie nicht preis.

**Negativ und Risiken:**

- Abhängigkeiten: `golang.org/x/sys` für die Dateisperre und `github.com/zalando/go-keyring` (MIT) für den Schlüsselbund, unter Linux mit `github.com/godbus/dbus/v5` (BSD-2-Clause).
- Unter Linux ohne Secret-Service (z. B. ohne Desktop) liegt der Schlüssel in einer Datei neben den Daten. Er schützt dann nur Backups und Kopien, die ohne diese Datei weitergegeben werden.
- Geht der Schlüssel verloren, sind die Secrets verloren. Die Anmeldungen müssen neu erfolgen; alle übrigen Daten bleiben erhalten.
- Mehrere Profile gleichzeitig zu betreiben, ist nicht vorgesehen (Backlog der Roadmap).

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen, den Index in [`README.md`](README.md) anpassen und die Backlog-Nummern in Plan und Roadmap nachziehen, erledigt 2026-09-29
- [x] `internal/store`, Profile, Sperre und Backups umsetzen (Roadmap Phase 2.1), erledigt 2026-09-29
- [x] Secrets mit Schlüsselquellen und Rotation umsetzen (Roadmap Phase 2.4), erledigt 2026-09-29. Das Paket heißt `internal/vault`, weil die Berechtigungsregeln des Projektinhabers Pfade mit „secret“ sperren; das Unterkommando heißt weiter `secret rotate`, die Tabelle `secrets`.
- [ ] `secret export-key|import-key` und Profilwechsel über die API (Roadmap Phase 6)
