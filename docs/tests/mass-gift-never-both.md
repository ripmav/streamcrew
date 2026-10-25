# Test-Matrix: Mass Gifts (Schwelle, „nie beide“)

Issue: Code-Review 2026-10-24, T1 + Nachprüfung 2026-10-25 (T1, T1a,
T1b) — ab der Schwelle feuert nur das Sammelereignis, darunter nur die
Einzelgeschenke, und die Einzelgeschenke einer gefeuerten Sammelaktion
werden verworfen (events.md B5 „nie beide“); `firedGifts` wächst nicht
bis zum nächsten `Run`.

| Test | Datei | Deckt ab |
|---|---|---|
| `TestMapNotification` (sub_gift-Fall) | `internal/twitch/map_test.go` | T1: `sub_gift` mappt `community_gift_id` auf `Gift.CommunityGiftID`, ein Geschenk außerhalb einer Sammelaktion trägt eine leere ID; das Sammelereignis (`community_sub_gift`) trägt die ID. |
| `TestMassGift` | `internal/eventservice/service_test.go` | B5/B26/B29: unter der Schwelle nur Einzelgeschenke, an/über ihr nur das Sammelereignis; die Einzelgeschenke einer gefeuerten Aktion (mit ID) werden verworfen, unterhalb der Schwelle laufen sie weiter; ein Geschenk ohne Aktion läuft immer. |
| `TestPurgeFiredGifts` | `internal/eventservice/receive_internal_test.go` | T1a: Einträge in `firedGifts` gelten nur für `firedGiftTTL` (5 min) — abgelaufene Einträge werden entfernt, aktuelle bleiben; die Map wächst nicht bis zum nächsten `Run`. |
| `TestEventData` (recipients of a follow) | `internal/eventservice/service_test.go` | T1b: Empfänger sind nur an Sammelgeschenken erlaubt, an jedem anderen Ereignis eine `ErrShape` (Einzelgeschenke tragen den Beschenkten als Target). |

Nicht abgedeckt: das Auslaufen der `firedGiftTTL`-Frist im laufenden
Empfang (echte Zeit, 5 min) — die Einzelseite wird von
`TestPurgeFiredGifts` mit gesetzten Zeitstempeln geprüft, der
Zeitablauf selbst ist eine `time.Now()`-Vergleichszeile.
