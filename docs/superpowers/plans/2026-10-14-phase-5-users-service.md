# Plan: Phase 5, Teil 5.2.6 – Nutzer-Service als Umsetzung der Ports

Status: **abgeschlossen**

**Goal:** Roadmap 5.2.6 (M): Der Nutzer-Service in `internal/userservice`
wird die Umsetzung der Phase-3-Ports und löst das `app.userLookup`-Gerüst
ab: Nutzer nach Login-Name (`UserByName` von Engine, Templates und
Actions) **inklusive der Suche auf der Plattform für Unbekannte** (die
heute die Actions selbst machen), damit das Merken und Wiederholen der
Durchlauf-Suche (command-engine.md, B17) auch sie erfasst; die Konten von
Streamer und Bot (`template.Users`); das Speichern über eine Plattform
gefundener Nutzer (`UpsertIdentity`, actions.md, B91); Strikes ändern und
zurücksetzen (`moderation.Strikes`, B84).

## Scope

| # | Item | Source | Size |
|---|------|--------|------|
| 1 | `Service.UserByName` = B91-Suche (bekannt: Name → Konten → ID, dann Plattform Name → ID, Speichern; B92-Limit) | Roadmap 5.2.6, command-engine.md B17, actions.md B91/B92 | M |
| 2 | `Service` implementiert `template.Users` (`Account`, `Chatters`), `requirement.Streamer` (`StreamerUser`), `UpsertIdentity` (B91), `moderation.Strikes` (`UpdateUser`, `ResetStrikes`) | Roadmap 5.2.6 | S |
| 3 | Actions (Nutzersuche, Moderation) und App: Plattformsuche + Limit aus den Actions raus, alles über den Service; `app.userLookup` entfällt | Roadmap 5.2.6 | M |

## Design decisions

- **`UserByName` (B91, B17):** Reihenfolge wie heute in der
  Nutzersuche: Login-Name unter den bekannten (`store.UserByLogin`),
  dann die Channel-Konten (Streamer, Bot) mit dem Namen, dann die
  Plattform-ID unter den bekannten (`store.UserByIdentity`), dann die
  Plattform (`Users().UserByLogin`, bei `ErrUnknownUser`
  `Users().UserByID`); ein gefundenes Konto wird gespeichert
  (`UpsertIdentity`, B91/B4). `ErrUnknownUser` = kein Fund ohne Fehler
  (die B17-Suche merkt Nicht-Funde nicht und sucht bei Bedarf erneut);
  ein Plattform-Fehler ist ein Fehler (B17 wiederholt ihn). Damit sucht
  auch die Zielauflösung der Engine (B81) über die Plattform.
- **`Lookup` (nicht speichernd):** die gleiche Suche wie `UserByName`,
  aber ohne das Speichern eines über die Plattform gefundenen Kontons.
  Die Moderations-Kinds, die auf einer Plattform wirken (Timeout, Ban,
  Mod, …), erzeugen dafür keine Nutzer (actions.md, B81); Strikes und
  die Nutzersuche speichern dagegen (B84, B91). Beide teilen sich das
  B92-Limit im Service.
- **B92-Limit im Service:** Das Limit „eine Suchanfrage an die Plattform
  je Minute, über alle Suchen und Plattformen, zählt nur Suchen, die die
  Plattform erreichen“, zieht in den Service (ein Globaler je Profil,
  vorher nur in der Nutzersuche-Action). Verweigert = kein Fund ohne
  Fehler, mit Info-Log. Damit gelten die B17-Versuche und Zeitgrenzen
  auch für die Plattformsuche, und die Moderation (B81) wird
  plattformweit mitgezählt.
- **`Account`/`Chatters`/`StreamerUser`:** Logik aus `app.userLookup`
  1:1 in den Service (Konten werden wie neu gesehene gespeichert, B4).
  Der Service importiert `internal/template` nur für die
  `template.Account`-Kennung (keine Abhängigkeit zurück).
- **`UpsertIdentity`/`UpdateUser`/`ResetStrikes`:** dünne Weitergabe an
  den Store-Port; die App gibt für Nutzerwerte den Service statt des
  Stores weiter (ein Ein-/Ausgang für Nutzerwerte).
- **Nutzersuche-Action (B90–B93):** `find` = `run.UserByName` (B17) +
  Auswahl der Identität auf der Plattform des Key (Name
  schreibungsunabhängig oder ID, sonst erste); Ports schrumpfen auf
  `Templates` und `Logger` (kein `Platforms`/`Users`, kein Limit mehr);
  leere Keys und die Info-Logs bleiben. Die B92- und
  Plattformsuch-Tests wandern in `internal/userservice`; die Action-Tests
  behalten B91 (Reihenfolge bekannt vor Plattform, via Engine-Fake) und
  B93.
- **Moderation-Action (B81, B84):** die Kinds, die auf einer Plattform
  wirken, suchen über den Port `Lookup` (nicht speichernd, B81); der
  Strike sucht über `run.UserByName` (speichernd, B84). Der Port `Users`
  entfällt, `Strikes` geht an den Service. Fehler vor „nicht gefunden“
  prüfen, leere Login-Names nicht suchen.
- **App:** `a.userSvc` entsteht dort, wo `a.users` entstand (vor der
  Action-Registry); `accountUserIDs` wird Methode des Services (der
  Port `AccountUsers` entfällt); `requirement.Streamer`,
  `template.UserFamily`, `engine.WithUsers`, die Action-Ports bekommen
  den Service.

## Files

- `internal/userservice/userservice.go` + `lookup.go`: `UserByName`,
  `Lookup` (nicht speichernd), `Account`, `Chatters`, `UserByPlatformID`,
  `UpsertIdentity`, `StreamerUser`, `UpdateUser`, `ResetStrikes`,
  `accountUserIDs` (privat), B92-Limit; Store-Port um `UserByLogin`,
  `UserByPlatformID`, `UpsertIdentity`, `ResetStrikes`; Tests
  (B91-Reihenfolge, B92-Limit, Konten, Strikes).
- `internal/action/users/users.go` + Tests: `find` über `run.UserByName`;
  Ports ohne `Platforms`/`Users`; `LookupInterval` und `limiter` weg.
- `internal/action/moderation/moderation.go` + Tests: Port `Lookup`
  (nicht speichernd) für die Kinds auf einer Plattform, `find` für den
  Strike über `run.UserByName`; Port `Users` weg, `identityOn`-Hilfe.
- `internal/app/wiring.go` + `app.go`: `userLookup` und
  `connectorAccount` entfallen; Service vor der Registry erzeugt;
  alle Port-Stellen auf `a.userSvc`.
- `docs/roadmap.md` (5.2.6), `docs/spec/actions.md` (B91/B92,
  Änderungshistorie), `docs/spec/command-engine.md` (B17,
  Änderungshistorie), `docs/spec/users-and-roles.md`
  (Umsetzung, Änderungshistorie), dieses Planfile.

## Tests

| Test | Level |
|------|-------|
| `UserByName`: Reihenfolge Name→Konto→ID→Plattform (B91), Speichern des Gefundenen, `ErrUnknownUser` ohne Fehler, Plattform-Fehler als Fehler, nicht verbunden ohne Fund, B92-Limit (eine je Minute, über Plattformen, nur Suchen an die Plattform) | unit (userservice, Test-Adapter) |
| `Account`/`Chatters`/`StreamerUser`: wie bisher im App-Wiring (Konto gespeichert, B4) | unit (userservice) |
| `UpdateUser`/`ResetStrikes`: Weitergabe an den Store | unit (userservice) |
| Nutzersuche-Action: B91 über `run.UserByName` (bekannt vor Plattform), B93, leere Keys; B92-Test entfällt (im Service) | unit (action, Engine-Fake) |
| Moderation-Action: Fund über `run.UserByName`, unbekannt = Fehler (B81) | unit (action) |
| App-E2E: die Nutzersuche findet über die Pipeline einen unbekannten Mock-Nutzer und speichert ihn (B91) | e2e (app) |

## Open questions

(keine — Entscheidungen oben dokumentiert)
