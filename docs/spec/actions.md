# Spezifikation: Actions (P0)

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-10-01 |
| **Bezug** | Roadmap Phase 3.3; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md), [ADR-0013](../adr/0013-sicherheitsmodell.md), [Code-ADR-0010](../adr/code/0010-polymorphe-serialisierung.md), [Code-ADR-0012](../adr/code/0012-template-engine.md), [Code-ADR-0013](../adr/code/0013-typ-registry.md), [Code-ADR-0017](../adr/code/0017-klare-signale-statt-magischer-werte.md); Plan §5.3, §6.9, §6.10, §6.11; [`command-engine.md`](command-engine.md), [`template.md`](template.md), [`commands.md`](commands.md), [`counters-and-quotes.md`](counters-and-quotes.md), [`users-and-roles.md`](users-and-roles.md) |
| **Umsetzung** | in Arbeit (Roadmap 3.3). Gemeinsame Regeln (B1–B9) in der Typ-Registry `internal/action` und der Engine nach [Code-ADR-0013](../adr/code/0013-typ-registry.md). Warten, Zufall, Gruppe, Wiederholen und Bedingung (B10–B29) in `internal/action/flow`, die Command-Action (B30–B38) in `internal/action/commands`, der Counter (B40–B43) und der Special Identifier (B50–B57) in `internal/action/values`, dessen Textfunktionen in `internal/textfunc` und die globalen Werte als `template.Globals`, die Chat-Action und die Plattformnachricht (B60–B67) in `internal/action/chat`, die Moderation (B80–B86) in `internal/action/moderation`, die Nutzersuche (B90–B93) in `internal/action/users`, das externe Programm (B110–B117) in `internal/action/host`. Die übrigen Typen folgen; die Plattform-Ports nach Plan §6.11 stehen in `internal/connector`. |

## Zweck und Umfang

Beschreibt das Verhalten der 15 plattformneutralen P0-Actions (Plan §5.3): Warten, Zufall, Gruppe, Wiederholen, Bedingung, Command, Counter, Special Identifier, Chat, Plattformnachricht, Web-Request, Moderation, Nutzersuche, Datei und externes Programm. Je Action stehen hier Konfiguration, Ablauf, gesetzte Identifier und Fehlerfälle. Dazu kommen die Regeln, die für alle Actions gelten: Templates, Mengenangaben, Werte des Durchlaufs, Scheitern, Capabilities und Zeitlimits.

Die Namen der Actions und ihrer Arten in `code`-Schrift sind ihre Typ-IDs und Arten nach [Code-ADR-0013](../adr/code/0013-typ-registry.md).

Nicht Teil dieser Spezifikation:

- die Typ-Registry mit Descriptor, JSON-Schema und UI-Hinweisen ([Code-ADR-0013](../adr/code/0013-typ-registry.md))
- Warteschlange, Sperren, Aufrufe, Fehlerpolitik und Verlauf ([`command-engine.md`](command-engine.md))
- die Prüfung der Anforderungen ([`requirements.md`](requirements.md))
- die Twitch-Action mit Clips, Markern, Umfragen und Werbung (Roadmap Phase 4)
- die P1-Actions, etwa Overlay, Sound, Sprachausgabe und Consumables
- Filter der Moderation und was nach einer Zahl von Strikes geschieht (`moderation.md`, Phase 5)

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Action | ein Schritt eines Commands mit Typ und Konfiguration ([`commands.md`](commands.md), B4) |
| Kind-Actions | Actions innerhalb einer anderen, etwa die Actions einer Bedingung, die bei „wahr“ laufen |
| Mengenangabe | ein Feld mit einer Zahl, etwa Sekunden, Anzahl, Betrag oder Zeilennummer |
| Wert des Durchlaufs | ein Wert, den eine Action für die übrigen Actions ihrer Instanz setzt; im Template ein Identifier ([`template.md`](template.md), B10, Quelle 1) |
| Globaler Wert | ein Wert, den eine Special-Identifier-Action für alle Instanzen setzt, bis der Core endet ([`template.md`](template.md), B10, Quelle 2) |
| Ergebniswert | ein Wert des Durchlaufs, den eine Action als Ergebnis setzt, unter einem festen oder vom Streamer gewählten Namen |
| Freigegebene Wurzel | ein Verzeichnis, das die Startkonfiguration für die Datei-Action freigibt ([ADR-0013](../adr/0013-sicherheitsmodell.md)) |

## Verhalten

### Gemeinsame Regeln

| ID | Regel | Quellen |
|---|---|---|
| B1 | Jede Action hat einen Typ, eine Konfiguration und einen Schalter „aktiv“. Eine inaktive Action wird übersprungen, samt ihrer Kind-Actions, und braucht bei `per_action_type` keine Sperre ([`command-engine.md`](command-engine.md), B22). | Q4, Code-ADR-0010 |
| B2 | Eine Action ist fertig, wenn ihre Wirkung ausgelöst ist oder ihr Ergebnis vorliegt; erst dann startet die nächste ([`command-engine.md`](command-engine.md), B70). Die Chat-Action ist fertig, sobald die Plattform die Nachricht angenommen hat. Länger halten nur Warten, Web-Request, ein externes Programm mit Warten, ein Command-Aufruf mit Warten und Actions mit Kind-Actions den Command auf. | Q1, Q10 |
| B3 | Texte in der Konfiguration sind Templates. Sie werden erst beim Ausführen der Action gerendert, mit dem Scope des Durchlaufs; so sieht eine Action die Werte früherer Actions. Die Kodierung richtet sich nach dem Ausgabeort ([`template.md`](template.md), B30); ohne Angabe in dieser Spezifikation gilt Text. Alle Templates einer Action entstehen in einem Rendervorgang ([`template.md`](template.md), B21). | Q9, QP (§6.10) |
| B4 | Mengenangaben sind Ausdrücke ([`template.md`](template.md), B50–B52): Identifier liefern Werte, dann wird gerechnet. Jede Mengenangabe hat einen erlaubten Bereich; wo die Action eine ganze Zahl braucht, lassen Nachkommastellen sie scheitern, statt gerundet zu werden. Ein Ergebnis, das keine Zahl ist oder außerhalb des Bereichs liegt, lässt die Action scheitern; die Meldung nennt Feld und Wert. | QP (§6.10), A1 |
| B5 | Ergebniswerte sind Werte des Durchlaufs: Folgende Actions derselben Instanz sehen sie, ebenso Commands, die sie mit Warten aufruft ([`command-engine.md`](command-engine.md), B35), andere Instanzen nicht. Ein neuer Wert ersetzt einen gleichnamigen. Namen, die der Streamer wählt, bestehen aus Kleinbuchstaben und Ziffern, ohne `$`, und dürfen weder eingebaute Identifier noch die festen Ergebnisnamen dieser Spezifikation verdecken ([`template.md`](template.md), B12); das prüft das Speichern. Feste Ergebnisnamen: **[Interop]** `$webrequestresult`, `$externalprogramresult`, `$lookupusername`, `$lookupdisplayname`, `$lookupid`, `$lookupavatarurl`, `$lookupsuccess` | Q9, Q11, Q14, Q16 |
| B6 | Eine Action scheitert mit einer Meldung, die Typ, Feld und Grund nennt; sie steht im Verlauf, und die Fehlerpolitik des Commands entscheidet, wie es weitergeht ([`command-engine.md`](command-engine.md), B60, B71). Actions melden ihr Scheitern nicht im Chat. Wo diese Spezifikation einen Ausgang ausdrücklich als „kein Scheitern“ nennt, etwa eine Nutzersuche ohne Treffer, gelingt die Action. | Code-ADR-0017 |
| B7 | Datei braucht die Capability `host:fs`, externes Programm `host:process`, Web-Request `net:outbound`. Fehlt sie im Betriebsmodus, scheitert die Action, ohne zu laufen, und die Meldung nennt die Capability. Beim Speichern eines Commands mit einer solchen Action gibt es eine Warnung, kein Verbot, damit Importe erhalten bleiben. | ADR-0013 |
| B8 | Zeitlimits ([`command-engine.md`](command-engine.md), B72): Standard sind 60 s. Warten hat seine Dauer plus 5 s, ein externes Programm mit Warten sein Zeitlimit plus 5 s; beide stehen erst beim Start der Action fest, weil die Dauer aus einem Template kommen kann. Actions mit Kind-Actions (Bedingung, Zufall, Gruppe, Wiederholen, Datei mit `each_line`, Command-Aufruf mit Warten) haben kein Limit für das Ganze: Jede Kind-Action läuft mit ihrem eigenen Limit, die eigene Arbeit der Action (etwa eine Bedingung auswerten) mit dem Standard. Vor Endlosschleifen schützen B74 und B73 der Command-Engine. | QP (§6.8), A13 |
| B9 | Für Kind-Actions gilt die Fehlerpolitik des Commands wie für Actions auf oberster Ebene: Bei `continue` läuft die nächste Kind-Action, und die umgebende Action gelingt, mit den Fehlern im Verlauf; bei `abort` endet die Instanz als `failed`. Im Verlauf steht die Position als Pfad, etwa `3.2` für die zweite Kind-Action der dritten Action. | [`command-engine.md`](command-engine.md), B71 |

### Warten, Zufall, Gruppe, Wiederholen

| ID | Regel | Quellen |
|---|---|---|
| B10 | **Warten** (`wait`): Dauer in Sekunden, Nachkommastellen erlaubt, von 0 bis 3 600. Die nächste Action startet erst nach Ablauf; 0 wartet nicht. Ein Abbruch der Instanz beendet das Warten sofort ([`command-engine.md`](command-engine.md), B50). | Q2, A1 |
| B11 | **Zufall** (`random`): Kind-Actions und eine Anzahl (ganze Zahl von 0 bis 1 000, [`command-engine.md`](command-engine.md), B74). Die Action zieht so oft, wie die Anzahl sagt, je eine aktive Kind-Action mit gleicher Wahrscheinlichkeit und führt die gezogenen in der Reihenfolge des Ziehens aus. Die Anzahl darf größer sein als die Zahl der Kind-Actions; dann kommen Kind-Actions mehrfach vor. | Q3 |
| B12 | Option „ohne Wiederholung“: Jede Kind-Action wird je Ausführung höchstens einmal gezogen. Sind alle gezogen, endet das Ziehen, auch wenn die Anzahl größer ist. | Q3 |
| B13 | Option „über Ausführungen merken“ (nur mit B12): Gezogene Kind-Actions bleiben über Ausführungen hinweg gesperrt, bis alle einmal gezogen wurden; danach beginnt die Auswahl von vorn. Das Gedächtnis gilt je Command und Action, liegt im Speicher und beginnt nach einem Neustart oder einer Änderung des Commands von vorn. | Q3, A2 |
| B14 | **Gruppe** (`group`): führt ihre Kind-Actions der Reihe nach aus. Sie ordnet Actions, fasst sie als eine Kind-Action für den Zufall zusammen und schaltet mehrere Actions gemeinsam aktiv oder inaktiv (B1). | Q4 |
| B15 | **Wiederholen** (`repeat`): Kind-Actions und eine Anzahl (ganze Zahl von 0 bis 1 000). Die Kind-Actions laufen so oft der Reihe nach; 0 führt sie nicht aus. Eine Anzahl über 1 000 lässt die Action scheitern, bevor der erste Durchgang beginnt ([`command-engine.md`](command-engine.md), B74). | Q5, [`command-engine.md`](command-engine.md) A15 |

### Bedingung

| ID | Regel | Quellen |
|---|---|---|
| B20 | **Bedingung** (`conditional`): eine oder mehrere Klauseln, eine Verknüpfung (`and`, `or`, `xor`), ein Schalter für Groß- und Kleinschreibung (beim Anlegen aus), Kind-Actions für „wahr“, optional Kind-Actions für „falsch“ und die Option „wiederholen, solange wahr“. | Q6, A3 |
| B21 | Eine Klausel hat einen linken Wert, einen Vergleich und einen rechten Wert, bei `between` zwei: Minimum und Maximum. Alle Werte sind Templates. Vergleiche: `equals`, `not_equals`, `greater`, `greater_or_equal`, `less`, `less_or_equal`, `contains`, `not_contains`, `between`, `replaced`, `not_replaced`, `regex`, `in`, `not_in`, `expression`. | Q6, A4 |
| B22 | Gleichheit und Ordnung: Sind beide Seiten Zahlen ([`template.md`](template.md), B51), wird numerisch verglichen, sonst als Text nach Unicode-Codepunkten, mit oder ohne Beachtung der Schreibweise nach dem Schalter. `between` gilt nur für Zahlen und schließt beide Grenzen ein; ist ein Wert keine Zahl, ist die Klausel falsch. | Q6 |
| B23 | `contains` und `not_contains` suchen den rechten Wert als Teiltext im linken; ein leerer rechter Wert ist immer enthalten. `in` und `not_in` lesen den rechten Wert als Liste, getrennt mit dem Trennzeichen der getrennten Argumente ([`command-engine.md`](command-engine.md), B90); Leerraum um die Einträge entfällt, verglichen wird wie bei Gleichheit (B22). | Q6 |
| B24 | `replaced` ist wahr, wenn jedes Token im linken Wert beim Rendern einen Wert bekommen hat ([`template.md`](template.md), B3, B4); ein Text ohne Token gilt als ersetzt. `not_replaced` ist das Gegenteil. Der rechte Wert entfällt. | Q6, [`template.md`](template.md) Q3 |
| B25 | `regex` sucht den rechten Wert als regulären Ausdruck in RE2-Syntax irgendwo im linken Wert; der Schalter macht die Suche unabhängig von der Schreibweise. Ein ungültiger Ausdruck lässt die Action scheitern. | Q6, A4 |
| B26 | `expression` wertet den linken Wert als Ausdruck aus ([`template.md`](template.md), B50–B52); das Ergebnis muss wahr oder falsch sein, sonst scheitert die Action. Der rechte Wert entfällt. | A4 |
| B27 | Verknüpfung: `and` ist wahr, wenn alle Klauseln wahr sind, `or`, wenn mindestens eine wahr ist, `xor`, wenn genau eine wahr ist. Alle Werte der Bedingung entstehen in einem Rendervorgang (B3), bevor verglichen wird. | Q6, A5 |
| B28 | Ist die Bedingung wahr, laufen die Kind-Actions für „wahr“, sonst die für „falsch“; die Bedingung endet, wenn sie fertig sind. | Q6 |
| B29 | „Wiederholen, solange wahr“: Nach den Kind-Actions für „wahr“ wird die Bedingung neu gerendert und ausgewertet, bis sie falsch ist. Nach 1 000 Durchgängen, in denen sie wahr war, scheitert die Action ([`command-engine.md`](command-engine.md), B74). Die Kind-Actions für „falsch“ laufen nur, wenn schon die erste Auswertung falsch ist. | Q6 |

### Command

| ID | Regel | Quellen |
|---|---|---|
| B30 | **Command** (`command`), Arten: `run` (ausführen), `enable`, `disable`, `toggle` (einen Command schalten), `enable_group`, `disable_group` (alle Commands einer Gruppe schalten), `cancel_all`, `pause`, `unpause`, `pause_entrance`, `unpause_entrance`, `start_cooldown`, `exit` (aktuellen Command beenden). | Q7 |
| B31 | Commands und Gruppen werden über ihre ID referenziert. Das Speichern lehnt unbekannte IDs ab; fehlt der Command oder die Gruppe beim Ausführen, scheitert die Action. | Q7 |
| B32 | `run` hat drei Optionen: „warten“ (beim Anlegen an), „Anforderungen prüfen“ (beim Anlegen aus) und „eigene Argumente“ mit einem Template. Ohne eigene Argumente bekommt der aufgerufene Command die der aufrufenden Instanz; mit ihnen wird der gerenderte Text an Leerraum in Argumente zerlegt, ein leerer Text heißt „keine Argumente“ ([`command-engine.md`](command-engine.md), B30–B34). | Q7, A6 |
| B33 | Ausgänge des Aufrufs (`engine.Run.Call`): `completed` (mit Warten) und `queued` (ohne Warten) lassen die Action gelingen. `disabled` (Command inaktiv), `rejected` (Anforderung nicht erfüllt, nur mit Prüfung) und `waiting` (Schwelle noch nicht erreicht) lassen sie ebenfalls gelingen, ohne dass der Command läuft; der Core loggt sie, die Meldung einer Ablehnung übernimmt die Engine ([`command-engine.md`](command-engine.md), B11, B12). Scheitern lassen sie nur Fehler des Aufrufs (Zyklus, Tiefe, Herunterfahren, volle Warteschlange) und ein Aufruf mit Warten, der als `failed` oder `canceled` endet ([`command-engine.md`](command-engine.md), B36, B73). | Q7, A7 |
| B34 | `enable`, `disable` und `toggle` ändern den Schalter „aktiv“ des Commands dauerhaft wie eine Änderung in der Oberfläche; sie wirken auf Instanzen, die danach eingereiht werden ([`command-engine.md`](command-engine.md), B3). `enable_group` und `disable_group` tun das für alle Commands der Gruppe. Ein Command, der schon den Zielzustand hat, bleibt unverändert; das ist kein Scheitern. | Q7 |
| B35 | `cancel_all` bricht alle eingereihten und laufenden Instanzen ab, auch die eigene; sie endet dann als `canceled` ([`command-engine.md`](command-engine.md), B51). | Q7 |
| B36 | `pause`, `unpause`, `pause_entrance` und `unpause_entrance` wirken wie Pause und Fortsetzen über die Oberfläche ([`command-engine.md`](command-engine.md), B40–B42). | Q7 |
| B37 | `start_cooldown` startet den Cooldown des genannten Commands, als wäre er gerade eingereiht worden, nach dessen Cooldown-Art ([`requirements.md`](requirements.md)); bei den Arten je Nutzer für den Nutzer dieses Durchlaufs. Ohne Cooldown-Anforderung gibt es nichts zu tun; das ist kein Scheitern. Eine Art je Nutzer in einem Durchlauf ohne Nutzer lässt die Action scheitern. | Q7 |
| B38 | `exit` beendet die eigene Instanz als `completed`; weitere Actions laufen nicht ([`command-engine.md`](command-engine.md), B4). In einem Command, der mit Warten aufgerufen wurde, endet nur dieser; der Aufrufer macht mit seiner nächsten Action weiter. | Q7 |

### Counter

| ID | Regel | Quellen |
|---|---|---|
| B40 | **Counter** (`counter`), Arten: `increment` und `decrement` (um die Schrittweite des Counters erhöhen bzw. verringern), `add` (Betrag, ganze Zahl, auch negativ), `set` (Wert, ganze Zahl), `reset` (auf 0) ([`counters-and-quotes.md`](counters-and-quotes.md), B2, B8). | Q8, A15 |
| B41 | Der Counter wird über seinen Namen gewählt. Nennt eine Action beim Speichern einen Counter, den es nicht gibt, legt das Speichern ihn mit dem Wert 0 an, nach den Namensregeln der Counter ([`counters-and-quotes.md`](counters-and-quotes.md), B1, B7). Fehlt er beim Ausführen, scheitert die Action. | Q8 |
| B42 | Die Änderung ist atomar und sofort gespeichert ([`counters-and-quotes.md`](counters-and-quotes.md), B6): Addieren zwei Instanzen gleichzeitig, zählen beide. Ein Ergebnis außerhalb von 64 Bit lässt die Action scheitern, der Wert bleibt unverändert. | Q8, [`counters-and-quotes.md`](counters-and-quotes.md) B5 |
| B43 | Folgende Actions sehen den neuen Wert über **[Interop]** `$<name>` und `$<name>display`; weitere Ergebniswerte setzt die Action nicht. | Q8 |

### Special Identifier

| ID | Regel | Quellen |
|---|---|---|
| B50 | **Special Identifier** (`special_identifier`): Name, Wert (Template), Option „Rechnen“ und Option „global“. Der Name folgt B5; ein `$` darin ist nicht erlaubt, weil es andere Identifier stören würde. | Q9 |
| B51 | Mit „Rechnen“ ist der Wert ein Ausdruck ([`template.md`](template.md), B50–B52). Das Ergebnis wird als Zahl geschrieben: ganze Zahlen ohne Nachkommastellen, sonst mit Punkt und so wenigen Stellen wie nötig. | Q9, [`template.md`](template.md) A3 |
| B52 | Ohne „Rechnen“ kann der Wert Textfunktionen enthalten. Eine Funktion ist ein Name, direkt gefolgt von Klammern mit Parametern, getrennt durch Kommas, etwa `tolower(…)` oder `replace(…,…,…)`; Funktionen lassen sich verschachteln, die Namen sind unabhängig von der Schreibweise. **[Interop]** `tolower`, `toupper`, `removespaces`, `removecommas`, `length`, `count`, `replace`, `urlencode`, `uriescape`, `datefrom`, `dateto` | Q9 |
| B53 | Die Struktur der Funktionen wird aus dem Wert gelesen, bevor Identifier eingesetzt werden; danach wird jeder Parameter für sich gerendert. Eingesetzte Werte werden so nie zu Funktionen oder Trennzeichen, auch wenn sie Klammern oder Kommas enthalten ([`template.md`](template.md), B5). Ein Parameter in doppelten Anführungszeichen ist wörtlicher Text samt Kommas und Klammern. Nur die Namen aus B52 bilden Funktionen; ein anderer Name mit Klammern, etwa `Score(s)`, bleibt Text, ebenso ein Funktionsname ohne schließende Klammer. | A8 |
| B54 | Die Funktionen: `tolower` und `toupper` ändern die Schreibweise (Unicode). `removespaces` und `removecommas` entfernen Leerzeichen bzw. Kommas. `length` zählt die Zeichen. `count(Text,Muster)` zählt die Treffer eines regulären Ausdrucks (RE2). `replace(Text,Suche,Ersatz)` ersetzt jedes Vorkommen des Suchtexts; ein leerer Suchtext lässt den Text unverändert. `urlencode` kodiert für Formulare (Leerzeichen als `+`), `uriescape` für Adressen (Leerzeichen als `%20`). `datefrom(Datum)` liefert die Zeitspanne seit einem vergangenen Datum im Format `JJJJ-MM-TT`, `dateto(Datum)` die bis zu einem künftigen, beide im Format der Zeitspannen ([`template.md`](template.md), B42) und in der Zeitzone des Profils. | Q9 |
| B55 | Ein ungültiges Muster bei `count`, ein ungültiges Datum und ein Datum in der falschen Richtung bei `datefrom` oder `dateto` lassen die Action scheitern. | Q9, A8 |
| B56 | Ohne „global“ setzt die Action einen Wert des Durchlaufs (B5). Mit „global“ setzt sie ihn zusätzlich als globalen Wert: Er gilt in jedem späteren Rendervorgang aller Instanzen, bis ihn eine Action ändert oder der Core endet. Globale Werte werden nicht gespeichert und nicht gesichert; setzen zwei Instanzen denselben Namen, gilt der zuletzt gesetzte. Ein Wert des Durchlaufs verdeckt einen gleichnamigen globalen ([`template.md`](template.md), B10). | Q9, QP (§6.10) |
| B57 | Es gibt höchstens 10 000 globale Werte. Ein neuer Name darüber hinaus lässt die Action scheitern; bestehende lassen sich weiter ändern. | A14 |

### Chat und Plattformnachricht

| ID | Regel | Quellen |
|---|---|---|
| B60 | **Chat** (`chat`): Nachricht (Template), Option „als Streamer senden“, Option „flüstern“ mit Empfänger (Template, beim Anlegen `$username`) und Option „als Antwort“. | Q10, A9 |
| B61 | Die Nachricht geht vom Bot-Konto der Plattform, wenn es verbunden ist, sonst vom Streamer-Konto; mit „als Streamer senden“ immer vom Streamer-Konto. | Q10, QP (§6.11) |
| B62 | Die Nachricht geht an alle verbundenen Plattformen zugleich. Ist keine verbunden, wird nichts gesendet; der Core loggt das, und es ist kein Scheitern. | Q10 |
| B63 | Flüstern: Die Nachricht geht privat an den Empfänger, einen Nutzernamen mit oder ohne `@`, auf jeder verbundenen Plattform, die Flüstern kennt und auf der der Empfänger bekannt ist. Auf den übrigen Plattformen entfällt sie, mit Eintrag im Log. Ist sie auf keiner Plattform möglich, scheitert die Action. | Q10 |
| B64 | Als Antwort bezieht sich die Nachricht auf der Plattform des Durchlaufs auf die auslösende Nachricht, wenn es eine gibt und die Plattform Antworten kennt; sonst und auf den anderen Plattformen ist sie eine gewöhnliche Nachricht. | QP (§6.11), A9 |
| B65 | Ist die Nachricht nach dem Rendern leer oder nur Leerraum, wird nichts gesendet; das ist kein Scheitern. Länge, Zeilenumbrüche und Rate-Limits übernimmt der Plattform-Adapter: Er teilt zu lange Nachrichten auf ([`template.md`](template.md), B32). | QP (§6.11) |
| B66 | Scheitert das Senden auf mindestens einer Plattform, scheitert die Action; die Meldung nennt die Plattformen. Auf den anderen bleibt die Nachricht gesendet. | Code-ADR-0017 |
| B67 | **Plattformnachricht** (`platform_message`): wie Chat, aber an genau eine gewählte Plattform und ohne Flüstern. Ist die Plattform nicht verbunden, geschieht nichts; der Core loggt das, und es ist kein Scheitern. | Q13 |

### Web-Request

| ID | Regel | Quellen |
|---|---|---|
| B70 | **Web-Request** (`web_request`): Methode (`GET`, `POST`, `PUT`, `DELETE`), Adresse (Template), Header (fester Name, Wert als Template), Body (Template, nur bei `POST` und `PUT`) und Ergebnis (`none`, `text` oder `json`). | Q11 |
| B71 | Kodierung ([`template.md`](template.md), B30): In der Adresse werden eingesetzte Werte mit URL kodiert, in Headern mit Text. Der Body richtet sich nach dem Header `Content-Type`: JSON bei `application/json`, URL bei `application/x-www-form-urlencoded`, sonst Text. | QP (§6.10), A10 |
| B72 | Nur `http` und `https` sind erlaubt; eine Adresse, die nach dem Rendern nicht absolut ist oder ein anderes Schema hat, lässt die Action scheitern. | ADR-0013 |
| B73 | Die Anfrage hat 10 s Zeit, samt Weiterleitungen (höchstens 10) und Lesen der Antwort. Die Antwort darf höchstens 1 MiB groß sein; eine größere lässt die Action scheitern. | Q11, A10 |
| B74 | Eine Antwort mit einem Status außerhalb von 200–299 lässt die Action scheitern; die Meldung nennt den Status, und es wird kein Ergebniswert gesetzt. | A10 |
| B75 | `text` setzt den ganzen Body als **[Interop]** `$webrequestresult`. | Q11 |
| B76 | `json` liest den Body als JSON und setzt je Eintrag einer Zuordnung aus Pfad und Name einen Ergebniswert. Die Teile eines Pfads trennt `\` **[Interop]**, etwa `job\title`; ein Teil aus Ziffern wählt in einer Liste den Eintrag ab 0. Text wird unverändert gesetzt, Zahlen, `true` und `false` wie im JSON, `null` als leerer Text, Objekte und Listen als kompaktes JSON. Ein Pfad ohne Treffer setzt keinen Wert und erzeugt eine Warnung im Log; das ist kein Scheitern. Ungültiges JSON lässt die Action scheitern. | Q11 |
| B77 | Im Server-Modus gilt der SSRF-Schutz aus [ADR-0013](../adr/0013-sicherheitsmodell.md) für die Adresse und jede Weiterleitung. | ADR-0013 |

### Moderation

| ID | Regel | Quellen |
|---|---|---|
| B80 | **Moderation** (`moderation`), Arten: `timeout` (Dauer in Sekunden, ganze Zahl ab 1), `purge` (Nachrichten des Nutzers entfernen), `clear_chat`, `ban`, `unban`, `mod`, `unmod`, `add_strike`, `remove_strike`, `reset_strikes` (alle Nutzer), `disable_chat`, `enable_chat`. | Q12 |
| B81 | Arten mit Nutzer nennen ihn über ein Template, mit oder ohne `@`. Er muss nicht im Chat sein: Der Core sucht ihn erst in der eigenen Datenbank, dann über die Plattform. Wird er nicht gefunden, scheitert die Action. | Q12 |
| B82 | Die Action wirkt auf der Plattform des Durchlaufs. Hat der Durchlauf keine Plattform, etwa bei einem Timer, wirkt sie auf jeder verbundenen Plattform, bei Arten mit Nutzer auf jeder, auf der er eine Identität hat. | A11 |
| B83 | `timeout` und `ban` haben einen optionalen Grund (Template), den der Core an die Plattform weitergibt, wo sie ihn annimmt. | QP (§6.11) |
| B84 | Strikes zählt streamcrew selbst, nicht die Plattform ([`users-and-roles.md`](users-and-roles.md), B9): `add_strike` erhöht die Zahl des Nutzers um 1, `remove_strike` senkt sie um 1, aber nicht unter 0, `reset_strikes` setzt sie bei allen Nutzern auf 0. | Q12 |
| B85 | `disable_chat` schaltet den Chat stumm: Bis `enable_chat` oder bis der Core endet, löscht der Core jede neue Chatnachricht auf den verbundenen Plattformen, außer denen von Streamer und Bot und denen, die die Plattform nicht löschen lässt. Der Zustand wird nicht gespeichert. | Q12 |
| B86 | Lehnt die Plattform ab, etwa mangels Rechten oder weil das Ziel der Streamer ist, scheitert die Action mit dem Grund der Plattform. | QP (§6.11) |

### Nutzersuche

| ID | Regel | Quellen |
|---|---|---|
| B90 | **Nutzersuche** (`user_lookup`): Plattform (fest gewählt) und Name oder ID (Template). | Q14 |
| B91 | Der Core sucht erst in der eigenen Datenbank nach einer Identität der Plattform mit diesem Namen (ohne Beachtung der Schreibweise, mit oder ohne `@`) oder dieser Plattform-ID, dann über die Plattform. Ein über die Plattform gefundener Nutzer wird wie ein neu gesehener gespeichert ([`users-and-roles.md`](users-and-roles.md)). | Q14 |
| B92 | Über die Plattform sucht der Core höchstens einmal je Minute, über alle Nutzersuchen hinweg. Ist die Suche gerade gesperrt oder die Plattform nicht verbunden, gilt sie als erfolglos, und das Log nennt den Grund. | Q14 |
| B93 | Ergebniswerte für den Rest der Instanz: **[Interop]** `$lookupusername`, `$lookupdisplayname`, `$lookupid` (ID auf der Plattform), `$lookupavatarurl` und `$lookupsuccess` mit `True` oder `False`. Ohne Treffer sind die ersten vier leer und `$lookupsuccess` ist `False`; das ist kein Scheitern. | Q14 |

### Datei

| ID | Regel | Quellen |
|---|---|---|
| B100 | **Datei** (`file`): Art, Wurzel (Name einer freigegebenen Wurzel aus der Startkonfiguration), Pfad relativ zur Wurzel (Template) und je nach Art ein Text (Template), eine Zeilennummer (ganze Zahl ab 1) und der Name des Ergebniswerts. | Q15, ADR-0013, A12 |
| B101 | Arten: `write` (anlegen oder überschreiben), `append`, `count_lines`, `read`, `read_line`, `read_random_line`, `each_line`, `remove_line`, `remove_random_line`, `remove_matching_line`, `insert_line`, `insert_random_line`. | Q15 |
| B102 | Der Pfad bleibt in der Wurzel: Absolute Pfade, `..` über die Wurzel hinaus und symbolische Links nach draußen lassen die Action scheitern, ebenso eine Wurzel, die die Startkonfiguration nicht kennt. | ADR-0013 |
| B103 | Dateien sind UTF-8; ungültige Bytes werden beim Lesen zu U+FFFD. Zeilen enden beim Lesen mit `\n` oder `\r\n`, beim Schreiben mit `\n`. Eine leere Zeile am Dateiende zählt nicht als Zeile. Zeilennummern beginnen bei 1. | Q15 |
| B104 | `write` legt die Datei und fehlende Verzeichnisse an und ersetzt den Inhalt. `append` hängt den Text als neue Zeile an und legt die Datei bei Bedarf an. `insert_line` fügt den Text vor Zeile N ein; N gleich Zeilenzahl plus 1 hängt an, ein größeres N lässt die Action scheitern. `insert_random_line` fügt an zufälliger Stelle ein. | Q15 |
| B105 | `read` setzt den ganzen Inhalt, `count_lines` die Zahl der Zeilen, `read_line` die Zeile N, `read_random_line` eine zufällige Zeile als Ergebniswert. | Q15 |
| B106 | `remove_line` entfernt Zeile N, `remove_random_line` eine zufällige, `remove_matching_line` die erste Zeile, die genau dem Text gleicht; die entfernte Zeile wird Ergebniswert. Findet `remove_matching_line` keine Zeile, bleibt die Datei unverändert und es gibt keinen Ergebniswert; das ist kein Scheitern. | Q15 |
| B107 | `each_line` führt die Kind-Actions für jede Zeile der Reihe nach aus, mit der Zeile als Ergebniswert. Mehr als 1 000 Zeilen lassen die Action scheitern, bevor der erste Durchgang beginnt ([`command-engine.md`](command-engine.md), B74). | Q15 |
| B108 | Eine fehlende Datei lässt jede Art außer `write` und `append` scheitern, ebenso eine Zeile N außerhalb der Datei und eine zufällige Zeile aus einer leeren Datei. Dateien über 1 MiB werden nicht gelesen oder geändert; die Action scheitert. | A12 |
| B109 | Änderungen ersetzen die Datei atomar: Der Core schreibt den neuen Inhalt vollständig und tauscht die Datei dann aus, sodass Leser wie eine Textquelle in OBS nie eine halbe Datei sehen. Zugriffe auf dieselbe Datei laufen im Core nacheinander, damit gleichzeitige Instanzen keine Änderung verlieren. | A12 |

### Externes Programm

| ID | Regel | Quellen |
|---|---|---|
| B110 | **Externes Programm** (`external_program`): Programm (Pfad, Template), Argumente (Template), Option „warten“, Option „Ausgabe speichern“ (nur mit Warten), Zeitlimit in Sekunden (von 1 bis 3 600, beim Anlegen 30), Option „Fenster anzeigen“ und Option „über das System öffnen“. | Q16 |
| B111 | Die Argumente werden vor dem Rendern in Wörter zerlegt: Leerraum trennt, doppelte Anführungszeichen fassen zusammen. Jedes Wort wird für sich gerendert und als ein Argument übergeben, ohne Shell. Eingesetzter Text erzeugt so weder zusätzliche Argumente noch Befehle. | A13 |
| B112 | Ohne „warten“ startet das Programm, und die Action ist fertig; das Programm läuft unabhängig vom Core weiter, auch über dessen Ende hinaus. | Q16 |
| B113 | Mit „warten“ ist die Action fertig, wenn das Programm endet. Endet es mit einem Exit-Code ungleich 0, scheitert die Action; die Ausgabe wird vorher gespeichert. | Q16, A13 |
| B114 | „Ausgabe speichern“ setzt Standardausgabe und Fehlerausgabe zusammen, als UTF-8 gelesen, als **[Interop]** `$externalprogramresult`; gespeichert wird höchstens 1 MiB, der Rest entfällt mit einer Warnung im Log. | Q16 |
| B115 | Läuft das Programm länger als sein Zeitlimit, beendet der Core es, speichert die bis dahin gelesene Ausgabe, und die Action scheitert. | Q16, A13 |
| B116 | „Fenster anzeigen“ wirkt nur auf Systemen mit Fenstern; sonst entfällt es. „Über das System öffnen“ öffnet den Pfad mit dem Programm, das das Betriebssystem dafür vorsieht, etwa ein Dokument mit seiner Anwendung; es schließt „warten“ und „Ausgabe speichern“ aus, das prüft das Speichern. | Q16 |
| B117 | Das Programm läuft im Verzeichnis, in dem es liegt. Es bekommt die Umgebungsvariablen des Cores außer denen, die mit `STREAMCREW_` beginnen, damit Konfiguration und Secrets des Cores nicht nach außen gelangen. | ADR-0013, A13 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B200 | Warten mit `$arg1text` als Dauer, das Argument ist „abc“ | Die Action scheitert; die Meldung nennt Feld und Wert. | B4, B10 |
| B201 | Wiederholen mit der Anzahl 1 001 | scheitert, bevor ein Durchgang läuft | B15 |
| B202 | Wiederholen mit 1 000 Durchgängen in einem Wiederholen mit 1 000 | erlaubt; die Grenze gilt je Action. Abbrechen ist jederzeit möglich. | B15, [`command-engine.md`](command-engine.md) B50 |
| B203 | Zufall ohne aktive Kind-Actions | Es läuft nichts; die Action gelingt. | B1, B11 |
| B204 | Zufall „ohne Wiederholung“ mit Anzahl 5 und drei Kind-Actions | Alle drei laufen je einmal in gezogener Reihenfolge. | B12 |
| B205 | Bedingung `in` mit links „B“, rechts „a \| b\|c“, Schreibweise egal | wahr | B23 |
| B206 | `greater` mit „10“ und „9“; mit „10“ und „9a“ | wahr (numerisch); falsch (als Text, „1“ vor „9“) | B22 |
| B207 | `replaced` auf `$arg2text` bei einem Argument | falsch; `not_replaced` ist wahr | B24 |
| B208 | Command A ruft mit der Command-Action sich selbst auf | Die Action scheitert (Zyklus). | B33, [`command-engine.md`](command-engine.md) B73 |
| B209 | `exit` in einer Bedingung eines Commands, der mit Warten aufgerufen wurde | Der aufgerufene Command endet als `completed`, der Aufrufer macht weiter. | B38 |
| B210 | Der Wert einer Special-Identifier-Action ist `toupper($arg1text)`, das Argument lautet `a),removespaces(b` | Ergebnis `A),REMOVESPACES(B`; das Argument bleibt ein Parameter. | B53 |
| B211 | Zwei Instanzen setzen gleichzeitig denselben globalen Wert | Der zuletzt gesetzte gilt. | B56 |
| B212 | Chat-Nachricht nur aus `$arg1text` ohne Argument | Das Token bleibt stehen ([`template.md`](template.md), B4) und wird gesendet. | B65 |
| B213 | Web-Request mit `$arg1text` in der Adresse, das Argument enthält `/../` oder `&x=1` | wird kodiert und ändert Pfad und Parameter nicht | B71 |
| B214 | JSON-Zuordnung mit einem Pfad, den die Antwort nicht hat | Die anderen Werte werden gesetzt, dieser nicht; Warnung im Log. | B76 |
| B215 | Timeout für den Streamer | Die Plattform lehnt ab; die Action scheitert. | B86 |
| B216 | Zweite Nutzersuche über die Plattform innerhalb einer Minute | erfolglos mit `$lookupsuccess` gleich `False`; kein Scheitern | B92, B93 |
| B217 | Datei mit dem Pfad `../geheim.txt` | Die Action scheitert. | B102 |
| B218 | Zwei Instanzen hängen gleichzeitig an dieselbe Datei an | Beide Zeilen stehen in der Datei. | B109 |
| B219 | Externes Programm mit dem Argument `$arg1text`, das Argument lautet `x; rm -rf ~` | Das Programm bekommt genau ein Argument mit diesem Text; es gibt keine Shell. | B111 |
| B220 | Counter `add` mit dem Betrag 1.5 | Die Action scheitert, der Counter bleibt. | B4, B40 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | Mengenangaben mit Identifiern; Grenzen, Rundung und Obergrenze beim Warten nicht dokumentiert | Ausdrücke mit festen Bereichen, keine Rundung, Warten bis 3 600 s (B4, B10) | Fehleingaben sollen auffallen statt still korrigiert zu werden (Code-ADR-0017); eine vergessene Null soll keine Sperre für Stunden halten |
| A2 | Gedächtnis der Zufallsauswahl über Ausführungen; Speicherung nicht dokumentiert | im Speicher, neu nach Neustart oder Änderung (B13) | Nach einer Änderung passen die gemerkten Kind-Actions nicht mehr; zu prüfen |
| A3 | Kind-Actions nur für „wahr“ dokumentiert | zusätzlich Kind-Actions für „falsch“ (B20, B28) | spart eine zweite, umgekehrte Bedingung; Roadmap 3.3 sieht Verzweigungen vor |
| A4 | reguläre Ausdrücke nach .NET; keine Ausdrucksklausel | RE2 (B25), neue Klausel `expression` (B26) | RE2 läuft in linearer Zeit und braucht kein Zeitlimit; Ausdrücke gibt es mit `internal/expr` ohnehin |
| A5 | Bedeutung von XOR bei mehr als zwei Klauseln nicht dokumentiert | genau eine wahr (B27) | entspricht der üblichen Lesart „entweder … oder“; zu prüfen |
| A6 | leeres Argumentfeld heißt „Argumente des Aufrufers“ | ausdrückliche Option „eigene Argumente“ (B32); der Import bildet ein leeres Feld auf „aus“ ab | keine magischen Werte (Code-ADR-0017) |
| A7 | nicht dokumentiert, was ein Aufruf eines inaktiven oder abgelehnten Commands für die Action heißt | benannte Ausgänge ohne Scheitern (B33) | Ein inaktiver Command ist eine Entscheidung des Streamers, eine Ablehnung ein normales Ergebnis der Anforderungen, beides kein Fehler |
| A8 | Textfunktionen werden nach dem Einsetzen der Identifier ausgewertet; ungültige Muster überspringt das Original | Struktur vor dem Einsetzen gelesen (B53); Fehler lassen die Action scheitern (B55) | Zuschauertext soll keine Funktionen einschleusen ([`template.md`](template.md), A1); Fehler sollen sichtbar sein |
| A9 | Empfänger beim Flüstern leer heißt „auslösender Nutzer“; keine Antwort-Option | Empfänger ausdrücklich, beim Anlegen `$username`; Option „als Antwort“ (B60, B64) | keine magischen Werte; Antworten kennt der Chat-Port (Plan §6.11) |
| A10 | keine Kodierung, kein Umgang mit Status und Größe dokumentiert | Kodierung je Teil (B71), Scheitern bei Status außerhalb 2xx (B74), höchstens 1 MiB (B73) | Sicherheit ([`template.md`](template.md), A2); Fehlerseiten sollen nicht als Ergebnis im Chat landen |
| A11 | nicht dokumentiert, auf welcher Plattform Moderation ohne Plattform des Durchlaufs wirkt | jede verbundene Plattform (B82) | entspricht der Chat-Action, die an alle Plattformen sendet; zu prüfen |
| A12 | beliebige Pfade; Größe, Fehlerfälle und gleichzeitige Zugriffe nicht dokumentiert | nur unter freigegebenen Wurzeln (B100, B102), Scheitern bei fehlenden Dateien und Zeilen (B108), atomares Ersetzen (B109) | Sicherheitsmodell (ADR-0013); klare Signale statt leerer Werte (Code-ADR-0017) |
| A13 | Argumente als ein Text; das Programm läuft nach dem Zeitlimit weiter; Exit-Code und Umgebung nicht dokumentiert | Wörter ohne Shell (B111), Beenden nach dem Zeitlimit (B115), Scheitern bei Exit-Code ungleich 0 (B113), Umgebung ohne `STREAMCREW_` (B117); Zeitlimits von Actions mit Kind-Actions je Kind-Action (B8) | Schutz vor eingeschleusten Befehlen und liegengebliebenen Prozessen; eine lange Schleife soll nicht am Limit der ganzen Action scheitern |
| A14 | keine Grenze für globale Werte dokumentiert | höchstens 10 000 (B57) | Speicherschutz, vor allem im Server-Modus |
| A15 | Der Betrag steht in jeder Counter-Action. | zusätzlich `increment` und `decrement` um die Schrittweite des Counters (B40) | Entscheidung des Projektinhabers; [`counters-and-quotes.md`](counters-and-quotes.md), A6 |

## Akzeptanzkriterien

- [ ] B1–B9: inaktive Actions und Kind-Actions; Rendern beim Ausführen; Mengenangaben mit Bereichen und ganzen Zahlen; Sichtbarkeit von Ergebniswerten in Instanz und Aufrufen; Kollision von Namen beim Speichern; fehlende Capability; Zeitlimits einschließlich Kind-Actions; Fehlerpolitik in Kind-Actions mit Pfad im Verlauf.
- [x] B10–B15, B200–B204: Warten mit `testing/synctest`; Zufall mit fester Zufallsquelle, auch „ohne Wiederholung“ und über Ausführungen; Gruppe; Wiederholen mit Grenze.
- [x] B20–B29, B205–B207: Tabelle je Vergleich mit Zahlen, Text, Schreibweise und Grenzfällen; Verknüpfungen; „falsch“-Zweig; Wiederholen bis zur Grenze.
- [x] B30–B38, B208, B209: jede Art der Command-Action gegen eine Engine mit Fakes; jeder Ausgang von `engine.Run.Call`.
- [x] B40–B43, B220: Counter-Arten samt Schritten, Anlegen beim Speichern, gleichzeitiges Addieren, Überlauf.
- [x] B50–B57, B210, B211: Rechnen, jede Textfunktion, verschachtelte Funktionen, eingeschleuste Klammern (Fuzz-Test), globale Werte als Quelle der Templates, Grenze der Anzahl.
- [x] B60–B67, B212: Absender, mehrere Plattformen, Flüstern, Antwort, leere Nachricht, Teilerfolg; gegen Fakes des Chat-Ports.
- [ ] B70–B77, B213, B214: Web-Request gegen `httptest`: Kodierung, Status, Größe, Zeitlimit, Text und JSON-Pfade, SSRF-Schutz im Server-Modus.
- [x] B80–B86, B215: jede Moderationsart gegen Fakes des Moderations-Ports; Strikes; stummer Chat.
- [x] B90–B93, B216: lokale Suche, Suche über die Plattform, Sperre je Minute, Ergebniswerte ohne Treffer.
- [ ] B100–B109, B217, B218: jede Datei-Art unter einer Wurzel mit `os.Root`; Pfade nach draußen; Zeilenenden; atomares Ersetzen; gleichzeitige Zugriffe.
- [x] B110–B117, B219: externes Programm mit Warten, ohne Warten, Ausgabe, Exit-Code, Zeitlimit und Argumenten ohne Shell; Umgebung ohne `STREAMCREW_`.

## Offene Fragen

- B1: Überspringt das Original mit einer inaktiven Action auch deren Kind-Actions?
- B10: Hat Warten im Original eine Obergrenze?
- B13: Übersteht das Gedächtnis der Zufallsauswahl im Original einen Neustart?
- B20: Wie ist der Schalter für die Schreibweise im Original voreingestellt, und gibt es Kind-Actions für „falsch“?
- B22: Vergleicht das Original numerisch, wenn beide Seiten Zahlen sind? Wie gibt man bei „between“ die Grenzen an?
- B27: Wie wertet das Original XOR bei mehr als zwei Klauseln aus?
- B29: Hat „wiederholen, solange wahr“ im Original eine Grenze?
- B36: Die Doku der Command-Action sagt, dass Entrance-Commands, die während ihrer Pause ausgelöst werden, nach dem Fortsetzen laufen. [`command-engine.md`](command-engine.md) legt in B41 (A7) das Gegenteil fest; das ist dort zu klären.
- B41: Legt das Original einen fehlenden Counter beim Speichern der Action an?
- B53: Wie schreibt man im Original ein Komma oder eine Klammer als Text in einen Parameter einer Textfunktion?
- B76: Wie schreibt das Original Wahrheitswerte und `null` aus JSON-Pfaden?
- B82: Auf welcher Plattform wirkt die Moderation im Original, wenn der Durchlauf keine Plattform hat?
- B92: Gilt die Grenze von einer Suche je Minute im Original je Plattform oder für alle?
- B103, B104: Schreibt das Original `\r\n`? Setzt `append` einen Zeilenumbruch vor oder nach dem Text?
- B113: Wie behandelt das Original einen Exit-Code ungleich 0?

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q1 | Doku | <https://mixitup.bot/docs/actions> | Übersicht der Action-Typen; die meisten Actions warten nicht auf das Ende ihrer Wirkung, Ausnahmen wie Warten, Web-Request, externes Programm und Command mit Warten; abgerufen 2026-09-30 |
| Q2 | Doku | <https://mixitup.bot/docs/actions/wait-action> | Dauer in Sekunden mit Nachkommastellen, hält die folgenden Actions auf; abgerufen 2026-09-30 |
| Q3 | Doku | <https://mixitup.bot/docs/actions/random-action> | Anzahl mit Identifiern, auch größer als die Zahl der Actions; ohne Wiederholung; über Ausführungen merken; abgerufen 2026-09-30 |
| Q4 | Doku | <https://mixitup.bot/docs/actions/group-action> | Ordnen, Zufall über Gruppen, gemeinsames Aktivieren und Deaktivieren; abgerufen 2026-09-30 |
| Q5 | Doku | <https://mixitup.bot/docs/actions/repeat-action> | Anzahl mit Identifiern, Wiederholung der Reihe nach; keine Grenze dokumentiert; abgerufen 2026-09-30 |
| Q6 | Doku | <https://mixitup.bot/docs/actions/conditional-action> | Vergleiche, Schreibweise, Und/Oder/Xor, „ersetzt“, reguläre Ausdrücke, Listen mit dem Trennzeichen der Argumente, Wiederholen solange wahr; abgerufen 2026-09-30 |
| Q7 | Doku | <https://mixitup.bot/docs/actions/command-action> | Arten der Command-Action, Optionen und ihre Voreinstellungen, Warten ohne Sperren, Cooldown setzen, aktuellen Command beenden; abgerufen 2026-09-30 |
| Q8 | Doku | <https://mixitup.bot/docs/actions/counter-action> | Addieren, Setzen, Zurücksetzen, Counter-Identifier; abgerufen 2026-09-30 |
| Q9 | Doku | <https://mixitup.bot/docs/actions/special-identifier-action> | Name ohne `$`, Rechnen, Textfunktionen und ihre Parameter, globale Werte für die Laufzeit, Namenskonflikte; abgerufen 2026-09-30 |
| Q10 | Doku | <https://mixitup.bot/docs/actions/chat-action> | Bot oder Streamer als Absender, Flüstern mit Empfänger, alle verbundenen Plattformen; abgerufen 2026-09-30 |
| Q11 | Doku | <https://mixitup.bot/docs/actions/web-request-action> | Methoden, Header, Body, Ergebnis als Text oder über JSON-Pfade mit `\`, Zeitlimit; abgerufen 2026-09-30 |
| Q12 | Doku | <https://mixitup.bot/docs/actions/moderation-action> | Arten, Suche des Nutzers in Datenbank und Plattform, Strikes, Chat stumm schalten durch Löschen; abgerufen 2026-09-30 |
| Q13 | Doku | <https://mixitup.bot/docs/actions/platform-message-action> | eine Plattform, ohne Flüstern, nichts geschieht ohne Verbindung; abgerufen 2026-09-30 |
| Q14 | Doku | <https://mixitup.bot/docs/actions/user-lookup-action> | Suche nach Name oder ID, Ergebnis-Identifier, Erfolg als Wahrheitswert, Grenze für Suchen über die Plattform; abgerufen 2026-09-30 |
| Q15 | Doku | <https://mixitup.bot/docs/actions/file-action> | Arten, Zeilennummern ab 1, Ergebnis unter gewähltem Namen, jede Zeile mit Kind-Actions; abgerufen 2026-09-30 |
| Q16 | Doku | <https://mixitup.bot/docs/actions/external-program-action> | Pfad, Argumente, Fenster, Shell, Warten, Ausgabe als Identifier, Zeitlimit; abgerufen 2026-09-30 |
| QP | Projekt | [Plan](../plan.md) §5.3, §6.8, §6.9, §6.10, §6.11 | P0-Actions, Zeitlimits und Grenzen, Typ-Registry, Templates und Ausdrücke, Plattform-Ports |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-30 | Erstfassung (Entwurf) aus der offiziellen Doku und dem Plan, ohne Code des Originals |
| 2026-10-01 | Vom Projektinhaber geprüft und akzeptiert. Die offenen Fragen bleiben bis zur Prüfung am Original offen; bis dahin gilt das hier beschriebene Verhalten. |
| 2026-10-01 | Typ-IDs nach Code-ADR-0013 eingetragen: `special_identifier`, `platform_message`, `web_request`, `user_lookup`, `external_program` statt der Arbeitsnamen. Das Verhalten ist unverändert. |
| 2026-10-01 | Warten, Zufall, Gruppe und Wiederholen umgesetzt (`internal/action/flow`). Festlegungen dabei: Die Felder heißen `seconds` (Warten), `count`, `draw` und `actions` (Zufall), `actions` (Gruppe) sowie `count` und `actions` (Wiederholen). Die beiden Optionen aus B12 und B13 sind beim Zufall ein Auswahlfeld `draw` mit den Werten `free` (B11), `unique` (B12) und `unique_remembered` (B12 mit B13), weil B13 nur mit B12 gilt: So lässt sich keine ungültige Kombination ausdrücken ([Code-ADR-0017](../adr/code/0017-klare-signale-statt-magischer-werte.md), Entscheidung des Projektinhabers). Der Import der beiden Schalter des Originals (Roadmap Phase 10) bildet sie auf diese Werte ab. Eine neue Zufalls-Action zieht mit `free`. Eine neue Zufalls-Action zieht einmal; Warten und Wiederholen haben keine Voreinstellung für Dauer und Anzahl, das Feld ist Pflicht. Mengen werden einmal beim Start der Action ausgewertet. Gezogen werden nur aktive Kind-Actions; Kind-Actions unbekannten Typs nehmen nicht teil, weil sie nicht laufen können. Das Gedächtnis über Ausführungen (B13) gilt je Command, Änderungszeitpunkt des Commands und Pfad der Action: Ist in einer Ausführung nichts mehr übrig, beginnt die Auswahl dort von vorn; ist nach einer Ausführung jede Kind-Action einmal gezogen, beginnt sie bei der nächsten von vorn. Warten setzt sein Zeitlimit, sobald die Dauer feststeht, auf die Dauer plus 5 s (B8). |
| 2026-10-01 | Bedingung umgesetzt (`internal/action/flow`). Festlegungen dabei: Die Felder heißen `clauses`, `combine` (`and`, `or`, `xor`; eine neue Bedingung verknüpft mit `and`), `caseSensitive` (beim Anlegen aus), `actions` (für „wahr“), `else` (für „falsch“) und `repeatWhileTrue`. Eine Klausel hat `left` und `compare`; welche Felder dazukommen, bestimmt der Vergleich: `right` bei den Vergleichen zweier Werte, `min` und `max` bei `between`, keines bei `replaced`, `not_replaced` und `expression`. Felder, die der Vergleich nicht nutzt, lehnen Schema und Dekodieren ab, damit keine Klausel Werte trägt, die nichts bewirken ([Code-ADR-0017](../adr/code/0017-klare-signale-statt-magischer-werte.md), Entscheidung des Projektinhabers). Als Zahl gilt, was nach der Regel der Ausdrücke eine Zahl ist ([`template.md`](template.md), B51): eine Dezimalzahl ohne Leerraum, sonst Text. Ohne Beachtung der Schreibweise zählt jeder Buchstabe wie die Kleinschreibung seiner Großschreibung, so sind „ſ“, „S“ und „s“ gleich; das gilt für Gleichheit, Ordnung, `contains` und `in`, bei `regex` schaltet der Schalter die Suche unabhängig von der Schreibweise. Ein regulärer Ausdruck ohne Token und ein Ausdruck bei `expression` werden schon beim Speichern geprüft, ein regulärer Ausdruck mit Token beim Ausführen. Alle Klauseln werden ausgewertet, auch wenn das Ergebnis schon feststeht; eine fehlerhafte Klausel lässt die Action also immer scheitern. Die Identifier eines Ausdrucks entstehen im selben Rendervorgang wie die übrigen Werte (B27). Im Pfad des Verlaufs (B9) zählen die Kind-Actions für „falsch“ nach denen für „wahr“ weiter. „Wiederholen, solange wahr“ erlaubt 1 000 Durchgänge; ist die Bedingung danach noch einmal wahr, scheitert die Action, bevor die Kind-Actions ein weiteres Mal laufen (B29). Jede Auswertung hat das Standard-Zeitlimit (B8). |
| 2026-10-01 | Command-Action umgesetzt (`internal/action/commands`). Festlegungen dabei: Die Felder heißen `kind`, `command` (ID des Commands bei `run`, `enable`, `disable`, `toggle` und `start_cooldown`) und `group` (ID der Gruppe bei `enable_group` und `disable_group`); nur `run` hat zusätzlich `wait`, `checkRequirements` und `args`. Felder anderer Arten lehnen Schema und Dekodieren ab. Die Option „eigene Argumente“ (B32) ist ein Objekt `args` mit `from`: `caller` gibt die Argumente der aufrufenden Instanz weiter, `own` rendert das Template in `text`; so steht kein leerer Text für „die des Aufrufers“, nach demselben Grundsatz wie beim Zufall und der Bedingung ([Code-ADR-0017](../adr/code/0017-klare-signale-statt-magischer-werte.md)). Eigene Argumente werden an jedem Leerraum geteilt, auch an Tabulatoren und Zeilenumbrüchen; Anführungszeichen fassen nicht zusammen. Ausgänge ohne Lauf (B33) loggt die Action auf Info-Ebene. `enable_group` und `disable_group` schalten alle Commands der Gruppe oder keinen; scheitert einer, etwa weil ein aktiver Chat-Command denselben Trigger hat ([`commands.md`](commands.md), B14), scheitert die Action. `start_cooldown` geht über den Requirement-Service (`engine.Requirements.StartCooldown`); solange es keinen gibt (Roadmap 3.4), gibt es keine Cooldowns, und die Art tut nichts. |
| 2026-10-01 | Counter umgesetzt (`internal/action/values`). Festlegungen dabei: Die Felder heißen `kind`, `counter` (Name, ohne Beachtung der Schreibweise), `amount` (nur bei `add`) und `value` (nur bei `set`). B40 erweitert (Entscheidung des Projektinhabers): `increment` und `decrement` erhöhen bzw. verringern um die Schrittweite des Counters ([`counters-and-quotes.md`](counters-and-quotes.md), B8) und lesen sie in derselben Transaktion wie die Änderung. Zwei Arten statt einer Quelle für den Betrag mit Schalter für die Richtung, damit kein Feld nur bei einer Wahl wirkt ([Code-ADR-0017](../adr/code/0017-klare-signale-statt-magischer-werte.md)). Eine neue Counter-Action erhöht um einen Schritt; `add` ohne Betrag addiert 1. Betrag und Wert sind ganze Zahlen von −9 007 199 254 740 991 bis 9 007 199 254 740 991, weil Ausdrücke mit `float64` rechnen und nur in diesem Bereich jede ganze Zahl genau treffen (B4); der Counter selbst fasst 64 Bit. B42 präzisiert (Entscheidung des Projektinhabers): Bei einem Überlauf bleibt der Wert unverändert, nicht am Grenzwert; [`counters-and-quotes.md`](counters-and-quotes.md), B43, ist angepasst. |
| 2026-10-01 | Special Identifier umgesetzt (`internal/action/values`, Textfunktionen in `internal/textfunc`, globale Werte als `template.Globals`). Festlegungen dabei: Die Felder heißen `kind`, `name`, `value` und `global`. Die Option „Rechnen“ (B50) ist die Art: `text` liest den Wert als Template mit Textfunktionen, `expression` als Ausdruck. So lehnt schon das Schema einen leeren Ausdruck ab, und Editoren zeigen das passende Feld ([Code-ADR-0017](../adr/code/0017-klare-signale-statt-magischer-werte.md)). Eine neue Action hat die Art `text` und setzt nur einen Wert des Durchlaufs. Beim Rechnen lässt ein Ergebnis, das keine Zahl ist, etwa ein Vergleich, die Action scheitern (B51); −0 wird als 0 geschrieben. Textfunktionen (B52, B53): Ein Funktionsname besteht aus ASCII-Buchstaben direkt vor `(` und beginnt nicht mitten in einem Wort oder einem Token. Parameter werden nicht gekürzt; ein Leerzeichen nach dem Komma gehört zum Parameter, die Beispiele der Doku schreiben keines. Klammern ohne Funktionsnamen bilden innerhalb eines Parameters Paare und sind samt den Kommas darin Text. Ein Parameter in Anführungszeichen beginnt direkt nach `(` oder `,` und endet direkt vor `,` oder `)`; Identifier darin werden eingesetzt wie in Text in Anführungszeichen eines Ausdrucks, ein `"` kann er nicht enthalten. Eine Funktion mit einer falschen Zahl von Parametern sowie Muster und Daten ohne Identifier, die ungültig sind, lehnt schon das Speichern ab (B55). `length` zählt Unicode-Codepunkte, `removespaces` entfernt jeden Leerraum, auch Tabulatoren, Zeilenumbrüche und geschützte Leerzeichen, `count` zählt Treffer, die sich nicht überlappen. Bei `datefrom` und `dateto` ergibt das heutige Datum in der Zeitzone des Profils „0 Days“. Grenzen: höchstens 100 verschachtelte Funktionen und ein Ergebnis je Funktion von höchstens 1 MiB wie die Antwort eines Web-Requests (B73), damit verschachteltes Ersetzen den Speicher nicht füllt. Mit `global` setzt die Action zuerst den globalen Wert, dann den Wert des Durchlaufs; scheitert der globale an der Grenze (B57), setzt sie keinen. Globale Werte sind die zweite Quelle der Template-Engine ([`template.md`](template.md), B10), ihre Namen gelten unabhängig von der Schreibweise. |
| 2026-10-01 | B53 und B55 geändert (Entscheidung des Projektinhabers): Nur die bekannten Funktionsnamen aus B52 bilden Funktionen. Ein anderer Name mit Klammern, etwa `Score(s)`, bleibt Text, statt die Action scheitern zu lassen; Funktionen darin wirken weiter, `Score(toupper(a))` ergibt `Score(A)`. So lässt sich solcher Text schreiben, ohne dass das Speichern ihn ablehnt. |
| 2026-10-01 | Chat-Action umgesetzt (`internal/action/chat`, Ports in `internal/connector`). Festlegungen dabei: Die Felder heißen `kind`, `message`, `asStreamer`, `reply` und `recipient`. Die Option „flüstern“ (B60) ist die Art: `message` sendet in den Chat und hat die Option `reply`, `whisper` flüstert und hat den Empfänger `recipient` (beim Anlegen `$username`); Felder der anderen Art lehnen Schema und Dekodieren ab, wie bei der Command-Action ([Code-ADR-0017](../adr/code/0017-klare-signale-statt-magischer-werte.md)). Die Nachricht ist Pflicht und nicht leer; eine neue Action hat keine. Eine Plattform gilt als verbunden, wenn ihr Streamer-Konto verbunden ist; ein Bot-Konto allein genügt nicht (B62). Die Action sendet an alle Plattformen zugleich und ist fertig, wenn alle die Nachricht angenommen haben oder gescheitert sind (B2, B66); die Meldung lautet etwa „not sent on twitch, kick: twitch: …; kick: …“. Der Absender gilt beim Flüstern wie bei Nachrichten (B61). Den Empfänger sucht die Action je Plattform erst unter den bekannten Nutzern, dann über die Plattform, wie bei der Moderation (B81); scheitert die Suche aus einem anderen Grund als einem unbekannten Nutzer, scheitert das Flüstern auf dieser Plattform (B66). Ist keine Plattform verbunden, gilt B62 auch beim Flüstern: nichts wird gesendet, kein Scheitern; sonst scheitert die Action, wenn auf keiner Plattform geflüstert wurde (B63). Eine Antwort (B64) braucht die ID der auslösenden Nachricht auf der Plattform; die Engine führt sie dafür als `engine.Params.MessageID`. Eine leere Nachricht (B65) steht im Log auf Debug-Stufe, übersprungene Plattformen beim Flüstern und ein Durchlauf ohne verbundene Plattform auf Info-Stufe. |
| 2026-10-01 | Plattformnachricht umgesetzt (`internal/action/chat`). Festlegungen dabei: Die Felder heißen `platform`, `message`, `asStreamer` und `reply`; es gibt keine Arten. Plattform und Nachricht sind Pflicht, eine neue Action hat keine. Der Name der Plattform folgt der Form der Plattformnamen (1 bis 32 Kleinbuchstaben und Ziffern); das Speichern nimmt auch Plattformen an, für die diese Version keinen Adapter hat, damit Commands einer neueren Version erhalten bleiben. Eine solche Plattform und eine, die das Profil nicht hat, gelten als nicht verbunden (B67). Absender, Antwort, leere Nachricht und Scheitern folgen der Chat-Action (B61, B64–B66); eine Antwort gibt es nur, wenn der Durchlauf auf der gewählten Plattform ausgelöst wurde. |
| 2026-10-01 | Moderation umgesetzt (`internal/action/moderation`). Festlegungen dabei: Die Felder heißen `kind`, `user` (bei den Arten mit Nutzer), `seconds` (nur bei `timeout`) und `reason` (nur bei `timeout` und `ban`); Felder anderer Arten lehnen Schema und Dekodieren ab. Eine neue Action hat die Art `timeout`. Nutzer und Dauer sind Pflicht und haben keine Voreinstellung. Die Dauer ist eine ganze Zahl von 1 bis 1 209 600 Sekunden (14 Tage, das Längste, das Twitch erlaubt); eine Plattform, die weniger erlaubt, lehnt ab (B86). Der Grund ist optional und, wenn vorhanden, nicht leer; ergibt er nach dem Rendern nur Leerraum, geht kein Grund an die Plattform. Nutzer, Grund und Dauer entstehen in einem Rendervorgang (B3). Hat der Durchlauf eine Plattform, muss sie verbunden sein, sonst scheitert die Action. Ohne Plattform des Durchlaufs (B82) wirken die Arten ohne Nutzer auf jeder verbundenen Plattform; ist keine verbunden, geschieht nichts, der Core loggt es, und es ist kein Scheitern. Die Arten mit Nutzer suchen ihn dann auf jeder verbundenen Plattform und wirken dort, wo sie ihn finden; finden sie ihn nirgends, scheitert die Action (B81). Scheitert die Wirkung auf einigen Plattformen, scheitert die Action mit ihnen in der Meldung; auf den anderen bleibt sie bestehen, wie bei der Chat-Action (B66). Strikes (B84) gehören dem Nutzer in streamcrew, nicht einem Konto: Ein Nutzer mit Konten auf mehreren Plattformen bekommt einen Strike nur einmal. Bekannte Nutzer bekommen Strikes auch, wenn die Plattform des Durchlaufs gerade nicht verbunden ist; über die Plattform gesucht wird nur, wenn sie verbunden ist. Ein nur über die Plattform gefundener Nutzer wird für einen Strike wie ein neu gesehener gespeichert, für die anderen Arten nicht. Den stummen Chat (B85) schaltet die Action über einen Port, den der Chat-Service umsetzt (Roadmap 5.1). |
| 2026-10-01 | Nutzersuche umgesetzt (`internal/action/users`). Festlegungen dabei: Die Felder heißen `platform` und `user`; beide sind Pflicht, eine neue Action hat keine. Plattformen ohne Adapter nimmt das Speichern an wie bei der Plattformnachricht. Die Suche (B91) prüft unter den bekannten Nutzern erst den Login-Namen, dann die Plattform-ID, über die Plattform ebenso; ein Name gewinnt also gegen die gleichlautende ID eines anderen Nutzers. Beide Anfragen an die Plattform zählen als eine Suche (B92). Die Grenze von einer Suche je Minute gilt für alle Nutzersuchen und alle Plattformen zusammen und zählt nur Suchen, die die Plattform erreichen: Treffer unter den bekannten Nutzern und Plattformen, die nicht verbunden sind oder die das Profil nicht hat, verbrauchen sie nicht. Ein Name, der nach dem Rendern leer ist, wird nicht gesucht; das ist ohne Treffer und steht im Log. Die Ergebniswerte setzt die Action auch ohne Treffer und ersetzt damit die einer früheren Suche (B93). Fehler der bekannten Nutzer, der Plattform (außer einem unbekannten Nutzer) und beim Speichern lassen die Action scheitern, ohne Ergebniswerte zu setzen. Die festen Ergebnisnamen (B5) stehen im Descriptor (`action.Descriptor.Results`); `action.Registry.Reserved` meldet sie dem Speichern. |
| 2026-10-02 | Externes Programm umgesetzt (`internal/action/host`). Festlegungen dabei: Die Optionen „warten“ und „über das System öffnen“ (B110) sind die Arten `start` (starten und fertig, B112), `run` (warten, B113–B115) und `open` (über das System öffnen, B116), weil Zeitlimit und „Ausgabe speichern“ nur beim Warten gelten und das Öffnen beides ausschließt ([Code-ADR-0017](../adr/code/0017-klare-signale-statt-magischer-werte.md)); Felder anderer Arten lehnen Schema und Dekodieren ab. Eine neue Action hat die Art `start`. Die Felder heißen `program`, `args` und `showWindow` (bei `start` und `run`), `timeout` und `saveOutput` (nur bei `run`, Zeitlimit beim Anlegen 30 s) und `path` (nur bei `open`). Argumente (B111): Leerraum trennt Wörter, doppelte Anführungszeichen fassen zusammen und gehören nicht zum Wort, `""` ist ein leeres Wort; es gibt kein Escape, ein Wort kann also kein `"` enthalten; ein nicht geschlossenes Anführungszeichen lehnt das Speichern ab. Programm, alle Wörter und das Zeitlimit entstehen in einem Rendervorgang (B3). Ein Name ohne Pfad wird im Suchpfad gesucht; das Programm läuft mit absolutem Pfad im Verzeichnis, in dem es liegt (B117). Bei `start` läuft das Programm in einer eigenen Sitzung (unter Windows in einer eigenen Prozessgruppe), damit ein Signal an den Core es nicht erreicht. Bei `run` laufen Programm und alles, was es startet, unter Unix in einer eigenen Prozessgruppe; nach dem Zeitlimit oder beim Abbrechen der Instanz endet die ganze Gruppe, unter Windows nur das Programm. Die gespeicherte Ausgabe (B114) wird auch gesetzt, wenn das Programm scheitert; ungültiges UTF-8 wird zu U+FFFD. Das Zeitlimit der Action ist das des Programms plus 5 s (B8). „Fenster anzeigen“ wirkt nur unter Windows; unter macOS und Linux zeigen Programme mit Fenster es ohnehin. Bei `open` öffnet `open` (macOS), `xdg-open` (übrige Unix-Systeme) oder `rundll32 url.dll,FileProtocolHandler` (Windows) den Pfad als ein Argument, ohne Shell. Ein Programm oder Pfad, der nach dem Rendern leer ist, lässt die Action scheitern. Die Umgebung kommt aus `config.ProgramEnv` ([Code-ADR-0019](../adr/code/0019-host-rechte-in-der-startkonfiguration.md), Punkt 6). |
