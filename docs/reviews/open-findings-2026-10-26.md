# Offene Findings: Zuordnung zu Phase 6 (Code-Review 2026-10-24)

- **Grundlage:** [`code-review-2026-10-24.md`](code-review-2026-10-24.md) und
  [`open-findings-2026-10-25.md`](open-findings-2026-10-25.md)
- **Geprüft:** Branch `fix/code-review-2026-10-24` bei `d7f7277`. Jede
  Fundstelle aus dem Review wurde im aktuellen Code nachgesehen; keins der
  zurückgestellten Findings ist inzwischen erledigt.
- **Zuordnung:** Maßstab ist das Exit-Kriterium von M2 (Phase 6): ein echter
  Twitch-Kanal ist ohne GUI betreibbar, mit API `v1alpha1`, CLI, TUI und dem
  ersten Release. Die Zuordnung ist ein Vorschlag und steht so noch nicht in
  der [Roadmap](../roadmap.md).
- **Wichtigkeit:** die Severity aus dem Review (Mittel, Niedrig, Info);
  „Unter Schwelle“ steht für die Punkte aus Review §7.

## 1. Bis Ende Phase 6 (M2)

### Twitch im Echtbetrieb (Phase 4/5 abgehakt, aber fehlerhaft)

- [x] T5 – **Mittel** – Keine Whispers/Replies über Twitch: Der Twitch-Chat setzt `connector.Replier`/`Whisperer` nicht um, obwohl Roadmap 5.1 erledigt ist.
- [x] T6 – **Mittel** – Der Stream-Status wird nach einem EventSub-Reconnect nicht neu gemeldet (`streamState` nur einmal je `channelSession`, `internal/twitch/platform.go:753`).
- [x] T7 – **Mittel** – EventSub: Die Reader-Goroutine blockiert ohne Abbruch auf `in` und leckt je Reconnect; Frames der alten Session gehen beim Warten auf den neuen Welcome verloren (`internal/twitch/ws.go:404-414`).
- [x] T8 – **Mittel** – `channel.moderate` ist nur für Ban/Timeout gemappt; Unban, Untimeout, Mod, VIP usw. landen als Warn „unmapped“ (`internal/twitch/map.go:215-240`).
- [x] T9 – **Mittel** – Der Event-Service hält seinen globalen Mutex während Netzwerk-I/O (Helix-Delete, `Moderation.Check`); ein langsamer Helix-Call blockiert alle Plattformen.
- [x] T10 – **Mittel** – Anonyme Cheers werden verworfen: Die leere Identität scheitert an `checkAccount` (`internal/eventservice/receive.go:647`).
- [x] T11 – **Mittel** – Die Follow-Zeit für die Teilnahmeregel kommt nur aus dem Cache des Heimkanals; neue Chatter gelten als „nicht erfüllt“ (`internal/moderation/moderation.go:206`).
- [x] T13 – **Niedrig** – IRC-Lebenszyklus: Der Backoff geht über das Maximum (32 s > 30 s) und wird nie zurückgesetzt, `streamStatePoll` startet je Reconnect einen weiteren Poller, Close-Goroutine und Welcome-Timeout.
- [x] T14 – **Niedrig** – IRC: `Send` entfernt kein CR/LF (ein Template-Zeilenumbruch kann IRC-Befehle einschleusen), Tag-Werte werden nicht unescaped, nur der 20/30-s-Bucket.
- [x] T15 – **Niedrig** – `Manager.Reconcile` hat keinen Mutex und kann parallel laufen (Ticker, Session- und Revocation-Callbacks).
- [x] IRC-CRLF – **Unter Schwelle** – `IRCSession.Send`/`writeLine` entfernen CR/LF nicht; zusammen mit T14 beheben.
- [x] Rollen – **Unter Schwelle** – Badges aus Zusatzkanälen werden global zu `Streamer`/`Moderator`; Rollen pro Kanal ableiten (users-and-roles.md B25).

### API, CLI, TUI (Phase 6)

- [x] S5 – **Mittel** – API-Tokens liegen global in `<data-dir>/api-tokens.json` statt in der Profil-DB, ohne ADR; README.md:330/377 behauptet „pro Profil, verschlüsselt“ und „ohne Token offen“. Weicht vom ADR ab: vorher den Besitzer fragen.
- [x] S6 – **Mittel** – Die CLI-API-Befehle nutzen weder `runtime.json` noch den Unix-Socket (Standard fest `http://127.0.0.1:8740`, `internal/cli/api.go:24`).
- [x] U4 – **Mittel** – `apiCounters.Create` ruft `CheckReserved` nicht auf; ein Counter `date` überschattet `$date` (`internal/app/api_wiring.go:95`).
- [x] U5 – **Mittel** – Fehler-Mapping: Decimal-Syntax, `id.Parse`, `role.Parse`, unbekannte Statistik, Settings-Validierung, `ErrReserved` und `ErrLoginInProgress` werden zu `CodeInternal` (HTTP 500) (`internal/api/counter.go:51`).
- [x] U6 – **Mittel** – TUI-Stream ohne Reconnect, stille Verluste ab 64 Events, `ctx` ist `context.Background()` (`internal/tui/model.go:151-154`).
- [x] S12 – **Niedrig** – Doppelte Token-Namen erlaubt, `Revoke(name)` entfernt alle; Read-Modify-Write nur prozessintern gesperrt; offene Streams überleben eine Rücknahme.
- [x] C11 – **Niedrig** – `checksums.txt` deckt nicht alle Release-Artefakte ab (Image-Tar, SBOM, Quell-Zip und systemd-Dateien werden nach goreleaser hochgeladen); betrifft den ersten Release-Tag (6.7).

### Commands und Actions

- [x] A1 – **Mittel** – `command_admin update` meldet Erfolg und speichert, auch wenn der Command keine Chat-Action hat.
- [x] A2 – **Mittel** – `command_admin find` unterscheidet „keins“, „mehrere“ und Fehler nicht; `add` wertet einen Listing-Fehler als „frei“.
- [x] A4 – **Mittel** – `user_command`-Antworten sind am Command-Namen geschlüsselt; eine Umbenennung (auch nur Groß/Klein) verwaist sie (`internal/action/usercommand/usercommand.go:154`).
- [x] A9 – **Niedrig** – `addcmd !hello` erzeugt `!!hello` (der Trigger wird ungekürzt mit `TriggerExclamation` übernommen); `find` und `Save` sind nicht atomar.

## 2. Nach Phase 6

### Phase 7 (Overlays) bzw. Web-Track

- [ ] S8 – **Niedrig** – Scope `overlay` zählt als `read` und darf personenbezogene Daten und alle Command-Definitionen lesen. *Seit 2026-10-29 Teil von 341-6c in [`review-stack-343-2026-10-28.md`](review-stack-343-2026-10-28.md) (WP2): Overlay-Tokens stehen in OBS-URLs, der Scope öffnet künftig nur den Overlay-Server.*
- [ ] S9 – **Niedrig** – Eigener CORS/CSRF-Handler statt `http.CrossOriginProtection`; Same-Origin-POSTs werden bei leerer Allowlist mit 403 abgelehnt.

### Phase 11 (Härtung, API `v1`)

- [ ] S10 – **Niedrig** – Kein h2c, native gRPC-Clients brauchen TLS.
- [ ] S11 – **Niedrig** – Keine API-Version im Handshake (`StatusResponse` hat nur die Core-Version); der Mode-Kommentar im Proto nennt `daemon` nicht.
- [ ] C12 – **Niedrig** – BTTV/FFZ werden immer kontaktiert, ohne Einstellung.

### Hängt an anderen Aufgaben

- [ ] T12 – **Mittel** – Ein EventSub-WebSocket je Zusatzkanal überschreitet Twitch-Limits. Praktisch hinfällig, solange Zusatzkanäle über IRC laufen (E2E-Befund in `twitch-multi-channel.md`).
- [ ] A3 – **Mittel** – Schwellen-Requirement speicherbar, scheitert aber immer mit `ErrNotSupported`; hängt an der Schwellen-Aufgabe aus 3.4.

### Niedrige Priorität, kein M2-Blocker

- [ ] T16 – **Niedrig** – Link-Regex zu weit („yes.Thanks“), Wortfilter kompiliert je Nachricht und ignoriert Satzzeichen.
- [ ] T17 – **Niedrig** – `timer.tick` lädt jede Sekunde alle Commands und Gruppen; Mute-Ausnahme kanalübergreifend; `Event.ID = string(Body)`.
- [ ] U7 – **Niedrig** – `refreshData` ohne Timeout auf der Event-Loop; `watchTick` scannt auch offline.
- [ ] U8 – **Niedrig** – Merge lässt die alte ID in `active`; Rollen-Update nicht atomar.
- [ ] U9 – **Niedrig** – Instabiles `SortFunc` in `DefaultTitle`, `RegularHours` ohne Obergrenze, `FollowedAt` Jahr 1.
- [ ] A5 – **Niedrig** – Race beim Zeitlimit: eine Action, die gerade beim Ablauf `nil` liefert, gilt als gescheitert.
- [ ] A6 – **Niedrig** – Template-Cache-Schlüssel case-sensitiv.
- [ ] A7 – **Niedrig** – Bits-Requirement und Reihenfolge nicht in der Spec.
- [ ] A8 – **Niedrig** – Zufallsgedächtnis wird nie geleert, die Größe eines einzelnen globalen Werts ist nicht begrenzt (die Anzahl schon, `MaxGlobals`).
- [ ] A10 – **Niedrig** – Kleinere Punkte in Web-Request, File-Locks und Engine-Shutdown.

### Querschnitt (laufend)

- [ ] C3 – **Mittel** – Nackte `go`-Anweisungen ohne Besitzer/Warten (u. a. `app/api_auth.go:79`, `app/api_stream.go:120,136`, `app/app.go:933,938`, `httpserver/server.go:171,214`, `tui/model.go:167`).
- [ ] C2 – **Niedrig** – `json.Marshal` teils ohne `Deterministic(true)`.
- [ ] C4 – **Niedrig** – `context.Context` in Structs (`runCtx`), `context.Background()` in Produktionspfaden (`app.go:696`).
- [ ] C5 – **Niedrig** – Fehler in `App.Run` geloggt und zurückgegeben.
- [ ] C6 – **Niedrig** – `errors.As` statt `errors.AsType` an 8 Stellen.
- [ ] C7 – **Niedrig** – `twitchmock` importiert `internal/config`.
- [ ] C8 – **Niedrig** – Paket-Zustand ohne dokumentierte Freigabe; `sync.Once` in `appcreds.go:65`.
- [ ] C9 – **Niedrig** – Magische Werte (`-1` in `expr.closing`, `""` für „alle“).
- [ ] C10 – **Niedrig** – Fehlende SPDX-Header in generiertem Code und `scripts/modulecheck.sh`.
- [ ] D7 – **Niedrig** – Schwache B-ID-Rückverfolgbarkeit in Tests.
- [ ] C13 – **Info** – `core.Options` mit 28 Feldern, breiter als in ADR-0006.
- [ ] D8 – **Info** – 20 Test-Matrizen gegen 207 `_test.go`-Dateien.
