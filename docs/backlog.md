# Backlog

Später oder nicht geplante Aufgaben, Ideen und zurückgestellte Entscheidungen. Seit 2026-10-09 ein
eigenes Dokument (davor ein Abschnitt der [Roadmap](roadmap.md)); die Roadmap verlinkt hierher.
Die Einträge behalten ihre Größenordnungen (S/M/L); Priorität und Zeitpunt werden festgelegt,
wenn eine Aufgabe wieder eingeplant wird.

**Weitere Plattformen** (bis 2026-09-30 Phase 9; ins Backlog verschoben durch Entscheidung des Projektinhabers). Priorität und Go/No-Go je Plattform ([ADR-0004](adr/0004-plattformumfang-zum-start.md)) werden festgelegt, wenn sie wieder eingeplant werden; dann wird daraus eine eigene Phase. Der gemeinsame Webhook-Eingang und ADR-0015 sind nach Phase 9.1 gewandert. Grobe Schätzung: 7–10 PW für YouTube, Kick und den Multiplattform-Betrieb, 4–6 PW für Velora und VPZone (P3). ADR-0016 (YouTube-Chat-Streaming) gehört dazu.

- Multiplattform:
  - Standardplattform einstellbar (bis dahin fest Twitch als `platform.Default`, [ADR-0004](adr/0004-plattformumfang-zum-start.md); genutzt etwa von der Moderation ohne Plattform des Durchlaufs, [`actions.md`](spec/actions.md), B82), Senden an alle oder bestimmte Plattformen, Plattformfilter in Commands und Requirements, Rollen-Mapping (M)
- YouTube:
  - ADR-0016 Chat-Streaming; Anleitung für eigene Google-Cloud-Credentials (S)
  - OAuth: Loopback + PKCE, Server-Callback, Einfügen des Codes als Fallback (M)
  - Chat-Empfang (L): Livestream-Erkennung; gRPC-Client für `liveChatMessages.streamList`, generiert aus `stream_list.proto`; quota-schonender Polling-Fallback
  - Chat senden und löschen, Timeout und Bann, Mitgliedschaften, Super Chats und Super Stickers, Jewels (L)
  - YouTube-Action, Events, Quota-Überwachung (M)
- Kick:
  - OAuth 2.1 + PKCE; Anleitung für eine eigene Kick-App (M)
  - Webhook-Empfang über den gemeinsamen Eingang (9.1): Kick-Signaturprüfung, Deduplizierung, erneutes Abonnieren nach automatischer Kündigung durch Kick (M)
  - REST-Client (L): Chat senden, Moderation, Kanal aktualisieren, Belohnungen; Events: Follow, Abos, Geschenke, Belohnungen, Kicks, Livestream-Status
  - Kick-Action sowie Kick-Channel-Points- und Kick-Kicks-Commands (M)
- Velora und VPZone (P3): offizielle API-Dokumentation sichten, Aufwand schätzen, Go/No-Go (S); Adapter umsetzen (je L), falls Go
- Exit-Kriterien, wenn die Plattformen eingeplant werden: Ein gleichzeitiger Stream auf Twitch, YouTube und Kick läuft mit gemeinsamen Commands und gemeinsamer Währung; plattformübergreifend verknüpfte Nutzer werden korrekt zusammengeführt.

**Robustheit:**

- Herunterfahren ohne Verlust: Eine Nachricht, die die Plattform dem Core schon übergeben hat, kann ihren Command verlieren, wenn die Command-Engine vor dem Auslösen durch das Ereignis-Service stoppt (in der CI von PR #140 beobachtet: der Trigger scheiterte mit „command engine is shut down“). Das Ereignis-Service soll vor der Command-Engine aufhören zu lösen und die Engine die schon anstehenden Instanzen noch abarbeiten (S)

**Abhängigkeiten:**

- Auf `go.yaml.in/yaml/v4` wechseln, sobald es stabil ist: Syntaxfehler in YAML nennen dann auch die Spalte ([Code-ADR-0005](adr/code/0005-konfiguration.md), Punkt 3; Entscheidung des Projektinhabers vom 2026-10-03) (S)

**Build / CI:**

- Docker-Image mit Alpine als Build-Basis bauen (aktuell `golang:<version>-trixie`): Build-Stage auf `golang:<version>-alpine` (weiterhin Digest-Pin, Renovate) umziehen, `ca-certificates` aus dem Alpine-Paket in das Scratch-Image kopieren und `scripts/docker-smoke.sh` grün halten (S) (Vorgabe des Projektinhabers vom 2026-10-09)
- CI-Job „Tests (race detector, short fuzz runs)“ für kürzere Laufzeit optimieren (aktuell rund 7–8 min): `scripts/fuzz.sh` läuft die 12 Fuzz-Targets je 20 s nacheinander (rund 4 min), dazu `go test -race -shuffle=on ./...`; zum Beispiel Parallelisierung der Fuzz-Läufe oder Sharding der Pakete über mehrere Runner prüfen (S) (Vorgabe des Projektinhabers vom 2026-10-09)

**Anforderungen** (in Roadmap 3.4 zurückgestellt, Entscheidung des Projektinhabers vom 2026-10-02):

- Rollen-Anforderung wie im Original: mehrere Rollen (eine davon genau), Beschränkung auf eine Plattform, Stufen bei Abonnenten (mit den Twitch-Stufen aus Phase 4), global „exakte Rollen“ ([`requirements.md`](spec/requirements.md), A12) (S)

**P3 (nur bei Bedarf):**

- Integrationen aus Tier 3
- Discord Reactive Voice
- Musik-Player, Alejo-Pronomen
- Kompatibilitätsfassade für die Developer-API

**Ideen ohne Priorität:**

- Mehrere gleichzeitig aktive Profile
- Plugin-System für Integrationen (z. B. per WASM)
- Geteilter Katalog für Command-Bundles
- Mobile Moderations-App auf Basis der Web-API

**Nicht geplant (Plan §4.3):**

- Nachbau der WPF-Oberfläche
- C#-Skripte
- Funktionen, die Blazing-Cacti-Server brauchen
- Mixer, Trovo, Glimesh, Facebook, Twitter, OvrStream, InfiniteAlbum
- Overlay v1/v2
- Inoffizielle Schnittstellen (Edge-TTS, TikTok-TTS, Kick-Pusher)
- SaaS-Betrieb für Dritte
