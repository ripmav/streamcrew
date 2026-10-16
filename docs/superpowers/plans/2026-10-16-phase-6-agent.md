# Phase 6.1.5: Agent-Schnittstelle (nur Vertrag)

Status: **erledigt** (2026-10-16)

## Ziel

Roadmap 6.1.5: Die Agent-Schnittstelle als **Vertrag** (Proto) skizzieren,
damit `v1` sie später aufnehmen kann (ADR-0010, Plan §6.19). Die
Umsetzung ist P2 (Phase 9).

## Konzept (Plan §6.19)

Läuft der Core auf einem Server, fehlen ihm Fähigkeiten des Streaming-PCs:
Tastatur-/Maus-Actions, globale Hotkeys, lokaler Ton, lokale Programme. Ein
**Agent** (die Desktop-App oder `streamcrew agent`) schließt die Lücke: Er
registriert sich über die API mit seinen Capabilities, und der Core
delegiert host-gebundene Actions an ihn. Der Agent verbindet sich mit dem
Core und erhält Tasks per Server-Stream; das Ergebnis meldet er per RPC.

## Vertrag (`agent.proto`)

`AgentService`:
- `Register` — Agent registriert sich mit Name und Capabilities.
- `Unregister` — Agent meldet sich ab.
- `ListAgents` — listet die registrierten Agents.
- `Tasks` — Server-Stream: der Core liefert dem Agent host-gebundene Tasks
  (Filter nach Art).
- `SubmitTaskResult` — der Agent meldet das Ergebnis einer Task.

Typen: `AgentCapability` (Name + gewährt), `AgentInfo` (ID, Name,
Capabilities, online, registriert_am), `AgentTask` (ID, Agent, Art,
JSON-Payload, Deadline), Request/Response-Messages.

## Festlegungen

- Caps folgen dem Sicherheitsmodell (Plan §6.15): `host:process`,
  `host:input`, `host:audio`, `hotkeys` (Beispiele).
- Tasks sind opake JSON-Payloads (`payload`), damit der Vertrag stabil
  bleibt, wenn neue Task-Arten dazukommen.
- Ein Agent ist `online`, solange er verbunden ist; der Core merkt den
  Verlust per Heartbeat (Umsetzung Phase 9).
- Nur Vertrag: kein Server-Code in diesem PR.

## Verzicht

- Keine Umsetzung (Phase 9, P2).
- Keine Task-Confirmation/Timeout-Logik im Vertrag (kommt mit der
  Umsetzung).
