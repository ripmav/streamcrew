# ADR-0011: Keine Telemetrie

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-28 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §4.2 (Ziel Z7), §6.21; Roadmap Phasen 1 und 6 |

## Kontext

- Das Original sendet Telemetrie und protokolliert Sitzungen am eigenen Server. Das darf der Port nicht nutzen und soll es auch nicht nachbauen ([ADR-0001](0001-neuimplementierung-und-nutzung-des-originals.md)).
- Streamer geben dem Bot Zugriff auf Konten, Chat und Zuschauerdaten. Vertrauen und Datenschutz haben deshalb hohes Gewicht.
- Telemetrie bräuchte eigene Server-Infrastruktur und eine Datenschutzerklärung.

## Entscheidung

1. **Der Core sendet keine Telemetrie:** keine Nutzungsstatistiken, keine Absturzberichte, keine Sitzungsprotokolle.
2. **Keine automatischen Verbindungen zum Projekt.** Ein Hinweis auf neue Versionen über den Release-Feed ist nur nach ausdrücklicher Zustimmung aktiv (Opt-in).
3. **Daten bleiben beim Nutzer:** Sie liegen in der lokalen Profildatenbank. Nach außen verbindet sich der Core nur mit den Plattformen und Diensten, die der Nutzer selbst eingerichtet hat.
4. **Fehlersuche ohne Telemetrie:**
   - lokale Logs
   - `streamcrew doctor` zur Selbstprüfung
   - ein Diagnose-Paket (`streamcrew diag bundle`) mit Logs, Versionen und Konfiguration, wobei Secrets maskiert sind; der Nutzer gibt es bei Bedarf selbst weiter
5. **Metrik-Export für Selbstbetreiber:** Ein optionaler Export (z. B. Prometheus oder OpenTelemetry) ist keine Telemetrie im Sinne dieses ADRs. Er ist standardmäßig aus und sendet nur an Ziele, die der Nutzer selbst festlegt.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Opt-in Absturzberichte | nützlich für die Fehlersuche, braucht aber eigene Infrastruktur und Datenschutzpflichten |
| Opt-in Nutzungsstatistik | größter Aufwand bei Infrastruktur und Datenschutz, geringer Nutzen für ein privates Projekt |

## Konsequenzen

**Positiv:**

- Hohes Vertrauen, einfache Datenschutzlage, keine Server-Infrastruktur.

**Negativ und Risiken:**

- Kein Überblick über Fehler und Nutzung im Feld.
- Fehlerberichte hängen davon ab, dass Nutzer sie selbst erstellen. Das Diagnose-Paket muss deshalb gut und einfach sein.

**Folgearbeiten:**

- [x] Maskierung von Secrets in Logs (Roadmap Phase 1), erledigt 2026-09-29 in `internal/logging` ([Code-ADR-0003](code/0003-fehler-und-logging.md))
- [ ] `streamcrew diag bundle` umsetzen (Roadmap Phase 6)
- [ ] Update-Hinweis nur als Opt-in umsetzen (Roadmap Phase 11)
