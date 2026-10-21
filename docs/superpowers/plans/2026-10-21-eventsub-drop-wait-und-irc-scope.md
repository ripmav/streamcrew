# Plan: EventSub- und IRC-Fixes aus der Log-Analyse 2026-10-21

**Status:** offen
**Stand:** 2026-10-21, `main` bei `91f120c` (PR #310)
**Begründung:** Analyse von `~/streamcrew/logs/2026-10-21T20:11:59+02:00.log` —
Details in `docs/analyse-log-fehler-2026-10-21.md` (Findings F1–F5, Doku-Quellen
der Twitch-Doku)
**Umfeld:** User- und Bot-Login neu verbunden, App-Credentials neu gesealed
(Commit auf `fix/irc-scope`), EventSub-403 = IP-basierter WAF-Block (Analyse
2026-10-20, fortbestehend)

Vier Pläne, Priorität steht bei jedem; Reihenfolge oben nach Priorität.

---

## Plan A — EventSub-Retry: fester 10-s-Wart (Priorität 1, Bug-Fix)

**Problem (F1):** `Client.Run` in `internal/twitch/ws.go` verwirft den
Stop-Flag von `backoff.Next()` (`d, _ :=`). go-retry v0.5.0
`WithMaxDuration(30s)` liefert nach 30 s `(0, true)` = „aufhören";
`time.After(0)` feuert sofort ⇒ Busy-Loop: ~286 Warn-Logs/s, CPU-Spin,
≈ 600 MB Log/Tag (Log-Beweis: Loop-Start exakt 30 s nach erstem `Next()`).

**Ziel:** EventSub-Connection down ⇒ **10 Sekunden warten**, dann erneut
dialen. Circuit Breaker (Code-ADR-0007) bleibt und sein `retry-after` wird
respektiert.

**Branch:** `fix/eventsub-drop-wait` (neuer Session-Branch auf `main`).

### Schritte

- [ ] `internal/twitch/ws.go` `Run()`: `retry.WithMaxDuration(30s,
  WithFullJitter(NewExponential(1s)))` und den `go-retry`-Import entfernen;
  `const dropWait = 10 * time.Second` + `Options.DropWait` (Default
  `dropWait`) für Tests.
- [ ] Wartezeit pro Drop: `dropWait`; bei `breaker.ErrUnavailable` mit
  `RetryAfter` in der Zukunft: `max(dropWait, RetryAfter - now)` (Breaker 30 s
  offen ⇒ 1–2 Versuche/min statt 286/s).
- [ ] `session_reconnect` bleibt `immediately: true`.
- [ ] Kommentar in `Run` aktualisieren (heute verweist er auf Code-ADR-0014
  Point 4 „base 1 s, full jitter, 30 s budget" — stimmt danach nicht mehr).
- [ ] Tests `internal/twitch/ws_test.go` (Fake-Clock vorhanden):
  `TestRunDialFailureRetriesAtTheBaseURL` auf 10-s-Wart umstellen;
  **Busy-Loop-Regression:** Breaker offen, `RetryAfter` +30 s ⇒ vor
  `RetryAfter` kein zweiter Dial; 10-s-Wart gilt auch nach einer zuvor
  erfolgreichen Session (kein Lifetime-Budget mehr).
- [ ] `rg 'backoff.Next'` über alle Aufrufstellen: prüfen, dass nirgends
  anders der Stop-Flag verworfen wird (`internal/breaker`,
  `internal/httpclient` bleiben sonst unverändert).
- [ ] Doku: `docs/adr/code/0015-websocket-bibliothek.md` — Retry-Verhalten
  (fester 10-s-Wart, `session_reconnect` sofort, Breaker-`retry-after`
  respektiert); `docs/analyse-log-fehler-2026-10-21.md` F1 abhaken.
- [ ] Test-Matrix `docs/tests/I-eventsub-drop-wait.md`.

### Verifikation

- [ ] `go build ./... && go test ./internal/twitch/... ./internal/breaker/...`
- [ ] Feldtest: Daemon-Run mit blockiertem EventSub ⇒ Log-Abstand
  „connection is down" ~10 s bzw. Breaker-Pausen ~30 s, CPU ~0.

---

## Plan B — IRC: Scope `chat:read` nachreichen (Priorität 2, klein)

**Problem (F3):** Doku `dev.twitch.tv/docs/chat/irc` („Authenticating with the
Twitch IRC server"): IRC verlangt einen **User Access Token** mit
**`chat:read`** (lesen) und **`chat:edit`** (senden). `Scopes` in
`internal/auth/twitch.go` hat `chat:edit`, aber nicht `chat:read`. Der
„Login unsuccessful" selbst ist ein Token-Problem (ungültig/abgelaufen) —
User + Bot sind neu connected, sollte durch den Re-Connect weg sein;
`chat:read` ist laut Doku trotzdem Pflicht.

**Branch:** bestehend `fix/irc-scope` (HEAD `b154b40`), dort liegt auch der
uncommitted neu gesealete `internal/appcreds/embedded.go`.

### Schritte

- [x] `internal/auth/twitch.go`: `"chat:read"` zu `Scopes` ergänzen (neben
  `chat:edit`).
- [x] `internal/appcreds/embedded.go` (neu gesealed) + Scope-Change commiten
  (Attribution-Footer; PR-Body mit Doku-Quelle).
- [ ] Bot neu autorisieren — Scope-List gewachsen, Login wird laut ADR-0014
  „required" bis zum Re-Login.
- [x] Doku: Scope-Tabelle in ADR-0014 um `chat:read` ergänzen.

### Verifikation

- [ ] `streamcrew auth status`: Bot-Login mit `chat:read` in den granted
  scopes.
- [ ] Daemon-Run: kein „Login unsuccessful", Channel-Joins kommen durch.
- [ ] Falls es trotz frischem Token + `chat:read`/`chat:edit` persistiert:
  IP-Problem → Plan C Schritt 1 (A/B-Test per Hotspot).

---

## Plan C — EventSub 403 auflösen (Priorität 3, extern, kein Code)

**Problem (F2):** 5× Handshake-403, fortbestehend aus der Analyse
2026-10-20: IP-basierter WAF-Block am CloudFront-Edge von
`eventsub.wss.twitch.tv` (jede Anfrage mit nicht-leerem Query-String → 403
„Request blocked"; `api.twitch.tv` und `irc-ws.chat.twitch.tv` sind frei
erreichbar). Doku-Check 2026-10-21: Close-Codes 4000–4007 sind
Nach-Verbindungs-Fehler; ein 403 auf dem Handshake ist kein
dokumentierter Anwendungsfehler. Der WS-Handshake verlangt einen **User
Access Token** (kein App Access Token).

### Schritte

- [ ] **A/B-Test mit anderer IP:** Router kurz stromlos (dynamische
  Telekom-IP) oder Smartphone-Hotspot — der definitive Test.
- [ ] Falls auch von anderer IP 403: Token-Typ prüfen (User Access Token,
  kein App Token) und frisch reconnecteden Streamer-Token verwenden.
- [ ] Falls Tage überdauert: Twitch-Support/Forum mit dem
  CloudFront-Request-ID aus der Analyse 2026-10-20:
  `wDsw1Y9cfICXAj-KKs2YB9sfkH6QNvkcJUkZLzN56FRonqGVJ-LllQ==`.
- [ ] Parallel greift Plan A: bei Dauerblockade bleibt der Daemon ruhig
  (10-s-Rhythmus) statt CPU/Log zu fressen.

---

## Plan D — Emote-Source „twitch" ersetzen/entfernen (Priorität 4, offen)

**Problem (F4):** `GET /helix/emotes` ist von Twitch entfernt (5×404 je
Kanal im Log, graceful via „keeping the last list"). Offener Folge-Fix aus
`docs/analyse-log-fehler-2026-10-20.md`, Abschnitt C.

### Schritte

- [ ] Entscheidung: twitch-Source in `internal/emote` **entfernen** (Emotes
  kommen mit den Chat-Events + BTTV-Socket) **oder** auf
  EventSub-`message`-Emote-Daten umstellen.
- [ ] Umsetzung + Tests + Doku (emote-Source-Doku/README) + Test-Matrix.

---

## Reihenfolge (nach Priorität)

| # | Plan | Typ | Branch |
|---|---|---|---|
| 1 | A — EventSub 10-s-Wart | Bug-Fix (Code) | `fix/eventsub-drop-wait` (neu, auf `main`) |
| 2 | B — Scope `chat:read` | Fix (Code + Re-Auth) | `fix/irc-scope` (bestehend) |
| 3 | C — EventSub 403 / IP | extern, kein Code | — |
| 4 | D — Emote-Source | Feature-Aufräumen | später, eigener Branch |

## Offene Punkte / nicht verifiziert

- Ob `chat:read` allein (ohne `user:read:chat`/`user:write:chat`)
  IRC-Lesen genügt — Legacy-Scopes bleiben zusätzlich in der Liste
  (übrige Features brauchen sie).
- Ob der WAF-Block (F2) auch `irc-ws.chat.twitch.tv` trifft, wenn er
  ausgeweitet wird (IRC-Tests 2026-10-20 sahen IRC frei).
- Warum genau diese IP geflaggt wurde — sagt nur Twitch.
