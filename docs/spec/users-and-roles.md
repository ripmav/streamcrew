# Spezifikation: Nutzer und Rollen

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-10-03 |
| **Bezug** | Roadmap Phase 2.2 (Nutzer, Rollenmodell), 5.2; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md), [Code-ADR-0009](../adr/code/0009-ids-und-zeit.md); Plan §5.5, §6.13, Anhang A.6, A.7 |
| **Umsetzung** | Datenmodell und Rollen umgesetzt: `internal/domain/role`, `internal/domain/user`, `internal/domain/platform`, Repository in `internal/store`; die feinen Stufen der Rangordnung (B20) seit Roadmap 3.5, mit Migration 0010 für gespeicherte Rollen und Version 2 der Rollen-Anforderung. Offen: Titelregeln (B5) und Vergabe der Regular-Rolle (B26) mit Phase 5.2, nutzerspezifische Chat-Commands (B8, P1), Import (B11, P2) |

## Zweck und Umfang

Beschreibt, welche Daten streamcrew über Personen im Chat führt, wie eine Person mit Konten auf mehreren Plattformen zusammenhängt und wie Rollen geordnet und geprüft werden.

Nicht Teil dieser Spezifikation:

- Währungen, Ränge und Inventare (Phase 8, eigene Spezifikation `economy.md`)
- wie Watchtime und Statistiken im laufenden Betrieb gezählt werden (Phase 5.2); hier stehen nur die gespeicherten Werte
- das Zusammenführen von Konten (Phase 5.2, P1); hier steht nur, dass das Datenmodell es erlaubt

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Nutzer | eine Person aus Sicht von streamcrew, mit eigener ID, unabhängig von der Plattform |
| Plattform-Identität | das Konto eines Nutzers auf einer Plattform (Twitch, YouTube, Kick …) mit dessen ID und Namen |
| Rolle | eine Eigenschaft eines Nutzers im Kanal, etwa Follower oder Moderator; manche Rollen gelten auf allen Plattformen, manche nur auf einer |
| Rang einer Rolle | ihre Position in der Rangordnung; höhere Rollen schließen die Rechte niedrigerer ein |
| Mindestrolle | die Rolle, die eine Anforderung verlangt; sie ist erfüllt, wenn der Nutzer diese oder eine höhere Rolle hat |
| Hauptrolle | die höchste Rolle eines Nutzers |
| Regular | Stammzuschauer; eine Rolle, die streamcrew selbst vergibt, nicht die Plattform |

## Verhalten

### Nutzer und Identitäten

| ID | Regel | Quellen |
|---|---|---|
| B1 | Jeder Nutzer hat eine eigene, unveränderliche ID (UUIDv7) unabhängig von den Plattformen. | Code-ADR-0009 |
| B2 | Ein Nutzer hat eine oder mehrere Plattform-Identitäten. Eine Identität besteht aus Plattform, ID auf der Plattform, Login-Name und Anzeigename; dazu optional Chatfarbe und Profilbild-URL. | Q1, Q2 |
| B3 | Plattform und ID auf der Plattform sind zusammen eindeutig: Dasselbe Plattformkonto gehört nie zu zwei Nutzern. Gesucht wird ein Nutzer über diese Kombination, nicht über den Namen, weil sich Namen ändern können. | Q1, Q2 |
| B4 | Ändert sich Login- oder Anzeigename auf der Plattform, übernimmt streamcrew den neuen Namen beim nächsten Kontakt; die ID bleibt. | Q2 (Nutzer-Identifier nennen Name und Anzeigename getrennt von der ID) |
| B5 | Ein Nutzer kann einen eigenen Titel haben, den der Streamer vergibt. Ohne eigenen Titel gilt der erste passende Titel aus den Titelregeln des Streamers: Jede Regel hat einen Namen, eine Mindestrolle und Mindestmonate, die bei Follower-Titeln ab dem Follow und bei Abonnenten-Titeln ab dem Abo-Beginn zählen; geprüft wird nach der höchsten Rolle, dann nach den meisten Monaten. Ab Werk gibt es keine Regel. Passt keine, lautet der Titel „Kein Titel“ in der Sprache des Profils. | Q1, Q2 (`$usertitle`), Q11 |
| B6 | Ein Nutzer kann Notizen haben (freier Text des Streamers). | Q2 (`$usernotes`) |
| B7 | Ein Nutzer kann von bestimmten Funktionen ausgenommen werden: Zuwachs und Anforderungen von Währung und Rang, zufällige Auswahl in Spielen, Ranglisten. Diese Ausnahme ist ein einzelnes Merkmal des Nutzers. | Q1, Q2 (`$userisspecialtyexcluded`) |
| B8 | Ein Nutzer kann auf einen Entrance-Command verweisen, der bei seiner ersten Nachricht in einer Sitzung läuft, solange der Stream live ist ([`command-engine.md`](command-engine.md), B41); dazu kommen später nutzerspezifische Chat-Commands (P1). Das Datenmodell hält nur den Verweis auf die Commands. | Q1, Q9 |
| B9 | Gespeicherte Statistiken je Nutzer: Watchtime in Minuten, Zahl gesendeter Chatnachrichten, Zahl ausgeführter Commands, wie oft der Nutzer erwähnt wurde, Zahl gesehener Streams, Zeitpunkt des ersten und letzten Kontakts, Summe der Spenden, Zahl der Moderations-Strikes. | Q2 (Nutzer-Identifier für Zeit, Nachrichten, Commands, Erwähnungen, Streams, zuletzt gesehen, Spenden, Strikes) |
| B10 | Daten, die die Plattform liefert (Follow-Datum, Abo-Beginn, Abo-Stufe, Kontoalter), werden je Identität zwischengespeichert, mit dem Zeitpunkt der letzten Aktualisierung. | Q2 (`$userfollowage`, `$usersubage`, `$usersubtier`, `$useraccountage`) |
| B11 | Nutzer lassen sich aus Text- oder Tabellendateien importieren; die Zuordnung der Spalten wählt der Streamer (P2). | Q1 |

### Rollen

| ID | Regel | Quellen |
|---|---|---|
| B20 | Rollen haben eine feste Rangordnung mit einer Stufe je Rolle, aufsteigend: `banned`, `user`, `twitch_affiliate`, `twitch_partner`, `follower`, `youtube_subscriber` (Abonnent auf YouTube), `regular`, `twitch_vip`, `kick_vip`, `kick_og`, `velora_vip`, `vpzone_plus`, `vpzone_founder`, `vpzone_ambassador`, `subscriber`, `youtube_member` (Mitglied auf YouTube), `twitch_global_mod`, `twitch_staff`, `moderator`, `editor` (Channel Editor), `streamer`. Auch die Stufen einer Plattform werden streng verglichen: Ein Kick-VIP erfüllt `kick_og` nicht, ein Affiliate nicht `twitch_partner`. **[Interop]** für den Import: die Bedeutung dieser Stufen | QP (Plan Anhang A.7), Q3, Q11 |
| B21 | Ein Nutzer kann mehrere Rollen zugleich haben, etwa Follower, Subscriber und Moderator. Jeder Nutzer hat mindestens `user`. | Q2 (`$userroles`) |
| B22 | Die Hauptrolle ist die höchste Rolle des Nutzers. | Q2 (`$userprimaryrole`) |
| B23 | Eine Mindestrolle ist erfüllt, wenn die Hauptrolle des Nutzers mindestens so hoch ist wie die verlangte Rolle. | Q3 („alle höheren Rollen dürfen ebenfalls“) |
| B24 | Rollen hängen an der Plattform-Identität, weil sie je Kanal und Plattform gelten. Die Rollen eines Nutzers sind die Vereinigung der Rollen seiner Identitäten auf den Plattformen, auf denen er gerade handelt; bei einer Chatnachricht zählt die Identität, von der die Nachricht kommt. | Q2, QP |
| B25 | `streamer` hat das Konto, mit dem der Kanal verbunden ist. Das Bot-Konto hat keine erhöhte Rolle, nur weil es der Bot ist. | Q10 |
| B26 | `regular` vergibt streamcrew selbst: Regular ist, wessen Watchtime (B9, über alle Plattformen) die in den Nutzer-Einstellungen festgelegte Zahl ganzer Stunden erreicht; Standard 0, und 0 heißt, dass niemand Regular ist. Die Rolle gehört zum Nutzer und gilt auf allen Plattformen. Sie wird neu bewertet, sobald sich die Watchtime oder die Schwelle ändert. | Q2 („Regular-Rolle, festgelegt unter Settings → Users“), Q11, A4 |
| B27 | `banned` hat ein Nutzer, der auf der Plattform gebannt ist. Ein gebannter Nutzer erfüllt keine Mindestrolle, auch nicht `user`. | QP (Rangordnung), A1 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B40 | Zwei Konten auf verschiedenen Plattformen mit gleichem Namen | Zwei Nutzer, solange sie nicht ausdrücklich verknüpft werden (P1). | B3 |
| B41 | Ein Plattformkonto wird gelöscht und die ID später neu vergeben | kommt bei den unterstützten Plattformen nicht vor; die ID bleibt der Schlüssel | B3 |
| B42 | Die Plattform meldet eine Rolle nicht mehr (etwa VIP entzogen) | Die Rolle entfällt beim nächsten Abgleich; `regular` bleibt davon unberührt. | B24, B26 |
| B43 | Anforderung `user` bei einem Nutzer ohne weitere Rollen | erfüllt | B21, B23 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | Die Rolle „gebannt“ ist veraltet und wird nie vergeben; ein Bann ändert keine Rollen. Gebannte Nutzer können auf Twitch nicht schreiben; kommt doch eine Nachricht an, erfüllen sie `user` (Q11). | `banned` aus dem Bann auf der Plattform; ein gebannter Nutzer erfüllt keine Mindestrolle (B27) | Sicherheit: Nachrichten, die kurz vor oder trotz des Banns ankommen, lösen nichts aus; Entscheidung des Projektinhabers (2026-10-02) |
| A2 | Rollennamen als Anzeigetexte | stabile Kennungen in Kleinbuchstaben (`follower`, `twitch_vip` …), Anzeige über i18n | Commands als Code und API brauchen stabile Werte (Plan §6.9, §6.22) |
| A3 | IDs je Plattform | zusätzlich eine plattformunabhängige ID je Nutzer | Mehrplattform-Betrieb und Verknüpfung (Plan §5.2) |
| A4 | Regular hängt am Plattformkonto, mit dem der Nutzer gerade aktiv ist; bewertet wird einmal pro Minute für Nutzer, die im Chat eines Live-Streams aktiv sind (Q11) | Regular gehört zum Nutzer und wird sofort neu bewertet (B26) | Die Watchtime zählt ohnehin am Nutzer; eine neue Schwelle soll sofort gelten; Entscheidung des Projektinhabers (2026-10-02) |

## Akzeptanzkriterien

- [x] B3: Ein zweiter Nutzer mit derselben Kombination aus Plattform und Plattform-ID wird abgelehnt (Constraint in der Datenbank).
- [x] B4: Eine Namensänderung aktualisiert die Identität, ohne einen neuen Nutzer anzulegen.
- [x] B20, B23: Tabellengetriebener Test über alle Paare aus Nutzerrolle und Mindestrolle.
- [x] B22: Hauptrolle eines Nutzers mit mehreren Rollen ist die höchste.
- [x] B27: Ein gebannter Nutzer erfüllt keine Mindestrolle.
- [x] B20: Die feinen Stufen über alle Paare aus Nutzerrolle und Mindestrolle; gespeicherte Rollen und Anforderungen mit den bisherigen Kennungen werden übernommen.
- [ ] B5: Titelregeln nach Rolle und Monaten, „Kein Titel“ in beiden Sprachen.
- [ ] B26: Regular ab der Schwelle, Schwelle 0, Neubewertung bei neuer Watchtime und neuer Schwelle.
- [x] B9: Statistiken werden gespeichert und gelesen (Integrationstest gegen SQLite).

## Offene Fragen

Keine.

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q1 | Doku | <https://mixitup.bot/docs/users> | Nutzerdaten, Titel, Ausnahmen, Import; abgerufen 2026-09-29 |
| Q2 | Doku | <https://mixitup.bot/docs/reference/special-identifiers> | Nutzer-Identifier (Rollen, Hauptrolle, Statistiken, Regular); abgerufen 2026-09-29 |
| Q3 | Doku | <https://mixitup.bot/docs/commands> | Mindestrolle „und höher“; abgerufen 2026-09-29 |
| Q9 | Doku | <https://mixitup.bot/docs/chat> | Entrance-Command, Chat-Ereignisse; abgerufen 2026-09-29 |
| Q10 | Doku | <https://mixitup.bot/docs/platforms/twitch> | Twitch-Rollen (VIP, Moderator, Subscriber, Channel Editor); abgerufen 2026-09-29 |
| Q11 | Original (Hilfestellung) | `MixItUp.Base/Model/User/UserRoles.cs @ v1.8.200`, `MixItUp.Base/ViewModel/User/UserV2ViewModel.cs @ v1.8.200`, `MixItUp.Base/Model/User/UserTitleModel.cs @ v1.8.200`, `MixItUp.Base/Model/Settings/SettingsV3Model.cs @ v1.8.200`, `MixItUp.Base/Services/ChatService.cs @ v1.8.200` | feine Stufen der Rangordnung (B20), Titelregeln (B5), Regular über ganze Stunden Watchtime (B26, A4), gebannte Nutzer (A1); gelesen 2026-10-02 von einem eigenen Recherche-Agenten, der nur das Verhalten in eigenen Worten weitergab |
| QP | Projekt | [Plan](../plan.md) §5.5, Anhang A.6, A.7 | Inventar aus dem Audit von Mix It Up v1.8.200 |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-29 | Erstfassung (Entwurf) aus der offiziellen Doku und dem Plan, ohne Code des Originals |
| 2026-09-29 | Vom Projektinhaber geprüft und akzeptiert. Die offenen Fragen bleiben bis zur Prüfung am Original offen; bis dahin gilt das hier beschriebene Verhalten. |
| 2026-09-29 | Rollen umgesetzt (`internal/domain/role`). Festlegung dabei: Die Hauptrolle eines gebannten Nutzers ist `banned`, auch wenn er weitere Rollen hat (B22 mit B27). Plattformspezifische Rollen zählen auf der Stufe, auf der sie stehen (B20). |
| 2026-09-29 | Nutzer umgesetzt (`internal/domain/user`). Festlegungen dabei: `regular` gehört zum Nutzer, nicht zu einer Plattform-Identität (B26). Beim erneuten Kontakt übernimmt streamcrew Login, Anzeigename, Chatfarbe und Profilbild (B4); Rollen und Plattformdaten kommen über einen eigenen Abgleich (B10, B42). Spenden werden in Hundertsteln der Hauptwährungseinheit summiert (B9). |
| 2026-09-30 | Identifier der Nutzer umgesetzt (`internal/template`, Spezifikation Templates, B60). Festlegung dabei: Bis zu den Standardtiteln (B5, Phase 5.2) gilt die Hauptrolle als Titel. |
| 2026-10-02 | B8: Der Entrance-Command läuft nur, solange der Stream live ist (Entscheidung des Projektinhabers, [`command-engine.md`](command-engine.md), B41). |
| 2026-10-02 | Offene Fragen am Original geklärt (Q11) und entschieden (Entscheidungen des Projektinhabers). Übernommen: die feinen Stufen der Rangordnung mit eigener Kennung je Stufe (B20; die Festlegung vom 2026-09-29, plattformspezifische Rollen zählten auf einer gemeinsamen Stufe, entfällt), Titelregeln mit „Kein Titel“ (B5) und Regular über ganze Stunden Watchtime mit Standard 0 (B26). Geblieben, mit dem Verhalten des Originals unter „Abweichungen“: gebannte Nutzer erfüllen keine Mindestrolle (A1), Regular gehört zum Nutzer (A4). Übereinstimmend: Rollen gelten je Plattform-Identität, nicht über verknüpfte Konten (B24). Die Einstellung für exakte Rollen und die Stufen bei Abonnenten bleiben im Backlog ([`requirements.md`](requirements.md), A12). |
| 2026-10-03 | Feine Stufen der Rangordnung umgesetzt (B20, Roadmap 3.5). Festlegungen dabei: Die bisherigen gemeinsamen Kennungen entfallen. Gespeicherte Rollen der Plattformkonten bekommen die Stufe ihrer Plattform, wo sie eindeutig ist (`follower` und `subscriber` auf YouTube werden `youtube_subscriber` und `youtube_member`, `vip` auf Kick `kick_vip`), sonst die niedrigste Stufe der gemeinsamen Kennung (`creator` wird `twitch_affiliate`, `vip` `twitch_vip`, `platform_staff` `twitch_global_mod`); so bekommt niemand eine höhere Rolle, und die nächste Aktualisierung durch die Plattform setzt die genaue (Migration 0010). Eine Rollen-Anforderung wird ebenso zur niedrigsten Stufe, damit jeder, der sie erfüllte, sie weiter erfüllt (Version 2 der Anforderung). Die Meldung der Rollen-Anforderung nennt jede Stufe mit eigenem Namen in beiden Sprachen. Für die Templates siehe [`template.md`](template.md). |
