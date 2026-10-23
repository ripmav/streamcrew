# Plan: EventSub — die 12 toten Subscriptionen (Log-20261023, Gruppe 2)

**Status:** PR #322 (Implementierung + Tests grün, 2026-10-23)
**Stand:** 2026-10-23, Branch `fix/eventsub-current-types` (von `main` bei `f86920e`)
**Scope:** nur die Event-Typen, die Twitch mit HTTP 400
`invalid subscription type and version` ablehnt (Log
`20261023_084328.log`, 12 Typen × 2 Versuche). Gruppe 1
(`moderator_user_id`/`required_one_of`) bleibt offen — Mod-Status fehlt.

## 1. Befund

Der `desired`-Tabelle in `internal/twitch/subscriptions.go` sind Typen
und Versionen aus einer älteren EventSub-Generation abgeglichen.
Gegenstand der Abgleichsquelle ist die aktuelle Doku
(dev.twitch.tv/docs/eventsub/eventsub-subscription-types/, abgerufen
2026-10-23) plus die Live-Ablehnung der API:

| im Code (alt) | aktuell bei Twitch | Änderung |
|---|---|---|
| `channel.chat.message` v2 | v1 | nur Version; Payload v1 = bereits das `chatMessage`-Struct |
| `channel.chat.message_delete` v2 | v1 | nur Version; Struct passt |
| `channel.chat.notification` v2 | v1 | nur Version; Struct passt |
| `channel.hype_train.start/progress/end` v1 | `.begin/progress/end` v2 | Umbenennung + Version + Payload: `level` statt `current_level`, keine `reward_interval`, kein `end_reason`; neu `total` |
| `channel.ad.started` v1 | `channel.ad_break.begin` v1 | Umbenennung + Payload: `duration_seconds` statt `length`, kein `message` |
| `channel.goal.start` / `channel.goal.complete` v1 | `channel.goal.begin` / `channel.goal.end` v1 | Umbenennung; Payload gleich (`currency` fehlt im neuen Payload → Feld bleibt leer) |
| `channel.charity.progress` / `channel.charity.complete` v1 | `channel.charity_campaign.progress` / `.stop` v1 | Umbenennung + Payload: `current_amount`/`target_amount` sind jetzt Objekte `{value, decimal_places, currency}` |
| `channel.shoutout.received` v1 | `channel.shoutout.receive` v1 | **ausgeschieden**: benötigt jetzt zwingend `moderator_user_id` im Condition → fällt in Gruppe 1 (kein Mod). Spec wird entfernt, Handler + Domain-Constants bleiben (können dann einfach nie feuern; Template-Families bleiben stabil). |

Folgen heute: 30 fehlgeschlagene POSTs + 30 WARNs pro Run, und
**keine** Chat-Events über EventSub (nur IRC) — das ist der echte Bug,
der Rest ist totes Gewebe.

## 2. Aufgaben

- [x] `internal/twitch/subscriptions.go`: `desired`-Tabelle auf aktuelle
      Typen/Versionen; shoutout-Spec entfernen.
- [x] `internal/twitch/map.go`: `dispatch`-Cases umbenennen;
      Payload-Structs anpassen:
      - `hypeTrain`: `level`, `progress`, `goal`, `total`;
        `reward_interval`/`end_reason` weg (Reward-Level-Berechnung
        und Outcome entfallen).
      - `adStarted` → `adBreak`: `duration_seconds` (int; Doku-Beispiel
        zeigt ein String — bei Parse-Fehler im Test auffallen).
      - `charity`: Money-Objekt mit `value` + `decimal_places`
        → Ganzzahl-Betrag (Wert / 10^dp, `shortcut:`-Kommentar).
      - `goal`: `currency`-Feld bleibt, kommt im v1-Goal-Payload nicht
        mehr (bleibt leer).
      - shoutout-Case + `shoutout`-Struct entfernen,
        `TwitchShoutoutReceive` bleibt als Constant mit Hinweis.
- [x] Tests: `subscriptions_test.go`, `map_test.go`,
      `contract_test.go`, `chaos_test.go`, `integration_test.go`,
      `platform_multi_test.go` auf neue Typen/Versionen/Payloads.
- [x] Docs: `docs/spec/twitch-events.md` + `docs/spec/events.md`
      aktualisieren (Typen/Versionen); historische Plan-Docs bleiben.
- [x] `go build ./...` + `go test ./...` grün.
- [x] PR (Base `main`), Commit mit Assisted-by-Footer.

## 3. Bewusst nicht gemacht

- Gruppe 1 (mod-Gating für `channel.follow`, `channel.moderate`,
  `channel.raid`, ab jetzt auch `shoutout.receive`): offen,
  Setup-/Design-Frage (moderated Kanäle / `moderator_user_id`-Condition
  im `Spec`).
- Neue Event-Typen aus der Doku (`channel.bits.use`,
  `channel.chat.clear`, Shield Mode, …): nicht gewünscht, nicht
  angefasst.
- Das Fehlen des Channel-Namens in den EventSub-Logzeilen
  (separater Log-Behinder-Fix, nicht hier).
