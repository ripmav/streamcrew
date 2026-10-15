# Plan: Phase 5, Teil 5.2.5 – Ausschlüsse von Bots und Streamer

Status: **abgeschlossen**

**Goal:** Roadmap 5.2.5 (S): Ranglisten und die Zufallsauswahl eines Nutzers
schließen aus, wer nicht dazugehört — Nutzer mit der Ausnahme (B7), der
Streamer und der Bot. Die Ranglisten (Top-N über die Statistiken) entstehen
damit erstmals (Verweis von 5.2.1).

## Scope

| # | Item | Source | Size |
|---|------|--------|------|
| 1 | `user.Stat` + `user.Ranked`, Store-Query `TopUsers` (Top-N je Statistik) | Roadmap 5.2.1 (Verweis), 5.2.5 | S |
| 2 | `userservice.Rankings` mit Ausschluss der Channel-Accounts (Streamer, Bot) | Roadmap 5.2.5 | S |
| 3 | Template-Zufallsauswahl (`$randomuser…`) schließt Streamer und Bot aus | Roadmap 5.2.5, template.md B22 | S |

## Design decisions

- **`user.Stat`:** String-Enum mit den fünf gezählten Statistiken
  (`watch_minutes`, `messages`, `commands_run`, `mentions`,
  `streams_watched`); `Valid()` prüft sie. `DonatedCents` (Phase 9) und
  `Strikes` (5.5) haben noch keine Rangliste.
- **`user.Ranked`:** `{User User, Value int64}` — der vollständige Nutzer
  (Anzeigename, Titel …) plus der Wert der Statistik.
- **Store-Query `TopUsers`** (`users.sql`): eine Query mit `CASE` über die
  Statistik; `WHERE u.excluded = 0` (B7 gilt schon in der Query), nur Werte
  `> 0` (Top-Listen ohne Null-Werte, Festlegung), `ORDER BY value DESC, id`
  (gleichstand deterministisch), `LIMIT n`. Der Store lädt die Nutzer im
  selben `readTx` über den bestehenden `loadUser`.
- **Ausschluss der Channel-Accounts:** Die Datenbank weiß nicht, welches
  Konto Streamer und Bot ist (Zustand der Adapter), also entscheidet der
  Service: neuer optionaler Port `AccountUsers func(ctx) ([]id.ID, error)`
  (nil = kein Ausschluss). `Rankings` holt `n + len(accounts)` aus dem
  Store und filtert die Account-Nutzer weg; das Ergebnis hat höchstens `n`
  Einträge.
- **Wiring:** `app.accountUserIDs` fragt je Plattform des Profils (neue
  `Set.All()`/`platformSet.All()`, nicht nur die verbundenen — Statistiken
  leben über Sessions) über `connector.Identities` die Accounts
  `streamer`/`bot` ab und löst sie über `store.UserByIdentity` auf; nicht
  gespeicherte Accounts fallen weg.
- **Zufallsauswahl (template):** `randomUser` holt über das bestehende
  `template.Users.Account` die Accounts Streamer und Bot und nimmt sie aus
  den Kandidaten heraus (neben `Excluded`, B7). Abweichung von B22
  ("Streamer und Bot sind wählbar") nach Roadmap 5.2.5; die Spezifikation
  template.md wird angepasst (Neufassung: ausgenommen sind B7, Streamer und
  Bot; neue Regel B28 in users-and-roles.md).

## Files

- `internal/domain/user/ranking.go` (neu): `Stat`, `Valid`, `Ranked`; Tests.
- `internal/store/queries/users.sql`: Query `TopUsers`; `sqlc generate`.
- `internal/store/users.go`: `Store.TopUsers`; Integrationstest in `users_test.go`.
- `internal/userservice/userservice.go`: Port `AccountUsers`, Methode `Rankings`,
  `TopUsers` im Store-Interface; Test `TestRankings` (memStore + Port).
- `internal/connector/set.go` + `internal/app/wiring.go`: `All()`,
  `app.accountUserIDs`; `internal/app/app.go`: Port an den User-Service.
- `internal/template/users.go`: `randomUser` schließt die Accounts aus; Test.
- `docs/roadmap.md` (5.2.5 + Status), `docs/spec/users-and-roles.md` (B28,
  Akzeptanz, Änderungshistorie), `docs/spec/template.md` (B22,
  Änderungshistorie), dieses Planfile.

## Tests

| Test | Level |
|------|-------|
| `Stat.Valid`: alle gültigen Kennungen, Unbekanntes | unit (domain) |
| `TopUsers`: Reihenfolge nach Wert, Gleichstand nach ID, `excluded` außen vor, Null-Werte außen vor, `LIMIT`, ungültige Statistik / `n` | integration (SQLite) |
| `Rankings`: Account-Nutzer fehlen, die Liste bleibt voll (n Einträge), ungültige Argumente | unit (userservice, memStore) |
| Template: `$randomuser…` wählt weder Streamer noch Bot (auch wenn nur sie chattern → kein Wert) | unit |

## Open questions

(keine — Entscheidungen oben dokumentiert)
