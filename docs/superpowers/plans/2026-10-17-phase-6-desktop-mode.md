# Phase 6.2.5: Lokaler Desktop-Modus (Core-Seite)

Status: **in Arbeit** (2026-10-17)
Branch/PR: `feat/desktop-mode`, auf `feat/core-package` gestapelt (Stack #258).

## Ziel (ADR-0005, ADR-0006, Roadmap 6.2.5)

Die Desktop-App startet den Core losgelöst und verbindet sich über die API.
Der Core liefert dafür:

1. **Lauscht auf einem Unix-Socket** (Desktop-Modus-Default; Windows 10+
   ebenfalls AF_UNIX)
2. **Laufzeitdatei** mit Adresse, PID und API-Version (+ Modus, Profil,
   Startzeit)
3. **Token im Datenverzeichnis** (0600)
4. Versions-Handshake und Shutdown-Aufruf (Version in der Laufzeitdatei +
   `StatusResponse.version`; Shutdown-RPC existiert)

## Entscheidungen

### Unix-Socket

- `config.DefaultListen(ModeDesktop)` ⇒ `unix:<data-dir>/core.sock`
  (Daemon/Server unverändert). `--listen` überschreibt weiterhin.
- `splitListen` akzeptiert das `unix:`-Präfix (keine Host/Port-Prüfung).
- `httpserver.Run`: Adressen mit `unix:`-Präfix lauschen über
  `net.Listen("unix", path)`; ein altes Socket-File wird vorher entfernt.
- Der Health-Check über das Socket funktioniert mit einem Client, der
  `DialContext` auf `unix` richtet (Test).

### Laufzeitdatei

- `<data-dir>/runtime.json` (0600, atomarer Write):
  `{"address": "unix:/…/core.sock" | "127.0.0.1:8740", "pid": 1234,
  "version": "0.5.0", "mode": "desktop", "profile": "demo",
  "started_at": "…"}`
- Geschrieben, sobald der HTTP-Server lauscht; beim Shutdown entfernt.
- In **allen** Modi geschrieben (günstig, nützt später auch `diag`);
  Desktop nutzt sie für die Erkennung „läuft für das Profil bereits ein
  Core“ (zusammen mit Profilsperre und Health-Check, ADR-0005).

### Desktop-Token

- Nur im Desktop-Modus: `<data-dir>/desktop-token` (0600) enthält den
  Token-Wert. Fehlt die Datei, erzeugt der Core beim Start ein Admin-Token
  (Name `desktop`) im apitoken-Store und schreibt den Wert (einmalig, wie
  sonst — das File ist die Ausnahme, weil die App sie lesen muss).
  Existiert das File, bleibt es (der App wird nie ein fremdes Token
  abgenommen).
- Der Wert kommt **niemals** auf die Kommandozeile (ADR-0005, Punkt 4).

### Was nicht gebaut wird

- Prozessverwaltung (losgelöster Start, Backoff-Neustart, UI-Zustand) ist
  Aufgabe der Desktop-App (anderes Repository).
- Der Versions-Handshake auf der App-Seite (Vergleich der Versionen)
  passiert in der App; der Core liefert nur die Version.

## Tests

- `internal/config`: Desktop-Default-Listen ist das Unix-Socket.
- `internal/httpserver`: Serve über ein Unix-Socket im Temp-Verzeichnis
  (Health-Request über `unix`-Dial), altes Socket-File wird ersetzt.
- `internal/app`: Laufzeitdatei enthält Adresse/PID/Version und wird beim
  Shutdown entfernt; im Desktop-Modus wird `desktop-token` (0600)
  erzeugt und validiert, ein zweiter Start nimmt sie nicht ab.

## Reihenfolge

1. config: Unix-Socket-Default + splitListen
2. httpserver: Unix-Listener
3. app: Laufzeitdatei + Desktop-Token
4. Tests, Roadmap/Changelog, Checks, PR
