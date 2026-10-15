# Spezifikation: Moderation

| | |
|---|---|
| **Status** | Entwurf |
| **Stand** | 2026-10-16 |
| **Bezug** | Roadmap 5.5 (Core-Services, Moderation), Plan §5.5; [Code-ADR-0013](../adr/code/0013-typ-registry.md); [`chat-service.md`](chat-service.md) (B1, B3, B21–B25), [`actions.md`](actions.md) (B80–B92), [`users-and-roles.md`](users-and-roles.md), [`commands.md`](commands.md) |
| **Umsetzung** | noch offen (später: `internal/moderation`, die Moderationsstufe in `internal/eventservice`, die Settings-Sektion `moderation` in `internal/settings`) |

## Zweck und Umfang

Legt die Moderation eingehender Chatnachrichten fest: die Prüfung an der
Moderationsstufe der Chat-Pipeline ([`chat-service.md`](chat-service.md), B3),
die Teilnahmeregeln, die Wortfilter und Bannwörter und die Strikes mit ihren
Folge-Commands. Dazu die Settings-Sektion `moderation`.

Nicht Teil dieser Spezifikation: der Stumme Chat (Roadmap 5.1,
[`chat-service.md`](chat-service.md), B21–B25) — er bleibt die erste Prüfung
der Moderationsstufe und greift vor allem, was hier geprüft wird. Die
Moderation-Actions, die ein Command auf einen Zielnutzer ausführt
([`actions.md`](actions.md), B80–B92) — sie sind der Wirkweg der
Folge-Commands. Die API und TUI für die Moderation (Phase 6).

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Moderationsstufe | der feste Platz in der Chat-Pipeline nach dem Stummen Chat, an dem eingehende Nachrichten geprüft werden ([`chat-service.md`](chat-service.md), B3) |
| Teilnahmeregeln | die Bedingungen, die ein Nutzer erfüllen muss, um chatten zu dürfen (Follow-Dauer, Watchtime, Rolle) |
| Filter | eine Regel, die eine Nachricht auf ein Verbot prüft: Bannwort, Link, Großbuchstaben, Satzzeichen oder Emotes |
| Verstoß | eine Nachricht, die einen Filter verletzt, dessen Ausnahmen den Autor nicht schützen |
| Strike | der Zähler eines Nutzers für Verstöße, gespeicherte Statistik (`users-and-roles.md`, B9) |
| Strike-Stufe | eine Zahl, die einem Folge-Command zugeordnet ist |
| Folge-Command | der Command, der bei einer Strike-Stufe läuft; seine Actions sind die Maßnahmen (löschen, Timeout, Bann) |
| Ausnahmen | die Rollen, die von einem Filter befreit sind |

## Verhalten

| ID | Regel | Quellen |
|---|---|---|
| B1 | Die Moderationsstufe prüft eingehende Chatnachrichten in dieser Reihenfolge: erst die Teilnahmeregeln, dann die Filter. Der Stumme Chat ([`chat-service.md`](chat-service.md), B21) greift davor. Eine Nachricht, die verworfen wird, läuft nicht weiter: keine Ereignisse, kein Trigger, keine Begrüßung, kein Verlauf und kein Protokoll (wie B22 dort). | QP, Roadmap 5.5 |
| B2 | Nachrichten des Streamer- und des Bot-Kontos laufen unverändert durch: keine Teilnahmeregeln, keine Filter. | Q2, [`chat-service.md`](chat-service.md) B21, B31 |
| B3 | **Teilnahme:** ein Nutzer darf chatten, wenn er alle gesetzten Teilnahmeregeln erfüllt: eine Mindest-Follow-Dauer (ganze Tage, ab Follow), eine Mindest-Watchtime (ganze Minuten) und eine Mindestrolle. Ohne jede gesetzte Regel darf jeder chatten. Erfüllt der Nutzer eine Regel nicht, wird seine Nachricht verworfen — ohne Strike und ohne Folge-Command. | Q5, Roadmap 5.5 |
| B4 | **Filter:** ein Filter ist eine Regel von einer Art: **Bannwort** (ein Wort oder ein Muster mit Wildcards, ohne Rücksicht auf die Groß- und Kleinschreibung), **Link** (die Nachricht nennt eine URL), **Großbuchstaben**, **Satzzeichen** oder **Emotes** (letzte drei mit einer Schwelle, absolut oder prozentual). Jeder Filter trägt Ausnahmen: eine Liste von Rollen, deren Inhaber befreit sind. | Q5, Roadmap 5.5 |
| B5 | **Bannwort:** die Nachricht verletzt den Filter, wenn ein Wort des Textes dem Muster entspricht; ein Muster mit `*` am Anfang, Ende oder beiden trifft auf jede Fort- bzw. Rück- bzw. Umstellung (z. B. `*bad*` ein Wort, das „bad“ enthält). Die Prüfung ist ohne Rücksicht auf die Schreibweise. | Q5 |
| B6 | **Link:** die Nachricht verletzt den Filter, wenn sie eine URL nennt (`http://`, `https://` oder ein Bare-Domain mit Punkt, z. B. `example.com`). | Q5 |
| B7 | **Großbuchstaben/Satzzeichen/Emotes:** die Nachricht verletzt den Filter, wenn der Wert die Schwelle erreicht. Absolut: Großbuchstaben = die Zahl der Worte, die ganz groß sind (mindestens zwei Zeichen); Satzzeichen = die Zahl der Satzzeichen; Emotes = die Zahl der Emotes. Prozentual: der Anteil an allen Buchstaben bzw. an allen Worten, der groß ist bzw. ein Satzzeichen bzw. ein Emote ist. Eine Schwelle von 0 lässt den Filter aus. | Q5 |
| B8 | **Verstoß:** eine Nachricht, die mindestens einen Filter verletzt und deren Autor von keinem dieser Filter befreit ist, ist ein Verstoß. Zählt wird der erste verletzte Filter in der Reihenfolge der Liste; eine Nachricht ist höchstens ein Verstoß. | Q5 |
| B9 | **Strike:** bei einem Verstoß steigt der Strike-Zähler des Nutzers um eins. | Q5, [`users-and-roles.md`](users-and-roles.md) B9 |
| B10 | **Folge-Command:** die Strike- Stufen sind eine Liste aus (Stufe, Command). Ein Verstoß, der den Nutzer auf die Stufe N bringt, führt den Folge-Command der Stufe aus, sofern gesetzt. Ist N über der höchsten gesetzten Stufe, gilt die höchste gesetzte Stufe. Der Command läuft mit den Parametern der Nachricht (Nutzer, Plattform, Channel, Nachricht, Emotes) und ohne Anforderungen; seine Actions sind die Maßnahmen (löschen, Timeout, Bann, [`actions.md`](actions.md), B80 ff.). Ohne gesetzte Stufen steigt nur der Zähler. | Q5, Roadmap 5.5 |
| B11 | **Verwerfen:** die Nachricht eines Verstoßes wird verworfen (B1). Der Folge-Command führt die Maßnahmen auf der Plattform aus (das Löschen der Nachricht, den Timeout, den Bann). | Q5, A2 |
| B12 | **Zurücksetzen:** das Zurücksetzen der Strikes ([`actions.md`](actions.md), B84) setzt den Zähler aller Nutzer auf 0; der nächste Verstoß ist wieder die Stufe 1. | Q2, [`actions.md`](actions.md) B84 |
| B13 | Die Sektion `moderation` hält die Teilnahmeregeln, die Filter und die Strike-Stufen. Sie gilt ab der nächsten Nachricht; die Filter in der Reihenfolge der Liste. | QP, Roadmap 5.5 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B20 | Kein Filter gesetzt | keine Filterprüfung; die Teilnahmeregeln gelten weiter | Q5 |
| B21 | Eine Nachricht verletzt zwei Filter | nur der erste (in der Liste) zählt; ein Strike | Q5, B8 |
| B22 | Ein befreiter Nutzer (z. B. Moderator) verletzt einen Filter | kein Verstoß; die Nachricht läuft durch | Q5, B4 |
| B23 | Ein Nutzer über der höchsten gesetzten Stufe verstößt | der Folge-Command der höchsten Stufe läuft | Q5, B10 |
| B24 | Keine Teilnahmeregeln gesetzt | jeder darf chatten | Q5, B3 |
| B25 | Ein Verstoß ohne gesetzten Folge-Command | der Zähler steigt, die Nachricht wird verworfen, es läuft kein Command | Q5, B10, B11 |
| B26 | Die Plattform kann die Nachricht nicht löschen | der Folge-Command warnt wie die anderen Maßnahmen; der Verstoß gilt | Q2, [`actions.md`](actions.md) B83 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | Die Filter treffen je nach Version unterschiedlich (Wildcards, Emotes) | eine feste Regelmenge (B4–B7) mit Wildcards nur am Wortanfang/-ende/-innen über `*` | nachvollziehbare, testbare Regeln |
| A2 | Der Stumme Chat und die Filter greifen vermischt | der Stumme Chat zuerst, dann Teilnahmeregeln, dann Filter (B1) | eine feste, prüfbare Reihenfolge an einer Stufe |
| A3 | Strikes und ihre Folgen sind an die Plattform gebunden | Strikes sind eine Nutzer-Statistik (plattformübergreifend); die Folge-Commands wirken über die Plattform (B9, B10) | die Rollen und Statistiken sind plattformübergreifend (users-and-roles.md) |

## Akzeptanzkriterien

- [ ] B1–B2: die Moderationsstufe prüft nach dem Stummen Chat; Streamer und Bot laufen durch; eine verworfene Nachricht löst nichts aus.
- [ ] B3, B24: die Teilnahmeregeln (Follow-Dauer, Watchtime, Rolle) lassen nur Erlaubte chatten; ohne Regeln jeder.
- [ ] B4–B7, B20: je Filter-Art das Verhalten (Bannwort mit Wildcards, Link, Großbuchstaben/Satzzeichen/Emotes absolut und prozentual); ohne Filter keine Prüfung.
- [ ] B8, B21, B22: der erste verletzte Filter zählt, eine Nachricht ist ein Verstoß, befreite Nutzer sind ausgenommen.
- [ ] B9–B12, B23, B25: der Verstoß steigert den Strike, der Folge-Command der Stufe (bzw. der höchsten) läuft mit den Parametern der Nachricht, ohne Stufen nur der Zähler, Zurücksetzen beginnt bei 1.

## Offene Fragen

(keine — die Regeln sind oben festgelegt; die Maßnahmen der Folge-Commands
sind die bestehenden Moderation-Actions)

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q1 | Doku | Roadmap 5.5 | Wortfilter, Bannwörter, Strikes, Teilnahmeregeln, Sektion `moderation` |
| Q2 | Doku | [`chat-service.md`](chat-service.md) | die Moderationsstufe (B3), der Stumme Chat (B21–B25), Verwerfen (B22) |
| Q5 | Doku | [`actions.md`](actions.md), [`users-and-roles.md`](users-and-roles.md) | die Moderation-Actions und Strikes als Maßnahmen und Statistik |
| QP | Plan | §5.5 | die Reihenfolge an der Stufe und die Sektion |
