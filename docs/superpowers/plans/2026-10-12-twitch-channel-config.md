# Plan: Roadmap 4.7 — Pro-Channel-Konfiguration

**Status:** offen
**Stand:** 2026-10-12, `main` bei `a99d7af`; 4.6 steht in Stack #206
([203](https://github.com/ripmav/streamcrew/pull/203),
[205](https://github.com/ripmav/streamcrew/pull/205); Umsetzungstasks 2–7
stehen noch aus), ADR-0024 in Stack #201
**Scope:** nur 4.7 — die pro-Channel-Konfiguration von Commands und deren
Umgebung. 4.7 setzt auf die Channel-Dimension von 4.6 auf (Spec
[`twitch-multi-channel.md`](../../spec/twitch-multi-channel.md), B5–B7); ohne
4.6 ist er kein Feature (ein Channel, keine Scope-Frage).

## 1. Was 4.7 liefert (Roadmap)

- [ ] Channel-Scope für Commands: ohne Scope (Standard: alle betriebenen
  Channels) oder ein bestimmter Channel (M)
- [ ] Engine: Scope-Check in der Trigger-Matching; die Timer der
  Command-Gruppen zielen auf einen Channel (M)
- [ ] Command-Format und -Verwaltung: `channel` im YAML/JSON-Format, in der
  Liste und im Import/Export; Validierung gegen die konfigurierten Channels (S)
- [ ] Explizit profiltweit (dokumentiert): Counter, Quotes, Währungen,
  Cooldowns, Nutzer-Statistiken (S)
- [ ] Tests, E2E auf zwei Channels, Doku (S)

## 2. Befunde (Stand nach 4.6)

1. **Die Channel-Dimension steht (4.6):** Events und Läufe tragen den
   Channel (`engine.Params`), die Port-Aufrufe wirken auf den Channel des
   Laufs (Spec 4.6, B5–B7). Der Scope-Check kommt **nur in das Matching**
   von Events auf Commands — die Ports und das Routing bleiben, wie 4.6 sie
   definiert.
2. **Das Datenmodell ist profiltweit** (im Code verifiziert): `commands`
   (Name UNIQUE je Profil, `0013_command_name_key`), `counters`, `quotes`,
   `cooldowns`, `currencies`/`user_balances` und die Settings-Sektionen
   haben keine Channel-Spalte; 4.6 B8 legt das Verhalten („identisch für
   alle betriebenen Channels") fest.
3. **Command-Struktur:** `Header` (Name, Art, aktiv, Gruppe, Trigger,
   Event, Error-Policy) plus Requirements und Actions; Speicher-Record als
   JSON-Dokumente. Die Timer sind das Intervall der Command-Gruppen
   (B31).
4. **Kein Original-Bezug:** Mix It Up kennt nur einen Channel (Spec 4.6,
   A1); es gibt keine Original-Spezifikation, von der abzuweichen wäre. Die
   Scope-Regeln sind eine streamcrew-Entscheidung.

## 3. Design (Festlegungen)

1. **Scope-Modell:** Commands tragen einen optionalen Channel (Login):
   leer = **global** (alle betriebenen Channels; Default, zu 100 %
   kompatibel mit bestehenden Profilen), gesetzt = nur in diesem Channel.
   **Matching-Regel:** ein Event aus Channel X löst ein Command aus, wenn
   der Scope leer oder X ist.
2. **Name-Unique:** die Name-Unique bleibt profiltweit (kein
   (Channel, Name)-Paar) — eindeutig pro Profil ist für Import, API und
   UI simpler und verwirrt weniger.
3. **Timer:** das Gruppen-Intervall (B31) feuert in einem Ziel-Channel;
   leer = eigener Channel (wie 4.6 B6 für Läufe ohne Channel).
4. **Validierung:** der Scope muss der eigene Channel oder ein Channel der
   Settings-Sektion `channels` sein; unbekannte Logins lehnt das Speichern
   ab. Ist der Channel nicht mehr betrieben (4.6 B3, z. B. Mod-Status
   entzogen), feuern seine scoped Commands dort nicht — ohne Fehler und
   ohne Konfigurationsänderung.
5. **Profiltweit (v1, ausdrücklich dokumentiert):** Counter, Quotes,
   Währungs-Bestände, Cooldowns und Cooldown-Gruppen, Nutzer-Statistiken
   und die Settings-Sektionen. Eine pro-Channel-Wirtschaft (Bits und
   Währungen je Channel getrennt) ist ein Datenmodell-Wechsel
   (Unique-Schlüssel je Channel) und gehört nicht in 4.7.
6. **UX:** `channel` im Command-Dokument (YAML/JSON,
   [`commands-as-code.md`](../../spec/commands-as-code.md)) und als
   `--channel`-Flag in der Verwaltung; die Command-Liste zeigt den Scope;
   Import/Export trägt ihn verlustfrei.
7. **Kein neues ADR:** Funktionsausbau der 4.6-Channel-Dimension; die
   Scope-Regeln kommen in die Spezifikationen (Task 1).

## 4. Tasks

### Task 1 — Spec-Delta (S)
`commands.md` (Scope-Feld, Matching-Regel, Name-Unique),
`command-engine.md` (Matching mit Scope, Timer-Ziel),
`commands-as-code.md` (`channel` im Format, Schemata),
`twitch-multi-channel.md` (B8: „seit 4.7 mit Scope"; B7/Offene-Punkte
anpassen).

### Task 2 — Domäne und Speicher (M)
`Header.Channel` (leer = global), Record-Feld plus `schema_version`,
Migration (neue Spalte `channel`, Default leer), Codec, Validierung
gegen die konfigurierten Channels (eigener Channel + `channels`).

### Task 3 — Engine: Matching und Timer (M)
Trigger-Matching mit Scope-Check (Channel aus `Params`, 4.6 B6);
Gruppen-Timer mit Ziel-Channel; Tests: scoped feuert nur im eigenen
Channel, unscoped in allen (Regression), Timer im Ziel-Channel.

### Task 4 — Format, CLI, TUI, API (S)
`channel` im YAML/JSON-Format und den Schemata, `--channel` in der
Command-Verwaltung, Spalte in der Liste, Import/Export-Tests.

### Task 5 — E2E und Doku (S)
Echt: zwei betriebene Channels; ein scoped Command feuert nur in seinem
Channel, ein unscoped in beiden; Timer im Ziel-Channel. README
(„Pro-Channel-Konfiguration"), Roadmap/Plan-Status.

## 5. Offene Punkte

1. **Pro-Channel-Wirtschaft:** getrennte Bits/Währungs-Bestände, Counter
   und Quotes je Channel — Datenmodell-Wechsel, eigenes Feature.
2. **Pro-Channel-Settings:** Chat-Settings je Channel — erst prüfen, ob
   es den Bedarf gibt (E2E, Task 5).
3. **Gruppen und Scope:** eine Gruppe mit Commands verschiedener
   Scope — v1 erlaubt es; die Timer-Zuordnung bleibt je Gruppe (Doku).
4. **Pro-Channel-Unique für Namen** — wird v1 nicht gebaut (D3).

## 6. Aufwand

Schätzung: **2–3 PW** (Task 2/3 sind die Blöcke; Tasks 1/4/5 klein).
