# Phase 5.6: Nutzerspezifische Chat-Commands (P1)

Status: **erledigt** (2026-10-16)

## Ziel

Roadmap 5.6, P1: Ein Chat-Command, der je Nutzer eine eigene Antwort kennt.
Der klassische Fall: `!mynote` antwortet jedem Nutzer mit **seiner** Notiz;
`!mynote <text>` legt/ersetzt die Notiz des Nutzers.

## Interpretation

„Nutzerspezifische Chat-Commands“ ist in `commands.md` B2 als eigene
Command-Art (P1) angekündigt, ohne konkrete Spezifikation (eine offene
Projektinhaber-Frage). Die hier umgesetzte Interpretation: Der
Nutzerspezifität kommt über eine **Action** (`user_command`), die einen
normalen Chat-Command nutzerspezifisch macht. Das ist der kleinste
vollständige Baustein und vermeidet eine neue Command-Art in der
Engine/Trigger-Pfadt (die Art `chat` bleibt).

## Umsetzung

### Action-Typ `user_command` (`internal/action/usercommand`)

- Art `remember`; Felder:
  - `text` (Template, Pflicht) – die zu speichernde Antwort (`$allargs`).
  - `default` (Template, optional) – die Antwort ohne gespeicherte.
  - `confirm` (Template, optional) – die Antwort nach dem Speichern.
- Ports: `Templates`, `Chat` (Interface `ChatSender` mit `Send`),
  `Answers` (Interface mit `SetUserAnswer`/`UserAnswer`), `Logger`.
- Schlüssel der Antwort = **Name des laufenden Commands**
  (`run.Command().Name`).
- Verhalten:
  - mit Argumenten (gerendeter `text` nicht leer): speichert den Text als
    die Antwort des auslösenden Nutzers für den Schlüssel und antwortet mit
    der Bestätigung (leere Bestätigung = ohne Antwort).
  - ohne Argumente: antwortet mit der gespeicherten Antwort, ohne eine mit
    der Standard-Antwort; ohne gespeicherte und ohne Standard antwortet sie
    nicht.
  - Der Durchlauf braucht einen auslösenden Nutzer (Timer o. ä. scheitern).
- Gespeicherte Antwort wird **nicht** gerendert (keine Identifier-Einschleusung);
  der Standard wird gerendert.

### Store (Migration 0020)

- Neue Tabelle `user_answer (user_id, key, answer, updated_at)`,
  Primärschlüssel `(user_id, key)`, `ON DELETE CASCADE` auf `users`.
- Methods `store.SetUserAnswer`, `store.UserAnswer`,
  `store.DeleteUserAnswer` (`internal/store/user_answer.go`), Queries in
  `internal/store/queries/user_answer.sql`, sqlc regeneriert.

### Registrierung

- `internal/app/app.go`: `usercommand.Descriptors` mit `a.templates`,
  `a.chat`, `a.store` (Antworten) und Logger.
- `internal/app/catalog.go`: `usercommand.Catalog()`.
- `internal/action/action.go`: neue Kategorie `user_command`.
- Schema golden regeneriert.

### Tests

- `internal/store/user_answer_test.go`: setzen, lesen, ersetzen, löschen,
  unabhängige Schlüssel.
- `internal/action/usercommand/usercommand_test.go`: speichert mit
  Argumenten, antwortet gespeichert, Standard ohne gespeicherte, stumm ohne
  beides, je Nutzer getrennt, braucht Nutzer, Validate.

### Doku

- `docs/spec/actions.md`: Abschnitt „Nutzer-spezifischer Chat-Command“
  (B126–B129) + Änderungshistorie.
- `docs/roadmap.md`: P1-Item erledigt, Phase 5 (Core-Services) vollständig;
  Änderungshistorie.

## Festlegungen

- Nutzerspezifität als Action (keine neue Command-Art), damit die
  Chat-Trigger-Pfad unverändert bleibt.
- Schlüssel = Name des laufenden Commands (Setter und Reader derselbe
  Command).
- Gespeicherte Antwort wird nicht gerendert (Sicherheit), der Standard schon.
- Bestätigung optional; ohne ist das Speichern still.

## Verzicht

- Keine neue Command-Art in der Engine (bleibt `chat`); die P1-Art aus
  `commands.md` B2 ist hier als Action umgesetzt.
- Kein Preset (der Streamer komponiert den Command mit der Action).
- Kein Löschen der eigenen Antwort über einen Befehl (nur das Löschen des
  Nutzers entfernt sie); falls gewünscht, folgt ein `delete`-Kind.
- Kein E2E auf echten Twitch-Kanälen (bleibt Exit-Kriterium Phase 6).
