# Test-Matrix: API-Auth (fail-closed, Throttle, Bootstrap)

Issue: Code-Review 2026-10-24, S1/S3 + Nachprüfung 2026-10-24 (S3, N1) +
Nachprüfung 2026-10-25 (N2a) — die API ist ohne Token geschlossen, der
Login-Throttle wirkt pro IP, und die Bootstrapping-Pfade (Local-Token,
STREAMCREW_TOKEN) registrieren das richtige Token.

| Test | Datei | Deckt ab |
|---|---|---|
| `TestTokenAuthFailsClosed` | `internal/api/token_auth_test.go` | S1/V1: unlesbarer Token-Store → 503, leerer Store → 401, gültiges Bearer-Token → 200. |
| `TestTokenAuthThrottlesFailedLogins` | `internal/api/token_auth_test.go` | S3: über dem Limit (10/min) → 429; der Zähler ist pro IP und nicht pro Port — fünf Versuche je Port derselben IP zählen zusammen, eine andere IP wird unabhängig gezählt. |
| `TestEnabledFailsClosed` | `internal/apitoken/apitoken_test.go` | S1: `Enabled()` ist falsch für den leeren Store, wahr mit einem Token, Fehler bei korrupter Datei (kein fail-open). |
| `TestCreateWith` | `internal/apitoken/apitoken_test.go` | S2/N3: ein Wert aus `STREAMCREW_TOKEN` wird als Admin-Token registriert und verifiziert; der Hash liegt im Store, nicht der Wert. |
| `TestAPITokensProtectTheAPI` | `internal/app/api_test.go` | Daemon legt `api.token` (0600) beim Start an; ohne Token → 401, mit dem Local-Token → 200; zusätzliche Tokens ändern nichts am Schutz. |
| `TestHotApply*` (Bootstrap-Abschnitt) | `internal/app/hot_test.go` | Der Server-Core nimmt das `--bootstrap-token`-Token (S2); der Daemon den Local-Token — die Bootstrapping-Pfade der Betriebsmodi. |
| `TestTokenForLocalServersOnly` | `internal/cli/commands_api_test.go` | N1: das lokale Admin-Token der Datenverzeichnisse geht nur an den lokalen Core (Runtime-Adresse, Loopback, Unix-Socket), nie an ein entferntes `--server`; `--token` gewinnt immer. |
| `TestLocalTokenStore` | `internal/app/runtime_test.go` | N2/N2a: ein gelöschter (leerer) Token-Store ist keine Rücknahme — der Wert aus `api.token` wird neu registriert, das File bleibt unverändert; ein nicht leerer Store ohne den Wert lässt das Token zurückgenommen, das File behält den Wert. |

Nicht abgedeckt: das Auslaufen der Throttle-Fenster (echte Zeit, kein synctest im api-Paket) und der 1024-Einträge-Prune — beides sind Abwehrpfade, die der Normalpfad deterministisch nicht erreicht.
