# Code-ADR-0011: Event-Bus

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §6.1, §6.7, §6.14; Roadmap Phase 2.2, 2.3; [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0004](0004-nebenlaeufigkeit-und-supervisor.md), [Code-ADR-0006](0006-teststrategie.md), [Code-ADR-0009](0009-ids-und-zeit.md), [Code-ADR-0010](0010-polymorphe-serialisierung.md) |

## Kontext

- Ein typisierter In-Process-Bus verbindet die Komponenten. Derselbe Strom geht gefiltert an die Frontends (Plan §6.1, §6.7, `StreamService.Subscribe` in §6.14).
- Plan §6.7 gibt vor: Umschlag mit ID (UUIDv7), Zeitstempel, Quelle, Typ als stabiler String und typisierte Nutzlast. Jeder Abonnent hat einen eigenen Puffer; langsame API-Abonnenten verlieren Ereignisse und bekommen einen Lag-Hinweis; die Engine blockiert nie.
- Veröffentlicht wird aus vielen Goroutinen: Plattform-Adapter, Integrationen, Supervisor, Engine. Konsumenten sind Event-Service, Chat, Nutzer, Statistik, Overlays und API-Streams.
- Ein Adapter, der beim Veröffentlichen blockiert, verpasst Nachrichten seiner Plattform, etwa das Keepalive von EventSub.

## Entscheidung

1. **Eigene Implementierung im Paket `internal/event`**, ohne Abhängigkeit.
2. **Umschlag:**

   ```go
   type Envelope struct {
   	ID      id.ID     // UUIDv7 (Code-ADR-0009)
   	Time    time.Time // UTC
   	Source  Source    // z. B. {Kind: "platform", Name: "twitch"}
   	Type    Type      // stabiler String, z. B. "twitch.channel.follow"
   	Payload any       // typisierte Nutzlast; ihr Go-Typ gehört fest zum Type
   }
   ```

   - **Typnamen:** Kleinbuchstaben, durch Punkte gegliedert `<bereich>.<gegenstand>.<vorgang>`, etwa `chat.message`, `twitch.channel.follow`, `app.started`, `supervisor.status`.
   - Einmal veröffentlichte Typnamen werden nicht umbenannt. Ein neuer Name ersetzt einen alten nur mit Übergangszeit.
   - **Katalog:** Jeder Typ steht im Katalog mit seinem Nutzlast-Typ. Ein generischer Helfer liefert die Nutzlast typisiert (`event.Payload[T](e)`).
   - Für API und Protokoll wird die Nutzlast nach [Code-ADR-0010](0010-polymorphe-serialisierung.md) kodiert.
3. **Veröffentlichen blockiert nie.** `Publish` legt das Ereignis in den Puffer jedes passenden Abonnements.
   - Ist ein Puffer voll, wird das Ereignis für dieses Abonnement verworfen und ein Zähler erhöht. Der Abonnent erhält vor seinem nächsten Ereignis einen Lag-Hinweis mit der Zahl verpasster Ereignisse. Der Bus loggt das als `WARN`, höchstens einmal je Abonnement und Minute.
   - Die Reihenfolge ist je Abonnement die der Veröffentlichung. Über mehrere Veröffentlicher hinweg gibt es keine globale Reihenfolge.
4. **Abonnements:**
   - `Subscribe(ctx, opts…)` liefert einen Kanal und endet mit dem Kontext oder mit `Close`.
   - Optionen: Filter nach Typ (genau oder Präfix wie `twitch.`), zusätzlich eine Prädikatfunktion; Puffergröße (Standard 256).
   - Interne Konsumenten, die nichts verlieren dürfen, etwa der Event-Service für Event-Commands, bekommen große Puffer (z. B. 4096). Sie reichen Ereignisse sofort an eigene Warteschlangen weiter, statt im Empfang zu arbeiten.
   - Jeder Konsument läuft als Runnable im Supervisor ([Code-ADR-0004](0004-nebenlaeufigkeit-und-supervisor.md)). Der Bus startet selbst keine Goroutinen.
5. **Nicht Aufgabe des Busses:**
   - Persistenz: Das Ereignisprotokoll (`event_log`) ist ein eigener Abonnent (Phase 5.3).
   - Deduplizierung von Plattform-Nachrichten: Das übernehmen die Adapter mit einem TTL-Cache vor dem Veröffentlichen.
   - Anfrage-Antwort zwischen Komponenten: Dafür gibt es Methodenaufrufe über Interfaces.
6. **Lebenszyklus:** Die Composition Root erzeugt einen Bus und reicht ihn weiter ([Code-ADR-0002](0002-dependency-injection.md)). `Close` beim Shutdown beendet alle Abonnements; danach verwirft `Publish` still.
7. **Tests** in `testing/synctest`: Reihenfolge je Abonnement, Filter, voller Puffer mit Lag-Hinweis, Ende über den Kontext, `Publish` blockiert nicht ([Code-ADR-0006](0006-teststrategie.md)).

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| blockierendes Veröffentlichen, bis alle Abonnenten angenommen haben | ein langsamer Konsument hielte Plattform-Adapter und Engine an |
| blockieren mit Zeitlimit | verschiebt das Problem nur und verzögert jedes Ereignis um bis zu dieses Limit |
| den ältesten statt den neuesten Eintrag verwerfen | bei Chat und Alerts zählt meist das Aktuelle, aber das Verwerfen des Ältesten braucht einen eigenen Ringpuffer mit Sperre je Abonnement; der Lag-Hinweis macht beides erkennbar, der einfache Kanal genügt |
| `github.com/ThreeDotsLabs/watermill` | Messaging-Rahmenwerk für verteilte Systeme mit vielen Abhängigkeiten; für einen Prozess überdimensioniert |
| `github.com/asaskevich/EventBus` | synchrone Aufrufe per Reflection, keine Puffer, kein Lag-Schutz |
| generischer Umschlag `Envelope[T]` | typsicher, aber ein Bus mit vielen Ereignistypen bräuchte ohnehin eine gemeinsame Hülle; der Katalog mit `Payload[T]` gibt die Typsicherheit beim Empfang |

## Konsequenzen

**Positiv:**

- Kein Produzent wird durch Konsumenten aufgehalten; Engine und Adapter bleiben reaktionsfähig.
- Verlorene Ereignisse sind sichtbar, im Log und beim Abonnenten.
- Ein einziger Strom für interne Konsumenten und Frontends.

**Negativ und Risiken:**

- Ein Konsument mit zu kleinem Puffer verliert Ereignisse. Puffergrößen für kritische Konsumenten werden mit den Lasttests (Phase 12) überprüft.
- `Payload any` prüft den Typ erst zur Laufzeit. Ein Test stellt sicher, dass jeder veröffentlichte Typ im Katalog steht und die Nutzlast den dort genannten Go-Typ hat.
- Keine globale Reihenfolge über Produzenten hinweg; wer sie braucht, ordnet nach Zeitstempel und ID.

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen und den Index in [`README.md`](README.md) anpassen, erledigt 2026-09-29
- [ ] `internal/event` mit Umschlag, Katalog, Bus, Filtern und Lag-Hinweis umsetzen (Roadmap Phase 2.2 und 2.3)
- [ ] Statusmeldungen des Supervisors als Ereignis `supervisor.status` veröffentlichen (Folgearbeit aus [Code-ADR-0004](0004-nebenlaeufigkeit-und-supervisor.md))
- [ ] Zuordnung der numerischen Ereignis-IDs des Originals für den Import (Roadmap Phase 2.2), vorbehaltlich der rechtlichen Einschätzung und des geplanten ADRs zum Import von Mix-It-Up-Daten (ADR-Backlog in Plan §12.1)
