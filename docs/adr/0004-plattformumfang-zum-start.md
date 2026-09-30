# ADR-0004: Plattformumfang zum Start: Twitch

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-27 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §5.1 und §6.11; Roadmap Phasen 3 und 4, Backlog (weitere Plattformen) |

## Kontext

- Das Original unterstützt Twitch, YouTube, Kick, Velora und VPZone.
- Für den Start reicht Twitch.
- Twitch ist für einen headless Core die beste Grundlage. EventSub läuft über WebSocket und braucht keinen öffentlichen Endpunkt. Der Device Code Flow funktioniert ohne Client-Secret und ohne Browser auf dem Zielsystem.
- YouTube (gRPC-Streaming, Quota, Google-Verifizierung) und Kick (nur Webhooks an öffentliche URLs) bringen zusätzliche Infrastruktur mit.

## Entscheidung

- Der MVP (M2) unterstützt ausschließlich **Twitch**, mit Streamer-Konto und optionalem Bot-Konto.
- Eine **Mock-Plattform** (P0) dient Tests und Entwicklung. Sie hält die Plattform-Abstraktion neutral.
- YouTube und Kick bleiben für Core 1.0 geplant (P1). **Nach M2 fällt je Plattform ein Go/No-Go.** Velora und VPZone bleiben P3.
- Beim Entwurf der Plattform-Ports werden die Besonderheiten von YouTube und Kick schon mitgedacht, damit die Abstraktion nicht Twitch-förmig wird.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Mehrere Plattformen im MVP | verzögert den MVP deutlich; Kick braucht zusätzlich Webhook-Infrastruktur |
| YouTube zuerst | Quota und Google-Verifizierung erschweren den Start |

## Konsequenzen

**Positiv:**

- Das ist der kürzeste Weg zu einem nutzbaren MVP.
- Twitch-Funktionen wie Channel Points, Bits und Hype Train sind früh verfügbar.

**Negativ und Risiken:**

- Die Abstraktionen könnten Twitch-lastig werden. Dagegen helfen die Mock-Plattform und ein Review der Ports gegen die Doku von YouTube und Kick.
- Nutzer anderer Plattformen profitieren erst ab M5.

**Folgearbeiten:**

- [ ] Go/No-Go für YouTube und Kick, wenn sie aus dem Backlog der Roadmap wieder eingeplant werden (bis 2026-09-30: nach M2, Roadmap Phase 9)

## Nachtrag 2026-09-30

Der Projektinhaber hat weitere Plattformen (YouTube, Kick, Multiplattform-Betrieb) aus der Roadmap-Phase 9 ins Backlog verschoben; sie gehören nicht mehr zu Core 1.0. Die Entscheidung dieses ADR, zum Start nur Twitch anzubinden, bleibt. Das Go/No-Go je Plattform fällt, wenn sie wieder eingeplant werden. Der Meilenstein M5 heißt seitdem „Integrationen Tier 1“; „erst ab M5“ in den Konsequenzen meint den früheren Meilenstein „M5 Multiplattform“.
