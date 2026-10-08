# ADR-0003: Betriebsmodi: Streaming-PC primär, Server-Anwendung sekundär

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-27 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §6.5, §6.15, §6.17, §7.6; Roadmap Phasen 1, 6, 7 und 10; geplante ADRs zum API-Protokoll und zum Sicherheitsmodell (ADR-Backlog, Plan §12.1) |
| **Ergänzt durch** | [ADR-0005](0005-core-in-desktop-builds.md): Core in Desktop-Builds als mitgelieferter eigener Prozess |

## Kontext

- Der Core ist headless, und Frontends bedienen ihn ausschließlich über die API (Plan §6).
- Der Core läuft hauptsächlich auf dem Streaming-PC. Er soll aber auch direkt auf einem Server als Server-Anwendung laufen können.

## Entscheidung

- **Primär: Streaming-PC**, in zwei Varianten:
  - eingebettet in der Desktop-App (ein Prozess, ein Binary für Endnutzer). *Präzisiert durch [ADR-0005](0005-core-in-desktop-builds.md): Die Desktop-App liefert den Core mit und startet ihn als eigenen Prozess.*
  - als lokaler Daemon (`streamcrew serve`), bedient über CLI/TUI oder die Desktop-App im Remote-Modus
- **Sekundär: Server-Anwendung** (`streamcrew serve --mode server`) auf einem Homeserver oder VPS, als Einzelbinary oder Container.
- Die Standardwerte sind für den Streaming-PC ausgelegt; den Server-Modus wählt man ausdrücklich.

| | Streaming-PC (Desktop, Daemon) | Server |
|---|---|---|
| API-Bindung | Loopback | hinter Reverse Proxy mit TLS |
| Authentifizierung | Token, lokal automatisch hinterlegt (Dateirechte 0600) | Token Pflicht |
| Host-Capabilities (Dateien, Prozesse, lokales Audio) | an, konfigurierbar | aus; später über einen Agent auf dem Streaming-PC (P2) |
| Audio | lokale Ausgabe oder Overlay | Overlay |
| Webhooks (z. B. Kick) | Tunnel oder Relay | direkt über die öffentliche URL |
| Auslieferung | Desktop-Paket, Einzelbinary | Einzelbinary, Container-Image, systemd-Unit |

- **Build:** Der Core bleibt CGO-frei, damit Server-Builds einfach bleiben. Funktionen mit CGO-Bedarf (lokales Audio unter Linux, Eingabesimulation, globale Hotkeys) liegen hinter Build-Tags bzw. in Desktop-App und Agent.
- **Einbettung:** Wie der Core in die Desktop-App eingebettet wird (In-Process über ein öffentliches Paket `core` oder als Sidecar-Prozess), entscheidet ein eigenes ADR zu API-Protokoll und Einbettung. Es ist für Phase 1 geplant. *Die Einbettung ist inzwischen in [ADR-0005](0005-core-in-desktop-builds.md) entschieden (eigener Prozess); das API-Protokoll ist weiter offen.*

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Nur Desktop | einfacher, aber kein Betrieb auf Server oder Homelab möglich |
| Server zuerst | Web-Frontend und Agent würden früher nötig; passt nicht zum Hauptszenario |

## Konsequenzen

**Positiv:**

- Die Entwicklung konzentriert sich auf das Hauptszenario, trotzdem ist der Core ohne Architekturbruch serverfähig.
- Der Desktop-Track hat Vorrang vor dem Web-Track (Plan §7.6).
- Die lokale Audioausgabe gehört zu Core 1.0 (P1), weil sie auf dem Streaming-PC gebraucht wird.

**Negativ und Risiken:**

- Zwei Betriebsarten müssen getestet und dokumentiert werden, vor allem Sicherheitsvoreinstellungen und Container.
- Host-gebundene Actions funktionieren im Server-Modus erst mit dem Agent (P2).

**Folgearbeiten:**

- [x] Einbettung in Desktop-Builds entschieden: [ADR-0005](0005-core-in-desktop-builds.md) (2026-09-27)
- [x] API-Protokoll entschieden: [ADR-0010](0010-api-protokoll.md) (2026-09-28)
- [x] ADR zum Sicherheitsmodell mit den Capabilities je Modus schreiben (Entwurf in Phase 2): [ADR-0013](0013-sicherheitsmodell.md), akzeptiert 2026-09-29
- [ ] Container-Image und Server-Dokumentation im MVP (Phase 6)
