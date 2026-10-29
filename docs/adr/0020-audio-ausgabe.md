# ADR-0020: Audioausgabe (Sinks: overlay, local mit Build-Tag, agent als Stub)

- **Status:** Akzeptiert
- **Entscheidung durch:** Projektinhaber (autonomer Arbeitsauftrag vom 2026-10-27)
- **Entstanden in:** Phase 7 Planungs-/Autonomie-Sitzung
- **Verwandt:** [ADR-0019](0019-overlay-architektur.md) (Overlay-Architektur), [ADR-0012](0012-persistenz.md) (Persistenz je Profil), [Code-ADR-0005](code/0005-konfiguration.md) (Konfiguration)
- **Betroffene Pakete:** `internal/media` (neu), `internal/tts` (neu), `internal/action` (`sound`, `text_to_speech`), `internal/app`, `internal/config`, `internal/capability`, `.goreleaser.yaml`
- **Referenzen:** [`docs/spec/overlays.md`](../spec/overlays.md) (B40, B41, Abweichung A10), [`docs/superpowers/plans/2026-10-27-phase-7-overlays-media.md`](../superpowers/plans/2026-10-27-phase-7-overlays-media.md) §2, Mix It Up Documentation (Q21–Q23 der Spezifikation)

## Kontext

Phase 7 bringt zwei neue Actions mit Audio: `sound` (Wiedergabe von Dateien aus dem Medienverzeichnis) und `text_to_speech` (gesprochene Text-Zeilen). Die offene Frage: **Wo** wird das Audio physisch ausgegeben?

Drei Zielgeräte sind sinnvoll:

1. **Overlay** (Browser-Pane im OBS-Scene-Layout) — das ist exakt das MIU-Verhalten: „Audio spielt über den Browser-Pane auf dem Streamer-PC" (Q21).
2. **Local** (Streamcrew-App selbst, direkt am Audiosystem des Streamer-PCs) — nützlich für SFX, die **nicht** im Stream erscheinen sollen, oder wenn OBS das Overlay-Audio nicht mischt.
3. **Agent** (Remote-Host über das Agent-Protokoll) — die Agent-Architektur (Phase 9) kennt einen Remote-Host; Audio-Durchleitung gehört dort in 9.6.

Zusätzliche Eingangsgrößen:

- Der Server-Modus (Capability-Modell) verweigert `host:audio` — die `local`-Ausgabe muss capability-geprüft bleiben (B40).
- Die Goreleaser-Config baut die Core-Binaries; ein Build mit `localaudio`-Tag braucht eine eigene Build-Definition.
- WAV ist trivial dekodierbar (Standard-Format, `encoding/binary`); MP3 wäre eine externe Dependency.

## Entscheidung

### 1. Sink-Architektur

`internal/media` (neues Paket) mit einem Sink-Interface und drei Implementierungen:

| Sink | Build/Modus | Implementation |
| --- | --- | --- |
| `overlay` | **Default**, immer verfügbar (außer ohne `host:fs`) | Schickt ein `audio`-Item an den **Default-Overlay-Endpunkt** (ADR-0019 §4); das Audio wird im Browser-Pane über den Streamer-PC wiedergegeben (Q21). Kein extra Audio-Device, keine `host:audio`-Capability nötig. |
| `local` | Build-Tag `localaudio`, `host:audio` erforderlich | `ebitengine/oto/v3` v3.5.1 (MIT-lizenziert, aktives Go-Audio-Backend für Desktop). `audio-device`-Konfiguration (Device-Index oder Name, Default: System-Standard). Nur 16-Bit-PCM-WAV; MP3 und andere Formate sind ein klarer Fehler (Nachtrag 2026-10-27, Punkt 1). |
| `agent` | Stub in 7.3, Implementation in 9.6 | Schickt das Audio-Event ins Agent-Protokoll; der Agent spielt es lokal ab. In 7.3 werden nur die Action-Zielwahl und der Capability-Check real; die eigentliche Durchleitung kommt mit 9.6. |

### 2. Konfiguration

- `audio-sink`: `overlay` (Default) | `local` | `agent` (Server-Modus: `local` ist per Capability `host:audio` unzulässig — Check bei der Aktion, nicht bei der Config-Prüfung).
- `audio-device`: nur für `local`; Device-Index (Default `-1` = System-Default) oder Name; Neustart erforderlich.
- API-Keys für TTS-Provider über Env (ADR-0019 §6), nicht in der Config-Datei.

### 3. TTS-Provider (Details)

Siehe ADR-0019 §6. Zusammenfassung der Audio-Aspekte:

- **`browser`** (Default): kein Key, kein `host:audio`; der Overlay-Client spricht via Web Speech API (B41).
- **`google`/`azure`**: REST-APIs, server-seitig generiertes Audio wird unter `<data-dir>/<profil>/media/generated/` abgelegt und als `audio`-Item auf dem Default-Endpunkt gespielt (MIU-äquivalent, Q21, Q23).
- **`piper`**: externer Prozess (`piper`-Binary), `host:process` erforderlich; erzeugt WAV lokal; wird als `audio`-Item gespielt (B41).

### 4. Medien-Audio

Medien-Items (`kind: audio|video|clip`) mit Audio-Track werden im Browser-Pane des Streamer-PCs wiedergegeben (ADR-0019 §8). Die Sound-Action mit `sink: overlay` zeigt ein `audio`-Item; mit `sink: local` spielt `internal/media` die Datei direkt ab (B40). Das ist die einzige Stelle, wo `host:audio` wirklich gebraucht wird.

## Konsequenzen

- **Pro:**
  - Default-Verhalten (`overlay`) deckt den MIU-Fall exakt ab (Q21) und braucht **keine** `host:audio`-Capability — der Server-Modus bleibt funktionsfähig für Overlays und Overlay-Audio.
  - `local` ist opt-in über Build-Tag: die Desktop-App (Goreleaser `streamcrew-desktop`) wird mit `localaudio` gebaut, das Server-Image nicht; `oto/v3` landet nicht in jedem Build.
  - `oto/v3` ist stabil, MIT-lizenziert und der De-facto-Standard für Go-Desktop-Audio; keine Abhängigkeits-Eskalation.
  - Agent-Sink-Stub in 7.3 hält die Action-Signatur stabil, 9.6 ergänzt nur die Durchleitung — keine Breaking-Change.
- **Contra:**
  - `local` spielt nur WAV (16-Bit-PCM); MP3 bräuchte eine zusätzliche Dependency und bleibt offen (Nachtrag 2026-10-27, Punkt 1).
  - Ein zweiter Build mit Tag (`streamcrew-desktop`) erweitert die Goreleaser-Config; die CI muss den Tag-Check fahren (Mindest: `go build -tags localaudio ./...`).
  - `local`-Audio im Server-Modus ist per Capability untersagt — muss in der Doku klar stehen (B40, A10).

## Abwägungen

| Variante | Pro | Contra |
| --- | --- | --- |
| **Nur `overlay`-Sink** | Am einfachsten, keine `host:audio`-Deps | Deckt den Fall „SFX außerhalb des Streams" nicht ab; gegen die Roadmap-Definition (Q21) unvollständig |
| **Alle drei Sinks ohne Build-Tag** | Einfachste Build-Config | `oto/v3` in jedem Binary, Server-Image trägt unnötiges Gewicht, `local`-Capability im Server-Modus wäre eine Sicherheitslücke |
| **Sinks mit Build-Tag (gewählt)** | Kleinstes mögliches Default, `local` ist opt-in, Server-Modus bleibt sauber | Zweiter Build (`streamcrew-desktop`) nötig, CI-Test für den Tag |

## Open Items

- ~~**MP3-Decoder** für `local`-Sink: in 7.3 entscheiden.~~ Erledigt 2026-10-27 (Nachtrag): WAV-only, der Kandidat existiert nicht.
- **Agent-Audio-Durchleitung** (9.6): Protokoll-Meldung `audio.play` (Datei oder URL) ist der voraussichtliche Vorschlag; wird in 9.6 spezialisiert.
- **Geräteauswahl** für `local`: weggefallen mit dem Nachtrag 2026-10-27 (oto/v3 hat keine Geräte-API). Falls sie gebraucht wird, ist ein anderer Backend-Weg nötig (z. B. ein Gerätewechsel auf Systemseite, ein zweites Backend oder eine neue Dependency).

## Nachtrag 2026-10-27 (Umsetzung 7.3)

Vier Punkte der Entscheidung haben sich bei der Umsetzung geändert:

1. **MP3 entfällt, WAV-only.** Der in der ursprünglichen Fassung genannte MP3-Kandidat existiert nicht (Repository 404). Die `local`-Ausgabe dekodiert 16-Bit-PCM-WAV; andere Formate (MP3, 24-Bit, float32, andere Kanalzahlen) sind ein klarer Fehler (`internal/media/wav.go`, `shortcut:`-Kommentar). Die Wiedergabe normalisiert alles auf 44100 Hz Stereo float32 — oto/v3 kennt pro Prozess genau ein Format (`FormatFloat32LE`) und einen einzigen Audio-Kontext.
2. **`audio-device` entfällt.** oto/v3 v3.5.1 hat keine API zur Geräteauswahl (ALSA/PulseAudio-Systemdefault). Der Config-Key kommt nicht, die Doku (usage) sagt es. Offener Punkt bleibt, ob das genügt.
3. **piper-Pitch wird ignoriert.** piper kennt kein Pitch-Flag (nur `--length-scale` für die Rate und `--volume`); die Pitch-Option der Aktion geht bei `piper` verloren (`internal/tts/piper.go`, `shortcut:`-Kommentar).
4. **Kein cgo nötig.** oto/v3 v3.5.1 ist auf Linux reines Go (purego/ALSA) — der Goreleaser-Build `streamcrew-desktop` (Tag `localaudio`) cross-compiliert mit `CGO_ENABLED=0` für linux, windows und darwin, ohne Mingw/osxcross. Die Wiedergabe selbst prüft die CI nicht (kein Audiogerät auf den Runnern); die Abnahme läuft auf dem Streaming-PC.
