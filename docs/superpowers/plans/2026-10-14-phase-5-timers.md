# Plan: 5.4 Timer-Commands und -Gruppen

**Status:** erledigt 2026-10-16
**Stand:** 2026-10-16

## Goal
Timer-Commands und -Gruppen laufen nach ihrem Intervall: globale
Timer-Einstellungen (Intervall, Mindestanzahl Chatnachrichten, nur live,
zufällige oder feste Reihenfolge) für Timer-Commands ohne eigenes
Gruppenintervall; Gruppen mit `TimerInterval` laufen unabhängig nach ihrem
eigenen Intervall in ihrem Ziel-Channel (B31).

## Design decisions

- **Settings-Sektion `timers`** (`internal/settings`): `interval`
  (Duration, 0 = aus), `minChatMessages` (int, 0 = keine Prüfung),
  `liveOnly` (bool), `order` (`fixed` | `random`). Defaults: `interval` 0,
  `minChatMessages` 0, `liveOnly` false, `order` `fixed`. Validierung:
  `interval` ≥ 0, `minChatMessages` ≥ 0, `order` bekannt.
- **Mindestanzahl Chatnachrichten:** ein Timer feuert auf einer Plattform nur,
  wenn es in den letzten `interval` mindestens `minChatMessages`
  Chatnachrichten (ohne Bot) gab. Fenster = das globale Intervall, gleitet mit
  jedem Tick.
- **Nur live:** ein Timer feuert auf einer Plattform nur, wenn ihr Stream laut
  Event-Service live ist.
- **Reihenfolge:** `fixed` = die gespeicherte Reihenfolge (nach ID), `random` =
  eine neue Zufallsreihenfolge je Tick.
- **Gruppen (B31):** eine Gruppe mit `TimerInterval > 0` feuert ihre
  Timer-Commands nach ihrem Intervall in `TimerChannel` (leer = Home),
  unabhängig von globalem Intervall und Mindestnachrichten. `liveOnly` und
  `order` gelten weiterhin. Ohne Intervall ist die Gruppe nur Ordnung.
- **Ablauf:** der Timer ist ein Runnable (`internal/timer`), das die App mit
  dem Supervisor laufen lässt. Ein 1-s-Poll prüft, was fällig ist: global
  (Intervall + live + Mindestnachrichten, je verbundener Plattform, Home-
  Channel) und je Gruppe mit Intervall (live, Ziel-Channel). Das Starten
  passiert über `engine.Start`; der Channel-Scope (B9) und „aktiv“ prüft die
  Engine bzw. der Timer (nur `Enabled`-Commands).
- **Zusammenhänge:** Chatnachrichten zählt der Chat-Service
  (`MessagesSince(p, since)`); der Live-Zustand kommt aus dem Event-Service
  (`IsLive(p)`); Commands und Gruppen liest der Timer aus dem Command-Service
  (`Commands`, `Groups`).

## Files
- `internal/settings/settings.go`: Sektion `timers` (`Timers`, `TimerOrder`,
  Defaults, Validate, Registry-Registrierung).
- `internal/chat/history.go` (+ Test): `MessagesSince(p, since) int`.
- `internal/eventservice/stream.go` (+ Test): `IsLive(p) bool`.
- `internal/timer/timer.go` (+ Test): das Runnable, Ports und der Poll-Loop.
- `internal/app/app.go`: Timer verdrahten (Ports, Supervisor-Add).
- `docs/roadmap.md` (5.4), `docs/spec/commands.md` (B21/B31 „umgesetzt"-Notiz
  optional), dieses Planfile.

## Tests
| Test | Level |
|------|-------|
| `Timers.Validate`: Intervall/Mindestnachrichten/Order | unit (settings) |
| `MessagesSince`: Fenster, Bot ausgeschlossen | unit (chat) |
| `IsLive`: live, grace, offline, keine Sitzung | unit (eventservice) |
| Timer-Loop: global feuert nach Intervall; liveOnly; minChatMessages; fixed/random Reihenfolge; Gruppen feuern nach eigenem Intervall im Ziel-Channel, ohne Mindestnachrichten | unit (timer) |
