# Plan: EventSub-Implementierung gegen die EventSub-Reference korrigieren

**Status:** offen
**Stand:** 2026-10-22, `main` bei `f86920e` (PR #316)
**Begründung:** Check der EventSub-Implementierung
(`internal/twitch/ws.go`, `subscriptions.go`, `map.go`, `message.go`,
`internal/helix/eventsub.go`) gegen die aktuelle Twitch-EventSub-Doku.
Die Implementierung (Plan 2026-10-10-eventsub-websocket.md, ADR-0015)
stammt von einer älteren Doku-Generation: Event-Namen, Versionen,
Condition-Felder und Payload-Felder haben sich bei Twitch geändert, und
der Session-Lebenszyklus (Reconnect, Revocation, Resubscription nach
Drop) weicht vom dokumentierten Verhalten ab.

**Referenz-Doku (Alleinige Wahrheit für diesen Plan):**

- [EventSub Reference](https://dev.twitch.tv/docs/eventsub/eventsub-reference/)
  — Request-/Response-Felder,
  [Conditions](https://dev.twitch.tv/docs/eventsub/eventsub-reference/#conditions),
  [Event-Payloads](https://dev.twitch.tv/docs/eventsub/eventsub-reference/#events),
  [Transport](https://dev.twitch.tv/docs/eventsub/eventsub-reference/#transport)
- [Subscription Types](https://dev.twitch.tv/docs/eventsub/eventsub-subscription-types/)
  — aktuelle Event-Typen-Namen + Versionen + Authorization-Scopes
- [Handling WebSocket Events](https://dev.twitch.tv/docs/eventsub/handling-websocket-events/)
  — Envelope-Typen, Welcome/Keepalive/Notification/Reconnect/Revocation/
  Close-Codes,
  [10-s-Subscription-Fenster](https://dev.twitch.tv/docs/eventsub/handling-websocket-events#welcome-message),
  [Resubscription nach Drop](https://dev.twitch.tv/docs/eventsub/handling-websocket-events#which-events-is-my-websocket-subscribed-to),
  [Subscription Limits](https://dev.twitch.tv/docs/eventsub/handling-websocket-events#subscription-limits)
- [WebSocket Reference](https://dev.twitch.tv/docs/eventsub/websocket-reference/)
  — Felder der Envelope-Typen
- [Managing Subscriptions](https://dev.twitch.tv/docs/eventsub/manage-subscriptions/)
  — Create/Get/Delete-API

**Mock-Realität (twitch-cli v1.1.24, CI):** Der offizielle Mock deckt die
neuen Event-Namen ab (z. B. `channel.hype_train.begin`,
`channel.ad_break.begin`), kennt aber **keine** Chat-Topics
(`channel.chat.message` etc. → 400, Manager skippt) und akzeptiert
`channel.hype_train.*` nur mit Version **1** (Produktion: **2** → 400,
Manager skippt). Conditions validiert der Mock nicht. Duplicated-Check
des Mocks: `(clientID, type, version)` — Revoke-vor-Create ist Pflicht.

---

## Findings

| # | Problem | Doku |
|---|---------|------|
| F1 | Alte Event-Namen: `channel.hype_train.start` → `channel.hype_train.begin`; `channel.ad.started` → `channel.ad_break.begin`; `channel.shoutout.received` → `channel.shoutout.receive`; `channel.goal.start` → `channel.goal.begin`; `channel.goal.complete` → `channel.goal.end`; `channel.charity.progress` → `channel.charity_campaign.progress`; `channel.charity.complete` → `channel.charity_campaign.stop` (hype-train-`end`/`progress` heißen so weiter) | [Subscription Types](https://dev.twitch.tv/docs/eventsub/eventsub-subscription-types/) |
| F2 | Falsche Versionen: `channel.hype_train.*` v1 → **v2**; `channel.chat.message` v2 → **v1**; `channel.chat.message_delete` v2 → **v1**; `channel.chat.notification` v2 → **v1** | [Subscription Types](https://dev.twitch.tv/docs/eventsub/eventsub-subscription-types/) |
| F3 | `channel.follow` v2 verlangt im Condition zusätzlich `moderator_user_id` (Broadcastertoken: Broadcaster-ID, Bot-Token: Bot-ID); Scope `moderator:read:followers` (bereits in `internal/auth/twitch.go`) | [Channel Follow Condition](https://dev.twitch.tv/docs/eventsub/eventsub-reference/#channel-follow-condition), [Subscription Types: Channel Follow](https://dev.twitch.tv/docs/eventsub/eventsub-subscription-types/#channelfollow) |
| F4 | `channel.raid` v1: Condition verlangt `from_broadcaster_user_id` **oder** `to_broadcaster_user_id` (beide = 400) — nicht `broadcaster_user_id` | [Channel Raid Condition](https://dev.twitch.tv/docs/eventsub/eventsub-reference/#channel-raid-condition) |
| F5 | `channel.chat.message`/`message_delete`/`notification` v1: Condition verlangt zusätzlich `user_id` („The User ID to read chat as“ = Token-Besitzer) | [Conditions](https://dev.twitch.tv/docs/eventsub/eventsub-reference/#conditions) |
| F6 | `channel.moderate` v2: Condition verlangt `broadcaster_user_id` **und** `moderator_user_id` | [Channel Moderate V2 Condition](https://dev.twitch.tv/docs/eventsub/eventsub-reference/#channel-moderate-v2-condition) |
| F7 | `channel.goal.*`: Condition `broadcaster_user_id` „must match the user_id in the OAuth access token“ → nur Home-Kanal (Token-Besitzer), für Zusatzkanäle nicht erstellbar → `HomeOnly` | [Goals Condition](https://dev.twitch.tv/docs/eventsub/eventsub-reference/#goals-condition) |
| F8 | Payload-Felder aller Event-Typen sind alt (Details in Plan B): `chatter_user_*`/`message_id`/`message{text,fragments}` statt `user_*`/`id`/`message:string`; `notice_type`-Struktur statt `type` bei Notifications; `level` statt `current_level` bei Hype Train; `from_broadcaster_*`/`viewers` bei Raid; `duration_seconds` bei Ad-Break; `session_id` bei Shared Chat; `from_user_*`/`whisper{text}` bei Whisper; `reward`-Objekt bei Channel Points; `type`/`ended_at`/`is_achieved` bei Goals; `current_amount{value,decimal_places,currency}`-Objekte bei Charity; `action` statt `moderation_action` bei Moderate | [Events](https://dev.twitch.tv/docs/eventsub/eventsub-reference/#events) |
| F9 | **Session-Lebenszyklus:** `session_reconnect` — Doku: alte Connection offen halten, bis die **neue** Connection die Welcome-Message gebracht hat (kein Event-Verlust), Reconnect-URL **unverändert** verwenden (keinen `access_token` anhängen!), 30 s Zeitfenster. Aktuell: alte Connection sofort zu, `access_token` an die Reconnect-URL angehängt | [Reconnect message](https://dev.twitch.tv/docs/eventsub/handling-websocket-events#reconnect-message) |
| F10 | **Resubscription nach Drop:** „If you disconnect from a WebSocket session, all subscriptions associated with that session are automatically disabled. … you’ll need to recreate the subscriptions.“ Der Manager re-pointet/neuerstellt nach einem Drop auf der Base-URL nie (specKey matcht die deaktivierten Einträge) → nach dem ersten Drop kommen in Produktion keine Events mehr. Nach `session_reconnect` tragen die Subscriptions mit der Session über (kein Re-point nötig) | [Which events is my WebSocket subscribed to?](https://dev.twitch.tv/docs/eventsub/handling-websocket-events#which-events-is-my-websocket-subscribed-to) |
| F11 | **Revocation:** Doku kennt drei Status — `authorization_revoked` (→ neues Token), `user_removed` und `version_removed` (→ Subscription ersetzen, **keine** Session-Neustart). Aktuell behandelt `ws.go` jede Revocation als Token-Revocation und reißt die ganze Session ab (reißt per F10 alle Subscriptions der Session mit) | [Revocation message](https://dev.twitch.tv/docs/eventsub/handling-websocket-events#revocation-message) |
| F12 | **10-s-Fenster:** Nach jeder Welcome-Message muss innerhalb von `keepalive_timeout_seconds` (Default 10 s) mindestens eine Subscription erstellt werden, sonst Close 4003 („Connection unused“). Das Re-pointing (F10) muss also **sofort** nach jeder neuen Session laufen, nicht erst am 10-min-Intervall | [Welcome message](https://dev.twitch.tv/docs/eventsub/handling-websocket-events#welcome-message), [Close codes](https://dev.twitch.tv/docs/eventsub/handling-websocket-events#close-message) |

Nicht betroffen (geprüft, ok): Envelope-Typen + `session_welcome`-Felder
(`keepalive_timeout_seconds`, Fallback `keepalive_interval_seconds` ist
Mock-artefakt und bleibt), `stream.online/offline` v1, `channel.cheer`
v1 (Condition ok, Payload fix in B), `user.whisper.message` v1
(Condition ok), `channel.shared_chat.*` v1 (Condition ok),
`channel.channel_points_*_reward_redemption.add` v1 (Condition ok),
Twitch-Session-Id-Header, „keine ausgehenden Nachrichten“ (Close 4001),
Ping/Pong (Library).

---

## Plan A — Event-Typen, Versionen, Conditions (PR 1)

**Branch:** `fix/eventsub-subscriptions` (neuer Session-Branch auf `main`).
**Doku-Quellen:** F1–F7.

### Schritte

- [x] `internal/helix/eventsub.go`: `SubscriptionCondition` um
  `ModeratorUserID` (`moderator_user_id`) und `ToBroadcasterUserID`
  (`to_broadcaster_user_id`) erweitern.
- [x] `internal/twitch/subscriptions.go`: desired-Tabelle auf die
  aktuellen Namen/Versionen (F1, F2); `Spec` um Flaggs:
  `RaidTo` (raid per `to_broadcaster_user_id`), `ChatAsUser`
  (chat-Events: `user_id` = Token-Besitzer), `Moderator`
  (follow + moderate v2: `moderator_user_id` = Token-Besitzer);
  `channel.goal.*` → `HomeOnly: true` (F7).
- [x] `ManagerOptions` um `Moderator string` (ID des Token-Besitzers;
  Home: Streamer-ID, Zusatzkanal: Bot-ID); `platform.go` setzt es pro
  Session. Condition-Aufbau in `Reconcile`:
  - follow: `{BroadcasterUserID: ch, ModeratorUserID: mod}`
  - moderate v2: `{BroadcasterUserID: ch, ModeratorUserID: mod}`
  - raid: `{ToBroadcasterUserID: ch}`
  - chat.message / message_delete / notification: `{BroadcasterUserID: ch, UserID: mod}`
  - goal: `{BroadcasterUserID: ch}` (nur Home)
- [x] Reconcile-Reihenfolge: **erst** überflüssige/stale Subscriptions
  reviden (409-im-Mock-Vorbereitung, gehört zu C, aber die Reihenfolge
  gehört hierher), **dann** fehlende erstellen.
- [x] `internal/twitch/map.go`: dispatch-Case-Namen der umbenannten
  Events mitziehen (F1) — Payloads bleiben Plan B.
- [x] Tests: `subscriptions_test.go` (neue Namen/Versionen/Conditions,
  `Moderator`-Option, goal nur Home), `map_test.go`/`contract_test.go`/
  `integration_test.go` nur soweit nötig für grünen Build
  (Fixtures selbst in B).
- [x] Doku: `docs/spec/twitch-events.md` (B10–B14 + Event-Typ-Tabelle)
  und ADR-0015-Abschnitt „desired table“ aktualisieren.
- [x] Test-Matrix `docs/tests/I-eventsub-reference-fix.md` (Sektion A).

### Verifikation

- [x] `go build ./... && go test ./internal/twitch/... ./internal/helix/...`

---

## Plan B — Event-Payloads (PR 2, auf PR 1)

**Branch:** `fix/eventsub-payloads` (Stack).
**Doku-Quellen:** F8, alle unter
[Events](https://dev.twitch.tv/docs/eventsub/eventsub-reference/#events).

### Schritte

- [x] `internal/twitch/map.go`: Payload-Strukturen auf die aktuellen
  Felder umstellen:
  - `channel.chat.message`: `chatter_user_id/name/login`, `message_id`,
    `message{text, fragments[]}` (Emotes aus `fragments[type=emote]`
    mit `emote{id, emote_set_id}`), `badges[]{set_id}`, `color`;
    `emotes`/`emoji`-Arrays und `profile_image_url` fallen weg.
  - `channel.chat.message_delete`: `target_user_id/name/login`,
    `message_id`.
  - `channel.chat.notification`: `notice_type` (`sub`/`resub`/
    `sub_gift`/`community_sub_gift`/…) statt `type`; Geber aus
    `chatter_user_*` bzw. `resub.gifter_*`/`sub_gift`-Feldern;
    `sub{sub_tier}`/`resub{cumulative_months, streak_months,
    sub_tier}`/`sub_gift{recipient_*, cumulative_total}`/
    `community_sub_gift{total}`; `system_message`.
  - `channel.cheer`: `user_id/login/name`, `is_anonymous`, `bits`,
    `message` (String bleibt).
  - `channel.follow`: `user_id/login/name` (nicht `user_display_name`).
  - `channel.raid`: `from_broadcaster_user_id/login/name`,
    `to_broadcaster_*`, `viewers`.
  - `channel.moderate` v2: `action` statt `moderation_action`; Ziel aus
    `ban{user_id,login,name,reason}`/`timeout{user_id,login,name}`.
  - `channel.shared_chat.*`: `session_id` statt `chat_session_id`;
    `title` fällt weg.
  - `channel.hype_train.*` v2: `level` statt `current_level`,
    `progress`, `goal`, `total`, `type`; `reward_interval` und
    `end_reason` fallen weg (Ende: `ended_at`).
  - `channel.ad_break.begin`: `duration_seconds`, `started_at`,
    `is_automatic`; `length`/`message` fallen weg.
  - `channel.shoutout.receive`: `from_broadcaster_user_id/login/name`,
    `viewer_count`.
  - `channel.goal.begin/progress/end`: `current_amount`,
    `target_amount`, `type`; `currency` fällt weg; `end` trägt
    `is_achieved`, `ended_at`.
  - `channel.charity_campaign.progress/stop`: `current_amount`/
    `target_amount` als Objekte `{value, decimal_places, currency}`,
    `charity_name`.
  - `user.whisper.message`: `from_user_id/name/login`, `whisper{text}`,
    `whisper_id`.
  - `channel.channel_points_automatic_reward_redemption.add`:
    `user_login`/`user_name`, `reward{type, cost}`, `user_input`,
    `id`.
  - `channel.channel_points_custom_reward_redemption.add`:
    `user_login`/`user_name`, `reward{id, cost, title}`, `user_input`,
    `status`, `id`.
- [x] `internal/twitch/testdata/*.json`: alle Fixtures auf die
  dokumentierten Felder neu bauen (anonymisiert, aus den Doku-Beispielen
  abgeleitet); umbenannte Dateien (`channel.ad_break.begin.json`,
  `channel.hype_train.begin.json`, `channel.goal.begin.json`,
  `channel.goal.end.json`, `channel.charity_campaign.progress.json`,
  `channel.charity_campaign.stop.json`, `channel.shoutout.receive.json`).
- [x] Tests: `map_test.go`, `contract_test.go`, `integration_test.go`
  (FireEvent-Payloads) + `chaos_test.go` (fireChat) auf die neuen
  Formate.
- [x] `eventtype`-Model prüfen: `Goal`/`Charity`-Currency und `AdBreak`
  ohne Message — Felder, die wegfallen, leeren (kein Schema-Change in
  `core/`, es sei denn ein Feld wird zwingend leer).
- [x] Doku: `docs/spec/twitch-events.md` B10–B14.
- [x] Test-Matrix (Sektion B).

### Verifikation

- [x] `go test ./internal/twitch/...` (Contract-Tests = Feld-für-Feld
  gegen die Fixtures)

---

## Plan C — Session-Lebenszyklus (PR 3, auf PR 2)

**Branch:** `fix/eventsub-session-lifecycle` (Stack).
**Doku-Quellen:** F9–F12,
[Handling WebSocket Events](https://dev.twitch.tv/docs/eventsub/handling-websocket-events/).

### Schritte

- [x] `internal/twitch/ws.go` — `session_reconnect`: alte Connection
  **nicht** vor der Welcome-Message der neuen Connection schließen
  (Doku: „You should not close the old connection until you receive a
  Welcome message on the new connection“); die `next`-Struktur trägt
  die alte `*websocket.Conn` mit in `Run` über, `Run` dialt die neue
  Session und schließt dann die alte (normal closure). Reconnect-URL
  **unverändert** verwenden: kein `access_token` anhängen (Doku: „Use
  the reconnect URL as is; do not modify it“); relative
  Mock-URLs weiter absolut gegen die Base auflösen.
- [x] `internal/twitch/ws.go` — `revocation`: `subscription.status`
  parsen; `authorization_revoked` → wie heute `OnRevoked` + Session
  neu (frisches Token); `user_removed`/`version_removed` → **neuer**
  Handler-Hook (z. B. `OnSubscriptionRevoked`), Session bleibt, der
  Manager revidet/erzeugt die Subscription neu.
- [x] `internal/twitch/message.go`: Revocation-Payload
  (`subscription{status, type, …}`) ergänzen.
- [x] `internal/twitch/subscriptions.go` — Re-pointing: `Reconcile`
  behandelt jede owned-Subscription mit Status != `enabled` (der
  Drop hat sie deaktiviert) als stale → revoke + neu erstellen
  (F10, F12). Status statt `transport.session_id`: die ID bleibt bei
  einem `session_reconnect` mit den Subscriptions unverändert (die
  tragen dort über und müssen nicht neu erstellt werden), im Mock wie
  in Produktion. `platform.go`: der Session-Callback meldet jede neue
  Session-ID an den Manager → sofortiger Reconcile (10-s-Fenster).
- [x] `internal/twitch/platform.go`: `Mapper.WithSubscriptionRevoked`
  an den Manager anbinden; falsche Kommentare korrigieren
  („subscriptions ride along with the session“ gilt nur für
  `session_reconnect`, nicht für Drops).
- [x] Tests: `ws_test.go` (Reconnect hält die alte Connection bis zur
  neuen Welcome; keine `access_token` in der Reconnect-URL;
  Revocation-Varianten), `subscriptions_test.go` (stale Re-pointing,
  sofortiger Reconcile auf Session-Change), `chaos_test.go`: nach dem
  Forced Drop müssen die Subscriptions in der neuen Session
  neu stehen (Assertion nach `freshSession`).
- [x] Doku: ADR-0015 (Lebenszyklus-Abschnitt) +
  `docs/adr/0014` bleibt unangetastet.
- [x] Test-Matrix (Sektion C).

### Verifikation

- [x] `go build ./... && go test ./internal/twitch/...`
- [x] Chaos-Test gegen den twitch-cli-Mock (CI; lokal ohne Binary
  skipped)

---

## Reihenfolge

| # | Plan | PR | Branch |
|---|---|---|---|
| 1 | A — Typen/Versionen/Conditions | PR 1 | `fix/eventsub-subscriptions` (auf `main`) |
| 2 | B — Payloads | PR 2 (Stack) | `fix/eventsub-payloads` (auf PR 1) |
| 3 | C — Session-Lebenszyklus | PR 3 (Stack) | `fix/eventsub-session-lifecycle` (auf PR 2) |

Stack-Regeln (AGENTS.md): erst existierenden Stack prüfen, PR mit
`--base` auf den Kopf des Vorfahren und per
`POST /repos/{owner}/{repo}/stacks/{n}/add` im Stack-Objekt
registrieren.

## Offene Punkte / bewusst nicht verifiziert

- `channel.hype_train.*` v2 vs. twitch-cli-Mock v1: Mock-skip (400) in
  den Integrationstests ist dann erwartet; in Produktion gilt v2.
- Ob Twitch die `user_id`-Conditions bei chat-Events (F5) hart
  durchsetzt oder optional behandelt — Doku sagt „yes“; wir senden sie
  immer (Token-Besitzer).
- Felder für die neuen notice_types (raid, announcement, …) in
  `channel.chat.notification` werden gemappt, wenn ein Handler sie
  braucht; übrige notice_types werden verworfen (warn-log), wie bisher
  unbekannte Event-Typen.
