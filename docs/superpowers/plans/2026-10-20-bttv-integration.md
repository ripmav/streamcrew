# Plan: BetterTTV (BTTV) korrekt einbinden

**Status:** Entwurf zur Abnahme — **nichts umgesetzt**.
**Grund:** Die BTTV-Integration ist aktuell doppelt defekt: der Legacy-Call aus dem
Ursprungscode (`api.betterttv.net/3/…`) ist tot, und der Fix aus PR #302 nutzt
`api.bttv.com/api/v2/…` — diese Domain ist **geparkt** (CNAME auf
`parkingcrew.net`). Beide Varianten liefern nur 404/„connection refused"
(siehe `docs/analyse-log-fehler-2026-10-20.md`, Abschnitt B).

Quellen: <https://betterttv.com/developers/api>, <https://betterttv.com/developers/websocket>
(beide JS-Apps; Inhalte aus dem Client-Bundle extrahiert) **plus** Live-Verifikation
aller Endpunkte am 2026-10-20 (Methoden unten angegeben).

---

## 1. Verifizierte Fakten (2026-10-20)

### REST (Base `https://api.betterttv.net`, Routen unter `/2/`)

| Endpoint | Antwort | Status |
|---|---|---|
| `GET /2/emotes` | `{urlTemplate, emotes: [{id, channel, code, imageType}]}` — **globale** Emotes (65 Stück im Test) | 200 verifiziert |
| `GET /2/channels/{login}` | `{urlTemplate, bots: [], emotes: [{id, channel, code, imageType}]}` | 200 verifiziert (mehrere Channels) |
| `GET /2/channels/{login}` (ohne BTTV-Daten) | `{"message":"user not found"}` | 404 verifiziert |
| `GET /2/channels/{twitchId}` (numerisch) | `{"message":"user not found"}` | 404 — **der Parameter ist der Login, nicht die ID** |

- **Keine Auth** nötig, keine Rate-Limit-Header beobachtet (429-Handling
  trotzdem über den vorhandenen `httpclient`-Breaker/Retry).
- `urlTemplate` aus der Antwort: `//cdn.betterttv.net/emote/{{id}}/{{image}}`.
  Verifizierte Bild-URLs: `https://cdn.betterttv.net/emote/{id}/1x|2x|3x`
  (HEAD 200; `…/1` = 404). Für `Emote.Image` (Base ohne Size-Suffix):
  `https://cdn.betterttv.net/emote/{id}`.
- `imageType` ist `png`/`gif` (gif ⇒ animiert). Das `emote.Emote`-Modell hat
  kein Animationsfeld → **ignoriert** (Entscheidung E1).
- Die alten `/3/…`-Routen sind weg (`Route GET:/3/emotes not found`); die
  Doku-Base `api.betterttv.net/3/` im Client-Bundle gehört zur
  **authentifizierten Dashboard-API** (`Authorization: Bearer`), nicht zur
  öffentlichen Emote-API — für streamcrew irrelevant.

### WebSocket (`wss://sockets.betterttv.net/ws`)

Verbindungs-Test am 2026-10-20: Handshake 101, JSON-Text-Frames
`{name, data}`. Nach `join_channel` (3 Channels, 45 s) **keine Frames** —
der Socket ist kein Initial-Listing, er pusht nur Ereignisse bei Aktivität.

**SEND** (aus der Doku, Formate verifiziert gegen die Beispiel-Nachrichten):

| name | data |
|---|---|
| `join_channel` | `{name: "{provider}:{providerId}"}` — z. B. `"twitch:38974721"`; subscribe auf Events des Channels |
| `part_channel` | `{name: "…"}` — unsubscribe |
| `broadcast_me` | `{provider, providerId, channel}` — eigenes User-Profil an die Channel-Clienten broadcasten (Presence) |

**RECEIVE** (aus der Doku mit Beispiel-Payloads):

| name | data (Beispiel aus der Doku) |
|---|---|
| `emote_create` | `{emote: {id, code, imageType, animated, user: {id, name, displayName, providerId}}, channel: "twitch:<id>"}` |
| `emote_update` | `{emote: {id, code}, channel: "twitch:<id>"}` (Code-Änderung/Alias) |
| `emote_delete` | `{emoteId, channel: "twitch:<id>"}` |
| `lookup_user` | `{name, providerId, channel, pro, emotes: [{id, code, imageType, animated, user: {…}}], …}` — User-Update (Presence; enthält je nach Kontext `channelEmotes`/`sharedEmotes`/`bots`/`avatar`/`badge`) |

Weitere in der Doku gelistete Event-Typen: `channel-join`, `channel-part`,
`user-broadcast`, `global-emotes`, `badges` (Details nicht weiter
verifiziert; für streamcrew nicht benötigt).

**Konsequenz:** Der WS ist ein **Update-Kanal** (Emote-Änderungen in
joinenden Channels), keine Quelle für die Init-Liste. Presence-Features
(`broadcast_me`, `lookup_user`) sind für einen Headless-Bot ohne
eigenes User-Profil wertlos.

---

## 2. Zielarchitektur

Bestand, der bleibt: `emote.Catalog` (TTL-Cache pro Source × Channel,
Fallback auf die letzte gute Liste) und die `Source`-Schnittstelle
`Emotes(ctx, channel string)` — `channel` ist der **Login** (so liefern
alle anderen Sources und die Twitch-Chat-Events).

### Phase 1 — REST-Integration reparieren (Pflicht)

`internal/emote/bttv.go` neu aufsetzen (ersetzt den #302-Stand):

1. Base `https://api.betterttv.net`, Routen `GET /2/emotes` (global) und
   `GET /2/channels/{login}` (Channel) — **Login-basiert, keine
   Helix-Auflösung** mehr: der in #302 eingeführte
   `bttvChannelID`-Resolver (Helix `GetUsers`) und die Verdrahtung in
   `internal/app` fallen wieder weg.
2. Antwort-Decoding: `urlTemplate` + `emotes[]` (`id`, `code`, `imageType`
   wird ignoriert). `Emote.Image` aus `urlTemplate` mit `{{id}}`
   ersetzt und dem Suffix `1x`/`2x`/`3x` gestrichen (Base ohne Size);
   `urlTemplate` fehlt/ist leer ⇒ Fallback-Konstante
   `https://cdn.betterttv.net/emote/{id}`.
3. **404 ⇒ leere Liste, kein Error** (Channel ohne BTTV-Daten ist der
   Normalfall — wie bei FFZ in #302 umgesetzt). Andere Fehler gehen an
   den Catalog, der die letzte gute Liste hält.
4. Globale + Channel-Emotes in einem `Emotes()`-Call (zwei HTTP-Requests,
   wie heute), Dedup über den vorhandenen `makeList`.
5. `NewBTTVSource(hc, base)` ohne Resolver-Parameter; `internal/app`-Wiring
   zurück zum #302-vorherigen Zustand (außer Base/Struktur).
6. Tests: `emote_test.go` — Fake-Server für beide Routen (Shape wie oben,
   inkl. 404-Channel-Fall und leerem `emotes`), URL-Bau aus `urlTemplate`,
   Dedup global ∪ channel. Test-Matrix-Datei je repo-Konvention.

**Abnahme Phase 1:** Lauf mit echten Channels (z. B. `shroud`, `xqc`):
keine BTTV-Warnungen mehr im Log, Emotes aus `Catalog.List` enthalten die
BTTV-Global- und Channel-Emotes mit korrekten CDN-URLs (1x-Base).

### Phase 2 — WebSocket für Live-Updates (optional, nach Abnahme Phase 1)

Nur, falls Emote-Änderungen ohne bis zu TTL (1 h) Verzögerung gewünscht
sind. Sonst bleibt der reine Polling-Weg (Phase 1) — das ist der
kleinere, stabilere Stand.

1. Neuer kleiner WS-Client `internal/emote/bttvws.go` (oder eigenes
   Subpaket): `wss://sockets.betterttv.net/ws`, Reconnect mit Backoff wie
   der EventSub-Client (Base/Backoff-Werte aus dem Twitch-Client
   übernehmen), JSON-Text `{name, data}`.
2. Beim Start `join_channel` für die betriebenen Channels
   (`twitch:{channelID}` — hier ist die Helix-Auflösung Login→ID wieder
   nötig, wie der #302-Resolver; dieser Code bleibt dafür erhalten oder
   wird erneut angelegt).
3. Behandelte Events: `emote_create` (eintragen), `emote_update` (Code
   aktualisieren), `emote_delete` (entfernen) — direkt in den
   Catalog-Cache des BTTV-Keys für den Channel, **ohne** den
   Rest-Cache zu rühren. `lookup_user`/`broadcast_me`/`channel-join`/
   `channel-part` werden **ignoriert** (Presence, nicht benötigt).
4. Der Polling-Weg (Phase 1, TTL) bleibt als Safety-Net und
   Initial-Listing — der WS ist nur ein Beschleuniger.
5. Offene Punkte, **vor** Umsetzung klären:
   - Keepalive: In 45 s wurden keine Frames ohne Aktivität gesehen
     (auch kein Server-Ping). Reconnect-Überwachung (Read-Deadline)
     muss definiert werden; ggf. eigener Ping-Interval.
   - `global-emotes`/`badges`-Events: Format unklar; wird in Phase 2
     nicht benötigt (Globale bleiben Polling).
   - Verhalten bei unbekannten `name`-Werten: loggen und ignorieren.
6. Tests: Fake-WS-Server (coder/websocket) mit event create/update/delete;
   Reconnect-Test; Catalog-Update-Tests.

**Abnahme Phase 2:** Emote-Änderung in einem joinenden Test-Channel
scheint im Catalog ohne TTL-Warten auf; WS-Abbruch führt zu Reconnect,
Polling deckt die Lücke.

---

## 3. Entscheidungen (bitte bei Abnahme bestätigen)

- **E1:** `imageType`/animierte Emotes werden nicht modeliert (das
  `emote.Emote`-Modell bekommt kein Animationsfeld).
- **E2:** 404 bei `/2/channels/{login}` = „keine Emotes", leere Liste,
  keine Warnung (konsistent mit FFZ).
- **E3:** Phase 2 ist **optional** — Standard-Empfehlung: erst Phase 1
  mergen, Phase 2 nur bei echtem Bedarf an Live-Emote-Updates.
- **E4:** BTTV bleibt eine von mehreren gleichberechtigten Sources im
  Catalog (keine Sonderstellung, keine Priorisierung).

## 4. Aufwand & PR-Aufteilung

| PR | Inhalt | Aufwand |
|---|---|---|
| 1 (Pflicht) | Phase 1: REST-Source reparieren + Tests + Test-Matrix | S |
| 2 (optional) | Phase 2: WS-Client + Catalog-Updates + Tests | M |

Stack-Regel: PR 2 baut auf PR 1 (einer Stack, PR 2 erst nach Abnahme).

## 5. Risiken / unbekannt

- **API-Versionsstabilität:** BTTV hat die API-Generationen schon zweimal
  gewechselt (`/3` → geparkte `api.bttv.com` → `/2` auf alter Domain).
  Doku und Produkt können wieder driften → 404/Shape-Drift ist der
  erwartete Ausfallmodus; Catalog-Fallback hält die letzte gute Liste.
- **WS-Semantik:** Presence-Lastigkeit des Sockets (nur Events bei
  Aktivität) ist aus Beobachtung, nicht aus garantierter Doku — Phase 2
  setzt den Polling-Fallback deshalb zwingend voraus.
- **Rate Limits:** nicht dokumentiert, nicht beobachtet; der
  `httpclient`-Breaker mit 429-Backoff ist vorhanden.
- `cdn.betterttv.net`-Erreichbarkeit in diesem Netz verifiziert (HEAD 200),
  `api.betterttv.net` ebenfalls.
