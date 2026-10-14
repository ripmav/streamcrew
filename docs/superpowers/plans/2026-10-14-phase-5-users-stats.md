# Plan: Roadmap 5.2.1 — Aktive Nutzer, Watchtime, Statistiken

**Status:** abgeschlossen (Tasks 1–4 erledigt, Stack #233)
**Stand:** 2026-10-14, `main` bei `5351093`
**Scope:** nur 5.2.1 (Nutzer-Service mit Aktivität, Watchtime und
Statistiken). Regular (5.2.2), Titel (5.2.3), Ausschlüsse (5.2.5) und die
Ports (5.2.6) bleiben offen.

## 1. Was 5.2.1 liefert (Roadmap)

- [ ] Aktive Nutzer (Join, Leave, Aktivität) (M)
- [ ] Watchtime (nur während live) (M)
- [ ] Statistiken wie Nachrichten, Commands und Tags (M)

## 2. Befunde

1. **Die Daten und die Ablage stehen schon.** `user.Stats` (B9) ist komplett
   in `user_stats` (Migration 0003); `store.UpdateUser` und die
   `user.Repository`-Methoden existieren. Nie gesetzt werden aber
   `FirstSeen`, `LastSeen`, `WatchMinutes`, `Messages`, `CommandsRun`,
   `Mentions` und `StreamsWatched` — die Templates lesen `LastSeen` bereits
   (`internal/template`, userprops).
2. **Die Ereignisse stehen schon.** `eventservice` veröffentlicht
   `chat.user.join` (einmal je Sitzungs-Nutzer), `chat.user.leave`,
   `chat.message`, `channel.stream.start/stop`; die Engine
   `command.instance.completed` mit `Instance.UserID`. Der Bus ist
   nicht-blockierend; ein Abonnent im Stil `eventservice` passt.
3. **Der Zustand der Streams steht.** `stream.Session` je Plattform mit
   `StateLive`/`StateGrace`/`StateOffline` im Speicher
   (`store.StreamSessions`); `eventservice` ist der Einzige, der sie führt.
4. **Es gibt keinen Ort für den Nutzer-Service.** `app.userLookup` ist nur
   eine Suchhilfe; `internal/action/users` ist die Action `user_lookup`.
   Phase 5.2 braucht einen dauerhaften Service — Name nach
   `eventservice`: `internal/userservice`.

## 3. Design

1. **Neues Paket `internal/userservice`** mit einem Runnable `Service`
   (Supervisor-Runnable, Code-ADR-0004). Ports:
   - `Store`: `AddStats` (Batch-Updates der Zähler von `user_stats`, dazu
     `FirstSeen`/`LastSeen`), `StreamSessions`
   - `Bus`: Abonnate für die Ereignisse von Punkt 2.1
   - Logger
2. **Aktive Nutzer:** je Plattform eine Menge pro Stream-Sitzung.
   `chat.user.join` und `chat.message` setzen die Aktivität, `chat.user.leave`
   entfernt sie. „Aktiv“ heißt: in der Sitzung angekommen und nicht
   gegangen. Eine öffentliche Abfrage `Active(p)` liefert ID, Login,
   Anzeigename und letzte Aktivität — die Grundlage für Ranglisten (5.2.5),
   Session-Statistiken (5.3) und den Timer (5.4).
3. **Watchtime:** ein Ticker je Minute. Je Plattform, deren Sitzung
   `live` ist (nicht `grace`, nicht `offline`), bekommt jeder aktive Nutzer
   eine Minute Watchtime; die Batch-Updates laufen in einer Transaktion.
   Bei Stopp bleibt der Rest der Minute weg (Entscheidung: nur ganze
   Minuten, wie B9 „Watchtime in Minuten“).
4. **Statistiken** (B9):
   - `Messages` und `LastSeen`: `chat.message` (der Bot-Konto-Filter steht
     bereits in der Pipeline).
   - `StreamsWatched`: `chat.user.join`, wenn die Plattform gerade live ist.
   - `FirstSeen`: erstes `chat.user.join` oder `chat.message`.
   - `CommandsRun`: `command.instance.completed` mit nicht leerem
     `Instance.UserID` (nur abgeschlossene Instanzen; fehlgeschlagene und
     abgebrochene zählen nicht — Entscheidung).
   - `Mentions`: je `chat.message` die aktiven Nutzer derselben Plattform,
     deren Login im Text mit `@` vorkommt (schreibweiseunabhängig für ASCII;
     der Absender selbst nicht). Die Roadmap nennt „Tags“; nach B9 ist das
     die Zählung der Erwähnungen.
   - `WatchMinutes`: wie Punkt 3; `DonatedCents` und `Strikes` bleiben bei
     Phase 9 bzw. 5.5.
5. **Store-Methode `AddStats`** mit sqlc-Query `AddUserStats`:
   `first_seen = COALESCE(first_seen, ?)`,
   `last_seen = COALESCE(?, last_seen)`, Zähler additiv; Batch in einer
   `Write`-Transaktion.
6. **Anschluss an die App:** `internal/app` baut den Service mit
   `store`, `bus` und Logger und registriert ihn beim Supervisor; die
   Shutdown-Reihenfolge braucht nichts Besonderes (kein Zustand, der
   verloren geht, außer dem Rest der laufenden Minute).

## 4. Tasks

- [x] T1: sqlc-Query `AddUserStats` + `store.AddStats` + Integrationstest
- [x] T2: `internal/userservice`: Aktivität, Statistiken, Watchtime-Ticker,
  `Active(p)`; Tests mit Fake-Store und `testing/synctest`
- [x] T3: Wiring in `internal/app`, E2E-Test über die Mock-Plattform
- [x] T4: Doku (Roadmap 5.2.1 abhaken, Spezifikation
  `users-and-roles.md` um Akzeptanzkriterien ergänzen), Pre-Commit-Checkliste

## 5. Festlegungen beim Arbeiten

- **Erwähnungen nur unter Aktiven:** `Mentions` zählt je Nachricht die
  anderen aktiven Nutzer derselben Plattform, deren Login mit `@` vorkommt
  (ASCII-schreibunabhängig, nicht als Präfix eines längeren Logins).
- **`StreamsWatched` nur bei live:** `chat.user.join` zählt nur, wenn die
  Sitzung der Plattform gerade `live` ist.
- **`CommandsRun` nur bei `completed`:** fehlgeschlagene und abgebrochene
  Instanzen zählen nicht (B9 „Zahl ausgeführter Commands").
- **Watchtime ohne Restminuten:** bei Stopp geht der Rest der laufenden
  Minute verloren; nur ganze Minuten zählen (B9).
