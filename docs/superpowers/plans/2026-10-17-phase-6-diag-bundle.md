# Phase 6.3 (4/4): `streamcrew diag bundle`

Status: **erledigt** (2026-10-17)
Branch/PR: `feat/cli-diag`, auf `feat/cli-completion` gestapelt (Stack #258).

## Ziel

Diagnose-Paket für Fehlerberichte: Logs, Versionen, Konfiguration —
Secrets maskiert (ADR-0011).

## Entscheidung

- `streamcrew diag bundle [--out FILE]`, Default
  `streamcrew-diag-<zeit>.zip` im aktuellen Verzeichnis.
- Inhalt: README (Zeit, System, Hinweis), `version.txt` (Build-Info),
  `config.yaml` (`cfg.View()` — enthält von sich aus keine Secrets),
  `paths.txt`, `profiles.txt`, `api-tokens.txt` (ID/Name/Scope/Datum,
  ohne Hashes), `runtime.json` (nur bei laufendem Core), `logs/`.
- Nicht im Paket: Token-Werte/Hashes, Secret-Key, Plattform-Tokens,
  TLS-Keys — dafür reicht, dass sie nicht kopiert werden; die README
  dokumentiert es.
- Fehlschlagen ⇒ Teil-Datei wird entfernt.

## Tests

- `TestDiagBundle`: alle Einträge, Logdatei-Inhalt, Profile/Token
  vorhanden, Token-Wert nirgends, keine `api-tokens.json`.
