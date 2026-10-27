# ADR-0020: Audioausgabe (Sinks: overlay, local mit Build-Tag, agent als Stub)

- **Status:** Akzeptiert
- **Entscheidung durch:** Projektinhaber (autonomer Arbeitsauftrag vom 2026-10-27)
- **Entstanden in:** Phase 7 Planungs-/Autonomie-Sitzung
- **Verwandt:** ADR-0019 (Overlay-Architektur), ADR-0010 (Konsistent-Sync), ADR-0017 (Konfiguration)
- **Betroffene Pakete:** `internal/audio` (neu), `internal/action` (`sound`, `text_to_speech`), `internal/app`, `internal/config`, `internal/capability`, `.goreleaser.yaml`
- **Referenzen:** `docs/spec/overlays.md` B28–B32, `docs/superpowers/plans/2026-10-27-phase-7-overlays-media.md` §7, Mix It Up Documentation Q8–Q14

## Kontext

Phase 7 bringt zwei neue Actions mit Audio: `sound` (Wiedergabe von Dateien aus dem Medienverzeichnis) und `text_to_speech` (gesprochene Text-Zeilen). Die offene Frage: **Wo** wird das Audio physisch ausgegeben?

Drei Zielgeräte sind sinnvoll:

1. **Overlay** (Browser-Pane im OBS-Scene-Layout) — das ist exakt das MIU-Verhalten: „Audio spielt über den Browser-Pane auf dem Streamer-PC" (Q14). OBS mischt das Overlay-Audio in den Stream ein, wenn das Browser-Pane als Audioquelle eingebunden ist.
2. **Local** (Streamcrew-App selbst, direkt am Audiosystem des Streamer-PCs) — nützlich für SFX, die **nicht** im Stream erscheinen sollen, oder wenn OBS das Overlay-Audio nicht mischt.
3. **Agent** (Remote-Host über das Agent-Protokoll) — die Agent-Architektur (Phase 9) kennt einen Remote-Host; Audio-Durchleitung gehört dort in 9.6.

Zusätzliche Eingangsgrößen:

- Der Server-Modus (Capability-Modell) verweigert `host:audio` — die `local`-Ausgabe muss capability-geprüft bleiben.
- Die Goreleaser-Config baut `streamcli`/`streamcrew` aus; ein Build mit `localaudio`-Tag braucht eine eigene Build-Definition.
- WAV ist trivial dekodierbar (Standard-Format, `encoding/binary` + `math`); MP3 wäre eine externe Dependency.

## Entscheidung

### 1. Sink-Architektur

`internal/audio` (neues Paket) mit der Interface `Sink` (eine `Play(path string) error` / `Speak(text string) error` abstrahierende Schnittstelle) und drei Implementierungen:

| Sink | Build/Mode | Implementation |
| --- | --- | --- |
| `overlay` | **Default**, immer verfügbar (außer ohne `host:fs`) | Schickt ein `audio`-Item an den **Default-Overlay-Endpoint** (ADR-0019 §4); das Audio wird im Browser-Pane über den Streamer-PC wiedergegeben (Q14). Kein extra Audio-Device, keine `host:audio`-Capability nötig. |
| `local` | Build-Tag `localaudio`, `host:audio` erforderlich | `ebitengine/oto/v3` v3.5.1 (MIT-Lizenziert, aktiv, Standard-Go-Audio-Backend für Desktop). `audio-device`-Konfiguration (Device-Index oder Name, Default: System-Standard). WAV ist garantiert dekodierbar; **MP3 wird in 7.3 entschieden** (Kandidat `github.com/dmarc/ito/mp3`, MIT); falls nicht: WAV-only mit `shortcut:`-Kommentar und klarem Fehler bei MP3. |
| `agent` | Stub in 7.3, Implementation in 9.6 | Schickt das Audio-Event ins Agent-Protokoll; der Agent spielt es lokal ab. In 7.3 wird nur die Action-Zielwahl und der Capability-Check real; die eigentliche Durchleitung kommt mit 9.6. |

### 2. Konfiguration

- `audio-sink`: `overlay` (Default) | `local` | `agent` (Server-Modus: `local` ist unzulässig, `agent` nur im Agent-Modus sinnvoll — Capability-Check bei der Aktion, nicht bei der Config-Prüfung).
- `audio-device`: nur für `local`; Device-Index (Default `-1` = System-Default) oder Name; Neustart erforderlich.
- API-Keys für TTS-Provider über Env (ADR-0019 §6), nicht in der Config.

### 3. TTS-Provider (Details)

Siehe ADR-0019 §6. Zusammenfassung der Audio-Aspekte:

- **`browser`** (Default): kein Key, kein `host:audio`; der Overlay-Client spricht via Web Speech API.
- **`google`/`azure`**: REST-APIs, server-seitig generiertes Audio wird unter `<data-dir>/<profil>/media/generated/` abgelegt und als `audio`-Item auf dem Default-Endpoint gespielt (MIU-äquivalent, Q14).
- **`piper`**: externer Prozess (`piper`-Binary), `host:process` erforderlich; erzeugt WAV lokal; wird als `audio`-Item gespielt.

### 4. Medien-Audio

Medien-Items (`kind: audio|video|clip`) mit Audio-Track werden im Browser-Pane des Streamer-PCs wiedergegeben (ADR-0019 §8). Der `sound`-Action mit `sink: overlay` zeigt ein `audio`-Item; mit `sink: local` spielt `internal/audio` die Datei direkt ab. Das ist die einzige Stelle, wo `host:audio` wirklich gebraucht wird.

## Konsequenzen

- **Pro:**
  - Default-Verhalten (`overlay`) deckt den MIU-Fall exakt ab (Q14) und braucht **keine** `host:audio`-Capability — der Server-Modus bleibt funktionsfähig für Overlays und Overlay-Audio.
  - `local` ist opt-in über Build-Tag: die Desktop-App (Goreleaser `streamcrew-desktop`) wird mit `localaudio` gebaut, das Server-Image nicht; `oto/v3` und ein MP3-Decoder landen nicht in jedem Build.
  - `oto/v3` ist stabil, MIT, C-less (Pure-Go-Wrapper um system Audio), und der De-facto-Standard für Go-Desktop-Audio; keine Abhängigkeits-Eskalation.
  - Agent-Sink-Stub in 7.3 hält die Action-Signatur stabil, 9.6 ergänzt nur die Durchleitung — keine Breaking-Change.
- **Contra:**
  - MP3-Unterstützung für `local` hängt von einer zusätzlichen Dependency ab; wenn die nicht passt, bleibt WAV-only (Shortcut, dokumentiert).
  - Zweiter Build mit Tag (`streamcrew-desktop`) erweitert die Goreleaser-Config; CI muss den Desktop-Build testen (Mindest: `go build -tags localaudio ./...` in der CI).
  - `local`-Audio im Server-Modus ist per Capability untersagt — muss in der Doku klar stehen.

## Abwägungen

| Variante | Pro | Contra |
| --- | --- | --- |
| **Nur `overlay`-Sink** | Am einfachsten, keine `host:audio`-Deps | Deckt den Fall „SFX außerhalb des Streams" nicht ab; gegen die Roadmap-Definition (Q8–Q14) unvollständig |
| **Alle drei Sinks ohne Build-Tag** | Einfachste Build-Config | `oto/v3` in jedem Binary, Server-Image trägt unnötiges Gewicht, `local`-Capability im Server-Modus wäre eine Sicherheitslücke |
| **Sinks mit Build-Tag (gewählt)** | Kleinstes mögliches Default, `local` ist opt-in, Server-Modus bleibt sauber | Zwitter-Build (`streamcrew-desktop`) nötig, CI-Test für den Tag |

## Open Items

- **MP3-Decoder** für `local`-Sink: in 7.3 entscheiden (`github.com/dmarc/ito/mp3` vs. WAV-only). Entscheidung wird in der 7.3-PR dokumentiert.
- **Agent-Audio-Durchleitung** (9.6): Protokoll-Meldung `audio.play` (path + base64 oder URL) ist der voraussichtliche Vorschlag; wird in 9.6 spezialisiert.
