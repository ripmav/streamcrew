# Spezifikation: Twitch-Ereignisse

| | |
|---|---|
| **Status** | Entwurf |
| **Stand** | 2026-10-10 |
| **Bezug** | Roadmap Phase 4.4; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md), [Code-ADR-0015](../adr/code/0015-websocket-bibliothek.md); [`events.md`](events.md), [`template.md`](template.md); Plan Anhang A.2, A.6; [Plan 4.3](../superpowers/plans/2026-10-10-eventsub-websocket.md) |
| **Umsetzung** | Abgleich der EventSub-Subscriptions in `internal/twitch` (Soll-Zustand), Abbildung auf das Event-Modell in `internal/twitch` (4.3), Identifier als Familien in `internal/template` (4.4) |

## Zweck und Umfang

Legt fest, welche Ereignisse der Twitch-Adapter aus dem EventSub-
WebSocket (Phase 4.3) an das Event-Modell übergibt, welche
katalogspezifische Nutzlast sie tragen und welche Identifier die
Ereignis-Commands je Ereignis erhalten ([`events.md`](events.md), B6,
B7, B9, Plan A.6). Sie gilt für die plattformspezifischen Twitch-Typen
und die plattformneutralen Entsprechungen, die Twitch als Quelle
liefert.

Nicht Teil dieser Spezifikation: die numerischen Ereignis-IDs des
Originals für den Import (Roadmap Gate O, O.1), das Verhalten der
Ereignis-Commands selbst ([`commands.md`](commands.md), B20) und die
Twitch-Actions (Roadmap 4.4, A4 für Umfragen und Vorhersagen).

## Begriffe

| Begriff | Bedeutung |
|---|---|
| EventSub-Ereignis | eine Nachricht des EventSub-WebSockets, z. B. `channel.raid` (Twitch-Doku) |
| Zuordnung | die Regel, aus welchem EventSub-Ereignis welches Ereignis des Katalogs entsteht |
| Identifier | ein `$`-Name, den ein Command in seiner Nachricht verwenden kann; hier: die je Ereignis |

## Verhalten

### Zuordnung der Ereignisse

| ID | Regel | Quellen |
|---|---|---|
| B1 | Der Adapter abonniert die EventSub-Ereignisse der Tabelle „Zuordnung“ und übergibt daraus die Ereignisse des Katalogs; jedes plattformspezifische Ereignis wird zusammen mit seiner plattformneutralen Entsprechung veröffentlicht ([`events.md`](events.md), B2). Die Subscription-Manager des Adapters hält diesen Soll-Zustand (4.3). | Q1, Q2, Q3 |
| B2 | `channel.chat.message` ist eine Chatnachricht: sie trägt Emotes (Code, Anker, Badges der Emote), Fragmente (Plattformsprache), Badges als Rollen und Bits; sie läuft durch die allgemeine Chat-Reihenfolge von [`events.md`](events.md), B13. | Q1, Q2, Q3 |
| B3 | `channel.chat.notification` liefert je Bedingung (Sub, Resub, Geschenk, Massengeschenk) das Abo-Event; geschenkte Abos nach [`events.md`](events.md), B5 (Schwelle zwischen Einzel- und Sammelereignis). Der Beschenkte ist der Zielnutzer, der Schenker der Nutzer; bei einem Massengeschenk ist der Initiator der Nutzer. | Q1, Q3 |
| B4 | `channel.cheer` ist ein Cheer: er trägt die Bits, die Stufe und die Nachricht; er löst auch `bits.cheer` aus. | Q1, Q3 |
| B5 | `channel.raid` trägt die Zahl der Zuschauer des raidenden Kanals. | Q1, Q2, Q3 |
| B6 | `channel.follow` ist ein Follow ohne weitere Werte. | Q1, Q2, Q3 |
| B7 | Moderationsereignisse: `chat.moderate.ban`/`chat.moderate.unban`, `chat.moderate.timeout`/`chat.moderate.untimeout`, `chat.moderate.add`/`chat.moderate.remove`, `chat.moderate.vip`/`chat.moderate.unvip` tragen den moderierten Nutzer als Nutzer; Ban und Timeout tragen außerdem Dauer und Grund, soweit die Plattform sie nennt. | Q1, Q3 |
| B8 | `chat.user.whisper` ist eine Flüsternachricht: sie trägt den Absender als Nutzer und die Nachricht mit Emotes und Fragmenten. | Q1, Q3 |
| B9 | `twitch.shared_chat.opened`/`comment`/`deleted` tragen die Thread-Nachricht (Betreff bei „geöffnet“, Kommentar oder Löschung bei „Kommentar“); sie sind Twitch-spezifisch ohne neutrale Entsprechung. | Q1, Q3 |
| B10 | `channel.goal.progress`/`complete` tragen den aktuellen und den Zielbetrag mit Währung; `complete` ist zugleich die vollständige Erreichung. | Q1, Q3 |
| B11 | `channel.hype_train.progress`/`start`/`end` tragen Stufe, Fortschritt, Ziel, erreichte Belohnungsstufen und bei `end` das Ergebnis. | Q1, Q3 |
| B12 | `channel.ad.started` trägt Dauer und optional die Nachricht der Werbeeinschaltung. | Q1, Q3 |
| B13 | `channel.shoutout.received` trägt den shoutenden Kanal als Nutzer und dessen Zuschauerzahl. | Q1, Q3 |
| B14 | `channel.charity.progress`/`complete` tragen den aktuellen und den Zielbetrag der Charity-Sammlung mit Währung. | Q1, Q3 |
| B15 | Channel Points: `channel.channel_points_custom_reward.redemption.add` (Custom Power-Up) und `channel.channel_points_automatic_reward.redemption.add` (automatische Belohnung) lösen die Channel-Points-Commands aus (Roadmap 4.4); sie tragen die Belohnung und den einlösenden Nutzer. | Q1, Q3, Q4 |
| B16 | `channel.channel_update` ist kein Ereignis; der Adapter meldet damit nur den Stream-Zustand ([`events.md`](events.md), B11). | Q1, Q3 |

### Umfragen und Vorhersagen

| ID | Regel | Quellen |
|---|---|---|
| B17 | Es gibt keine Ereignisse und keine Ereignis-Commands für Umfragen und Vorhersagen. Stattdessen hat die Twitch-Action, die eine Umfrage oder Vorhersage erstellt, eine Unterliste, die nach dem Ende läuft, mit den Identifier `$pollchoice` bzw. `$predictionoutcome` (Roadmap 4.4, A4). Auch auf Twitch selbst gestartete Umfragen lösen deshalb keine Commands aus; die Action bleibt für den Import. | Q5 |

### Identifier je Ereignis

| ID | Regel | Quellen |
|---|---|---|
| B18 | Jedes Ereignis trägt die allgemeinen Werte nach [`events.md`](events.md), B7: `$streamingplatform`, `$raidviewercount`, `$message`, `$usersubplan`, `$usersubplanname`, `$isanonymous`, `$subsgiftedamount`; ein Wert, den das Ereignis nicht nennt, fehlt. **[Interop]** | Q2, Q6 |
| B19 | Je Ereignis gelten zusätzlich die Identifier der Tabelle „Identifier je Ereignis“; sie sind Werte des laufenden Ereignisses ([`template.md`](template.md)) und haben keinen Wert außerhalb eines solchen. **[Interop]** Die Namen der neuen Twitch-Identifikatoren sind Vorschläge des Cores und stehen unter dem Vorbehalt der rechtlichen Prüfung (Roadmap Gate O, O.1). | Q2, Q6 |

#### Tabelle: Identifier je Ereignis

| Ereignis | Nutzer | Zielnutzer | Identifier |
|---|---|---|---|
| `channel.raid` | raidender Kanal | geraideter Kanal | `$raidviewercount` (B5) |
| `channel.subscription` | Abonnent | Streamer | `$usersubplan`, `$usersubplanname`, `$message` |
| `channel.subscription.gift` | Schenker | Beschenkter | `$usersubplan`, `$usersubplanname`, `$subsgiftedamount` |
| `channel.subscription.gift.mass` | Initiator | — | `$usersubplan`, `$usersubplanname`, `$subsgiftedamount` |
| `bits.cheer` | Cheerer | Streamer | `$cheerbits` (B4); die Nachricht ist `$message` |
| `chat.user.moderate.timeout`/`.ban` | Moderierter | Streamer | `$moderationmessage` (B7) |
| `twitch.goal.progress`/`.complete` | — | Streamer | `$goalcurrentamount`, `$goaltargetamount`, `$goalcurrency` (B10) |
| `twitch.hype_train.start`/`.progress`/`.end` | — | Streamer | `$hypetrainlevel`, `$hypetrainprogress`, `$hypetraingoal`, `$hypetrainrewardlevel`, `$hypetrainoutcome` (B11) |
| `twitch.ad.started` | — | Streamer | `$adbreakduration`, `$adbreakmessage` (B12) |
| `twitch.shoutout.received` | shoutender Kanal | — | `$shoutoutviewers` (B13) |
| `twitch.charity.progress`/`.complete` | — | Streamer | `$donationcurrentamount`, `$donationtargetamount`, `$donationcurrency` (B14) |
| Channel-Points-Command (B15) | Einlösender | Streamer | `$channelpointsamount`, `$channelpointsreward` |
| Custom Power-Up-Command (B15) | Einlösender | Streamer | `$custompowerup`; die Nachricht des Einlösenden ist `$message` |

### Abweichungen und Verwerfungen

| ID | Regel | Quellen |
|---|---|---|
| B20 | Die Antwort-Felder der Chatnachricht (`reply_parent_message_*` in EventSub) werden verworfen: die kanonische Chatnachricht des Cores hat kein Antwort-Feld. Ein Abbilden wäre ein Spec-Nachtrag mit `reply`-Aktion (Backlog, Roadmap 4.4/5.x). | Q1, Q3 |
| B21 | Bei einem Massengeschenk nennt die Plattform die Beschenkten nur als Login; der Core bildet sie als Zielnutzer mit dem Login als Plattform-ID ab. | Q1 |
| B22 | Umfragen und Vorhersagen als Ereignisse sind verworfen (B17); die Katalog-Typen `twitch.poll.*` und `twitch.prediction.*` bleiben im Katalog, werden aber nicht abonniert und lösen nichts aus. | Q5 |

## Quellen

| ID | Quelle |
|---|---|
| Q1 | Twitch-Doku: EventSub, WebSocket und die Event-Typen `channel.*`, `chat.*` (Felder je Ereignis) |
| Q2 | [`events.md`](events.md), B2, B5, B6, B7, B9, B13 (neutraler Katalog, allgemeine Werte, Chat-Reihenfolge) |
| Q3 | Phase 4.3 (Abgleich der EventSub-Subscriptions, Abbildung in `internal/twitch`, [Plan 4.3](../superpowers/plans/2026-10-10-eventsub-websocket.md)) |
| Q4 | Roadmap 4.4 (Channel Points, Custom Power-Ups) |
| Q5 | [`events.md`](events.md), A4: keine Ereignisse für Umfragen und Vorhersagen, nur die Twitch-Action mit Unterliste; Entscheidung des Projektinhabers (2026-10-02) |
| Q6 | [`template.md`](template.md) (Bedeutung und Zuordnung der Identifier), Plan A.6 |

## Abweichungen vom Original

- **Keine Umfrage-/Vorhersage-Ereignisse** (B17): Das Original löst auf Umfragen und Vorhersagen aus; der Core ersetzt das durch die Twitch-Action mit Unterliste (A4, Q5).
- **Keine Antwort-Felder** (B20): Das Original trägt die Antwort einer Chatnachricht; der Core verwirft sie, bis ein Spec-Nachtrag das Antwort-Feld einführt (Backlog).
- **Neue Twitch-Identifikatoren** (B19): Wo das Original keine gleichwertigen Werte nennt, wählt der Core die Namen; sie stehen unter dem Vorbehalt von Gate O.

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-10-10 | Erstellt für Phase 4.4: Zuordnung (B1–B16), Umfragen/Vorhersagen (B17), Identifier je Ereignis (B18–B19), Verwerfungen (B20–B22). |
| 2026-10-10 | Identifier an die Implementierung angepasst (Task 4): ein `$moderationmessage` statt Dauer/Grund (die Plattform nennt beides in einem Feld), die Cheer-Nachricht ist das allgemeine `$message`, Shoutout trägt den shoutenden Kanal als Nutzer, kein `$channelpointsrewardname`/`$custompowerupname` (die Nutzlasten nennen nur die ID) und kein `$donationpercent` (die Plattform sendet keinen Prozentsatz). |
