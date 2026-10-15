# Plan: 5.3 Events, Feed, Statistik

Status: **erledigt 2026-10-15** (PR in Stack, auf `main` nach dem Merge von Stack #237)

**Goal:** Roadmap 5.3 (M): die letzten Ereignisse (Follower, Abo, Raid,
Cheer, Spende) werden als Event-Feed gespeichert, aus dem Frontends (API in
Phase 6) lesen; dazu Session-Statistiken je Stream-Sitzung. Der Core
zeichnet auf, was die Event-Pipeline auslöst, und hält die Statistiken der
aktuellen Sitzung aktuell.

## Scope

| # | Item | Source | Size |
|---|------|--------|------|
| 1 | Feed-Tabelle (begrenzte Liste der letzten Feed-Ereignisse) + Session-Statistiken-Tabelle, Migration 0019 | Roadmap 5.3 | S |
| 2 | Aufzeichnung in der Event-Pipeline: Feed-Ereignisse + Session-Statistik inkrementieren | Roadmap 5.3 | M |
| 3 | Lesezugriff für Frontends (Feed + Session-Statistik) als Dienst, für die API (Phase 6) | Roadmap 5.3 | S |

## Design decisions

- **Feed-Typen (plattformneutral):** `channel.follow` (Follower),
  `channel.subscribe` (Abo), `channel.resubscribe` (Re-Abo),
  `channel.subscription.gift` **und** `channel.subscription.mass_gift`
  (Geschenk-Abo), `channel.raid` (Raid), `twitch.bits.cheer` (Cheer).
  Spende (`donation.receive`) kommt mit den Spenden-Integrationen (Phase 9);
  das Schema trägt schon die `cents`-Spalte, die Aufzeichnung folgt dann.
- **Feed-Tabelle `event_feed`:** eine begrenzte Liste der letzten Feed-Ereignisse
  (Standard die letzten 200, beim Einfügen ältere löschen). Spalten:
  `id` (Autoincrement), `platform`, `type` (Ereignistyp), `user_id`
  (optional), `occurred_at`, dazu je Typ eine Zahl/Text: `amount`
  (Bits, Zuschauer, Geschenk-Anzahl), `cents` (Spende), `tier` (Abo-Stufe),
  `text` (Abo-/Cheer-Text).
- **Session-Statistiken `session_stats`:** je (Plattform, Sitzungsbeginn)
  eine Zeile mit Zählern (Follower, Abo, Re-Abo, Geschenk-Abo, Raids, Cheers,
  Spenden) und Summen (Bits, Spende-Cents). Aktualisiert wird die Zeile der
  **aktuellen** Sitzung der Plattform; vor dem ersten Stream-Start
  (kein Sitzungsbeginn) gibt es noch keine Statistiken. Alte Sitzungen
  bleiben erhalten (Verlauf).
- **Aufzeichnung:** die Event-Pipeline zeichnet ein, wenn sie das Ereignis
  tatsächlich auslöst (also nach dem „einmal je Sitzung“-Filter) — nicht bei
  jedem Eingang. Ein Ereignis wird einmal pro Eingang aufgezeichnet (ein
  Massen-Geschenk ist ein Eintrag mit Anzahl). Die Session-Statistik wird nur
  aktualisiert, wenn die Plattform eine Sitzung mit Beginn hat.
- **Lesezugriff:** kein eigener Dienst-Paket (Ponytail): der Store trägt die Lese-Queries –
  `LatestFeed(ctx, n)` (die letzten Feed-Ereignisse, älteste zuerst),
  `CurrentSessionStats(ctx, p)` (Statistik der aktuellen Sitzung; eine Sitzung
  ohne Ereignisse gibt Null-Zähler), `SessionStats(ctx, p, start)` (nach dem
  Sitzungsbeginn) und `RecentSessions(ctx, p, n)` (Verlauf). Die API (Phase 6)
  liest über diese Store-Queries, wie die CLI über den Store liest.

## Files

- `internal/store/migrations/0019_feed_sessions.sql` + `sqlc generate`:
  Tabellen `event_feed`, `session_stats`; Queries
  `InsertFeedEvent`, `PruneFeed`, `LatestFeed`, `AddSessionStats`,
  `GetSessionStats`, `ListSessionStats`.
- `internal/domain/feed/feed.go` + Test: `Event` und `SessionStats`
  (Domänenwerte) + `FromPayload`-Kartierung der Ereignistypen und
  `StatsDelta`.
- `internal/store/feed.go` + Test: `RecordFeedEvent` (Einfügen + Pruning +
  Session-Statistik, eine Transaktion), `LatestFeed`, `CurrentSessionStats`,
  `SessionStats`, `RecentSessions`.
- `internal/eventservice`: Store-Port um `RecordFeedEvent`; in `Event` nach
  dem Sitzungsfilter aufzeichnen (Feed + aktuelle Sitzungsstatistik),
  Geschenke im Geschenkweg.
- `docs/roadmap.md` (5.3), `docs/spec/events.md` (Feed/Statistik,
  Änderungshistorie), dieses Planfile.

## Tests

| Test | Level |
|------|-------|
| `RecordFeedEvent`: Feed-Eintrag, Pruning auf die letzten N, Session-Statistik inkrementiert (je Kategorie + Bits/Cents), ohne Sitzungsbeginn nur Feed ohne Statistik | unit (store) |
| `feed`-Kartierung: je Feed-Typ die Felder (Amount/Cents/Tier/Text) und `StatsDelta` | unit (domain) |
| Event-Pipeline: ein Follow landet im Feed und in der Session-Statistik; ein zweites identische Sitzung-Ereignis (Filter) nicht | unit (eventservice) |
| Store-Lesen: `LatestFeed` (älteste zuerst), `CurrentSessionStats` (Sitzung ohne Ereignisse = Null), `RecentSessions` (neueste zuerst) | unit (store) |

## Open questions

(keine — Entscheidungen oben dokumentiert; Spende wird mit Phase 9 aufgezeichnet)
