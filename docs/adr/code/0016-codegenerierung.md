# Code-ADR-0016: Codegenerierung: buf für Protobuf/Connect, esbuild für TypeScript

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-10-16 |
| **Entscheidung durch** | Projektinhaber (ripmav) — abwartend auf Review |
| **Bezug** | [ADR-0010](../0010-api-protokoll.md) (API-Protokoll), [ADR-0003](../0003-betriebsmodi.md), [Code-ADR-0008](0008-datenbankzugriff.md) (sqlc-Konventionen), [Code-ADR-0001](0001-go-toolchain-und-linting.md) (Versionspinning, Linting); Plan §6.14, §6.16; Roadmap Phase 6.1 |

## Kontext

- Der API-Vertrag ist Protobuf in `api/proto/streamcrew/v1alpha1/`; `buf` übernimmt Linting, Breaking-Change-Prüfung und Codegenerierung (ADR-0010).
- Generiert wird Go (Messages und ConnectRPC-Handler) für Core, TUI und Desktop. Die Weboberfläche erzeugt ihren TypeScript-Client aus demselben Vertrag in ihrem eigenen Repository (ADR-0009) — das fällt nicht in dieses ADR.
- Später (Phase 7, Overlays) bündelt esbuild die TypeScript-Overlay-Runtime in Go per `go generate` (Plan §6.16), ohne Node.js. Dieses ADR legt die gemeinsame Konvention für beide Generatoren fest, damit sie identisch zu sqlc (Code-ADR-0008) arbeiten.
- sqlc hat die vorbildliche Konvention: Werkzeug per `go run …@<version>` mit gepinnter Version, generierter Code eingecheckt, `go:generate` als Einstieg, CI-Check, dass der eingecheckte Code zum Eingabedatum passt.

## Entscheidung

1. **`buf` für Protobuf/Connect** (`github.com/bufbuild/buf`, Apache-2.0):
   - Protobuf-Dateien in `api/proto/streamcrew/v1alpha1/` sind die einzige Quelle (ADR-0010). `buf.yaml` im Repository-Root definiert das Modul (`path: api/proto`), das Lint-Profil (`STANDARD`) und das Breaking-Profil (`FILE`).
   - `buf.gen.yaml` generiert mit `protoc-gen-go` und `protoc-gen-connect-go` (`connectrpc.com/connect`, MIT) nach `api/gen`, `paths=source_relative`. Der generierte Go-Code liegt eingecheckt in `api/gen/streamcrew/v1alpha1/` (+ `…connect/` für die Handler).
   - Einstieg ist `go generate ./api/...` über `api/generate.go`, die die Plugin- und die buf-Version pinnt — wie `internal/store/generate.go` für sqlc. `go install …@<version>` für die Plugins, `go run …@<version>` für buf.
   - Eine `tool`-Direktive in `go.mod` wird bewusst nicht verwendet (wie bei sqlc): buf und die Plugins würden viele Module in die `go.mod` des Cores ziehen. Die lokale Konvention aus Code-ADR-0001/0008 geht vor.
   - Die CI prüft mit `buf lint`, dass der Vertrag lintet, und regeneriert per `buf generate` in `api/gen` und scheitert über `git diff --exit-code`, wenn der eingecheckte Code nicht zum Protobuf passt (Analogon zu `sqlc diff`).
   - **Namenskonvention der Request-/Response-Typen:** Der Vertrag ist ein einziges Paket `streamcrew.v1alpha1` für alle Services (ADR-0010), deshalb würden die strikten je-Methode-Namen (`ListRequest` in mehreren Services) kollidieren. Stattdessen gibt es deskriptive, paket-eindeutige Namen (z. B. `ListCommandsRequest`, `ListUsersRequest`, `GetCounterRequest`); der Item-Typ des Server-Streams ist der aussagekräftige Typ `Event`. Die beiden Lint-Regeln `RPC_REQUEST_STANDARD_NAME` und `RPC_RESPONSE_STANDARD_NAME` werden in `buf.yaml` entsprechend ausgenommen (kommentiert).
2. **`esbuild` für die Overlay-Runtime (Phase 7, vorweggenommen):**
   - Die TypeScript-Overlay-Runtime wird per `go generate` mit `github.com/evanw/esbuild/pkg/api` (MIT) gebündelt; kein Node.js. Das Bündel wird eingecheckt und per `go:embed` ausgeliefert (Plan §6.16).
   - Gleiche Konvention wie buf/sqlc: gepinnte Version (für esbuild in `go.mod`, für buf und die Plugins im `go:generate`-Aufruf), eingechecktes Artefakt, CI-Check, dass das eingecheckte Bündel zum TypeScript-Quellcode passt. esbuild wird nur vom Generator-Programm `internal/overlay/internal/bundlegen` importiert und landet nicht im Core-Binary.
3. **Versionierung:**
   - Bis Core 1.0 gilt `v1alpha1`; ab `v1` blockiert `buf breaking` inkompatible Änderungen in der CI. Größere Brüche kommen als neues Paket (`v2`), parallel zum alten (ADR-0010).
4. **Abhängigkeiten:** `connectrpc.com/connect` (MIT) und `google.golang.org/protobuf` (BSD-3-Clause) sind Laufzeitabhängigkeiten des generierten Codes und des Servers; beide stehen auf der Lizenz-Allowlist (Code-ADR-0001). `buf` und die Plugins sind Build-Werkzeuge und landen nicht in `go.mod`.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `protoc` direkt statt `buf` | `buf` bringt Lint, Breaking-Checks und ein einheitliches Modul-/Konfigurationsformat; `protoc` allein hat keins davon |
| buf- und Plugin-Versionen als `tool`-Direktive in `go.mod` | viele Module von buf/protoc in der `go.mod` des Cores; Renovate-Updates und `go.sum` würden davon dominiert — wie bei sqlc verworfen |
| Remote-Plugins (Buf Schema Registry) statt lokaler `protoc-gen-*` | externe Abhängigkeit und Netz zur Buildzeit; lokale Plugins über `go install …@<version>` sind reproduzierbar und offline |
| Generierten Code nicht einchecken, in der CI erzeugen | die CI-Prüfung „generiert = eingecheckt“ geht verloren; eingecheckter Code ist revidierbar und braucht kein Werkzeug zur Kompilierung |
| gRPC-Web/Transcoding für den Browser | Connect läuft im Browser ohne Proxy (ADR-0010); Transcoding wäre ein zweiter Mechanismus |

## Konsequenzen

**Positiv:**

- Ein Vertrag, aus dem typisierter Go-Code für Core, TUI und Desktop entsteht; Lint und Breaking-Checks sind automatisiert.
- Die Konvention ist zu sqlc identisch (gepinnte Version, eingecheckter Code, `go:generate`, CI-Check), also kein neues Muster.
- Laufzeitabhängigkeiten bleiben minimal und lizenzkonform.

**Negativ und Risiken:**

- `go generate` braucht beim ersten Lauf Netz und etwas Zeit, um buf und die Plugins zu bauen (wie sqlc).
- `buf generate` findet die Plugins auf dem `PATH`; lokal und in der CI muss `GOPATH/bin` auf dem `PATH` stehen.
- `STANDARD`-Lint kann bei späteren, exotischen Konstrukten (z. B. `oneof`-Namen, Custom-Options) Regeln erzwingen, die dann einzeln ausgenommen werden müssen.

**Folgearbeiten:**

- [x] `buf.yaml`, `buf.gen.yaml`, `api/generate.go` anlegen, `SystemService` als ersten Service generieren (Roadmap 6.1.1), erledigt 2026-10-16
- [x] CI: `buf lint` und Regenerierungs-Check (`buf generate` + `git diff --exit-code`) (Roadmap 6.1.1), erledigt 2026-10-16
- [ ] Restliche Services von `v1alpha1` (Auth, Stream, Chat, Command, User, Counter, Settings, Backup) schreiben (Roadmap 6.1.2)
- [ ] `buf breaking` ab `v1` in der CI aktivieren (Roadmap, ab Core 1.0)
- [x] esbuild-Generierung für die Overlay-Runtime (Roadmap Phase 7): über die Go-API `pkg/api` statt der CLI (`internal/overlay/internal/bundlegen`, Review 341-11c), erledigt 2026-10-29
