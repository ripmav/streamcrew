# Test-Matrix: eventsub-reference-fix

EventSub-Implementierung gegen die aktuelle [EventSub
Reference](https://dev.twitch.tv/docs/eventsub/eventsub-reference/)
korrigieren (Plan
[2026-10-22](../superpowers/plans/2026-10-22-eventsub-reference-fix.md),
Findings F1–F12). Drei gestapelte PRs; jede Sektion dieser Matrix gehört
zu einer PR.

## Sektion A — Event-Typen, Versionen, Conditions (F1–F7)

Der Subscription-Manager abonniert die aktuellen Event-Typen-Namen und
Versionen der EventSub Reference; die Conditions tragen die zusätzlichen
Felder `moderator_user_id` (follow v2, moderate v2), `to_broadcaster_user_id`
(raid) und `user_id` (Chat-Events, „The User ID to read chat as“) des
Token-Besitzers; `channel.goal.*` wird nur für den Home-Kanal abonniert
(das Condition muss den Token-Besitzer nennen).

### Erwartetes Verhalten

| Fall | Erwartet |
|---|---|
| Soll-Tabelle Home-Kanal | 25 Subscriptions: aktuelle Namen/Versionen (u. a. `channel.hype_train.begin` v2, `channel.ad_break.begin` v1, `channel.goal.begin` v1, `channel.charity_campaign.progress` v1, `channel.shoutout.receive` v1, `channel.chat.*` v1) |
| Condition follow v2 / moderate v2 | `broadcaster_user_id` + `moderator_user_id` (Token-Besitzer; Home: Broadcaster, Zusatzkanal: Bot) |
| Condition raid | `to_broadcaster_user_id` = Kanal (kein `broadcaster_user_id`) |
| Condition chat.* v1 | `broadcaster_user_id` + `user_id` (Token-Besitzer) |
| Zusatzkanal | keine `stream.*`, keine `channel.goal.*` (19 Channel-Specs + Whisper) |
| Überflüssige/alte Subscriptions | werden revokiert **vor** dem Erstellen der fehlenden (keine 409-Kollision beim Re-Crate) |
| 400 beim Create (Limit, ungültige Kombination) | wird mit Warnung übersprungen, keine Fehler-Kette |

### Abgedeckte Tests

| Fall | Test | Datei |
|---|---|---|
| Vollständige Home-Tabelle: Namen, Versionen, Conditions | `TestReconcileCreatesTheMissingSubscriptions` | `internal/twitch/subscriptions_test.go` |
| Zusatzkanal: 19 Channel-Specs × 2 + Whisper, keine stream/goal, Moderator=Bot | `TestReconcileAdditionalChannels` | `internal/twitch/subscriptions_test.go` |
| Überflüssiges wird revokiert, altes `channel.hype_train.start` v1 als Alt-Name, Soll-Zustand bleibt | `TestReconcileRevokesTheSurplus` | `internal/twitch/subscriptions_test.go` |
| 400-Skip | `TestReconcileSkipsThe400` | `internal/twitch/subscriptions_test.go` |
| Reconcile zu Start und Intervall | `TestRunReconcilesAtTheStartAndOnTheInterval` (synctest) | `internal/twitch/subscriptions_test.go` |

## Sektion B — Event-Payloads (F8)

Die Mapper bilden die dokumentierten Nutzlasten der EventSub Reference
(Stand 2026-10-22) auf das Event-Modell ab: strukturierte Chatnachricht
mit Fragmenten (Emotes sind Fragmente vom Typ „emote“; Anker entfallen),
Chatter unter `chatter_user_*`, raid `from_broadcaster_*`/`viewers`,
cheer `user_*`/`bits`, moderate v2 mit Aktionen-Objekten,
`message_delete` `target_user_*`/`message_id`, whisper `from_*`/
`whisper.text`, shared chat `session_id`, hype train v2 ohne
Reward-Intervall/Endgrund, `duration_seconds` (ad), `viewer_count`
(shoutout), goal ohne Währung, charity als Betragsobjekte, Redemptions
mit Belohnungs-Objekt.

### Erwartetes Verhalten

| Fall | Erwartet |
|---|---|
| Strukturierte Chatnachricht | Emote-Codes in Text-Reihenfolge aus den Fragmenten; Katalog-Fallback über die Emote-ID, wenn das Fragment keinen Text trägt |
| Massengeschenk | Gesamtzahl und Anonymität; keine Beschenkten in der Nutzlast |
| Hype train v2 | Stufe, Fortschritt, Ziel; `RewardLevel`/`Outcome` bleiben leer |
| Ad break | Dauer in Sekunden; keine Nachricht |
| Goal | aktueller und Zielwert; keine Währung |
| Charity | Betrag als Objekt mit Währung und Ganzzahlstellen |
| IRC-Pfad | gleicher Nutzlastaufbau (Fragmente aus dem IRC-Emotes-Tag, Text des Fragments als Code) |

### Abgedeckte Tests

| Fall | Test | Datei |
|---|---|---|
| Alle dokumentierten Nutzlasten → Event-Modell (24 Fixturen) | `TestEventContract` | `internal/twitch/contract_test.go`, `internal/twitch/testdata/` |
| Emote-Fragmente: Code, Reihenfolge, Katalog-Fallback | `TestMapChatMessage`, `TestMapChatMessageResolvesEmotes`, `TestMapEmotesInTextOrder` | `internal/twitch/map_test.go` |
| Sub/Resub/Geschenk/Massengeschenk aus `channel.chat.notification` | `TestMapNotification` | `internal/twitch/map_test.go` |
| moderate v2 inkl. Grund | `TestMapModerate`, `TestMapModerationMessage` | `internal/twitch/map_test.go` |
| Hype train, Ad, Shoutout, Goal, Charity | `TestMapHypeTrain`, `TestMapAdStart`, `TestMapShoutout`, `TestMapGoal`, `TestMapCharity` | `internal/twitch/map_test.go` |
| Redemptions mit Belohnungs-Objekt | `TestMapChannelPointsRedemption`, `TestMapCustomPowerUpRedemption` | `internal/twitch/map_test.go` |
| IRC-Nachricht/-Flüsterung auf den neuen Nutzlasten | IRC-Tests | `internal/twitch/irc_test.go` |
| Event-Fluss über den Mock (Chat, Cheer, Redemption) | Integrationstest | `internal/twitch/integration_test.go` |

## Sektion C — Session-Lebenszyklus (F9–F12)

*folgt mit PR 3 (`fix/eventsub-session-lifecycle`)*
