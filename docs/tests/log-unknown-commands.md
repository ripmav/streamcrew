# Test-Matrix: Unbekannte Chat-Commands im Log

Anfrage des Projektinhabers (2026-10-18): unbekannte Commands sollen
geloggt werden — aber nur im Debug-Log-Level oder im Dry-Run.
Spec: [`commands.md`](../spec/commands.md), B70 und B16.

## Erwartetes Verhalten

| Fall | Erwartet |
|---|---|
| Nachricht beginnt wie ein Präfix-Trigger (exakt oder ohne Schreibweise), kein aktives Command passt (z. B. `!hugg` bei Trigger `hug`) | `command.Recognition.Attempt = true` |
| Normaler Chattext | `Attempt = false` |
| Passender Trigger | `Outcome = triggered`, `Attempt = false` |
| Mehrdeutiger Treffer | `Outcome = ambiguous`, `Attempt = false` |
| Bekanntes, aber deaktiviertes Command | `Attempt = false` (deaktivierte Commands stehen nicht im Index) |
| Platzhalter-Trigger | `Attempt = false` (Platzhalter machen keinen Command-Eindruck) |
| `Attempt = true` und Gate offen (Debug-Log-Level oder Dry-Run) | Info-Eintrag `unknown chat command` mit der Nachricht |
| `Attempt = true` und Gate zu / keine Option | kein Eintrag |
| Gate zur Laufzeit umgeschaltet | der neue Zustand gilt (Gate ist eine Funktion) |

## Abgedeckte Tests

| Fall | Test | Datei |
|---|---|---|
| `Attempt` je Nachricht (Near-Miss, Schreibweise, Literal-Trigger, Chattext, Wildcard, Treffer, deaktiviertes Command) | `TestRecognize` („unknown command attempt“) | `internal/domain/command/recognize_test.go` |
| Gate offen: unbekanntes Command geloggt, Chattext nicht; Gate zu: nicht geloggt; ohne Option: nicht geloggt | `TestUnknownCommandLog` | `internal/eventservice/service_test.go` |

## Manuelle Prüfung

- [ ] `streamcrew serve --log-level debug` (oder `--dry-run`): `!hugg`
      im Chat liefert einen Log-Eintrag `unknown chat command`.
- [ ] Ohne beides: kein Eintrag, Chat-Log bleibt ruhig.
