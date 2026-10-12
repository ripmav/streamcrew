# Spezifikation: Twitch Multi-Channel (zusätzliche Kanäle)

| | |
|---|---|
| **Status** | Entwurf |
| **Stand** | 2026-10-12 |
| **Bezug** | Roadmap Phase 4.6; [Plan 4.6](../superpowers/plans/2026-10-12-twitch-multi-channel.md); [`events.md`](events.md), [`actions.md`](actions.md), [`twitch-events.md`](twitch-events.md), [ADR-0024](../adr/0024-eingebaute-twitch-app.md) |
| **Umsetzung** | noch offen (später: `internal/config`, `internal/connector`, `internal/twitch`) |

## Zweck und Umfang

Legt fest, in welchen Kanälen der Bot außer im eigenen Channel arbeitet,
welche Voraussetzung ein solcher Channel erfüllt, wie der Channel in
Ereignissen, Chat und Moderation wirkt und welche Aktionen beim eigenen
Channel bleiben. Sie gilt für die Twitch-Plattform (Roadmap 4.6).

Nicht Teil dieser Spezifikation: pro-Channel-Konfiguration von Commands,
Requirements und Timern (offen, v2), zusätzliche Logins pro Channel
(verworfen: es bleibt ein Account-Paar, A1), die Multi-Channel-Unterstützung
anderer Plattformen (der Mock-Adapter folgt der Spezifikation), die
Empfangs-Vorgaben je EventSub-Topic (Abschnitt „Offene Fragen“, E2E).

## Begriffe

| Begriff | Bedeutung |
|---|---|
| eigener Channel | der Channel des Streamer-Kontos |
| zusätzlicher Channel | ein Channel, dessen Login in der Settings-Sektion `channels` steht und in dem der Bot zusätzlich arbeitet |
| betriebener Channel | der eigene Channel und die zusätzlichen Channels, deren Voraussetzung erfüllt ist (B2, B3) |
| Mod-Status | das Bot-Konto ist Moderator des Channels; gewährt vom Channel-Owner, kein Login und keine App |
| Owner-Action | eine Aktion, die die Broadcaster-Rechte auf dem Ziel-Channel braucht (Q1) |

## Verhalten

| ID | Regel | Quellen |
|---|---|---|
| B1 | Die Settings-Sektion `channels` listet die Logins der zusätzlichen Channels; der eigene Channel ist immer betrieben und nicht konfigurierbar. Sie nimmt keine leeren Einträge, keine Duplikate, keinen eigenen Channel und keine fehlerhaften Logins an (1–25 Zeichen: Buchstaben, Ziffern, Unterstrich; 4.8). Konfiguriert wird die Sektion über die CLI-Befehle `profile channels list|add|remove` (4.8). | Q2, Q3 |
| B2 | Die einzige Voraussetzung eines zusätzlichen Channels ist der Mod-Status des Bot-Kontos dort. Er wird vom Channel-Owner gewährt; es braucht keinen zweiten Login, keine zweite App und keine App-Credentials. | Q1, Q2 |
| B3 | Der Adapter prüft den Mod-Status jedes zusätzlichen Channels beim Start und bei jedem Subscription-Abgleich. Fehlt er, ist der Channel nicht betrieben (keine Subscriptions, kein Chat-Ziel, keine Moderation), und der Adapter meldet die Warnung `mod_missing`. Der Adapter fügt sich nicht selbst als Moderator hinzu. | Q1, Q2 |
| B4 | Der eigene Channel wird mit dem Streamer-Konto betrieben (volle Funktionsbreite), die zusätzlichen Channels mit dem Bot-Konto (Moderator-Rechte). Die Subscriptions der zusätzlichen Channels tragen den Channel als Broadcaster; die Flüsternachrichten empfangt das Bot-Konto. | Q1, Q2 |
| B5 | Der Adapter empfängt Ereignisse aller betriebenen Channels. Jedes Channel-Ereignis trägt seinen Channel (den Broadcaster), und der Adapter gibt ihn an das Event-Modell weiter ([`twitch-events.md`](twitch-events.md)). | Q1, Q4 |
| B6 | Chat, Chat löschen und Moderation wirken auf einen Channel. Ein ereignisgetriggertes Läufen wirkt auf den Channel des auslösenden Events; ein Läufen ohne Channel (Timer, manuelle Aufrufe) wirkt auf den eigenen Channel. | Q2 |
| B7 | Owner-Actions (Raid, Werbeeinschaltung, Titel und Kategorie, Clip, Stream-Marker, Umfrage, Vorhersage, Channel-Points-Belohnungen und Power-Ups) wirken nur auf den eigenen Channel. Für einen anderen Channel geben sie eine klare Fehlermeldung (der Channel wird nicht vom Streamer-Konto betrieben), die Frontends als deaktiviert darstellen können. | Q1, Q2 |
| B8 | Commands, Requirements, Timer und Settings gelten identisch für alle betriebenen Channels; es gibt in v1 keine pro-Channel-Konfiguration. | Q2, Q3 |
| B9 | Die Rate-Limits des Chat-Sendens sind je Channel eigene Buckets. | Q2 |
| B10 | Die Channel-Information (Titel, Kategorie, Live-Zustand, Zuschauer) wird für den eigenen Channel gemeldet; für zusätzliche Channels bleibt sie in v1 ohne Angabe. | Q2 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B11 | Ein konfigurierter Channel existiert nicht (Konto gelöscht) | der Mod-Check schlägt fehl; der Channel wird nicht betrieben, die Warnung bleibt, bis der Eintrag entfernt ist | Q1 |
| B12 | Der Mod-Status wird während des Laufens entzogen | beim nächsten Abgleich ist der Channel nicht mehr betrieben; seine Ereignisse werden nicht mehr empfangen | Q1 |
| B13 | Ein Event eines zusätzlichen Channels löst ein Command aus, das eine Owner-Action ausführt | die Owner-Action schlägt mit dem Fehler von B7 fehl; die übrigen Aktionen des Commands (Chat, Moderation) wirken weiter in dem Channel | Q2 |
| B14 | Ein Timer oder ein manueller Aufruf sendet eine Nachricht oder moderiert | es wirkt auf den eigenen Channel (B6) | Q2 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | Mix It Up arbeitet in genau einem Channel (dem Channel des Streamers) | der Bot arbeitet in mehreren Channels mit einem Account-Paar, ohne zusätzliche Logins | Feature-Anforderung des Projektinhabers (2026-10-12); das Twitch-Berechtigungsmodell (Moderator-Status) macht es ohne zusätzlichen Login möglich (Q1) |

## Offene Fragen (E2E-Verifizierung, Plan-Task 7)

- Die exakte je-Topic-Empfangs-Vorgabe für den Bot-Token in zusätzlichen
  Channels (die Twitch-Dokumentation verlangt für die `channel.*`-Topics mit
  User-Token Moderator- oder Broadcaster-Status; die Doku war bei der
  Recherche bot-geschützt, HTTP 403). B2 und B3 gehen von der sicheren
  Voraussetzung (Mod-Status) aus; die Verifizierung je Topic dokumentiert,
  wo sie enger oder locker ist.
- Die Whisper-Lieferung an das Bot-Konto aus zusätzlichen Channels.
- Die Shared-Chat-Ereignisse in zusätzlichen Channels (ob der Bot sie als
  Moderator erhält).

Abdeckungsstand (2026-10-12): Die twitch-cli-Mock-Server stellen einen
Broadcaster pro Umgebung dar, die Empfangs-Vorgaben je Topic lassen sich
gegen sie nicht prüfen; sie werden im manuellen E2E auf echten Kanälen
verifiziert. Automatisch abgedeckt sind stattdessen: der Mod-Check und
das Nicht-Betreiben fehlender oder entzogener Channels (B2, B3, B11,
B12), der Subscription-Sollzustand je Channel×Topic ohne die
Stream-Events (B10), die Channel-Dimension in den Eingängen und dem
Routing (B5, B6, B14), das Ownership-Gate (B7, B13) und die
Send-Buckets je Channel (B9).

## Akzeptanzkriterien

- [ ] B1: die Sektion `channels` akzeptiert Login-Listen und lehnt leere, doppelte und den eigenen Channel ab
- [ ] B3: ein Channel ohne Mod-Status wird nicht betrieben und meldet `mod_missing`
- [ ] B5: Events aus einem zusätzlichen Channel lösen Commands in diesem Channel aus
- [ ] B6: ein ereignisgetriggertes Chat- oder Moderations-Action wirkt auf den Channel des Events; Timer auf den eigenen Channel
- [ ] B7: eine Owner-Action auf einem zusätzlichen Channel schlägt mit der klaren Fehlermeldung fehl
- [ ] B9: der Send-Bucket eines Channels wird nicht durch den anderen limitiert

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q1 | Plattform-Doku | <https://dev.twitch.tv/docs/eventsub/> (User-Token-Vorgaben), <https://dev.twitch.tv/docs/api/reference/#getModerators> (Mod-Check), <https://dev.twitch.tv/docs/api/reference/#sendChatMessage> (broadcaster_id je Channel) | die je-Topic-Empfangs-Vorgaben stehen unter „Offene Fragen“ (E2E) |
| Q2 | Plan | [Plan 4.6](../superpowers/plans/2026-10-12-twitch-multi-channel.md), Abschnitt 3 (Design) | Festlegungen des Projekts |
| Q3 | Roadmap | [Roadmap 4.6](../roadmap.md) | Items und Exit-Kriterien |
| Q4 | Plattform-Doku | <https://dev.twitch.tv/docs/eventsub/websocket-eventsub/> (Event-Typen `channel.*` mit Broadcaster-Feldern) | Channel je Ereignis |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-10-12 | Erstellt für Phase 4.6: Voraussetzung und Prüfung (B1–B3), Token-Zuordnung (B4), Channel-Dimension (B5–B7, B9), geteilte Konfiguration (B8, B10), Randfälle (B11–B14), Abweichung A1, E2E-Fragen. |
| 2026-10-12 | Abdeckungsstand der offenen Fragen dokumentiert: die twitch-cli-Mock-Server stellen einen Broadcaster pro Umgebung dar, die je-Topic-Empfangs-Vorgaben bleiben das manuelle E2E; automatisch abgedeckt sind Mod-Check, Nicht-Betreiben fehlender Channels, Sollzustand je Channel×Topic, Channel-Dimension und Routing, Ownership-Gate und Send-Buckets je Channel. |
| 2026-10-12 | 4.8: B1 um die Login-Form-Prüfung beim Speichern ergänzt (1–25 Zeichen, Buchstaben, Ziffern, Unterstrich) und der Benutzerweg zur Konfiguration der Sektion: die neuen CLI-Befehle `profile channels list|add|remove` (roadmap 4.8). |
