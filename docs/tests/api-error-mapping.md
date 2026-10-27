# Test-Matrix: API-Fehler-Mapping

Issue: Code-Review 2026-10-24, U5 — die Anwendungs-Sentinels (Decimal-Syntax, `id.Parse`, `role.Parse`, unbekannte Statistik, Settings-Validierung, `ErrReserved`, `ErrLoginInProgress`, …) durften nicht zu `CodeInternal` (HTTP 500) werden, sondern müssen die passenden Client-Codes tragen.

| Test | Datei | Deckt ab |
|---|---|---|
| `TestAPIErrorMapping` | `internal/api/error_test.go` | `apiError` mappt jeden Sentinel auf seinen Code: `store.ErrNotFound` → NotFound, `store.ErrConflict` → AlreadyExists, `ErrRestoreRunning` → FailedPrecondition, `ErrLoginInProgress` → Aborted; Decimal/ID/Rolle/Statistik/Settings/Counter (inkl. `ErrReserved`) → InvalidArgument; verpackte Sentinel bleiben erkannt; ein unbekannter Fehler bleibt Internal. |

Nicht abgedeckt: das Mapping über den echten ConnectRPC-Stack (die Codes sind die des Wire-Formats, `apiError` ist die einzige Stelle, die sie vergibt).
