# Spezifikation: Chat-Service

| | |
|---|---|
| **Status** | Entwurf |
| **Stand** | 2026-10-13 |
| **Bezug** | Roadmap Phase 5.1 (Core-Services, Chat), Plan §6.11, §6.14; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md), [Code-ADR-0011](../adr/code/0011-event-bus.md); [`events.md`](events.md), [`actions.md`](actions.md), [`requirements.md`](requirements.md), [`commands.md`](commands.md), [`twitch-multi-channel.md`](twitch-multi-channel.md) |
| **Umsetzung** | noch offen (geplant: `internal/chat`, Änderungen an `internal/eventservice`, `internal/action/chat`, `internal/settings`, `internal/requirement`, `internal/connector`) |

## Zweck und Umfang

Legt den Chat-Service des Cores fest: den Sendepfad (Nachricht, Antwort,
Flüstern, löschen, Chat leeren), den Chat-Verlauf im Speicher, das
optionale Chat-Protokoll auf Platte und den Stummen Chat. Dazu legt sie
die Stufen der Chat-Pipeline fest, die der Event-Service für eine
Chatnachricht durchläuft (Roadmap 5.1), und die Settings-Sektionen
`general` und `chat`.

Nicht Teil dieser Spezifikation: die Wortfilter, Bannwörter, Strikes und
Teilnahmeregeln der Moderation (Roadmap 5.5, `spec/moderation.md`); sie
kommen an die Moderationsstufe der Pipeline (B3). Die API für Senden und
Verlauf (Phase 6, `ChatService`) und die TUI-Ansicht (Phase 6.4).
Watchtime und Statistiken (Roadmap 5.2).

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Chat-Pipeline | der Weg einer Chatnachricht durch den Core: Normalisierung (Adapter), Validierung, Nutzer-Auflösung, Moderation, Trigger, Folge-Events, Verlauf (B1) |
| Chat-Service | der Dienst des Cores, der an die Chat-Plattformen schreibt (Senden, Flüstern, Löschen, Leeren), den Verlauf und das Protokoll hält und den Stummen Chat (B4–B25) |
| Chat-Verlauf | die zuletzt aufgenommenen Chatnachrichten einer Plattform, im Speicher (B12–B16) |
| Chat-Protokoll | das optionale Log aller aufgenommenen Chatnachrichten auf Platte (B17–B20) |
| Stummer Chat | der Zustand, in dem der Core jede neue Chatnachricht löscht, bis er ihn aufhebt (B85 von [`actions.md`](actions.md), hier B21–B25) |
| Standardplattform | die Plattform, die Regeln verwenden, die eine Plattform brauchen, der Durchlauf aber keine nennt (B26–B28) |

## Verhalten

### Chat-Pipeline

| ID | Regel | Quellen |
|---|---|---|
| B1 | Eine empfangene Chatnachricht läuft in dieser Reihenfolge durch die Pipeline: **normalisieren** (der Adapter übergibt eine Nachricht mit Text, Emotes, Rollen, Kanal und der Plattform-ID der Nachricht; [`events.md`](events.md), B13), **validieren** (das Konto gehört zur Plattform, der Text ist nicht leer; Nachrichten des Bot-Kontos lösen weder Ereignisse noch Commands aus, [`events.md`](events.md), B13), **Nutzer auflösen** (die Plattform-Identität wird dem plattformübergreifenden Nutzer zugeordnet, neu angelegt, wenn unbekannt), **Moderation** (der Stumme Chat, B21–B25; ab Roadmap 5.5 die Wortfilter und Bannwörter), **Trigger** (der Chat-Command, dessen Trigger die Nachricht nennt, [`commands.md`](commands.md), B16), **Folge-Events** (`chat.user.new`, `chat.user.join`, `chat.user.first_message` und die Begrüßung in der Reihenfolge von [`events.md`](events.md), B13) und zuletzt **Verlauf** (B15). Ein Schritt, der die Nachricht verwirft (Stummer Chat), macht mit ihr nichts weiter; die übrigen Schritte halten die Reihenfolge von [`events.md`](events.md), B13. | QP, Q1 |
| B2 | Ein Beitritt ohne Nachricht läuft durch dieselben Stufen ohne Trigger, Folge-Events und Verlauf, nur `chat.user.new` und `chat.user.join` ([`events.md`](events.md), B14); der Stumme Chat greift nicht darauf. | QP, Q1 |
| B3 | Die Moderationsstufe ist der feste Platz in der Pipeline für die Prüfung eingehender Nachrichten. Roadmap 5.1 bezieht nur den Stummen Chat (B21–B25); die Wortfilter und Bannwörter (Roadmap 5.5) prüfen an derselben Stelle, nach dem Stummen Chat, und können die Nachricht ebenfalls verwirfen. | QP |

### Chat-Service: Senden

| ID | Regel | Quellen |
|---|---|---|
| B4 | Der Chat-Service ist der einzige Weg des Cores, an die Chat-Plattformen zu schreiben: `Send` (Nachricht in den Chat), `Whisper` (private Nachricht), `Delete` (eine Nachricht löschen) und `ClearChat` (den Chat leeren). Actions, die schreiben (Chat-Action, Plattform-Nachricht, das Löschen der auslösenden Nachricht, der Stumme Chat), gehen über ihn; direkte Port-Aufrufe aus Actions entfallen. | QP, A1 |
| B5 | `Send` geht an alle verbundenen Plattformen zugleich, in ihren Channels nach [`twitch-multi-channel.md`](twitch-multi-channel.md), B6; ist keine verbunden, wird nichts gesendet, der Core loggt das, und es ist kein Scheitern ([`actions.md`](actions.md), B62). | Q1, QP |
| B6 | Der Absender ist das Bot-Konto der Plattform, wenn es verbunden ist, sonst das Streamer-Konto; „als Streamer senden“ zwingt das Streamer-Konto ([`actions.md`](actions.md), B61). Das gilt für `Send`, `Whisper`, `Delete` und `ClearChat`. | Q1 |
| B7 | Ist der Text nach dem Rendern leer oder nur Leerraum, wird nichts gesendet; das ist kein Scheitern ([`actions.md`](actions.md), B65). | Q1 |
| B8 | `Send` kann eine Antwort auf die auslösende Nachricht sein: auf der Plattform des Durchlaufs, wenn es eine gibt und die Plattform Antworten kennt; auf den übrigen Plattformen ist sie eine gewöhnliche Nachricht ([`actions.md`](actions.md), B64). | Q1, QP |
| B9 | Scheitert das Senden auf mindestens einer Plattform, scheitert `Send`; die Meldung nennt die Plattformen, und auf den anderen bleibt die Nachricht gesendet ([`actions.md`](actions.md), B66). | A2 |
| B10 | `Whisper` geht privat an den Empfänger, einen Nutzernamen mit oder ohne `@`, auf jeder verbundenen Plattform, die Flüstern kennt und auf der der Empfänger bekannt ist; den Empfänger sucht der Service je Plattform erst unter den bekannten Nutzern, dann über die Plattform ([`actions.md`](actions.md), B81). Auf den übrigen Plattformen entfällt sie, mit Eintrag im Log; ist sie auf keiner möglich, scheitert `Whisper` ([`actions.md`](actions.md), B63). | Q1, Q3 |
| B11 | Aufteilen zu langer Nachrichten und Rate-Limits übernimmt der Plattform-Adapter ([`actions.md`](actions.md), B65; Plan §6.11). Der Service übergibt den vollen Text. Die Mock-Plattform kennt bewusst keine solchen Regeln: sie loggt und publiziert, was sie bekommt. | QP, A3 |

### Chat-Verlauf und Protokoll

| ID | Regel | Quellen |
|---|---|---|
| B12 | Der Chat-Verlauf hält die zuletzt aufgenommenen Nachrichten im Speicher, als Ringpuffer je Plattform; der Channel steht als Feld des Eintrags. Er übersteht keinen Neustart. | Q1, A4 |
| B13 | Ein Eintrag trägt: Zeit (UTC), Plattform, Channel (leer für den eigenen), Absender (Nutzer-ID und Anzeigename), Text, einen Schalter für Bot-Nachrichten und die Plattform-ID der Nachricht, wenn es eine gibt. | QP |
| B14 | Aufgenommen wird, was die Pipeline als Chatnachricht entgegennimmt und nicht verwirft — auch Nachrichten des Bot-Kontos; was der Stumme Chat löscht, wird nicht aufgenommen (B21). Beitritte ohne Nachricht (B2) und Ereignisse sind keine Chatnachrichten und werden nicht aufgenommen. | QP |
| B15 | Die Größe des Ringpuffers legt `chat.historySize` fest (ganze Zahl von 1 bis 10 000, Standard 200); die geänderte Größe gilt ab dem nächsten Speichern. | Q1, A5 |
| B16 | Die ältesten Einträge fallen heraus, wenn der Puffer voll ist; die API (Phase 6) liest den Verlauf je Plattform. | Q1 |
| B17 | Das optionale Chat-Protokoll auf Platte legt `chat.protocol` fest (Standard aus). Ist es an, schreibt der Service jeden Eintrag, den er in den Verlauf nimmt, zusätzlich in eine Datei je Plattform `<data-dir>/chat/<plattform>.jsonl` (JSON Lines, dieselben Felder wie der Eintrag, B13). | QP, A6 |
| B18 | Die Datei rotiert nach Größe (10 MiB, drei alte Dateien); der Schreibfehler ist eine Warnung im Log und nie ein Fehler der Pipeline oder des Sendens. | A6 |
| B19 | Ein Neustart setzt den Verlauf, nicht das Protokoll: die Datei wächst weiter. | A6 |

### Stummer Chat

| ID | Regel | Quellen |
|---|---|---|
| B20 | Die Moderation-Action `disable_chat`/`enable_chat` schaltet den Stummen Chat an und aus ([`actions.md`](actions.md), B85). Der Zustand lebt im Chat-Service, wird nicht gespeichert, und endet mit dem Core. | Q2 |
| B21 | Solange der Chat stumm ist, löscht der Core jede neue Chatnachricht der verbundenen Plattformen, außer denen von Streamer- und Bot-Konto und denen, die die Plattform nicht löschen lässt ([`actions.md`](actions.md), B85). Der Stumme Chat ist die erste Prüfung der Moderationsstufe (B3). | Q2 |
| B22 | Eine Nachricht, die der Stumme Chat löscht, löst nichts aus: keine Ereignisse, kein Trigger, keine Begrüßung, keine Aufnahme in Verlauf und Protokoll (B1, B14). Nachrichten von Streamer- und Bot-Konto laufen unverändert durch. | Q2, A7 |
| B23 | Das Löschen geht über den Chat-Service (`Delete`), der Adapter entscheidet, welches Konto löscht (B6). Kann die Plattform die Nachricht nicht löschen, schreibt der Core eine Warnung ins Log; die Nachricht wird verworfen, wie gelöscht. | Q2, A7 |
| B24 | Ist keine Plattform verbunden, wenn der Chat stumm geschaltet wird, passiert nichts; das Schalten ist kein Fehler. Die Pipeline fragt den Zustand je Nachricht, nicht beim Schalten. | QP |
| B25 | Der Stumme Chat greift nicht auf Beitritte ohne Nachricht (B2). | QP |

### Settings: `general` und `chat`

| ID | Regel | Quellen |
|---|---|---|
| B26 | Die Settings-Sektion `general` hält `defaultPlatform`: den Namen der Standardplattform (Standard `twitch`); ein unbekannter Name lehnt das Speichern ab. Gilt ab dem nächsten Lesen. | Q1, QP |
| B27 | Die Standardplattform ersetzt die feste in allen Regeln, die eine Plattform brauchen, der Durchlauf aber keine nennt: die Reihenfolge der verbundenen Plattformen für Arten ohne Nutzer ([`actions.md`](actions.md), B82), die Suche nach Nutzern in Anforderungen ([`requirements.md`](requirements.md), B4) und in der Moderation ([`actions.md`](actions.md), B81, B82), und der Name des Cooldowns je Nutzer ohne Plattform. | Q1, QP |
| B28 | `defaultPlatform` muss ein gültiger Plattformname sein; ob das Profil sie kennt, prüft der Core nicht beim Speichern — eine unbekannte Standardplattform ist ein Fehler der jeweiligen Regel, gemeldet wie eine fehlende Plattform. | A8 |
| B29 | Die Settings-Sektion `chat` hält: `historySize` (B15), `protocol` (B17) und `deleteTriggerMessage` (B30). Alle gelten ab dem nächsten Speichern bzw. Lesen. | Q1, QP |
| B30 | `deleteTriggerMessage` (Standard aus) ist der globale Schalter des „Löschen der auslösenden Nachricht“ ([`requirements.md`](requirements.md), B61): Ist er an, löscht der Requirement-Service die auslösende Nachricht für jeden Chat-Command des Profils, zusätzlich zu (ODER) dem Schalter je Command. Er wirkt nur auf Commands, die eine Chatnachricht auslösen. | Q4 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B31 | Stummer Chat an, Nachricht vom Streamer-Konto | läuft unverändert durch, nichts wird gelöscht | Q2, A7 |
| B32 | Stummer Chat an, die Plattform kann die Nachricht nicht löschen | Warnung im Log; die Nachricht wird verworfen, ohne Ereignisse und Trigger | Q2, A7 |
| B33 | `Send` ohne verbundene Plattform | nichts wird gesendet, Log-Eintrag, kein Fehler | Q1 |
| B34 | `Whisper` an einen Namen, den nur eine der verbundenen Plattformen kennt | die Nachricht geht auf dieser Plattform, auf den anderen ein Log-Eintrag; kein Fehler, solange eine zugeht | Q1 |
| B35 | `Whisper`, den keiner der verbundenen Plattformen zu liefern vermag | `Whisper` scheitert | Q1 |
| B36 | Verlauf: die Größe wird verkleinert | die überflüssigsten Einträge fallen sofort heraus | QP |
| B37 | Protokoll: der Schreibfehler (z. B. volles Laufwerk) | Warnung im Log; der Verlauf und die Pipeline laufen weiter | A6 |
| B38 | `deleteTriggerMessage` global an, der Schalter des Commands aus | die auslösende Nachricht wird gelöscht (ODER) | Q1, Q4 |
| B39 | `defaultPlatform` auf eine Plattform, die nicht verbunden ist | die Regeln verhalten sich wie ohne verbundene Plattform für sie: nichts wird gesendet, keine Suche, klare Fehlermeldung der jeweiligen Regel | A8 |
| B40 | Stummer Chat wird geschaltet, während Nachrichten ankommen | der Zustand gilt je Nachricht zum Zeitpunkt ihrer Prüfung; bereits verarbeitete Nachrichten holt er nicht nach | QP |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | Der Sendepfad liegt in der Chat-Action und in der Chat-Ansicht (GUI) | ein Chat-Service im Core (`internal/chat`), die Action delegiert, die API (Phase 6) nutzt denselben | Plan §6.14: die `ChatService`-API braucht einen Dienst; eine einzige Stelle für B5–B10 |
| A2 | Teilerfolg beim Senden über mehrere Plattformen: die GUI meldet pro Plattform | `Send` scheitert, wenn mindestens eine Plattform scheitert; die Meldung nennt die Plattformen | [`actions.md`](actions.md), B66; Code-ADR-0017: klare Signale statt magischer Werte |
| A3 | Die Plattform-Adapter kennen ihre eigenen Regeln (Länge, Rate); die Mock-Plattform des Originals imitiert sie | die Mock-Plattform kennt keine Regeln: sie loggt und publiziert, was sie bekommt | Die Mock-Plattform dient Tests und Demos (ADR-0004); eine künstliche Begrenzung würde Tests verlangsamen und nichts prüfen, das der Twitch-Adapter nicht schon hat |
| A4 | Der Chat-Verlauf ist an die GUI gebunden und gilt je Chat-Ansicht | Ringpuffer je Plattform im Core, Channel als Feld des Eintrags; die GUI (Desktop-Track) und die API (Phase 6) lesen denselben | Headless-MVP (M2): der Verlauf muss ohne GUI existieren; Multi-Channel mit einem Puffer je Plattform (Plan-Abschnitt 5) |
| A5 | Die Nachrichtengrenze ist eine GUI-Einstellung (Anzahl der angezeigten Nachrichten) | `chat.historySize` begrenzt den Ringpuffer des Cores (1–10 000, Standard 200) | der Core hält den Verlauf, nicht die GUI; Standard wie der Command-Verlauf (200) |
| A6 | kein Chat-Protokoll auf Platte | optionales JSON-Lines-Protokoll je Plattform mit Rotation nach Größe (10 MiB, drei alte Dateien) | Nachvollziehbarkeit ohne Telemetrie (ADR-0011); `shortcut:` — keine Aufbewahrung nach Tagen, nur nach Größe |
| A7 | Der Stumme Chat in der GUI verhindert zusätzlich das Empfangen | der Stumme Chat löscht eingehende Nachrichten und unterdrückt damit Ereignisse, Trigger und Aufnahme; er verhindert nichts beim Senden | ADR-0003: der Core ist Headless; das Senden ist der Zweck des Cores, der Stumme Chat betrifft den Chat der Zuschauer |
| A8 | die Standardplattform ist in der GUI wählbar, aber ans GUI-Konto gebunden | `general.defaultPlatform` in den Settings des Profils; eine nicht verknüpfte Standardplattform ist kein Fehler beim Speichern, sondern in der jeweiligen Regel | die Settings sind pro Profil, nicht pro GUI-Sitzung; keine magischen Werte (Code-ADR-0017) |

## Akzeptanzkriterien

Prüfbare Kriterien. Tests verweisen auf diese IDs.

- [ ] B1: eine Nachricht löst in der Reihenfolge von [`events.md`](events.md), B13 aus und landet im Verlauf; ein Fake des Chat-Port zeigt, dass die Stufen in dieser Reihenfolge laufen.
- [ ] B4: die Chat-Action sendet über den Chat-Service; es gibt keinen direkten Port-Aufruf aus der Action (Code-Inspektion + Tests gegen Fakes).
- [ ] B5–B9, B33: Senden auf alle verbundenen Plattformen, Konto je B6, leere Nachricht, Antwort nur auf der Plattform des Durchlaufs, Teilerfolg mit Meldung je Plattform.
- [ ] B10, B34, B35: Flüstern je Plattform mit Empfänger-Suche, übersprungene Plattformen im Log, Scheitern ohne eine lieferbare.
- [ ] B12–B16: der Ringpuffer je Plattform mit den Feldern von B13, die Größe von B15, das Herausfallen der ältesten (B16, B36).
- [ ] B14, B22: Nachrichten des Bot-Kontos werden aufgenommen; was der Stumme Chat löscht, nicht — und löst nichts aus.
- [ ] B17–B19: das Protokoll schreibt JSON Lines je Plattform, rotiert nach Größe, überlebt einen Neustart; ein Schreibfehler ist eine Warnung (B37).
- [ ] B20–B25, B31, B32, B40: der Stumme Chat schaltet an und aus, löscht, was B21 sagt, lässt Streamer und Bot durch, warnt bei einem fehlgeschlagenen Löschen, und greift nicht auf Beitritte.
- [ ] B26–B28, B39: `defaultPlatform` validiert, gilt ab dem nächsten Lesen, und wird von der Reihenfolge, den Anforderungen und der Moderation verwendet.
- [ ] B29, B30, B38: die Sektion `chat` validiert; der globale Schalter löscht die auslösende Nachricht zusätzlich zu dem je Command (Q4).

## Offene Fragen

- Keine; die Entscheidungen stehen in der Änderungshistorie (2026-10-13).

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q1 | Doku | <https://mixitup.bot/docs/chat> | Chat-Funktion: Absender (Bot, sonst Streamer; „Send as Streamer“), Flüstern mit Empfänger oder auslösender Nutzer, Chat leeren, Chat deaktivieren/aktivieren, Ereignisse von Chat-Aktivität, Slash-Commands; abgerufen 2026-10-13 |
| Q2 | Doku | <https://mixitup.bot/docs/actions/moderation-action> | `disable_chat`/`enable_chat`: der Chat wird stumm, bis er wieder an ist; abgerufen 2026-10-13 |
| Q3 | Doku | <https://mixitup.bot/docs/actions/chat-action> | Chat-Action: Senden auf allen verbundenen Plattformen, Bot-Konto oder Streamer-Konto, Antwort, leere Nachricht; abgerufen 2026-10-13 |
| Q4 | Projekt | Roadmap 5.1; [`requirements.md`](requirements.md), B61 | „einer für alle Commands kommt mit der Settings-Sektion ‚chat‘“: der globale Schalter neben dem je Command |
| QP | Projekt | [Plan](../plan.md) §6.11, §6.12, §6.14; [ADR-0003](../adr/0003-betriebsmodi.md), [ADR-0011](../adr/0011-keine-telemetrie.md); [Code-ADR-0011](../adr/code/0011-event-bus.md), [Code-ADR-0017](../adr/code/0017-klare-signale-statt-magischer-werte.md) | Plattform-Abstraktion (normalisiertes Chat-Modell, Rate-Limits, Aufteilung), API `ChatService`, Betriebsmodi, keine Telemetrie |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-10-13 | Erstfassung nach dem Plan [2026-10-13-phase-5-chat.md](../superpowers/plans/2026-10-13-phase-5-chat.md). Entscheidungen dabei: die Pipeline-Stufen gelten wie [`events.md`](events.md), B13, der Stumme Chat ist die erste Prüfung der Moderationsstufe (B3), die Chat-Action delegiert auf den Chat-Service (B4, A1), Teilerfolg scheitert mit Meldung je Plattform (B9, A2), die Mock-Plattform kennt keine Senderegeln (B11, A3), der Verlauf ist je Plattform mit Channel als Feld (B12, A4) und standardmäßig 200 Einträge (B15, A5), das Protokoll ist JSON Lines mit Rotation nach Größe (B17–B19, A6), der Stumme Chat unterdrückt Ereignisse und Aufnahme statt das Empfangen zu verhindern (B22, A7) und greift nicht auf Beitritte (B25), die Standardplattform steht in `general` (B26–B28, A8), und der globale Schalter von B30 wirkt per ODER neben dem je Command. |
