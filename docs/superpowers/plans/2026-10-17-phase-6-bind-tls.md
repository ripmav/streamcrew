# Phase 6.2.3: Bindung je Betriebsmodus + optionales TLS

Status: **in Arbeit** (2026-10-17)
Branch/PR: `feat/api-bind`, auf `feat/api-auth` gestapelt (Stack #258).

## Ziel

- Die Bindung folgt dem Betriebsmodus (bereits gegeben: `config.DefaultListen` —
  Daemon/Desktop auf `127.0.0.1:8740`, Server auf `:8740`).
- **Optionales TLS**: `--tls-cert FILE --tls-key FILE` schaltet den HTTP-Server
  auf TLS um; beide zusammen oder keiner (Usage-Fehler).

## Entscheidungen

- **Konfiguration**: `config.Config` erhält `TLSCert` und `TLSKey`
  (Kong-Flags `--tls-cert`, `--tls-key`). `Resolve` prüft: beide gesetzt oder
  keiner (Usage-Fehler, exit 2).
- **Server**: `httpserver.Config` erhält `TLSCert`/`TLSKey`. `Run` lädt das
  Paar mit `tls.LoadX509KeyPair` (fehler ⇒ `supervisor.Permanent`, der Core
  startet nicht mit kaputtem Zertifikat) und dient mit
  `srv.ServeTLS(ln, cert, key)`; ohne Paar bleibt `srv.Serve`.
- **Keine** Modus-Zwangskopplung: TLS darf in jedem Modus an, auch der
  Server-Modus darf ohne TLS laufen (Reverse Proxy davor); der Loopback-
  Standard bleibt unverändert.
- `httpserver.Server` meldet im Log, ob TLS aktiv ist.

## Tests

- `internal/httpserver`: TLS-Server mit einem in der Test generierten
  Self-Signed-Zertifikat — HTTPS-Request auf `/healthz` liefert 200, ein
  HTTP-Client ohne TLS-Fallback erhält keinen klaren Text.
- `internal/config`: `Resolve` mit nur einem der beiden Flags ist ein Fehler,
  mit beiden keiner.
- `internal/cli`: Kong-Struktur akzeptiert `serve --tls-cert X --tls-key Y`.

## Reihenfolge

1. Config-Flags + Resolve-Prüfung + Test
2. httpserver: TLS-Optionen + ServeTLS + Test
3. App-Verdrahtung
4. Roadmap/Changelog, Checks, PR
