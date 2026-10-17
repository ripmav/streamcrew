# Phase 6.3 (3/4): Shell-Completion

Status: **erledigt** (2026-10-17)
Branch/PR: `feat/cli-completion`, auf `feat/cli-chat-event` gestapelt (Stack #258).

## Ziel

Shell-Completion für bash, zsh und fish (Roadmap 6.3, S).

## Entscheidung

- Kong v1.16.1 hat **kein** eingebautes Completion ⇒ statische Skripte.
- `streamcrew completion bash|zsh|fish` gibt das Skript der Shell aus.
- Ein einzige Quelle: `commandTree` (Gruppen → Subkommandos) + `topLevel`
  (Reihenfolge wie in Root); die drei Skripte werden daraus generiert
  (bash `compgen`, zsh `compadd`/`#compdef`, fish
  `__fish_seen_subcommand_from`).
- Neu hinzukommende Befehle werden in die Tabelle aufgenommen;
  `TestCompletionCoversTheTree` prüft, dass jedes Skript die ganze
  Tabelle listt.

## Tests

- `TestCompletionKong`: Kong-Struktur, `completion <shell>`
- `TestCompletionCoversTheTree`: jedes Skript enthält jeden Top-Level-
  Befehl und jedes Subkommando; der Befehl druckt das Skript
- bash-Skript besteht `bash -n` (manuell verifiziert)
