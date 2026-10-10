# Plan: Roadmap 4.4 — Twitch-Funktionen

**Status:** in Ausführung
**Stand:** 2026-10-10, `main` bei `56cfdeb`; 4.1–4.3 stehen in Stack #162
(4.2) und #170 (4.3), Merging ausstehend
**Scope:** nur 4.4 (Funktionen). 4.5 (Tests: Fixtures/Contract-Tests gegen
die Twitch-CLI) bleibt offen.

## 1. Was 4.4 liefert (Roadmap)

- [ ] Spezifikation `docs/spec/twitch-events.md`: Event-Zuordnung und
  Identifier je Event (S)
- [ ] Chat senden, löschen, leeren; Moderation; Titel und Kategorie
  setzen (S)
- [ ] Event-Commands (M): Stream Start/Stop, Follow, Raid (ein- und
  ausgehend), Abo, Resub, Geschenk, Massengeschenk, Cheer, Channel
  Points, Hype Train, Werbung, Shoutout, Ziele, Charity;
  Moderationsereignisse (Umfragen und Vorhersagen: keine Events, A4)
- [ ] Channel-Points-Commands: Belohnung ↔ Command, Einlösung
  abschließen oder erstatten (M)
- [ ] Bits-Commands mit Schwellen und Bereichen (S) (P1)
- [ ] Twitch-Action: Clip, Stream-Marker, Umfrage/Vorhersage (mit
  Unterliste nach dem Ende, `$pollchoice`/`$predictionoutcome`, A4),
  Werbung, Raid, Shoutout, Belohnungen verwalten (L)
- [ ] Custom Power-Ups als Command-Typ (S) (P2)
- [ ] Emote-Kataloge für Twitch, BetterTTV und FrankerFaceZ mit Cache
  (M) (P1)

## 2. Befunde der Recherche (verifiziert 2026-10-10)

1. **Helix-Endpunkte aus 4.2** (`internal/helix`): `SendChatMessage`,
   `DeleteChatMessage`, `GetChatSettings`, `UpdateChatSettings`,
   `SendAnnouncement`, `SendShoutout`, `Ban`, `Unban`, `Timeout`,
   `Untimeout`, `GetModerators`, `GetVIPs`, `AddVIP`, `RemoveVIP`,
   `GetChannel`, `UpdateChannel`, `GetGames`, `SearchGames`,
   `GetUsers`, `GetFollowers`, `GetSubscriptions`, `GetCategories`,
   EventSub-Subscriptions, `GetStreams`, `GetChannels`.
2. **Fehlende Helix-Endpunkte** (Helix-Doku): `ClearChat`
   (POST /moderation/chat_delete), `AddMod`/`RemoveMod`
   (POST/DELETE /moderation/mods), Clips (GET /clips), Stream-Marker
   (GET/POST /stream markers), Raid senden (POST /raids), Werbung
   (POST /ads), Ziele (GET /goals), Charity (GET /charity_campaigns,
   /charity_donations), Umfragen (GET/POST/DELETE /polls),
   Vorhersagen (GET/POST/DELETE /predictions), Channel-Points-
   Belohnungen (GET/POST/DELETE /rewards, PATCH), Einlösungen
   (GET/POST /redemptions), Emotes (GET /emotes), Bits-Bilanzen
   (GET /bits/cheermotes, /bits/charity). BTTV und FrankerFaceZ sind
   externe APIs (api.betterttv.dev, api.frankerfacezh.com) —
   eigener httpclient-Name, keine Helix-Breaker.
3. **Platform-Port**: `internal/twitch` liefert in 4.3
   Chat/Moderation/Users/Channel als `notYet`
   (`connector.ErrNotImplemented`); 4.4 ersetzt sie. `Identities`
   (Streamer-Login/ID) fehlt noch — die Identifier `$streamer…`
   brauchen es.
4. **Event-Commands**: Commands as Code referenzieren
   `spec.event` mit Typen aus dem Katalog (4.3 hat die Twitch-Typen
   bus-kompatibel registriert). Die Ereignisse kommen seit 4.3 an;
   fehlt sind die **Identifier** (A.6) für die Nutzlasten
   (`$raider`, `$gifter`, `$recipient`, `$cheer…`, `$hype_train…`,
   `$shoutout…`, `$goal…`, `$donation…`, `$raidviewers` …).
5. **A4 (Entscheidung 2026-10-02)**: keine Event-Commands für
   Umfragen und Vorhersagen; nur die Twitch-Action mit Unterliste
   (`$pollchoice`/`$predictionoutcome`) nach dem Ende.
6. **Actions**: neue Action-Typen kommen in die Registry
   (`internal/action/*.Catalog()`); die Moderation- und Chat-Actions
   sind das Muster (Ports + Descriptor + Schema). Die Twitch-Action
   wird ein neuer Ordner `internal/action/twitch` mit den Operationen
   Clip, Marker, Umfrage, Vorhersage, Werbung, Raid, Shoutout,
   Belohnungen.
7. **Channel Points**: Einlösungen kommen über
   `channel.channel_points_automatic_reward_redemption.add` (4.3:
   nicht abonniert, kein Handler); 4.4 abonniert sie (Soll-Zustand
   des 4.3-Managers wächst) und löst sie zu Commands auf.

## 3. Design (Festlegungen)

- **Ein Ordner je Aufgabe**, gestapelt auf dem 4.3-Stack-Top
  (`docs/eventsub-docs`); eigener 4.4-Stack.
- **Identifier je Event** stehen in der Spezifikation
  `twitch-events.md` (Tabellen wie `events.md` B9) und werden als
  Familien in `internal/template` ergänzt (neue `twitchprops.go`
  neben `userprops.go`/`message.go`); die Namen der Werte sind
  Konstanten (Konvention `events.md`, Umsetzung).
- **Chat/Moderation/Channel/Users** der Platform mappen 1:1 auf die
  vorhandenen Helix-Endpunkte; `Purge` gibt es in Helix nicht →
  `ErrRefused` („die Plattform kann keine Purge“); `Identities`
  meldet den Streamer (aus dem Account-Zustand, 4.3) und den Bot,
  falls angemeldet.
- **Emote-Katalog** (`internal/emote`): Quelle pro Plattform
  (Twitch via Helix, BTTV, FFZ), Cache mit Ablauf (Twitch 1 h, BTTV/FFZ
  1 h; Fallback: alte Liste bei Fehler), gemeinsame ID → Code/Namen
  für die 4.3-Abbildung (die heute die Rohtypen trägt) und für
  Templates.
- **Channel-Points-Commands**: Belohnung ↔ Command über eine
  Einstellung (Command-Name ↔ Belohnungs-ID); Einlösung abschließen
  (POST /redemptions) oder erstatten (POST
  /redemptions/reward_redemptions/fulfillment … — exakter Pfad in der
  Aufgabe); der Command-Trigger kommt über den abonnierten
   EventSub-Event.
- **Bits-Commands**: Schwellen (Bits, Gesamtbetrag) und Bereiche
  (Cheer-Nachricht) wie im Original; neue Anforderungs-Familie.
- **Custom Power-Ups (P2)**: Command-Typ, der eine
  `channel.custom_power_up_redemption.add`-Einlösung aufnimmt (Typ
   `TwitchCustomPowerUpRedeem` existiert im Katalog).

## 4. Tasks

### Task 1 — Spezifikation `twitch-events.md`

**Zweig:** `docs/spec-twitch-events` (auf `docs/eventsub-docs`)
**Dateien:** `docs/spec/twitch-events.md`, `docs/spec/README.md`
(Verweis), Roadmap-Historie.
**Inhalt:** Event-Zuordnung (welcher EventSub-Event liefert welchen
Katalog-Typ mit welcher Nutzlast — die 4.3-Abbildung als Basis,
ergänzt um die 4.4-Events: Channel Points, Hype Train, Ad, Shoutout,
Goal, Charity, Umfragen/Vorhersagen als A4-Querverweis), Identifier
je Event (Tabellen wie `events.md` B9: Nutzer, Zielnutzer, Werte),
Begründung der A4-Entscheidung, offene Nutzlasten.
**Commit:** `docs(spec): the twitch events specification`

### Task 2 — Platform: Chat, Moderation, Channel, Users, Identities

**Zweig:** `feat/twitch-chat` (auf Task 1)
**Dateien:** `internal/helix/moderation.go` (+ `ClearChat`, `AddMod`,
`RemoveMod`), `internal/twitch/chat.go`, `internal/twitch/mode.go`,
`internal/twitch/users.go`, Tests.
**Inhalt:** Die `notYet`-Stubs werden zu echten Adaptern: `Chat.Send`
(`SendChatMessage`, Rate-Limits und Aufteilen nach Plattformregel),
`Chat.Delete` (`DeleteChatMessage`), `Replier`/`Whisperer`
(Reply-Parameter von `SendChatMessage`; Whisper = `SendChatMessage`
mit `reply_parent` … — exakte Felder in der Aufgabe), `Moderation`
(`Timeout`, `Ban`, `Unban`, `Mod`/`Unmod`, `ClearChat`; `Purge` →
`ErrRefused`), `Users` (`GetUsers`), `Channel` (`GetChannel` +
`GetStreams`), `Identities` (Streamer aus dem 4.3-Zustand, Bot falls
angemeldet). Titel/Kategorie: `UpdateChannel` (für die
Set-Operation, wenn eine sie braucht; die 4.3-Platform meldet den
Zustand, das Setzen kommt mit der Twitch-Action).
**Tests (Fake-Helix + In-Memory):** je Operation einer; Purge
verweigert; Identities mit/ohne Bot.
**Commit:** `feat(twitch): chat, moderation and the channel`

### Task 3 — Helix-Endpunkte der neuen Familien

**Zweig:** `feat/helix-twitch-ops` (auf Task 2)
**Dateien:** `internal/helix/clips.go`, `markers.go`, `raids.go`,
`ads.go`, `goals.go`, `charity.go`, `polls.go`, `predictions.go`,
`rewards.go`, `redemptions.go`, `emotes.go`, `bits.go`, Tests.
**Inhalt:** Die in Befund 2 fehlenden Endpunkte, getippt wie 4.2
(JSON/v2, paginiert wo nötig, `Client-Id`/Bearer über den
`httpclient`); nur was die Tasks 4–8 brauchen, kein Gold-Plating.
**Tests:** je Endpunkt einer (Fake-Helix: Request-Form,
Antwort-Parsing, Paginierung).
**Commit:** `feat(helix): the twitch operation endpoints`

### Task 4 — Event-Commands: Identifier und Trigger

**Zweig:** `feat/twitch-event-commands` (auf Task 3)
**Dateien:** `internal/template/twitchprops.go` (neue Familien),
`internal/app` (Wiring der Familien), `docs/spec/twitch-events.md`
(Nachträge, wenn sich Namen ändern), Tests.
**Inhalt:** Identifier je Event laut Spezifikation (Task 1):
`$raid…`/`$raidviewers`, `$sub…`/`$gift…`/`$recipient…`,
`$cheerbits`/`$cheermessage`, `$hype_train…`, `$ad…`, `$shoutout…`,
`$goal…`, `$donation…`, `$channel_points…`; die Familien hängen an
dem laufenden Event (wie `message.go`); die Event-Commands selbst
sind YAML (keine Go-Änderung nötig, außer die Familien). Channel
Points und die restlichen 4.3-nicht-abonnierten Events:
Soll-Zustand des 4.3-Managers wächst (die Typen aus Task 1).
**Tests:** je Familie einer (Event-Nutzlast → Identifier),
Wiring-Test (Familie ist registriert).
**Commit:** `feat(template): the twitch event identifiers`

### Task 5 — Channel-Points-Commands

**Zweig:** `feat/channel-points` (auf Task 4)
**Dateien:** `internal/engine` (Trigger), `internal/settings`
(Belohnung ↔ Command), `internal/twitch` (Einlösung abschließen/
erstattet über die Task-3-Endpunkte), Tests.
**Inhalt:** Belohnung ↔ Command (Einstellung); Einlösung löst den
Command aus; der Command kann die Einlösung abschließen oder
erstattet; Ablehnung (Requirement) → erstatten.
**Tests:** Belohnung löst Command, Abschluss, Erstattung, Ablehnung.
**Commit:** `feat(engine): the channel points commands`

### Task 6 — Bits-Commands

**Zweig:** `feat/bits-commands` (auf Task 5)
**Dateien:** `internal/requirement` (Schwellen/Bereiche),
`internal/template` (Bits-Identifier), Tests.
**Inhalt:** Bits-Commands mit Schwellen (Bits, Gesamtbetrag) und
Bereichen (Cheer-Nachricht) wie im Original (P1).
**Tests:** Schwellen, Bereiche, Gesamtbetrag.
**Commit:** `feat(requirement): the bits commands`

### Task 7 — Twitch-Action

**Zweig:** `feat/twitch-action` (auf Task 6)
**Dateien:** `internal/action/twitch` (neuer Ordner: Descriptor,
Schema, Operationen Clip, Marker, Umfrage, Vorhersage, Werbung, Raid,
Shoutout, Belohnungen), `internal/app` (Registry), Tests.
**Inhalt:** Eine Action-Typ `twitch` mit den Operationen;
Umfrage/Vorhersage mit **Unterliste nach dem Ende**
(`$pollchoice`/`$predictionoutcome`, A4); Belohnungen verwalten
(POST/DELETE/PATCH der Task-3-Endpunkte).
**Tests:** je Operation einer (Fake-Ports), Unterliste nach Ende.
**Commit:** `feat(action): the twitch action`

### Task 8 — Custom Power-Ups als Command-Typ

**Zweig:** `feat/power-up-commands` (auf Task 7)
**Dateien:** `internal/command` (neuer Typ), `internal/commandfile`,
`internal/engine` (Trigger), Tests. (P2)
**Inhalt:** Command-Typ für `channel.custom_power_up_redemption.add`
(Typ `TwitchCustomPowerUpRedeem`), Einlösung als Trigger.
**Tests:** Typ konvertiert, Einlösung löst aus.
**Commit:** `feat(command): the custom power up commands`

### Task 9 — Emote-Kataloge

**Zweig:** `feat/emote-catalogs` (auf Task 8)
**Dateien:** `internal/emote` (neuer Ordner: Quellen Twitch/BTTV/FFZ,
Cache, Fallback), `internal/twitch/map.go` (4.3-Abbildung nutzt den
Katalog), `internal/template` (Emote-Identifier), Tests.
**Inhalt:** Kataloge mit Cache (1 h; Fallback alte Liste), Emote-
Codes in Chat-Nachrichten werden aufgelöst (4.3 lieferte Rohtypen);
`$emote…`-Identifier.
**Tests:** Cache-Treffer/-Verfall, Fallback, Auflösung in der
Abbildung.
**Commit:** `feat(emote): the emote catalogs`

### Task 10 — Doku: README, Roadmap (Docs-PR, oberstes PR)

**Zweig:** `docs/twitch-functions-docs` (auf Task 9)
**Inhalt:** README-Statussatz (4.4), Roadmap 4.4 abhaken + Historie,
Backlog-Nachträge, Plan abschließen.
**Commit:** `docs: the twitch functions of phase 4.4`

## 5. Scope-Liste

**Dabei:** Spezifikation, Platform-Chat/Moderation/Channel/Users/
Identities, neue Helix-Endpunkte, Event-Command-Identifier, Channel
Points, Bits, Twitch-Action, Custom Power-Ups, Emote-Kataloge, Doku.

**Nicht dabei:** 4.5 (Fixtures/Contract-Tests gegen die Twitch-CLI),
die Multiplattform-Aufgaben (Backlog), YouTube/Kick, die Antworten
der Chat-Nachricht (Backlog, 4.4/5.x).

## 6. Offene Punkte

1. **Purge**: Helix hat keinen Purge-Endpunkt; die Moderation verweist
   auf `ErrRefused`. Im Original war Purge ein Command — ob ein
   Ersatz (z. B. Timeout + ClearChat) sinnvoll ist, entscheidet der
   Projektinhaber.
2. **Umfragen/Vorhersagen als Events**: A4 sagt „keine Events“; die
   Katalog-Typen `twitch.poll.*`/`twitch.prediction.*` existieren
   aber. Task 4 abonniert sie nicht; sie bleiben für 4.5/Backlog.
3. **Bits-Bilanzen** (`/bits/cheermotes`): für „Gesamtbetrag“ nötig;
   falls der Original-Begriff „Bereich“ anders fällt, entfällt der
   Endpunkt.
4. **BTTV/FFZ-APIs**: externe Abhängigkeiten (eigene
   httpclient-Namen, Allowlist-Meldung); die URLs und Formate in
   Task 9 verifizieren.

## 7. Exit von 4.4

- [ ] Spezifikation `twitch-events.md` (Event-Zuordnung + Identifier)
- [ ] Chat senden/löschen/leeren, Moderation, Titel/Kategorie
- [ ] Event-Commands für die 4.4-Typen mit Identifier
- [ ] Channel-Points-Commands (Belohnung ↔ Command, abschließen/
  erstatten)
- [ ] Bits-Commands (Schwellen, Bereiche)
- [ ] Twitch-Action (Clip, Marker, Umfrage/Vorhersage mit Unterliste,
  Werbung, Raid, Shoutout, Belohnungen)
- [ ] Custom Power-Ups als Command-Typ
- [ ] Emote-Kataloge (Twitch, BTTV, FFZ) mit Cache
- [ ] Doku: README, Roadmap 4.4 abgehakt, Historie

## 8. Stack-Aufbau (Zusammenfassung)

```
docs/eventsub-docs (4.3-Stack #170, Top)
 └ PR 1  docs/spec-twitch-events      (docs/spec/twitch-events.md)
 └ PR 2  feat/twitch-chat             (Platform: Chat/Mod/Channel/Users/Identities)
 └ PR 3  feat/helix-twitch-ops        (internal/helix: neue Endpunkte)
 └ PR 4  feat/twitch-event-commands   (internal/template: Identifier)
 └ PR 5  feat/channel-points          (engine/settings: Channel Points)
 └ PR 6  feat/bits-commands           (requirement/template: Bits)
 └ PR 7  feat/twitch-action           (internal/action/twitch)
 └ PR 8  feat/power-up-commands       (command: Custom Power-Ups)
 └ PR 9  feat/emote-catalogs          (internal/emote)
 └ PR 10 docs/twitch-functions-docs   (README, roadmap, historie)
```
