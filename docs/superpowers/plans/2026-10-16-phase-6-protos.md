# Phase 6.1.2: Protos v1alpha1 (alle Services)

Status: **erledigt** (2026-10-16)

## Ziel

Roadmap 6.1.2: Die Protobuf-Dateien von `v1alpha1` in
`api/proto/streamcrew/v1alpha1/` schreiben (Plan §6.14, ADR-0010). `buf`
lintet und generiert den Go-Code nach `api/gen`.

## Services (Roadmap-Liste)

| Service | Zweck | Datei |
|---|---|---|
| `SystemService` | Status (Versions-Handshake), Profile, Herunterfahren | `system.proto` (6.1.1) |
| `AuthService` | Konten je Plattform, Login-Flow starten/abbrechen, Abmelden | `auth.proto` |
| `StreamService` | `Subscribe` (Server-Streaming: Chat, Events, Command-Instanzen, Plattformstatus, Logs, Prompts), Prompts beantworten | `stream.proto` |
| `ChatService` | senden, löschen, leeren, Verlauf | `chat.proto` |
| `CommandService` | CRUD, ausführen, Queue (pausieren, abbrechen, wiederholen), Verlauf, Typkatalog, Import/Export | `command.proto` |
| `UserService` | suchen, Details, bearbeiten, verknüpfen, zusammenführen, Ranglisten | `user.proto` |
| `CounterService` | Verwaltung (CRUD, Reset) | `counter.proto` |
| `SettingsService` | Einstellungs-Sektionen lesen/schreiben | `settings.proto` |
| `BackupService` | Backups erstellen, auflisten, wiederherstellen | `backup.proto` |

## Design-Entscheidungen

- **Ereignisstrom** (`StreamService.Subscribe`): Server-Streaming. Ein
  `Event`-Wrapper trägt `type` (String), `platform`, `channel`,
  `occurred_at` und ein `oneof payload` (Chat, Feed, CommandInstance,
  Platform, Log, Prompt). Filter per `EventFilter` (Kinds, Plattformen,
  Channels); Wiederaufsetzen ab den letzten N Ereignissen (`since`).
  Kein bidirektionales Streaming (ADR-0010).
- **Prompts**: ein `Prompt`-Payload im Stream (Art information/confirmation/
  input, Text, ID); beantwortet per `AnswerPrompt`-RPC.
- **Commands als Code**: Der API-Vertrag trägt Command-Dokumente als
  opakes Dokument (String, YAML/JSON) + ein typisiertes `CommandSummary`
  (ID, Name, Art, aktiv, Trigger, Channel). Der `TypeCatalog` liefert die
  Struktur der Action-/Requirement-Typen für generische Editoren (Plan
  §7.1). So wird das polymorphe Command-Schema nicht 1:1 in Proto
  gespiegelt.
- **IDs/Zeiten**: IDs als `string` (UUID), Zeitpunkte als
  `google.protobuf.Timestamp`.
- **Scopes**: Authentifizierung (6.2.2) wird per Interceptor geprüft; die
  Services tragen hier noch keine Scope-Annotationen.

## Ausführung

1. Plan schreiben (dieses File).
2. Die acht Service-Protos schreiben (system.proto existiert).
3. `buf generate` → Go-Code nach `api/gen`.
4. `buf lint` + Build + Tests.
5. Roadmap/Plan aktualisieren.

## Verzicht

- `EconomyService`, `QuoteService`, `OverlayService`,
  `IntegrationService`, `AgentService` (Plan §6.14) sind nicht Teil des
  MVP-Listen-Teils von 6.1.2 — sie kommen mit ihren Phasen (Overlay 7,
  Agent 9, Economy/Integration später). `AgentService` wird in 6.1.5 als
  Vertrag skizziert.
- REST-Transcoding (Developer-API, Phase 10) wird nicht vorgezogen.
