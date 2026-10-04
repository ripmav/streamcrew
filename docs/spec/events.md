# Spezifikation: Ereigniskatalog

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-10-04 |
| **Bezug** | Roadmap Phase 2.2 (Event-Modell), 3.6, 4.4, 5.1; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md), [Code-ADR-0011](../adr/code/0011-event-bus.md); Plan §6.7, Anhang A.1, A.2; [`commands.md`](commands.md), [`command-engine.md`](command-engine.md) |
| **Umsetzung** | Katalog mit Namen, plattformneutraler Entsprechung und Häufigkeit je Typ in `internal/domain/eventtype`, dazu die Werte eines Ereignisses (`eventtype.Details`) und die Tabelle der Nutzlast als Daten (`eventtype.Shape`, B9); Umschlag und Bus in `internal/event`; die Namen der Ereigniswerte (B7) als Konstanten in `internal/template`; der Eingang der Adapter als Port `connector.Receiver` und die Deduplizierung (B22) als `connector.Dedup`; die Mock-Plattform in `internal/connector/mock`; der Event-Service mit den Auslöseregeln (B2 bis B6, B8 bis B14, B20 bis B29) in `internal/eventservice`, die Stream-Sitzungen in `internal/domain/stream` und im Store (Migration 0015), die Settings-Sektion `events` in `internal/settings`. Die Twitch-Typen bekommen ihre Quelle mit Phase 4 |

## Zweck und Umfang

Legt die stabilen Namen der Ereignistypen fest, die Ereignis-Commands auslösen können, die Regeln, wann ein Ereignis ausgelöst oder unterdrückt wird, und was die plattformneutralen Ereignisse tragen (B9). Die Nutzlast der plattformspezifischen Typen wird mit der Phase festgelegt, in der ihre Quelle entsteht, für Twitch in Phase 4 mit der Spezifikation `twitch-events.md`.

Nicht Teil dieser Spezifikation: die Zuordnung zu den numerischen Ereignis-IDs des Originals für den Import. Sie wartet auf die rechtliche Einschätzung (Roadmap Gate O, O.1).

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Ereignistyp | stabiler Name eines Ereignisses, z. B. `channel.follow`; Namensregel nach Code-ADR-0011 |
| plattformneutrales Ereignis | Ereignis, das auf mehreren Plattformen vorkommt, etwa ein Follow |
| plattformspezifisches Ereignis | Ereignis einer Plattform, etwa ein Twitch-Hype-Train, oder die plattformspezifische Fassung eines neutralen Ereignisses |
| Stream-Sitzung | Zeitraum von einem Stream-Start bis zum nächsten Stream-Start desselben Kanals; eine kurze Unterbrechung innerhalb der Karenzzeit beendet sie nicht (B8). Jede Plattform hat ihre eigene (B11). |
| Adapter | Teil des Cores, der eine Plattform anbindet, etwa die Mock-Plattform oder Twitch; er übergibt, was er empfängt, dem Event-Service |

## Verhalten

### Allgemeine Regeln

| ID | Regel | Quellen |
|---|---|---|
| B1 | Jedes Ereignis wird im Umschlag nach Code-ADR-0011 veröffentlicht; sein Typ ist einer der Namen dieses Katalogs. Veröffentlichte Namen werden nie umbenannt. | Code-ADR-0011 |
| B2 | Hat ein plattformspezifisches Ereignis eine plattformneutrale Entsprechung, werden beide veröffentlicht: erst das spezifische, dann das neutrale mit der Plattform als Quelle. Ereignis-Commands können auf das eine, das andere oder beide reagieren. | Q8 (Generische Ereignisse „auf mehreren Plattformen“), Q10 |
| B3 | Folgende Ereignisse werden je Stream-Sitzung (B8) höchstens einmal ausgelöst: Stream-Start, Stream-Ende; je Nutzer: Follow, Abo, Resub; je raidendem Kanal: Raid. | Q8, A1 |
| B4 | Ereignisse ohne diese Einschränkung (Chatnachrichten, Geschenk-Abos, Bits, Spenden …) werden jedes Mal ausgelöst. | Q8 |
| B5 | Geschenkte Abos: Für jeden Beschenkten gibt es ein Ereignis `…subscription.gift`, für die Sammelaktion ein Ereignis `…subscription.mass_gift`. Eine Einstellung legt eine Schwelle fest, Standard 2, mindestens 2: Ab dieser Zahl von Geschenken einer Sammelaktion wird nur das Sammelereignis ausgelöst, darunter nur die Einzelereignisse; beide Arten zugleich gibt es nie. | Q8, A3 |
| B6 | Ereignisse, die ein Nutzer auslöst, tragen den Nutzer; Ereignisse mit einem Ziel (etwa Beschenkter, Geraidete) tragen zusätzlich den Zielnutzer. | Q8 („User“ und „Target User“ Identifier) |
| B7 | Ereignisspezifische Werte, die Commands verwenden können: Plattform, Zahl der Raid-Zuschauer, Abo-Nachricht, Abo-Stufe und ihr Name, anonym ja/nein, Zahl geschenkter Abos. **[Interop]** `$streamingplatform`, `$raidviewercount`, `$message`, `$usersubplan`, `$usersubplanname`, `$isanonymous`, `$subsgiftedamount` | Q8 |
| B8 | Geht der Stream offline, endet die Stream-Sitzung erst, wenn er nicht innerhalb der Karenzzeit wieder online geht: Standard 10 Minuten, einstellbar von 0 bis 60 Minuten, 0 heißt ohne Karenzzeit. Erst dann wird `channel.stream.stop` ausgelöst, samt der plattformspezifischen Fassung. Geht er innerhalb der Karenzzeit wieder online, läuft die Sitzung weiter: kein neues `channel.stream.start`, und die Sperren nach B3 und die Begrüßungen ([`command-engine.md`](command-engine.md), B41) gelten weiter. Laufende Begrüßungen bricht der Core trotzdem ab, sobald der Stream offline geht. | A1 |
| B9 | Jedes plattformneutrale Ereignis trägt die Plattform, auf der es geschah, und je nach Typ einen Nutzer, einen Zielnutzer und Werte nach B7, wie die Tabelle „Nutzlast der plattformneutralen Ereignisse“ sagt. Ein Wert, den die Plattform nicht nennt, fehlt; kein Ersatzwert steht für ihn. | Q8, QP |
| B10 | Die Settings-Sektion `events` hält die Schwelle der Sammelgeschenke (B5, `massGiftThreshold`, ganze Zahl von 2 bis 1 000, Standard 2) und die Karenzzeit (B8, `streamGracePeriod`, von 0 bis 60 Minuten, Standard 10 Minuten). Eine geänderte Schwelle gilt ab der nächsten Sammelaktion, eine geänderte Karenzzeit ab dem nächsten Offline-Gehen. | A1, A3 |
| B11 | Jede Plattform hat ihre eigene Stream-Sitzung. Ihr Adapter meldet, wenn der Stream online oder offline geht, und nach jedem Verbinden, ob er gerade live ist; daraus entstehen `channel.stream.start` und `channel.stream.stop` nach B3 und B8. Vor dem ersten Stream-Start einer Plattform gilt eine Sitzung ohne Start. Die Sperren nach B3 und die für `chat.user.join` und `chat.user.entrance` gelten je Plattform und Sitzung, die für `chat.user.new` und `chat.user.first_message` je Nutzer über alle Plattformen. | A1, QP |
| B12 | Jedes veröffentlichte Ereignis löst den Ereignis-Command seines Typs aus ([`commands.md`](commands.md), B20), mit der Plattform, dem Nutzer, dem Zielnutzer und den Werten des Ereignisses; Ereignisse mit einer Chatnachricht (`chat.message`, `chat.user.entrance`, `chat.user.first_message`) geben sie als auslösende Nachricht mit. Ein unterdrücktes Ereignis (B3, B5, B22) wird weder veröffentlicht, noch löst es etwas aus. Die Ereignis-Commands werden in der Reihenfolge der Ereignisse ausgelöst; der Eingang wartet nicht auf ihre Entscheidungen ([`command-engine.md`](command-engine.md), B16). | Q8, QP |
| B13 | Eine Chatnachricht löst in dieser Reihenfolge aus: `chat.user.new`, wenn streamcrew den Nutzer zum ersten Mal im Chat erkennt; `chat.user.join`, wenn er in der Sitzung zum ersten Mal erkannt wird; `chat.message`; den Chat-Command, dessen Trigger passt ([`commands.md`](commands.md), B16); `chat.user.first_message`, wenn es seine erste Nachricht überhaupt ist; zuletzt die Begrüßung, also `chat.user.entrance` und den Entrance-Command des Nutzers ([`command-engine.md`](command-engine.md), B41). Nachrichten des Bot-Kontos lösen weder Ereignisse noch Commands aus. | Q9, QP |
| B14 | Meldet die Plattform, dass ein Nutzer dem Chat beitritt, löst das `chat.user.new` und `chat.user.join` aus wie eine Nachricht (B13), aber keine Begrüßung. | Q9 |

### Nutzlast der plattformneutralen Ereignisse

Nutzer und Zielnutzer nach B6, Werte nach B7 (B9). „–“ heißt: Das Ereignis hat keinen. Die Anwendungsereignisse `app.started` und `app.stopping` tragen weder Plattform noch Nutzer.

| Typ | Nutzer | Zielnutzer | Werte |
|---|---|---|---|
| `channel.stream.start`, `channel.stream.stop` | – | – | – |
| `channel.follow` | der Follower | – | – |
| `channel.raid` | der raidende Kanal | – | Zahl der Raid-Zuschauer |
| `channel.subscribe` | der Abonnent | – | Abo-Stufe und ihr Name |
| `channel.resubscribe` | der Abonnent | – | Abo-Stufe und ihr Name, Abo-Nachricht |
| `channel.subscription.gift` | der Schenkende, – bei einem anonymen Geschenk | der Beschenkte | Abo-Stufe und ihr Name, anonym ja/nein |
| `channel.subscription.mass_gift` | der Schenkende, – bei einem anonymen Geschenk | – | Abo-Stufe und ihr Name, anonym ja/nein, Zahl geschenkter Abos |
| `chat.message`, `chat.whisper` | der Absender | – | die Nachricht |
| `chat.message.delete` | der Absender der gelöschten Nachricht | – | die Nachricht, wenn die Plattform sie nennt |
| `chat.user.join`, `chat.user.leave`, `chat.user.new` | der Nutzer | – | – |
| `chat.user.entrance`, `chat.user.first_message` | der Nutzer | – | die Nachricht |
| `chat.user.timeout`, `chat.user.ban` | der gesperrte Nutzer | – | – |

Abo-Stufe und ihr Name stehen so da, wie die Plattform sie nennt; für Twitch legt sie `twitch-events.md` fest (Phase 4). „Anonym“ ist `true` oder `false` wie die anderen Ja-Nein-Werte der Templates ([`template.md`](template.md)).

### Katalog

**Anwendung**

| Typ | Bedeutung | Quellen |
|---|---|---|
| `app.started` | Der Core ist gestartet (einmal je Start). | Q8 (Application Launch) |
| `app.stopping` | Der Core fährt herunter. | Q8 (Application Exit) |

**Kanal, plattformneutral**

| Typ | Bedeutung | Quellen |
|---|---|---|
| `channel.stream.start` | Der Stream ist online gegangen. | Q8 |
| `channel.stream.stop` | Der Stream ist offline gegangen. | Q8 |
| `channel.follow` | Ein Nutzer folgt dem Kanal. | Q8 |
| `channel.raid` | Ein anderer Kanal raidet diesen. | Q8 |
| `channel.subscribe` | Ein Nutzer hat ein Abo abgeschlossen (YouTube: Mitgliedschaft). | Q8 |
| `channel.resubscribe` | Ein Nutzer hat sein Abo verlängert und teilt das. | Q8 |
| `channel.subscription.gift` | Ein Nutzer hat ein Abo geschenkt bekommen; je Beschenktem. | Q8 |
| `channel.subscription.mass_gift` | Ein Nutzer hat mehrere Abos auf einmal verschenkt. | Q8 |

**Chat, plattformneutral**

| Typ | Bedeutung | Quellen |
|---|---|---|
| `chat.message` | Eine Chatnachricht ist eingegangen (jede). | Q9 |
| `chat.message.delete` | Eine Chatnachricht wurde gelöscht. | Q9 |
| `chat.whisper` | Eine Flüsternachricht an das Streamer-Konto ist eingegangen. | Q9 |
| `chat.user.join` | Ein Nutzer wurde in dieser Stream-Sitzung zum ersten Mal im Chat erkannt. | Q9, A1 |
| `chat.user.leave` | Ein Nutzer hat den Chat verlassen. | Q9 |
| `chat.user.entrance` | Ein Nutzer hat in dieser Stream-Sitzung seine erste Nachricht geschrieben, während der Stream live ist ([`command-engine.md`](command-engine.md), B41). | Q9, A1 |
| `chat.user.new` | Ein Nutzer, den streamcrew noch nie gesehen hat, wurde erkannt. | Q9 |
| `chat.user.first_message` | Ein Nutzer hat zum allerersten Mal eine Nachricht geschrieben; nie wieder für ihn. | Q9 |
| `chat.user.timeout` | Ein Nutzer wurde auf Zeit gesperrt. | Q9 |
| `chat.user.ban` | Ein Nutzer wurde gebannt. | Q9 |

**Twitch** (Phase 4)

| Typ | Bedeutung | Quellen |
|---|---|---|
| `twitch.stream.start`, `twitch.stream.stop` | Stream online bzw. offline | Q10 |
| `twitch.channel.update` | Titel oder Kategorie geändert | Q10 |
| `twitch.channel.follow` | Follow | Q10 |
| `twitch.channel.raid` | eingehender Raid | Q10 |
| `twitch.raid.outgoing` | eigener Raid auf einen anderen Kanal abgeschlossen | Q10 |
| `twitch.channel.subscribe`, `twitch.channel.resubscribe` | Abo, Resub | Q10 |
| `twitch.subscription.gift`, `twitch.subscription.mass_gift` | Geschenk-Abo je Beschenktem, Sammelaktion | Q10 |
| `twitch.chat.watch_streak` | Nutzer teilt seine Watch Streak | Q10 |
| `twitch.chat.modiversary` | Moderator teilt sein Moderations-Jubiläum | Q10 |
| `twitch.chat.highlighted_message` | hervorgehobene Nachricht (Kanalpunkte-Belohnung) | Q10 |
| `twitch.chat.user_intro` | Nutzer stellt sich vor (User Intro) | Q10 |
| `twitch.power_up.message_effect`, `twitch.power_up.gigantified_emote`, `twitch.power_up.celebration` | Power-ups | Q10 |
| `twitch.custom_power_up.redeem` | eigenes Power-up eingelöst | Q10 |
| `twitch.channel_points.redeem` | Kanalpunkte-Belohnung eingelöst | Q10 |
| `twitch.bits.cheer` | Bits gecheert | Q10 |
| `twitch.ad.upcoming`, `twitch.ad.start`, `twitch.ad.end` | Werbepause steht bevor, beginnt, endet | Q10 |
| `twitch.charity.donation` | Spende an eine Charity-Kampagne | Q10 |
| `twitch.hype_train.start`, `twitch.hype_train.progress`, `twitch.hype_train.level_up`, `twitch.hype_train.end` | Hype Train | Q10 |
| `twitch.moderation.user_warn` | ein Moderator hat einen Nutzer verwarnt | Q10 |
| `twitch.shoutout.receive` | ein anderer Kanal hat diesem einen Shoutout gegeben | Q10 |
| `twitch.suspicious_user.message`, `twitch.suspicious_user.update` | Nachricht bzw. Statuswechsel eines verdächtigen Nutzers | Q10 |
| `twitch.shield_mode.start`, `twitch.shield_mode.end` | Shield Mode an, aus | Q10 |
| `twitch.unban_request.create`, `twitch.unban_request.resolve` | Entbannungsanfrage gestellt, entschieden | Q10 |
| `twitch.goal.start`, `twitch.goal.progress`, `twitch.goal.end` | Creator Goal | Q10 |
| `twitch.poll.start`, `twitch.poll.progress`, `twitch.poll.end` | Umfrage beginnt, Stimmen ändern sich, Umfrage endet | QP (Anhang A.2), A4 |
| `twitch.prediction.start`, `twitch.prediction.progress`, `twitch.prediction.lock`, `twitch.prediction.end` | Vorhersage beginnt, Einsätze ändern sich, Einsätze gesperrt, Vorhersage aufgelöst oder abgebrochen | QP (Anhang A.2), A4 |

**Weitere Plattformen und Dienste** (Phasen 7, 9, 10): YouTube, Kick, Velora, VPZone, OBS Studio und Spendendienste bekommen ihre Typen nach demselben Schema mit ihrer Phase (Plan Anhang A.1), etwa `youtube.super_chat`, `obs.scene.change`, `donation.receive`.

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B20 | Ein Nutzer folgt, entfolgt und folgt in derselben Stream-Sitzung erneut | nur ein `channel.follow` | B3 |
| B21 | Der Core startet neu, während der Stream läuft | die Stream-Sitzung gilt als fortgesetzt; kein zweites `channel.stream.start`, sofern der Stream nicht länger als die Karenzzeit offline war (B8) | A1 |
| B22 | Dieselbe Plattformnachricht kommt zweimal an (Wiederholung nach Verbindungsabbruch) | wird vom Adapter anhand der Nachrichten-ID verworfen und nur einmal veröffentlicht | Code-ADR-0011 |
| B23 | Twitch Hype Chat | kein Ereignistyp; in der Doku als nicht funktionsfähig vermerkt | Q10 |
| B24 | Der Stream bricht für 2 Minuten ab und geht wieder online (Karenzzeit 10 Minuten) | kein `channel.stream.stop`, kein zweites `channel.stream.start`; ein zweiter Follow desselben Nutzers löst weiter nichts aus, und wer schon begrüßt wurde, wird nicht erneut begrüßt | B3, B8 |
| B25 | Der Stream bleibt 11 Minuten offline (Karenzzeit 10 Minuten) | `channel.stream.stop` 10 Minuten nach dem Offline-Gehen; der nächste Start beginnt eine neue Sitzung | B8 |
| B26 | Eine Sammelaktion mit einem Abo, Schwelle 2 | nur das Einzelereignis | B5 |
| B27 | Der Core lief nicht, als die Karenzzeit ablief: Er war gestoppt, oder er findet den Stream beim Start offline und hat ihn zuletzt vor mehr als der Karenzzeit live gesehen | Die Sitzung endet ohne `channel.stream.stop`, mit einem Eintrag im Log; der nächste Start beginnt eine neue Sitzung. Ein verspätetes Stream-Ende löste Commands lange nach dem Stream aus. | B8, B21 |
| B28 | Der Bot schreibt `!hug` in den Chat | kein Command, keine Ereignisse | B13 |
| B29 | Ein anonymes Geschenk-Abo | `channel.subscription.gift` ohne Nutzer, mit dem Beschenkten als Zielnutzer und „anonym“ `true` | B9 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | einmal je Programmlauf, nie zurückgesetzt, für Stream-Start/-Ende, Follow, Raid, Abo, Resub, Join, Leave, Entrance und weitere; nach einem Neustart mitten im Stream gibt es gar keinen Stream-Start (Q11) | einmal je Stream-Sitzung, auch über einen Neustart des Cores hinweg, mit Karenzzeit für kurze Unterbrechungen (B3, B8) | Der Core läuft im Server-Modus und als Daemon über viele Streams; „je Programmlauf“ unterdrückt ab dem zweiten Stream alles (ADR-0003). Die Sitzung muss dafür gespeichert werden. Die Karenzzeit hält einen kurzen Verbindungsabbruch aus der Sitzung heraus; Entscheidung des Projektinhabers (2026-10-02) |
| A2 | Ereignisse als Aufzählung mit numerischen Werten | stabile Namen nach Namensregel | Code-ADR-0011; lesbar in API, Logs und Commands als Code |
| A3 | Schwelle „Mass Gifted Subs Filter Amount“ mit „mehr als“, Standard 1; 0 schaltet den Filter ab, und beide Arten laufen (Q11) | „ab“ der Schwelle, Standard 2, mindestens 2; nie beide Arten (B5) | gleiches Verhalten im Standard, ohne doppelte Ereignis-Commands für dieselben Geschenke; Entscheidung des Projektinhabers (2026-10-02) |
| A4 | keine Ereignisse für Umfragen und Vorhersagen; nur die Twitch-Action, die eine Umfrage oder Vorhersage erstellt, hat eine Unterliste, die nach dem Ende läuft, mit `$pollchoice` bzw. `$predictionoutcome` (Q11) | Ereignistypen für Umfragen und Vorhersagen; die Action mit Unterliste gibt es zusätzlich (Phase 4) | Auch auf Twitch selbst gestartete Umfragen lösen Commands aus; die Action bleibt für den Import; Entscheidung des Projektinhabers (2026-10-02) |

## Akzeptanzkriterien

- [x] B1: Alle Typen dieses Katalogs erfüllen die Namensregel und stehen im Katalog von `internal/event`, sobald ihre Quelle existiert.
- [x] B2: Ein Twitch-Follow veröffentlicht `twitch.channel.follow` und `channel.follow`. Erledigt 2026-10-04 im Event-Service (`TestSpecificAndNeutral`); der Twitch-Adapter folgt in Phase 4.
- [x] B3, B20: Ein zweiter Follow desselben Nutzers in derselben Sitzung löst kein Ereignis aus; nach einem neuen Stream-Start wieder. Erledigt 2026-10-04: `TestOncePerSession`.
- [x] B5, B26: Unter, an und über der Schwelle entstehen die richtigen Geschenk-Ereignisse, nie beide Arten. Erledigt 2026-10-04: `TestMassGift`.
- [x] B8, B24, B25: Karenzzeit mit `testing/synctest`: kurze Unterbrechung ohne neue Sitzung, lange mit `channel.stream.stop` nach Ablauf, Karenzzeit 0. Erledigt 2026-10-04: `TestStreamSession`.
- [x] B21: Nach einem Neustart während des Streams entsteht kein zweiter Stream-Start. Erledigt 2026-10-04: `TestRestart`.
- [x] B9, B29: Jedes plattformneutrale Ereignis trägt Plattform, Nutzer, Zielnutzer und Werte nach der Tabelle, als Tabellentest. Erledigt 2026-10-04: `TestShapes`, `TestShapeDetails`, `TestEventData`.
- [x] B10: Die Settings-Sektion `events` hat die Standardwerte und lehnt Werte außerhalb der Grenzen ab. Erledigt 2026-10-04.
- [x] B11: Die Sperren gelten je Plattform und Sitzung, die Einmal-Ereignisse je Nutzer über alle Plattformen. Erledigt 2026-10-04: `TestOncePerSession`, `TestGreetingOnlyLive`, `TestStreamSessions` im Store.
- [x] B12: Ein Ereignis löst seinen Ereignis-Command mit den Daten des Ereignisses aus; ein spezifisches und sein neutrales Ereignis lösen beide aus; ein unterdrücktes keines. Erledigt 2026-10-04.
- [x] B13, B14, B28: Reihenfolge der Ereignisse und Commands einer Chatnachricht und eines Beitritts; Nachrichten des Bots lösen nichts aus. Erledigt 2026-10-04: `TestMessageOrder`, `TestJoin`, `TestBotMessage`.
- [x] B22: Eine doppelt gemeldete Plattformnachricht wird einmal veröffentlicht. Erledigt 2026-10-04: `connector.Dedup`, `TestRepeat` der Mock-Plattform.
- [x] B27: Nach einem Neustart mit abgelaufener Karenzzeit endet die Sitzung ohne `channel.stream.stop`. Erledigt 2026-10-04: `TestRestart`, `TestRestartDuringGrace`.

## Offene Fragen

Keine.

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q8 | Doku | <https://mixitup.bot/docs/events> | generische Ereignisse, einmal je Start, Sammelgeschenke, Identifier; abgerufen 2026-09-29 |
| Q9 | Doku | <https://mixitup.bot/docs/chat> | Chat-Ereignisse; abgerufen 2026-09-29 |
| Q10 | Doku | <https://mixitup.bot/docs/platforms/twitch> | Twitch-Ereignisse; abgerufen 2026-09-29 |
| Q11 | Original (Hilfestellung) | `MixItUp.Base/Services/EventService.cs @ v1.8.200`, `MixItUp.Base/Services/Twitch/New/TwitchSession.cs @ v1.8.200`, `MixItUp.Base/Services/Twitch/New/TwitchClient.cs @ v1.8.200`, `MixItUp.Base/Model/Settings/SettingsV3Model.cs @ v1.8.200`, `MixItUp.Base/Model/Actions/TwitchActionModel.cs @ v1.8.200` | Einmal-Sperre je Programmlauf (A1), Schwelle der Sammelgeschenke (A3), Umfragen und Vorhersagen nur über die Action (A4); gelesen 2026-10-02 von einem eigenen Recherche-Agenten, der nur das Verhalten in eigenen Worten weitergab |
| QP | Projekt | [Plan](../plan.md) §6.7, Anhang A.1, A.2 | Inventar aus dem Audit, EventSub-Subscriptions |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-29 | Erstfassung (Entwurf) aus der offiziellen Doku und dem Plan, ohne Code des Originals |
| 2026-09-29 | Vom Projektinhaber geprüft und akzeptiert. Die offenen Fragen bleiben bis zur Prüfung am Original offen; bis dahin gilt das hier beschriebene Verhalten. |
| 2026-09-29 | Katalog umgesetzt (`internal/domain/eventtype`). Festlegungen dabei: `chat.user.join` und `chat.user.entrance` gelten wie B3 einmal je Nutzer und Stream-Sitzung, `chat.user.new` und `chat.user.first_message` einmal je Nutzer überhaupt; plattformspezifische Typen haben die Häufigkeit ihrer neutralen Entsprechung. |
| 2026-10-02 | `chat.user.entrance` zählt nur Nachrichten, während der Stream live ist; Nachrichten offline lösen die Begrüßung nicht aus und zählen nicht als erste (Entscheidung des Projektinhabers, [`command-engine.md`](command-engine.md), B41). |
| 2026-10-02 | Offene Fragen am Original geklärt (Q11) und entschieden (Entscheidungen des Projektinhabers). Neu: Karenzzeit für kurze Unterbrechungen des Streams (B8, Randfälle B24, B25), Ereignistypen für Twitch-Umfragen und -Vorhersagen (A4). Geändert: Die Schwelle der Sammelgeschenke hat den Standard 2 und das Minimum 2, nie laufen beide Arten (B5, A3, B26). A1 nennt das Verhalten des Originals. Übereinstimmend: ein spezifisches und ein neutrales Ereignis, in dieser Reihenfolge (B2). |
| 2026-10-04 | Für den Event-Service und die Mock-Plattform als erste Quelle (Roadmap 3.6) ergänzt: Nutzlast der plattformneutralen Ereignisse (B9), Settings-Sektion `events` (B10), eine Stream-Sitzung je Plattform (B11), Auslösen der Ereignis-Commands (B12), Reihenfolge bei einer Chatnachricht und einem Beitritt (B13, B14), Randfälle B27–B29. Entscheidung des Projektinhabers: Adapter übergeben, was sie empfangen, über einen Port an den Event-Service; er wendet die Regeln an, veröffentlicht die Ereignisse und löst die Ereignis-Commands aus, statt sie vom Bus zu lesen ([Code-ADR-0011](../adr/code/0011-event-bus.md), Punkt 4). So wird ein unterdrücktes Ereignis gar nicht erst veröffentlicht, und kein Eingang geht bei vollem Puffer verloren. Doppelte Nachrichten verwirft weiter der Adapter (B22, Code-ADR-0011, Punkt 5). Festlegungen dabei: Die Schwelle reicht bis 1 000 (B10). Vor dem ersten Stream-Start gilt eine Sitzung ohne Start (B11). Ereignisse mit einer Chatnachricht geben sie dem Ereignis-Command als auslösende Nachricht mit, etwa für eine Antwort (B12). Die Erkennung des Nutzers (`chat.user.new`, `chat.user.join`) kommt vor der Nachricht, `chat.user.first_message` und die Begrüßung nach dem Chat-Command (B13), wie in der Chat-Pipeline der Roadmap (5.1). Nachrichten des Bot-Kontos lösen nichts aus, damit sich der Bot nicht selbst antwortet (B13). Bei `chat.user.timeout` und `chat.user.ban` ist der gesperrte Nutzer der Nutzer, weil Plattformen den Moderator nicht immer nennen. Ein Stream-Ende, dessen Karenzzeit ablief, während der Core nicht lief, wird nicht nachgeholt (B27). |
| 2026-10-04 | Mock-Plattform, Eingangs-Port und Deduplizierung umgesetzt (Roadmap 3.6). Festlegungen dabei: Ein Adapter merkt sich die ID einer Nachricht oder eines Ereignisses 10 Minuten lang (`connector.DefaultDedupTTL`, B22); was ohne ID kommt, kann er nicht als Wiederholung erkennen. Die Mock-Plattform simuliert nur plattformneutrale Typen; Chatnachrichten, Beitritte und Start und Ende des Streams haben eigene Wege (B11, B13, B14), und die Typen, die der Core selbst ableitet (`chat.user.new`, `chat.user.entrance`, `chat.user.first_message`), simuliert sie nicht. Sie lehnt wie eine echte Plattform ab, den Streamer zu moderieren. |
| 2026-10-04 | Event-Service umgesetzt (`internal/eventservice`, Roadmap 3.6). Festlegungen dabei: Die Rollen eines Nutzers übernimmt der Service nur aus Chatnachrichten; Ereignisse und Beitritte nennen sie nicht verlässlich. `chat.user.new` gilt für das erste Erkennen im Chat, nicht für das Anlegen des Nutzers, das auch ein Follow auslösen kann (B13). Die Einmal-Sperren nach B3 stehen unter dem plattformneutralen Typ, sodass `twitch.channel.follow` und `channel.follow` gemeinsam einmal auslösen. Laufende Begrüßungen bricht der Core ab, wenn keine Plattform mehr live ist ([`command-engine.md`](command-engine.md), B41). Ob ein Stream nach einem Neustart zu lange offline war (B27), misst der Service vom letzten Zeitpunkt, an dem der Core ihn live sah: beim Live-Gehen, bei jeder Meldung „live“ und beim Herunterfahren; nach einem Absturz ist das die letzte solche Meldung. Eine Karenzzeit, die bei einem Neustart noch läuft, läuft weiter. Eine Sammelaktion nennt so viele Beschenkte, wie sie Geschenke zählt. Plattformspezifische Typen ohne neutrale Entsprechung nimmt der Service erst mit ihrer Phase an. Die Ereignisse der Anwendung (`app.started`, `app.stopping`) veröffentlicht der Core über den Service, damit auch sie ihre Ereignis-Commands auslösen (B12). Die Einstellungen liest der Service bei jeder Regel, die sie braucht, sodass Änderungen sofort gelten. |
