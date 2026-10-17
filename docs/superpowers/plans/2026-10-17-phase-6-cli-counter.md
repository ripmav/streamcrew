# Phase 6.3 (1/4a): `counter`-Befehle über die API

Status: **in Arbeit** (2026-10-17)
Branch/PR: `feat/cli-counter`, auf `feat/cli-diag` gestapelt (Stack #258).

## Ziel (Plan §7.2, Roadmap 6.3.1)

`counter list|get|set|add|reset` als CLI-Befehle gegen einen laufenden
Core über die API, mit `--output json` (MVP-Ausschnitt von 6.3.1).

## Entscheidungen

- `counterCmd` mit List, Get, Set, Add, Reset; die RPCs existieren
  (CounterService), es gibt nur den CLI-Zugang.
- Jeder Befehl embeddet das bestehende `output`-Struct (`-o text|json`,
  Plan §7.2 „`--output json` für Skripte“).
- Text-Ausgabe wie die anderen Listenbefehle (tabwriter); JSON = die
  Protobuf-Antwort-Struktur (`writeJSON`).
- `add` mit leerem Betrag fügt einen Schritt hinzu (RPC-Vertrag).

## Tests

- `TestCounterKong`: Kong-Struktur + `--output json`
- `TestCounterCommands`: list/get/set/add/reset gegen einen in-process
  Core mit Mock-Plattform (wie `startMockCore`), Text- und JSON-Ausgabe.

## Reihenfolge

1. internal/cli/counter.go (Befehle + counterClient-Helper)
2. Root-Registrierung
3. Tests, Roadmap/Changelog, Checks, PR
