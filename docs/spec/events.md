# Spezifikation: Ereigniskatalog

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-10-02 |
| **Bezug** | Roadmap Phase 2.2 (Event-Modell), 3.6, 4.4, 5.1; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md), [Code-ADR-0011](../adr/code/0011-event-bus.md); Plan §6.7, Anhang A.1, A.2; [`commands.md`](commands.md) |
| **Umsetzung** | teilweise: Katalog mit Namen, plattformneutraler Entsprechung und Häufigkeit je Typ in `internal/domain/eventtype`; Umschlag und Bus in `internal/event`; die Namen der Ereigniswerte (B7) als Konstanten in `internal/template`. Die Auslöseregeln (B2 bis B6, B20 bis B22) folgen mit den Quellen: Engine (Phase 3.6), Twitch (Phase 4), Chat (Phase 5) |

## Zweck und Umfang

Legt die stabilen Namen der Ereignistypen fest, die Ereignis-Commands auslösen können, und die Regeln, wann ein Ereignis ausgelöst oder unterdrückt wird. Die Nutzlast jedes Typs wird mit der Phase festgelegt, in der seine Quelle entsteht (Chat in Phase 5, Twitch in Phase 4), zusammen mit der Spezifikation `twitch-events.md`.

Nicht Teil dieser Spezifikation: die Zuordnung zu den numerischen Ereignis-IDs des Originals für den Import. Sie wartet auf die rechtliche Einschätzung (Roadmap Gate O, O.1).

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Ereignistyp | stabiler Name eines Ereignisses, z. B. `channel.follow`; Namensregel nach Code-ADR-0011 |
| plattformneutrales Ereignis | Ereignis, das auf mehreren Plattformen vorkommt, etwa ein Follow |
| plattformspezifisches Ereignis | Ereignis einer Plattform, etwa ein Twitch-Hype-Train, oder die plattformspezifische Fassung eines neutralen Ereignisses |
| Stream-Sitzung | Zeitraum von einem Stream-Start bis zum nächsten Stream-Start desselben Kanals |

## Verhalten

### Allgemeine Regeln

| ID | Regel | Quellen |
|---|---|---|
| B1 | Jedes Ereignis wird im Umschlag nach Code-ADR-0011 veröffentlicht; sein Typ ist einer der Namen dieses Katalogs. Veröffentlichte Namen werden nie umbenannt. | Code-ADR-0011 |
| B2 | Hat ein plattformspezifisches Ereignis eine plattformneutrale Entsprechung, werden beide veröffentlicht: erst das spezifische, dann das neutrale mit der Plattform als Quelle. Ereignis-Commands können auf das eine, das andere oder beide reagieren. | Q8 (Generische Ereignisse „auf mehreren Plattformen“), Q10 |
| B3 | Folgende Ereignisse werden je Stream-Sitzung höchstens einmal ausgelöst: Stream-Start, Stream-Ende; je Nutzer: Follow, Abo, Resub; je raidendem Kanal: Raid. | Q8, A1 |
| B4 | Ereignisse ohne diese Einschränkung (Chatnachrichten, Geschenk-Abos, Bits, Spenden …) werden jedes Mal ausgelöst. | Q8 |
| B5 | Geschenkte Abos: Für jeden Beschenkten gibt es ein Ereignis `…subscription.gift`, für die Sammelaktion ein Ereignis `…subscription.mass_gift`. Eine Einstellung legt eine Schwelle fest: Ab dieser Zahl von Geschenken läuft nur das Sammelereignis, darunter nur die Einzelereignisse. | Q8 |
| B6 | Ereignisse, die ein Nutzer auslöst, tragen den Nutzer; Ereignisse mit einem Ziel (etwa Beschenkter, Geraidete) tragen zusätzlich den Zielnutzer. | Q8 („User“ und „Target User“ Identifier) |
| B7 | Ereignisspezifische Werte, die Commands verwenden können: Plattform, Zahl der Raid-Zuschauer, Abo-Nachricht, Abo-Stufe und ihr Name, anonym ja/nein, Zahl geschenkter Abos. **[Interop]** `$streamingplatform`, `$raidviewercount`, `$message`, `$usersubplan`, `$usersubplanname`, `$isanonymous`, `$subsgiftedamount` | Q8 |

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

**Weitere Plattformen und Dienste** (Phasen 7, 9, 10): YouTube, Kick, Velora, VPZone, OBS Studio und Spendendienste bekommen ihre Typen nach demselben Schema mit ihrer Phase (Plan Anhang A.1), etwa `youtube.super_chat`, `obs.scene.change`, `donation.receive`.

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B20 | Ein Nutzer folgt, entfolgt und folgt in derselben Stream-Sitzung erneut | nur ein `channel.follow` | B3 |
| B21 | Der Core startet neu, während der Stream läuft | die Stream-Sitzung gilt als fortgesetzt; kein zweites `channel.stream.start`, sofern die Plattform den Stream als unverändert meldet | A1 |
| B22 | Dieselbe Plattformnachricht kommt zweimal an (Wiederholung nach Verbindungsabbruch) | wird vom Adapter anhand der Nachrichten-ID verworfen und nur einmal veröffentlicht | Code-ADR-0011 |
| B23 | Twitch Hype Chat | kein Ereignistyp; in der Doku als nicht funktionsfähig vermerkt | Q10 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | „einmal je Programmstart“ für Stream-Start/-Ende, Follow, Raid, Abo, Resub, Join, Entrance | einmal je Stream-Sitzung, auch über einen Neustart des Cores hinweg | Der Core läuft im Server-Modus und als Daemon über viele Streams; „je Programmstart“ würde ab dem zweiten Stream alles unterdrücken (ADR-0003). Die Sitzung muss dafür gespeichert werden. |
| A2 | Ereignisse als Aufzählung mit numerischen Werten | stabile Namen nach Namensregel | Code-ADR-0011; lesbar in API, Logs und Commands als Code |

## Akzeptanzkriterien

- [x] B1: Alle Typen dieses Katalogs erfüllen die Namensregel und stehen im Katalog von `internal/event`, sobald ihre Quelle existiert.
- [ ] B2: Ein Twitch-Follow veröffentlicht `twitch.channel.follow` und `channel.follow`.
- [ ] B3, B20: Ein zweiter Follow desselben Nutzers in derselben Sitzung löst kein Ereignis aus; nach einem neuen Stream-Start wieder.
- [ ] B5: Unter und über der Schwelle entstehen die richtigen Geschenk-Ereignisse.
- [ ] B21: Nach einem Neustart während des Streams entsteht kein zweiter Stream-Start.

## Offene Fragen

- B2: Laufen im Original bei einem Twitch-Follow sowohl der Command für „Channel Followed“ als auch für „Twitch Channel Followed“?
- B5: Wie genau wirkt die Schwelle für Sammelgeschenke im Original, und wie heißt die Einstellung?
- B3: Unterdrückt das Original einen zweiten Stream-Start in einer laufenden Sitzung tatsächlich, auch wenn der Stream zwischendurch offline war?
- Katalog: Umfragen und Vorhersagen (Twitch EventSub `channel.poll.*`, `channel.prediction.*`, Plan Anhang A.2) stehen nicht in der Twitch-Seite der Doku. Gibt es dafür Ereignisse im Original?

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q8 | Doku | <https://mixitup.bot/docs/events> | generische Ereignisse, einmal je Start, Sammelgeschenke, Identifier; abgerufen 2026-09-29 |
| Q9 | Doku | <https://mixitup.bot/docs/chat> | Chat-Ereignisse; abgerufen 2026-09-29 |
| Q10 | Doku | <https://mixitup.bot/docs/platforms/twitch> | Twitch-Ereignisse; abgerufen 2026-09-29 |
| QP | Projekt | [Plan](../plan.md) §6.7, Anhang A.1, A.2 | Inventar aus dem Audit, EventSub-Subscriptions |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-29 | Erstfassung (Entwurf) aus der offiziellen Doku und dem Plan, ohne Code des Originals |
| 2026-09-29 | Vom Projektinhaber geprüft und akzeptiert. Die offenen Fragen bleiben bis zur Prüfung am Original offen; bis dahin gilt das hier beschriebene Verhalten. |
| 2026-09-29 | Katalog umgesetzt (`internal/domain/eventtype`). Festlegungen dabei: `chat.user.join` und `chat.user.entrance` gelten wie B3 einmal je Nutzer und Stream-Sitzung, `chat.user.new` und `chat.user.first_message` einmal je Nutzer überhaupt; plattformspezifische Typen haben die Häufigkeit ihrer neutralen Entsprechung. |
| 2026-10-02 | `chat.user.entrance` zählt nur Nachrichten, während der Stream live ist; Nachrichten offline lösen die Begrüßung nicht aus und zählen nicht als erste (Entscheidung des Projektinhabers, [`command-engine.md`](command-engine.md), B41). |
