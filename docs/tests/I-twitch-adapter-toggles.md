# Test-Matrix: twitch-adapter-toggles

EventSub- und IRC-Adapter per Startkonfiguration an- und abschaltbar
(Plan 2026-10-22): `twitch-eventsub`/`twitch-irc` (Flag, Env,
Konfigurationsdatei) schalten die Adapter; der Heimkanal läuft über
EventSub, solange es an ist, sonst über die IRC-Session (die ihn dann
zusätzlich joinnt); die zusätzlichen Kanäle über IRC, solange es an
ist, sonst EventSub. Beide aus ⇒ der Core startet nicht. Das Settings-
Feld `channels.chatAdapter` und der CLI-Befehl `profile channels chat`
sind entfernt (Sections-Version 2, Migration). Spec
`docs/spec/twitch-multi-channel.md`, B15 (revidiert), B16.

## Erwartetes Verhalten

| Fall | Erwartet |
|---|---|
| Beide Adapter an (Default: eventsub on, irc off) | Heimkanal über EventSub, zusätzliche Kanäle über EventSub (heutiges Default) |
| eventsub on, irc on | Heimkanal über EventSub, zusätzliche Kanäle über IRC (keine doppelten Events) |
| eventsub off, irc on | keine EventSub-Session; IRC-Session joinnt Heimkanal + zusätzliche; Heim-Events ohne Channel-Stempel; Stream-State aus Helix-Poll |
| eventsub off, irc off | `Resolve` meldet Usage-Fehler; der Core startet nicht |
| unbekannter Schalter-Wert (z. B. `maybe`) | `Resolve` meldet Usage-Fehler |
| eventsub off, irc on, Bot nicht bereit | kein Join, Transition-Warnung „channels are not operated over irc", Poll wiederholt ruhig, kein Log-Spam |
| `channels`-Dokument v1 mit `chatAdapter` | Migration streicht das Feld, die Liste bleibt (Version 2) |
| `profile channels list` | listet nur die Logins (keine Adapter-Zeile) |

## Abgedeckte Tests

| Fall | Test | Datei |
|---|---|---|
| Beide aus ⇒ Fehler „at least one chat adapter must be on" | `TestResolve/both_chat_adapters_off` | `internal/config/config_test.go` |
| Unbekannter Wert ⇒ Fehler `--twitch-eventsub` | `TestResolve/unknown_chat_adapter_switch` | `internal/config/config_test.go` |
| Einzelne Schalter-Combos sind valide | `TestResolve/eventsub_off,_irc_on`, `TestResolve/irc_off,_eventsub_on` | `internal/config/config_test.go` |
| IRC-only: kein EventSub-Dial, IRC joinnt Heimkanal, Heim-Events ohne Stempel, Stream-Poll liefert den Zustand | `TestPlatformIRCOnly` (Fake-IRC-Server, Fake-Helix) | `internal/twitch/platform_multi_test.go` |
| IRC-only, Bot nicht bereit: nichts verbindet, `Run` endet sauber | `TestPlatformIRCOnlyBotNotReady` | `internal/twitch/platform_multi_test.go` |
| irc on, eventsub on: Heimkanal bleibt EventSub, zusätzlicher Kanal ohne EventSub-Session | `TestPlatformIRCMode` | `internal/twitch/platform_multi_test.go` |
| irc off (Default): zusätzliche Kanäle über EventSub (Token, Mod-Check) | `TestPlatformAdditionalChannel`, `TestPlatformModMissing`, `TestPlatformSkipsTheHomeEntry` | `internal/twitch/platform_multi_test.go` |
| Chat-Senden der zusätzlichen Kanäle über Helix (irc off) | `TestChatSendToAdditionalChannel`, `TestChatSendUnknownChannel`, `TestChatSendPerChannelBuckets` | `internal/twitch/ops_test.go` |
| `channels` v1-Dokument mit `chatAdapter` wird migriert (Feld gestrichen) | `TestChannelsVersion1` | `internal/settings/settings_test.go` |
| Gespeichertes `channels`-Dokument ist Version 2 ohne `chatAdapter` | `TestSaveAndLoad` (JSONEq) | `internal/settings/settings_test.go` |
| `profile channels` ohne `chat`-Subkommando; `list` ohne Adapter-Zeile | `TestChannelsParse`, `TestChannelsAddRemoveList` | `internal/cli/channels_test.go` |
