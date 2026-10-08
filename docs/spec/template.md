# Spezifikation: Templates und `$`-Identifier

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-10-01 |
| **Bezug** | Roadmap Phase 3.1; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md), [Code-ADR-0009](../adr/code/0009-ids-und-zeit.md), [Code-ADR-0012](../adr/code/0012-template-engine.md); Plan §3, §6.10, Anhang A.6; [`commands.md`](commands.md), [`counters-and-quotes.md`](counters-and-quotes.md), [`events.md`](events.md), [`users-and-roles.md`](users-and-roles.md) |
| **Umsetzung** | `internal/template`: Kern mit Syntax, Quellen, Auswertung, Kodierung, `$linebreak` und `$unicode<n>` (B1–B7, B10–B12, B20, B21, B23, B24, B30–B33); alle Identifier-Familien des MVP (B22, B40–B43, B60); `internal/expr`: Ausdrücke (B50–B52); globale Werte der Special-Identifier-Action als Quelle `template.Globals` (B10) |

## Zweck und Umfang

Beschreibt, wie streamcrew Texte mit `$`-Identifiern auswertet: in Chatnachrichten von Actions, in Overlays, in Web-Requests und in Ausdrücken. Festgelegt werden die Syntax, die Regel des längsten Präfixes, die Reihenfolge der Quellen, das Verhalten bei unbekannten oder leeren Identifiern, die Kodierung je Ausgabeort und die Ausdrücke. Dazu kommen die Identifier-Familien des MVP.

Die Namen der Identifier folgen dem Original, damit bestehende Commands und Importe funktionieren. Sie stehen unter dem **Interop-Vorbehalt** aus [`README.md`](README.md), Regel 6: Sie hängen von der rechtlichen Einschätzung ab (Roadmap Gate O, O.1). Bis dahin nutzt streamcrew sie, hält sie aber an einer Stelle zusammen, damit sie sich austauschen lassen.

Nicht Teil dieser Spezifikation:

- die Textfunktionen der Special-Identifier-Action (etwa Groß- und Kleinschreibung, Ersetzen, Datumsrechnung) und die Vergleiche der Conditional-Action; sie kommen mit den Actions (Roadmap 3.3)
- Identifier, deren Quelle erst später entsteht: plattformspezifische Werte (Twitch Phase 4, weitere Plattformen im Backlog der Roadmap), Währungen, Ränge und Inventare (Phase 8), Spenden und Integrationen (Phase 9). Sie folgen denselben Regeln und werden mit ihrer Phase ergänzt.
- die Übersetzung von Texten der Oberfläche (ADR-0022, Roadmap 3.6)

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Template | Text, der `$`-Identifier enthalten kann, etwa die Nachricht einer Chat-Action |
| Token | ein `$` mit den unmittelbar folgenden Buchstaben, Ziffern und Doppelpunkten |
| Identifier | ein bekannter Name, der für einen Wert steht, etwa `username` in `$username` |
| Muster | ein Identifier mit Zahlen darin, etwa `$arg2text` oder `$randomnumber1:6` |
| Präfix-Familie | Identifier aus einem Subjekt und einer Eigenschaft, etwa Subjekt `user` und Eigenschaft `displayname` |
| Kontext | die Werte eines Durchlaufs: auslösender Nutzer, Zielnutzer, Argumente, Ereigniswerte und lokale Werte der Actions |
| Rendern | ein Template mit einem Kontext in fertigen Text umwandeln |
| Kodierung | wie eingesetzte Werte für ihren Ausgabeort maskiert werden: Text, URL, HTML oder JSON |

## Verhalten

### Syntax

| ID | Regel | Quellen |
|---|---|---|
| B1 | Ein Token beginnt mit `$`, gefolgt von so vielen Buchstaben (a–z), Ziffern und Doppelpunkten wie möglich. Groß- und Kleinschreibung spielt keine Rolle: `$UserName` ist `$username`. | QP (§3, §6.10) |
| B2 | Innerhalb eines Tokens gilt der **längste bekannte Präfix**: Er wird ersetzt, der Rest des Tokens bleibt als Text stehen. `$usernames` ergibt den Nutzernamen und ein „s“. | QP (§6.10) |
| B3 | Ein Token ohne bekannten Präfix bleibt unverändert stehen, samt `$`. Ein einzelnes `$` ohne folgende Zeichen ist gewöhnlicher Text. | Q1, Q3 |
| B4 | Ein bekannter Identifier, der im Kontext keinen Wert hat, bleibt ebenfalls unverändert stehen, etwa `$arg3text` bei zwei Argumenten oder `$targetusername` ohne Zielnutzer. So lässt sich „nicht ersetzt“ erkennen. | Q3 |
| B5 | Eingesetzte Werte werden nie erneut ausgewertet. Schreibt ein Zuschauer `$streamerusername` in ein Argument, erscheint genau dieser Text. | QP (§6.10), A1 |
| B6 | Ein Muster enthält Zahlen im Namen. Es gilt nur, wenn die Zahlen gültig sind; sonst behandelt die Regel des längsten Präfixes das Token wie jedes andere. | Q1 |
| B7 | Es gibt keine Maskierung: Ein `$` vor einem Identifier-Namen leitet immer ein Token ein, auch `$$` maskiert nicht (B71). Ein wörtliches `$` vor einem Identifier-Namen entsteht mit `$unicode36` (B78). | Entscheidung des Projektinhabers |

### Quellen und Reihenfolge

| ID | Regel | Quellen |
|---|---|---|
| B10 | Identifier kommen aus vier Quellen, in dieser Rangfolge: (1) Werte des Kontexts: lokale Werte der Actions, Argumente, Nachricht und Ereigniswerte; (2) globale Werte, die eine Special-Identifier-Action für die Laufzeit des Cores setzt; (3) dynamische Namen: Counter, später Währungen, Ränge und Inventare; (4) eingebaute Identifier und Muster. | QP (§6.10), Q2 |
| B11 | Die Regel des längsten Präfixes gilt über alle Quellen hinweg. Nur wenn mehrere Quellen einen gleich langen Präfix kennen, entscheidet die Rangfolge aus B10. | QP (§6.10) |
| B12 | Eigene Namen (lokale, globale Werte, Counter) dürfen eingebaute Identifier nicht verdecken; das prüft die jeweilige Funktion beim Anlegen (für Counter: [`counters-and-quotes.md`](counters-and-quotes.md), B7). | Q2, A3 der Counter-Spezifikation |

### Auswertung

| ID | Regel | Quellen |
|---|---|---|
| B20 | Ein Identifier wird nur ausgewertet, wenn er im Template vorkommt. Teure Werte, etwa das Follow-Alter über die Plattform-API, entstehen so nur bei Bedarf. | QP (§6.10) |
| B21 | Innerhalb eines Rendervorgangs hat derselbe Identifier überall denselben Wert; er wird einmal bestimmt. Ausnahme: `$randomnumber…` zieht bei jedem Vorkommen eine neue Zahl. | Q1 |
| B22 | Ein zufällig gewählter Nutzer (`$randomuser…` und Verwandte) wird je Rendervorgang einmal gewählt, sodass Name, Anzeigename und andere Eigenschaften zum selben Nutzer gehören. Ausgeschlossen sind Nutzer mit Ausnahme (Spezifikation Nutzer, B7); Streamer und Bot sind wählbar. | Q1, A5 |
| B23 | Kann ein Wert nicht bestimmt werden, etwa weil eine Plattform-API nicht antwortet, bleibt der Identifier wie in B4 stehen, und der Core schreibt eine Warnung ins Log. Das Rendern bricht nicht ab. | A6 |
| B24 | Das Rendern respektiert Abbruch und Zeitlimit des auslösenden Durchlaufs; bei Abbruch endet es mit Fehler. | Code-ADR-0004 |

### Kodierung

| ID | Regel | Quellen |
|---|---|---|
| B30 | Wer ein Template rendert, wählt die Kodierung des Ausgabeorts: **Text** (Chat, Dateien) setzt Werte unverändert ein; **URL** (Adresse eines Web-Requests) maskiert alles außer Buchstaben, Ziffern und `-._~`, Leerzeichen als `%20`; **HTML** (Overlays) maskiert `<`, `>`, `&`, `"` und `'`; **JSON** (Body eines Web-Requests) maskiert wie in einem JSON-String, ohne Anführungszeichen hinzuzufügen. | QP (§6.10), A2 |
| B31 | Kodiert werden nur die eingesetzten Werte, nie der Text des Templates selbst. | QP (§6.10) |
| B32 | `$linebreak` fügt einen Zeilenumbruch ein. Wo der Ausgabeort keine Zeilenumbrüche kennt, etwa im Twitch-Chat, entscheidet der Plattform-Adapter, was daraus wird. **[Interop]** `$linebreak` | Q1 |
| B33 | `$unicode<n>` fügt das Zeichen mit der dezimalen Nummer n ein, etwa `$unicode937` für Ω. Ungültige Nummern und Steuerzeichen außer dem Zeilenumbruch bleiben nach B6 stehen. **[Interop]** | Q1 |

### Datum und Zeit

| ID | Regel | Quellen |
|---|---|---|
| B40 | Datum und Uhrzeit gelten in der Zeitzone des Profils (Settings-Sektion „Zeit“), nicht in der des Hosts. | Code-ADR-0009, QP (§6.10), A4 |
| B41 | Die Formate folgen der Locale des Profils. Bis es die Locale-Einstellung gibt (Roadmap 3.6), gelten die Formate des Originals (Englisch, USA): Datum wie `6/15/2009`, Uhrzeit wie `1:45 PM`. | Q1 |
| B42 | Zeitspannen wie Follow- oder Abo-Alter werden als Jahre, Monate und Tage ausgegeben; Einheiten ohne Wert entfallen. | Q1 |
| B43 | Die Uptime-Teile `…hours`, `…minutes`, `…seconds` sind die Stellen einer Uhr: Minuten und Sekunden laufen nach 59 wieder bei 0 an. **[Interop]** `$streamuptimetotal`, `$streamuptimehours`, `$streamuptimeminutes`, `$streamuptimeseconds` | Q1 |

### Ausdrücke

| ID | Regel | Quellen |
|---|---|---|
| B50 | Wo eine Funktion Rechnen oder Bedingungen vorsieht (Special-Identifier-Action mit Rechenoption, Mengenangaben, Conditional-Action), wird der Text als Ausdruck ausgewertet: Zahlen, Grundrechenarten, Potenz, Klammern, Vergleiche und logische Verknüpfungen. | Q2, QP (§6.10) |
| B51 | Identifier in einem Ausdruck liefern Werte, keinen Ausdruckstext: Ein Wert, der wie eine Zahl aussieht, zählt als Zahl, sonst als Text. Ein Argument wie `1)+(2` wird dadurch nicht zu Code. | A3 |
| B52 | Ein Ausdruck, der sich nicht auswerten lässt, liefert einen Fehler der auslösenden Action; er wird nicht als Text ausgegeben. Größe und Laufzeit eines Ausdrucks sind begrenzt. | A3 |

### Identifier-Familien im MVP

Die Tabellen nennen die Namen, die das MVP (Roadmap 3.1) auflöst. Alle Namen sind **[Interop]**. Welche Werte eine Plattform liefert, legt die Plattform-Spezifikation fest (Twitch: Phase 4); ohne Wert gilt B4.

**Präfix-Familien für Nutzer (B60):** Die Subjekte teilen sich dieselben Eigenschaften.

| Subjekt | Bedeutung | Quellen |
|---|---|---|
| `$user` | der auslösende Nutzer | Q1 |
| `$targetuser` | der erwähnte Zielnutzer; ohne Erwähnung der auslösende Nutzer | Q1 |
| `$streameruser`, `$botuser` | Streamer- und Bot-Konto | Q1 |
| `$arg<n>user` | der Nutzer, den das n-te Argument nennt | Q1 |
| `$randomuser`, `$randomfolloweruser`, `$randomsubscriberuser`, `$randomregularuser` | ein zufälliger Nutzer aus dem Chat, mit Einschränkung nach B22 | Q1 |

| Eigenschaften (Auswahl des MVP) | Quellen |
|---|---|
| `id`, `name`, `displayname`, `fulldisplayname`, `url`, `avatar`, `color` | Q1, Spezifikation Nutzer B2 |
| `roles`, `displayroles`, `primaryrole`, `title`, `notes`, `isspecialtyexcluded` | Q1, Spezifikation Nutzer B5–B7, B20–B22 |
| `isfollower`, `isregular`, `issubscriber`, `isvip`, `ismod` | Q1, Spezifikation Nutzer B20 |
| `time`, `hours`, `mins`, `totalstreamswatched`, `totalchatmessagessent`, `totalcommandsrun`, `totaltimestagged`, `totalamountdonated`, `moderationstrikes` | Q1, Spezifikation Nutzer B9 |
| `accountage`, `accountdays`, `followage`, `followdays`, `followmonths`, `followyears`, `subage`, `subdays`, `submonths`, `subtier`, `lastseenage`, `lastseendays`, `lastseendate` | Q1, Spezifikation Nutzer B9, B10 |

**Weitere Familien:**

| Familie | Identifier | Quellen |
|---|---|---|
| Argumente | `$allargs`, `$argcount`, `$arg<n>text`, `$arg<n>:<m>text` (Argumente n bis m), `$argdelimited<n>text`, `$argdelimitedcount` (Trennzeichen standardmäßig `\|`, Leerraum um die Teile entfällt) | Q1 |
| Nachricht | `$message` (Spezifikation Commands, B15), `$messagenoemotes`, `$messageemotecount` | Q1, Q4 |
| Datum und Zeit | `$datetime`, `$date`, `$dateyear`, `$datemonth`, `$datemonthname`, `$dateday`, `$dayoftheweek`, `$time`, `$timehour`, `$timeminute`, `$timesecond`, `$timedigits` | Q1 |
| Zufall | `$randomnumber<max>` (1 bis max), `$randomnumber<min>:<max>` (beide Grenzen eingeschlossen) | Q1 |
| Stream | `$streamtitle`, `$streamgamename`, `$streamviewercount`, `$streamchattercount`, `$streamfollowercount`, `$streamislive`, `$streamstartdatetime`, `$streamstartdate`, `$streamstarttime` und die Uptime aus B43 | Q1 |
| Counter | `$<name>` und `$<name>display` (Spezifikation Counter, B1, B4) | Q5 |
| Ereignisse | die Ereigniswerte aus der Spezifikation Ereigniskatalog, B7, etwa `$raidviewercount` | Q8 |
| Sonstiges | `$commandname`, `$streamingplatform`, `$linebreak`, `$unicode<n>` | Q1 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B70 | `$username!` oder `($username)` | Satzzeichen beenden das Token; der Name wird eingesetzt, die Zeichen bleiben. | B1 |
| B71 | `$$username` | Das erste `$` ist Text, danach folgt das Token `$username`. | B1, B3 |
| B72 | `$arg1text2` | erstes Argument, danach „2“ | B2 |
| B73 | `$randomnumber0`, `$randomnumber6:1` | ungültige Grenzen: Das Muster gilt nicht, das Token bleibt nach B3 stehen. | B6 |
| B74 | Ein Counter heißt wie der Anfang eines eingebauten Identifiers, etwa `user` | Anlegen wird abgelehnt (B12); bestehende Daten aus einem Import behandelt die Rangfolge (B10, B11). | B12 |
| B75 | Ein Argument enthält `%26` oder `&` und landet in einer URL | Es wird maskiert und verändert die Adresse nicht (B30). | B30 |
| B76 | Ein Ausdruck enthält ein Argument mit Buchstaben, etwa `$arg1text * 2` mit „zwei“ | Fehler der Action nach B52, kein Ergebnis im Chat | B51, B52 |
| B77 | Sehr lange Templates oder viele Tokens | Die Laufzeit wächst linear mit der Länge; es gibt keine verschachtelte Auswertung (B5). | B5 |
| B78 | `$unicode36username` | `$unicode36` ergibt `$`, der Rest „username“ bleibt Text; die Ausgabe ist wörtlich `$username` und wird nicht erneut ausgewertet. | B2, B5, B7, B33 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | Identifier werden per Textersetzung nacheinander ersetzt; eingesetzte Werte, auch Zuschauertext, können von späteren Ersetzungen erneut erfasst werden. | Tokenizer mit einmaliger Auswertung; eingesetzte Werte bleiben unverändert (B5). | Template-Injection durch Zuschauertext (Plan §6.10) |
| A2 | Keine dokumentierte Maskierung je Ausgabeort | Kodierung für Text, URL, HTML und JSON (B30) | Sicherheit in Overlays und Web-Requests |
| A3 | Rechnen über die Bibliothek Jace; Identifier werden vorher als Text eingesetzt. | Ausdrücke mit `expr-lang/expr`; Identifier liefern Werte, keinen Ausdruckstext (B51); Größe und Laufzeit begrenzt (B52) | Sicherheit; Jace gibt es für Go nicht. Funktionsnamen, die Jace anders nennt, bildet der Import ab (Roadmap 10.2). |
| A4 | Datum und Uhrzeit in der Zeitzone des Rechners | Zeitzone des Profils (B40) | Server-Modus (ADR-0003); der Host kann in einer anderen Zone stehen als der Streamer |
| A5 | Zufallsnutzer: nicht dokumentiert, ob mehrere Eigenschaften zum selben Nutzer gehören | einmal je Rendervorgang (B22) | Name und Anzeigename müssen zusammenpassen; zu prüfen (offene Frage) |
| A6 | Fehler einer Datenquelle: nicht dokumentiert | Identifier bleibt stehen, Warnung im Log (B23) | Ein Ausfall soll keinen Command abbrechen; wie B4 erkennbar |

## Akzeptanzkriterien

- [x] B1, B2, B7, B70–B72, B78: Golden Files mit Templates und erwarteter Ausgabe, auch für Groß- und Kleinschreibung.
- [x] B3, B4, B6, B73: Tabellengetriebener Test: unbekannte, leere und ungültige Identifier bleiben unverändert.
- [x] B5: Ein Wert, der selbst Identifier enthält, erscheint unverändert; das gilt für alle Quellen aus B10.
- [x] B10, B11: Rangfolge und längster Präfix über Quellen hinweg, etwa lokaler Wert gegen Counter gegen eingebauten Identifier.
- [x] B20: Ein Resolver, dessen Identifier nicht vorkommt, wird nicht aufgerufen; einer, der mehrfach vorkommt, einmal.
- [x] B21, B22: `$randomnumber` zieht je Vorkommen neu; ein Zufallsnutzer bleibt innerhalb eines Rendervorgangs derselbe.
- [x] B23, B24: Fehler einer Quelle und Abbruch des Kontexts.
- [x] B30, B31, B75: Tabelle je Kodierung mit Sonderzeichen in Werten und im Template.
- [x] B40–B43: Datum, Uhrzeit, Zeitspannen und Uptime in einer festen Zeitzone (`testing/synctest`).
- [x] B50–B52, B76: Ausdrücke mit Zahlen, Text, Fehlern und Grenzen.
- [x] Fuzz-Test: Rendern bricht bei keiner Eingabe ab; ohne bekannte Identifier ist die Ausgabe gleich der Eingabe.

## Offene Fragen

- B1: Unterscheidet das Original Groß- und Kleinschreibung von Identifiern tatsächlich nicht (so im Audit, Plan §3)?
- B4: Bleiben im Original Identifier ohne Wert, etwa `$arg3text` bei zwei Argumenten, als Text stehen, oder werden sie leer?
- B22/A5: Gehören im Original mehrere Eigenschaften von `$randomuser…` in einem Text zum selben Nutzer?
- B41: Welche Formate für Datum und Zeitspannen nutzt das Original bei anderen Sprachen als Englisch?
- A3: Welche Jace-Funktionen nutzen bestehende Commands, und wie heißen ihre Entsprechungen in `expr`?

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q1 | Doku | <https://mixitup.bot/docs/reference/special-identifiers> | Aufbau aus Präfix und Eigenschaft, Familien und Namen, Argumente, Zufall, Uptime, Formate, unbekannte Identifier; abgerufen 2026-09-29 |
| Q2 | Doku | <https://mixitup.bot/docs/actions/special-identifier-action> | lokale und globale Werte, Namenskonflikte, Rechnen mit Jace, Identifier vor der Rechnung ersetzt; abgerufen 2026-09-29 |
| Q3 | Doku | <https://mixitup.bot/docs/actions/conditional-action> | „ersetzt“ und „nicht ersetzt“ als Prüfung; abgerufen 2026-09-29 |
| Q4 | Doku | <https://mixitup.bot/docs/chat/chat-commands> | `$message`; abgerufen 2026-09-29 |
| Q5 | Doku | <https://mixitup.bot/docs/actions/counter-action> | Counter-Identifier; abgerufen 2026-09-29 |
| Q6 | Doku | <https://mixitup.bot/docs/consumables/currency> | Namensschema dynamischer Identifier für Währungen (Phase 8); abgerufen 2026-09-29 |
| Q7 | Doku | <https://mixitup.bot/docs/actions/web-request-action> | Ergebnis als Identifier, JSON-Pfade; Maskierung nicht dokumentiert; abgerufen 2026-09-29 |
| Q8 | Projekt | [`events.md`](events.md), B7 | Ereigniswerte als Identifier |
| QP | Projekt | [Plan](../plan.md) §3, §6.10, Anhang A.6 | Audit der Ersetzung im Original, Zielbild der Engine, Identifier-Familien |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-29 | Erstfassung (Entwurf) aus der offiziellen Doku und dem Plan, ohne Code des Originals; Identifier-Namen unter Interop-Vorbehalt (Entscheidung des Projektinhabers) |
| 2026-09-30 | Keine Maskierung von `$` (Entscheidung des Projektinhabers): B7 und Randfall B78 ergänzt, offene Frage dazu entfernt; Verweis auf Code-ADR-0012 (akzeptiert) aktualisiert |
| 2026-09-30 | Vom Projektinhaber geprüft und akzeptiert. Die offenen Fragen bleiben bis zur Prüfung am Original offen; bis dahin gilt das hier beschriebene Verhalten. |
| 2026-09-30 | Kern umgesetzt (`internal/template`). Festlegungen dabei: Ein eigener Name kollidiert (B12), wenn er mit einem eingebauten Namen oder dem festen Anfang eines Musters beginnt oder der Anfang eines solchen ist. Scheitert eine Quelle beim Suchen der Namen, bleibt sie für den Rest des Rendervorgangs außen vor, und ihre Tokens bleiben stehen (B23). Ein gescheiterter Identifier gilt im selben Rendervorgang weiter als gescheitert (B21) und wird einmal geloggt. `$unicode<n>` erlaubt führende Nullen. Ungültiges UTF-8 wird bei der Kodierung JSON zu U+FFFD. |
| 2026-09-30 | Familien ohne Nutzer umgesetzt. Festlegungen dabei: Alle Identifier für Datum und Zeit nutzen innerhalb eines Rendervorgangs denselben Zeitpunkt; `$timehour` zählt von 00 bis 23. `$allargs` ist ohne Argumente leer, `$argcount` und `$argdelimitedcount` sind dann 0. `$arg<n>:<m>text` endet beim letzten Argument, wenn m darüber hinausgeht; ohne Argument n hat es keinen Wert. Getrennte Argumente behalten leere Teile, damit die Positionen stimmen. `$messagenoemotes` lässt die Wörter weg, die die Plattform als Emote markiert, und fasst Leerraum zusammen. `$randomnumber<min>:<max>` erlaubt 0 als Untergrenze; beide Grenzen reichen bis 2⁵³. Stream: Was die Plattform nicht meldet, hat keinen Wert; ohne laufenden Stream haben Start und Uptime keinen Wert; `$streamislive` ist `true` oder `false`. Counter: Bei gleicher Länge gewinnt ein Counter gegen den formatierten Wert eines anderen, etwa `xdisplay` gegen `x` mit `display`. `$streamingplatform` schreibt den Namen der Plattform wie diese selbst, etwa `YouTube`. Die Ports für Daten des ganzen Profils (Stream-Zustand, später Nutzer) bekommen die Familien beim Aufbau der Registry; der Scope hält die Daten des Durchlaufs. |
| 2026-09-30 | Nutzer-Familien umgesetzt. Festlegungen dabei: Werte, die von der Plattform abhängen, kommen von der Identität auf der Plattform des Durchlaufs, sonst von der ersten Identität. `$userid` ist die eigene ID des Nutzers (Spezifikation Nutzer, B1). `$userfulldisplayname` ist der Anzeigename, gefolgt vom Login in Klammern, wenn beide sich nicht nur in der Schreibweise unterscheiden. `$userroles` nennt die Rollen auf Englisch von der höchsten abwärts, `User` nur ohne weitere Rolle; `$userdisplayroles` ist bis zur Übersetzung (ADR-0022) gleich. `$usertitle` ist ohne eigenen Titel bis zu den Standardtiteln (Phase 5.2) die Hauptrolle. `$usermins` sind die Minuten nach den vollen Stunden wie in `$usertime`. `$usertotalamountdonated` hat zwei Nachkommastellen ohne Währungszeichen. `$usersubtier` ist `Tier <n>`; `$usersubmonths` zählt die Monate seit dem Abo-Beginn, bis die Plattform die Summe meldet (Phase 4). `$userlastseendate` hat das Format von `$datetime`. Zeitspannen (B42) rechnen in Kalendertagen der Profil-Zeitzone; ein Monat nach dem 31. endet am letzten Tag eines kürzeren Monats, ein Datum in der Zukunft ergibt `0 Days`. Daten, die die Plattform nicht gemeldet hat (Farbe, Profilbild, Follow- und Abo-Datum), haben keinen Wert; leere Notizen sind leerer Text. `$arg<n>user…` findet den Nutzer mit oder ohne `@` vor dem Namen. |
| 2026-09-30 | Ausdrücke umgesetzt (`internal/expr`). Festlegungen dabei: Die Sprache ist, was B50 nennt: Zahlen, Text in Anführungszeichen für Vergleiche, `+ - * / %`, die Potenz mit `^` und `**`, Klammern, Vergleiche, `and`, `or`, `not` (auch `&&`, `\|\|`, `!`) und die Funktionen `abs`, `ceil`, `floor`, `round`, `min`, `max`; alles andere lehnt der Core beim Kompilieren ab. Alle Zahlen sind Gleitkommazahlen; `%` ist der Rest mit dem Vorzeichen des Dividenden, auch für Kommazahlen. Ein Wert zählt als Zahl, wenn er eine Dezimalzahl ist, auch mit Exponent; hexadezimale Zahlen, `Inf`, `NaN`, `true` und `false` sind Text (B51). Ein Identifier ohne Wert ist sein eigener Text (B4). Text in Anführungszeichen mit Identifiern, etwa `"$arg1text"`, wird als Ganzes gerendert und ist immer Text; Escape-Sequenzen sind darin nicht erlaubt. Ein `$` ohne Identifier-Namen außerhalb von Anführungszeichen ist ein Fehler. Division durch 0 und Ergebnisse außerhalb der Gleitkommazahlen sind Fehler (B52). Grenzen: 500 Knoten im Syntaxbaum, ein Speicherbudget von 10 000 Einheiten. |
| 2026-09-30 | Die Locale-Einstellung kommt mit ADR-0022 in Roadmap 3.6 statt 3.2 (B41; Entscheidung des Projektinhabers). |
| 2026-09-30 | Nach Code-ADR-0017 bereinigt, ohne Änderung des Verhaltens in einem Durchlauf der Engine: Ein Rendervorgang braucht die Zeitzone und das Trennzeichen des Profils; ein Scope ohne sie oder mit Argumenten ohne den Text, aus dem sie stammen, wird abgelehnt (`template.ErrInvalidScope`), statt auf UTC, `|` oder die Argumente mit Leerzeichen zu fallen. Den Zielnutzer setzt die Command-Engine, ohne anderes Ziel den auslösenden Nutzer ([`command-engine.md`](command-engine.md), B81); ein Scope ohne Ziel hat für `$targetuser…` keinen Wert. |
| 2026-10-01 | Globale Werte als Quelle umgesetzt (`template.Globals`, B10): Die Special-Identifier-Action setzt sie ([`actions.md`](actions.md), B56, B57); sie ranken nach den Werten des Durchlaufs und vor den Countern, ihre Namen gelten unabhängig von der Schreibweise. `template.FormatSpan` gibt Zeitspannen nach B42 auch für die Textfunktionen `datefrom` und `dateto` aus. |
