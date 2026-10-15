# Plan: 5.2.4 Konten verknüpfen und Nutzer zusammenführen

Status: **abgeschlossen**

**Goal:** Roadmap 5.2.4 (M, P1): Zwei Nutzer, die derselben echten Person
gehören, lassen sich verknüpfen — die Plattform-Konten (Identitäten) des
einen werden auf den anderen gelegt, und die beiden Nutzer werden zu
einem zusammengeführt. Bisher bleiben zwei Konten mit gleichem Namen auf
verschiedenen Plattformen zwei getrennte Nutzer
([`users-and-roles.md`](../../spec/users-and-roles.md), B40), weil sich
Namen nicht als Schlüssel eignen (B3: Schlüssel ist
Plattform + Plattform-ID).

## Design decisions

- **Befehl statt API:** Die API kommt mit Phase 6; bis dahin ist die
  Befehlszeile die Steuerfläche für Profildaten. Neuer Befehl
  `streamcrew user merge <beibehalten-plattform> <beibehalten-login>
  <zusammenführen-plattform> <zusammenführen-login>` (neue `user`-Gruppe).
  Jede Seite wird über Plattform + Login aufgelöst
  (`store.UserByLogin`), eindeutig ohne Plattform-Suche.
- **Semantik (Zusammenführung):** Alle Identitäten von
  `<zusammenführen>` wandern auf `<beibehalten>`
  (`UPDATE user_identities SET user_id = …`); die Statistiken werden
  addiert (Watchtime, Nachrichten, Commands, Erwähnungen, Streams,
  Spenden, Strikes; `first_seen` = Minimum, `last_seen` = Maximum); Titel,
  Notizen, Ausschluss, Regular und Begrüßung bleiben die von
  `<beibehalten>`, dessen `updated_at` wird berührt; `<zusammenführen>`
  wird gelöscht (seine Statistiken mit). Alles in einer Transaktion.
- **Grenze B3:** Ein Plattform-Konto gehört genau einem Nutzer; die
  Datenbank erzwingt das (Primärschlüssel Plattform + Plattform-ID).
  Der Merge prüft den Konflikt zusätzlich defensiv und lehnt ab; gleiche
  Logins auf *verschiedenen* Plattformen sind kein Konflikt (das ist
  genau der zu lösende Fall).
- **Kein Service-Port:** Das Merge ist eine Datenoperation, kein Port der
  Phase 3. Die Befehlszeile öffnet die Profil-Datenbank direkt (wie die
  anderen profilverändernden Befehle, z. B. `profile channels`) und ruft
  `store.MergeUsers` auf; der Core muss nicht laufen. Die API (Phase 6)
  nutzt dieselbe Store-Methode.
- **Fehler:** Unbekannter Login, beide Seiten derselbe Nutzer und
  ungültige Plattform sind Usage-Fehler; ein erfolgreiches Merge gibt den
  zusammengeführten Nutzer aus.

## Files

- `internal/store/queries/users.sql` + `sqlc generate`: neue Query
  `MoveIdentitiesToUser`.
- `internal/store/users.go` + Test: `MergeUsers(keep, drop id.ID)
  (user.User, error)` — B3-Check, Move, Statistiken addieren (min
  first_seen / max last_seen), `UpdateUser` + `DeleteUser`; Tests
  (Zwei-Plattform-Fall, fehlender Nutzer, gleicher Nutzer).
- `internal/cli/user.go` + Test: neue Gruppe `user` mit
  `user merge <beibehalten-plattform> <beibehalten-login>
  <zusammenführen-plattform> <zusammenführen-login>` (profil-ändernd,
  Datenverzeichnis-Lock wie die anderen profilverändernden Befehle),
  Ergebnis als Text.
- `docs/spec/users-and-roles.md` (B40, Änderungshistorie),
  `docs/roadmap.md` (5.2.4), dieses Planfile.

## Tests

| Test | Level |
|------|-------|
| `MergeUsers`: Identitäten wandern, Statistiken addiert (min/max first/last_seen), der gelöschte Nutzer ist weg, fehlender Nutzer = Fehler, gleicher Nutzer = Fehler | unit (store) |
| `user merge`-Befehl: erfolgreiche Zusammenführung, gleicher Nutzer, unbekannte Login, ungültige Plattform | unit (cli) |

## Open questions

(keine — Entscheidungen oben dokumentiert; die Addition der Statistiken ist
die bewusste, einfachste und dokumentierte Wahl)
