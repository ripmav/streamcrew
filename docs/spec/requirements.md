# Spezifikation: Anforderungen (Requirements)

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-10-01 |
| **Bezug** | Roadmap Phase 3.4; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md), [Code-ADR-0010](../adr/code/0010-polymorphe-serialisierung.md), [Code-ADR-0017](../adr/code/0017-klare-signale-statt-magischer-werte.md), ADR-0022 (geplant); Plan §5.4, §6.8, §6.9; [`commands.md`](commands.md), [`command-engine.md`](command-engine.md), [`users-and-roles.md`](users-and-roles.md), [`template.md`](template.md), [`actions.md`](actions.md) |
| **Umsetzung** | noch offen (Roadmap 3.4): Requirement-Service hinter dem Port `engine.Requirements`, zu dem seit der Command-Action auch `StartCooldown` gehört ([`actions.md`](actions.md), B37). Das Datenmodell der Anforderungen gibt es seit Phase 2.2 (`internal/domain/command`). |

## Zweck und Umfang

Beschreibt, wie der Requirement-Service entscheidet, ob ein ausgelöster Command läuft: die Reihenfolge der Prüfungen, jede Anforderungsart, Kosten und Cooldowns, die Meldungen und wann der Nutzer sie erfährt, Argumente als Werte des Durchlaufs, die Schwelle, die Einstellungen und die Prüfung beim Speichern. Dazu kommt, wie Währung, Rang und Inventar behandelt werden, bis Phase 8 sie liefert.

Nicht Teil dieser Spezifikation:

- das Datenmodell der Anforderungen ([`commands.md`](commands.md), B40–B47)
- wann die Engine prüft, der Fehler-Cooldown und was eine Entscheidung für die Warteschlange heißt ([`command-engine.md`](command-engine.md), B10–B15)
- wie Texte übersetzt werden (ADR-0022, Roadmap 3.4)
- Währungen, Ränge und Inventare selbst (`economy.md`, Phase 8)

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Anforderung | ein Eintrag nach [`commands.md`](commands.md), B40–B47 |
| Entscheidung | das Ergebnis der Prüfung: `met` (erfüllt), `waiting` (wartend) oder `rejected` (abgelehnt) |
| Ablehnung | die erste nicht erfüllte Anforderung, mit Art, Begründung und der Angabe, ob der Nutzer sie erfährt |
| Kosten | was bei Erfüllung abgebucht wird: Währung und Gegenstände |
| Cooldown | Sperrzeit, die beim Einreihen beginnt |
| Nutzergebundene Anforderung | eine Anforderung, die einen Nutzer braucht: Rolle, Cooldown je Nutzer, Währung, Rang, Inventar, Schwelle |
| Teilnahme | ein Auslösen, das bei einer Schwelle mitzählt |
| Fehlerhafter Command | ein Command, dessen Anforderung auf etwas verweist, das es nicht gibt ([`commands.md`](commands.md), B63) |

## Verhalten

### Ablauf

| ID | Regel | Quellen |
|---|---|---|
| B1 | Der Requirement-Service entscheidet für jedes Auslösen und jeden Aufruf mit Prüfung genau einmal: erfüllt mit mindestens einem Durchlauf, wartend oder abgelehnt ([`command-engine.md`](command-engine.md), B10, B11, B33). Kann er nicht entscheiden, etwa weil die Datenbank nicht antwortet, liefert er einen Fehler; dann entsteht keine Instanz. | Q1, QP (§6.8) |
| B2 | Die Reihenfolge der Prüfungen ist fest: (1) fehlerhafte und unbekannte Anforderungen (B7, B8), (2) Rolle, (3) Cooldown, (4) Argumente, (5) Rang, (6) Währung, (7) Inventar, (8) Schwelle. Die erste nicht erfüllte Anforderung bestimmt die Ablehnung; die folgenden werden nicht mehr geprüft. Die Einstellungen prüfen nichts (B60). | A1 |
| B3 | Kosten und Cooldowns gelten erst, wenn alle Anforderungen erfüllt sind ([`command-engine.md`](command-engine.md), B10). Prüfen und Anwenden sind atomar: Entscheidungen über denselben Command fallen nacheinander, und die Kosten eines Nutzers bucht eine Transaktion ab, die den Stand dabei erneut prüft. | Q1 |
| B4 | Nutzergebundene Anforderungen brauchen einen Nutzer. Hat der Durchlauf keinen, etwa bei einem Timer-Command, ist die erste von ihnen nicht erfüllt; eine Meldung gibt es nicht. Die Rolle `user`, die ohne Rollen-Anforderung gilt, zählt nicht dazu (B11). | A11 |
| B5 | Ein Aufruf mit Prüfung prüft mit dem Nutzer, der Plattform und den Argumenten, die der aufgerufene Command bekommt ([`command-engine.md`](command-engine.md), B33, B34). | Q1 |
| B6 | Start von Hand und Wiederholen prüfen nichts ([`command-engine.md`](command-engine.md), B14, B54). | Q1 |
| B7 | Ein fehlerhafter Command, etwa mit einer gelöschten Währung oder einem Gruppen-Cooldown ohne Gruppe, wird abgelehnt, ohne dass der Nutzer es erfährt, mit einer Warnung im Log; die API zeigt ihn als fehlerhaft, bis der Verweis repariert ist ([`commands.md`](commands.md), B63). | Q1 |
| B8 | Eine Anforderung unbekannten Typs, etwa aus einer neueren Version, ist nie erfüllt (Code-ADR-0010): Ablehnung ohne Meldung, Warnung im Log. | Code-ADR-0010 |

### Rolle

| ID | Regel | Quellen |
|---|---|---|
| B10 | Die Rolle ist erfüllt, wenn die Hauptrolle des Nutzers auf der Plattform des Durchlaufs mindestens die verlangte ist ([`users-and-roles.md`](users-and-roles.md), B22–B24). | Q1, Q2 |
| B11 | Ohne Rollen-Anforderung gilt `user` ([`commands.md`](commands.md), B40): Abgelehnt werden nur gebannte Nutzer ([`users-and-roles.md`](users-and-roles.md), B27); ein Durchlauf ohne Nutzer ist erfüllt. | Q1 |
| B12 | Die Meldung nennt die verlangte Rolle. Gebannte Nutzer bekommen keine Meldung. | A9 |

### Cooldown

| ID | Regel | Quellen |
|---|---|---|
| B20 | Arten ([`commands.md`](commands.md), B41): `standard` sperrt den Command für alle; `group` sperrt alle Commands der Gruppe, die selbst einen Gruppen-Cooldown haben; `per_user` sperrt den Command für den Nutzer; `per_user_group` sperrt für den Nutzer alle Commands der Gruppe mit dieser Art. | Q1 |
| B21 | Ein Cooldown beginnt beim Einreihen ([`command-engine.md`](command-engine.md), B10) und dauert so lange, wie die Anforderung des Commands sagt, der ihn gestartet hat. Bei den Gruppen-Arten gilt also die Dauer des Commands, der zuletzt durchkam. | Q1, A3 |
| B22 | Cooldowns je Nutzer gelten für den Nutzer, nicht für eine einzelne Plattform-Identität ([`users-and-roles.md`](users-and-roles.md)): Wer auf zwei Plattformen verknüpft ist, teilt seinen Cooldown. | QP (§6.8) |
| B23 | Laufende Cooldowns werden gespeichert und überstehen einen Neustart. Ändert der Streamer die Dauer, behalten laufende Cooldowns ihr Ende. | A4 |
| B24 | Die Meldung nennt die Restzeit, auf ganze Sekunden aufgerundet, ab einer Minute in Minuten und Sekunden; bei `per_user` und `per_user_group` spricht sie den Nutzer an, sonst gilt sie für alle. | A2 |
| B25 | Die Action `start_cooldown` startet einen Cooldown, ohne dass der Command eingereiht wird ([`actions.md`](actions.md), B37). | Q1 |

### Argumente

| ID | Regel | Quellen |
|---|---|---|
| B30 | Die Argumente des Durchlaufs, die Wörter nach dem Trigger ([`command-engine.md`](command-engine.md), B80), werden den definierten Argumenten der Reihe nach zugeordnet: das erste Wort dem ersten Argument und so weiter. | Q1 |
| B31 | Ist das letzte definierte Argument vom Typ `text`, bekommt es alle übrigen Wörter, mit je einem Leerzeichen dazwischen. Sonst bleiben überzählige Wörter unbeachtet, und die Anforderung ist trotzdem erfüllt. | A5 |
| B32 | Fehlt ein Pflichtargument, ist die Anforderung nicht erfüllt. Die Meldung zeigt die Verwendung: Trigger, Pflichtargumente in spitzen und optionale in eckigen Klammern, etwa `!hug <nutzer> [grund]`. | Q1, A5 |
| B33 | Typen: `text` nimmt jedes Wort. `number` nimmt eine Dezimalzahl wie in [`template.md`](template.md), B51, mit Punkt als Dezimaltrennzeichen. `user` nimmt einen Nutzer, den es auf der Plattform des Durchlaufs gibt, mit oder ohne `@` ([`command-engine.md`](command-engine.md), B81). Ein Wert, der nicht passt, lässt die Anforderung scheitern; die Meldung nennt Argument und erwarteten Typ, bei `user` den unbekannten Namen. | Q1, [`commands.md`](commands.md) B45 |
| B34 | Ein Pflichtargument nach einem optionalen ist beim Speichern ungültig, weil die Zuordnung nach Position sonst mehrdeutig wäre. | A5 |
| B35 | Hat ein Argument einen Identifier-Namen, ist sein Wert im Durchlauf als `$<name>` verfügbar, als Wert des Durchlaufs ([`template.md`](template.md), B10, Quelle 1): bei `text` und `number` wie eingegeben, bei `user` der Login-Name ohne `@`. Ein optionales Argument ohne Eingabe setzt keinen Wert ([`template.md`](template.md), B4). `$arg1text` und die übrigen Argument-Identifier gelten unverändert weiter. | Q1 |
| B36 | Die Namen folgen den Regeln für Ergebniswerte ([`actions.md`](actions.md), B5); das Speichern prüft sie. Beim Import entsteht der Name aus dem Namen des Arguments, klein geschrieben und ohne Leerzeichen, wie im Original **[Interop]**. | Q1, A6 |

### Währung, Rang und Inventar

| ID | Regel | Quellen |
|---|---|---|
| B40 | Bis Phase 8 gibt es keine Währungen, Ränge und Gegenstände. Eine solche Anforderung verweist deshalb auf etwas, das es nicht gibt; der Command ist fehlerhaft (B7) und läuft nicht, bis Phase 8 den Verweis auflöst. Importierte Commands mit Kosten laufen so nie kostenlos. | Q3, A7 |
| B41 | Ab Phase 8, Einzelheiten in `economy.md`: Die Währung verlangt einen festen Betrag (`required`), einen Mindestbetrag (`minimum`) oder einen Betrag in einem Bereich (`range`) und bucht ihn ab. Der Rang vergleicht den Rang des Nutzers mit dem verlangten (`at_least`, `exactly`, `at_most`). Das Inventar verlangt eine Menge eines Gegenstands und bucht sie ab. | Q1 |
| B42 | Reicht der Stand nicht, ist die Anforderung nicht erfüllt; die Meldung nennt Betrag und Währung, den Rang oder Menge und Gegenstand. | Q1 |
| B43 | Alle Kosten eines Durchlaufs bucht eine Transaktion ab (B3). Reicht der Stand beim Abbuchen nicht mehr, etwa nach einer gleichzeitigen Ausgabe, ist die Anforderung nicht erfüllt, und nichts wird abgebucht. Beim Abbrechen gibt es keine Erstattung ([`command-engine.md`](command-engine.md), B53). | Q1 |

### Schwelle (P1)

| ID | Regel | Quellen |
|---|---|---|
| B50 | Ein Auslösen, das alle übrigen Anforderungen erfüllt, ist eine Teilnahme. Jeder Nutzer zählt höchstens einmal; eine weitere Teilnahme desselben Nutzers ersetzt seine frühere, mit neuem Zeitpunkt und neuen Parametern. | Q1 |
| B51 | Eine Teilnahme verfällt nach dem Zeitfenster, gerechnet ab ihrem Zeitpunkt. Solange weniger Nutzer teilnehmen als verlangt, ist die Entscheidung `waiting`: Es läuft nichts, nichts wird abgebucht, kein Cooldown startet, und der Nutzer bekommt keine Meldung. | Q1, A8 |
| B52 | Erreicht eine Teilnahme die verlangte Zahl, ist die Schwelle erfüllt. Der Command läuft einmal mit den Parametern dieser Teilnahme oder, mit „für jeden Nutzer“, einmal je Teilnahme mit deren Parametern, in der Reihenfolge der Teilnahmen ([`command-engine.md`](command-engine.md), B82). Danach beginnt das Zählen von vorn. | Q1 |
| B53 | Beim Erfüllen bucht der Service die Kosten jedes Durchlaufs bei dessen Nutzer ab. Kann ein Teilnehmer nicht mehr zahlen, entfällt sein Durchlauf, mit Eintrag im Log; die anderen laufen. Der Cooldown startet einmal. | A8 |
| B54 | Teilnahmen liegen im Speicher; ein Neustart oder eine Änderung des Commands beginnt das Zählen von vorn. | A8 |

### Einstellungen

| ID | Regel | Quellen |
|---|---|---|
| B60 | Die Einstellungen ([`commands.md`](commands.md), B47) prüfen nichts und lehnen nie ab. | Q1 |
| B61 | „Auslösende Nachricht löschen“: Ist die Entscheidung erfüllt und hat der Durchlauf eine Chatnachricht, löscht der Service sie über den Chat-Port, sobald der Command eingereiht ist. Bei einer Ablehnung bleibt sie stehen. Kann die Plattform sie nicht löschen, schreibt der Core eine Warnung ins Log; der Command läuft trotzdem. | Q1 |
| B62 | „Im Kontextmenü anbieten“ ist ein Hinweis für Frontends und gilt nur für Chat-Commands; die Prüfung beachtet ihn nicht. | Q1 |

### Meldungen

| ID | Regel | Quellen |
|---|---|---|
| B70 | Jede Ablehnung hat eine Begründung in eigenen Worten, in der Sprache des Profils (ADR-0022), gebildet aus einem Schlüssel und Werten wie Rolle, Restzeit, Verwendung oder Betrag. Die Texte stammen nicht aus dem Original. | A9 |
| B71 | Der Nutzer erfährt eine Ablehnung (`Rejection.Tell`), wenn der Durchlauf eine auslösende Chatnachricht hat und die Anforderung Rolle, Cooldown, Argumente, Währung, Rang oder Inventar ist. Keine Meldung gibt es bei Durchläufen ohne Chatnachricht (Ereignisse, Timer), bei fehlerhaften und unbekannten Anforderungen und für gebannte Nutzer. Ob die Meldung dann gesendet wird, entscheidet der Fehler-Cooldown der Engine ([`command-engine.md`](command-engine.md), B12). | Q1, A10 |
| B72 | Die Meldung geht auf der Plattform des Durchlaufs in den Chat, als Antwort auf die auslösende Nachricht, wo die Plattform das kann, sonst mit `@` und dem Namen des Nutzers davor. Absender wie bei der Chat-Action ([`actions.md`](actions.md), B61). | QP (§6.11), A10 |

Die Meldungen je Art:

| Art | Inhalt der Meldung | Nutzer erfährt sie |
|---|---|---|
| Rolle | die verlangte Rolle | ja, außer gebannte Nutzer |
| Cooldown | die Restzeit, an den Nutzer oder an alle (B24) | ja |
| Argumente | die Verwendung, oder Argument und erwarteter Typ, bei `user` der unbekannte Name | ja |
| Währung, Rang, Inventar | Betrag und Währung, Rang oder Menge und Gegenstand (ab Phase 8) | ja |
| Schwelle | keine, sie wartet (B51) | – |
| fehlerhaft oder unbekannt | nur im Log | nein |

### Prüfung beim Speichern

| ID | Regel | Quellen |
|---|---|---|
| B80 | Beim Speichern prüft der Service die Anforderungen eines Commands als Menge. Abgelehnt wird: eine Art mehr als einmal ([`commands.md`](commands.md)), ein Gruppen-Cooldown bei einem Command ohne Gruppe, ein Pflichtargument nach einem optionalen (B34), ein Identifier-Name, der B36 verletzt, und „im Kontextmenü anbieten“ bei einem Command, der kein Chat-Command ist (B62). | Q1, QP (§6.9) |
| B81 | Eine Warnung, aber kein Verbot, gibt es für Verweise auf Währungen, Ränge oder Gegenstände, die es nicht gibt (B40), und für nutzergebundene Anforderungen an Timer-Commands, die deshalb nie laufen (B4). So bleiben Importe erhalten. | A7, A11 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B100 | Derselbe Nutzer löst einen Command mit `per_user`-Cooldown zweimal gleichzeitig aus | genau ein Durchlauf; das zweite Auslösen wird mit der Restzeit abgelehnt | B3, B20 |
| B101 | Ein Nutzer ohne die verlangte Rolle löst einen Command aus, dessen Cooldown läuft | Die Meldung betrifft die Rolle (B2). | B2 |
| B102 | Die Dauer wird von 60 auf 10 s geändert, während noch 50 s übrig sind | Der Cooldown endet nach den 50 s. | B23 |
| B103 | Die Gruppe eines Commands mit Gruppen-Cooldown wird gelöscht | Der Command ist fehlerhaft: keine Meldung, Warnung im Log. | B7, [`commands.md`](commands.md) B62 |
| B104 | Ein Timer-Command ruft mit Prüfung einen Command mit Rollen-Anforderung auf | abgelehnt ohne Meldung (B4); die Command-Action gelingt ([`actions.md`](actions.md), B33) | B4 |
| B105 | Ein Argument vom Typ `user` lautet `@Name` in anderer Schreibweise | Der Nutzer wird gefunden; der Wert ist sein Login-Name. | B33, B35 |
| B106 | Ein Argument vom Typ `number` lautet `1,5` | nicht erfüllt; die Meldung nennt den erwarteten Typ | B33 |
| B107 | Ein Nutzer löst während einer Schwelle zweimal aus | Er zählt einmal, mit der späteren Teilnahme. | B50 |
| B108 | Eine Schwelle mit Kosten wird erreicht, ein Teilnehmer hat nicht mehr genug | Sein Durchlauf entfällt, die anderen laufen. | B53 |
| B109 | Ein importierter Command mit Währungs-Anforderung vor Phase 8 | läuft nicht; fehlerhaft, keine Meldung | B40 |
| B110 | Ein gebannter Nutzer löst einen Command ohne Anforderungen aus | abgelehnt ohne Meldung | B11, B12 |
| B111 | `!quote add Das ist ein Zitat` mit den Argumenten `aktion` (Text) und `zitat` (Text) | `aktion` ist „add“, `zitat` ist „Das ist ein Zitat“. | B31 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | Reihenfolge der Prüfungen nicht dokumentiert | feste Reihenfolge (B2) | Wer die Rolle nicht hat, erfährt nichts über Cooldowns oder Argumente; ein gesperrter Command lädt nicht zum Korrigieren der Eingabe ein; Kosten nach den Argumenten, weil ein Betrag aus einem Argument kommen kann; die Schwelle zählt nur vollständige Auslösungen. Zu prüfen |
| A2 | Inhalt der Meldungen nicht dokumentiert | Restzeit aufgerundet (B24) | Die Restzeit hilft dem Nutzer mehr als ein bloßes „gesperrt“ |
| A3 | nicht dokumentiert, ob die Dauer eines Gruppen-Cooldowns an der Gruppe oder am Command hängt | Dauer des Commands, der ihn startet (B21) | folgt dem Datenmodell, in dem die Dauer an der Anforderung des Commands steht; zu prüfen |
| A4 | nicht dokumentiert, ob Cooldowns einen Neustart überstehen | gespeichert (B23) | Tägliche Commands mit langen Cooldowns sollen nach einem Neustart nicht wieder frei sein; zu prüfen |
| A5 | Zuordnung, überzählige Wörter und fehlende Argumente nicht dokumentiert | Zuordnung nach Position, Rest im letzten Text-Argument, Meldung mit Verwendung (B30–B34) | Befehle wie das Hinzufügen einer Quote brauchen den ganzen Text; die Verwendung zeigt, wie es richtig geht |
| A6 | ein Schalter für alle Argumente; der Identifier heißt wie das Argument, klein und ohne Leerzeichen | Identifier-Name je Argument ausdrücklich, der Import leitet ihn ab (B35, B36) | keine abgeleiteten Namen zur Laufzeit (Code-ADR-0017); der Name lässt sich beim Speichern prüfen |
| A7 | Währung, Rang und Inventar gibt es von Anfang an | bis Phase 8 fehlerhaft, der Command läuft nicht (B40, B81) | Kosten dürfen nicht still entfallen |
| A8 | Meldungen, Zurücksetzen und Kosten bei einer Schwelle nicht dokumentiert | keine Meldung, Verfall je Teilnahme, Neubeginn nach dem Erfüllen, Kosten je Durchlauf (B50–B54) | vorhersehbar; wartend ist keine Ablehnung ([`command-engine.md`](command-engine.md), B10); zu prüfen |
| A9 | Texte der Meldungen | eigene Texte, übersetzt (B12, B70) | keine Übernahme von Texten (ADR-0001); Mehrsprachigkeit (ADR-0022) |
| A10 | nicht dokumentiert, wer Meldungen bekommt und wie | nur bei einer auslösenden Chatnachricht, als Antwort (B71, B72) | Wer einem Kanal folgt oder raidet, soll keine Meldung über einen Cooldown bekommen |
| A11 | nicht dokumentiert, was ohne Nutzer geschieht | nutzergebundene Anforderungen sind dann nicht erfüllt, mit Warnung beim Speichern (B4, B81) | keine stillen Ausnahmen; der Streamer sieht beim Speichern, dass der Command so nie läuft |

## Akzeptanzkriterien

- [ ] B1–B8, B101: Reihenfolge der Prüfungen mit je einer nicht erfüllten Anforderung an jeder Stelle; Entscheidungen, die zu `engine.Decision` passen; Aufrufe mit Prüfung; fehlerhafte und unbekannte Anforderungen; Durchläufe ohne Nutzer.
- [ ] B3, B100, B43: Nebenläufigkeit mit `-race`: gleichzeitige Auslösungen desselben Nutzers und desselben Commands, gleichzeitige Ausgaben.
- [ ] B10–B12, B110: Rollen gegen die Rangordnung, gebannte Nutzer, Plattform des Durchlaufs.
- [ ] B20–B25, B102, B103: jede Cooldown-Art mit `testing/synctest`; verknüpfte Identitäten; Neustart; geänderte Dauer; gelöschte Gruppe.
- [ ] B30–B36, B105, B106, B111: Zuordnung, Rest im letzten Text-Argument, jeder Typ mit gültigen und ungültigen Eingaben, Werte als Identifier, Namen beim Import.
- [ ] B40, B109: Währung, Rang und Inventar vor Phase 8 fehlerhaft.
- [ ] B50–B54, B107, B108: Schwelle mit Zeitfenster, doppelter Teilnahme, je Nutzer, Kosten und Neubeginn.
- [ ] B60–B62: Löschen der auslösenden Nachricht über einen Fake des Chat-Ports; Kontextmenü ohne Wirkung auf die Prüfung.
- [ ] B70–B72: Meldung je Art in EN und DE; `Tell` nach B71; Antwort oder `@` je nach Plattform.
- [ ] B80, B81: Ablehnungen und Warnungen beim Speichern.

## Offene Fragen

- B2/A1: In welcher Reihenfolge prüft das Original die Anforderungen?
- B21/A3: Wo stellt das Original die Dauer eines Gruppen-Cooldowns ein? Gilt er auch für Commands der Gruppe ohne Gruppen-Cooldown ([`commands.md`](commands.md), offene Frage zu B41)?
- B23/A4: Übersteht ein Cooldown im Original einen Neustart?
- B24: Nennt das Original in der Meldung die Restzeit?
- B31: Bekommt im Original das letzte Argument den Rest des Texts?
- B33: Welche Argumenttypen gibt es im Original ([`commands.md`](commands.md), offene Frage zu B45)?
- B51/A8: Bekommen Nutzer im Original eine Meldung, solange die Schwelle nicht erreicht ist? Wann beginnt sie von vorn?
- B53: Wer zahlt im Original die Kosten eines Commands mit Schwelle?
- B61: Löscht das Original die Nachricht vor oder nach dem Lauf, und auch bei einer Ablehnung?
- B71/A10: Bekommen Nutzer im Original Meldungen zu Ereignis-Commands?

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q1 | Doku | <https://mixitup.bot/docs/commands> | Anforderungsarten; Rolle mit allen höheren Rollen; vier Cooldown-Arten; Argumente mit Name, Typ und optional, Zuweisung an Identifier mit dem Namen des Arguments; Schwelle mit Zeitfenster und Ausführung je Nutzer in der Reihenfolge der Beiträge; Einstellungen (Nachricht löschen, Kontextmenü nur für Chat-Commands); Kosten nur bei Erfolg; Fehlermeldungen mit eigenem Cooldown; abgerufen 2026-09-30 |
| Q2 | Doku | <https://mixitup.bot/docs/users> | Rollen und ihre Rangordnung; abgerufen 2026-09-30 |
| Q3 | Doku | <https://mixitup.bot/docs/consumables/currency> | Währungen; keine Angaben zur Prüfung als Anforderung; abgerufen 2026-09-30 |
| QP | Projekt | [Plan](../plan.md) §5.4, §6.8, §6.9, §6.11 | Prioritäten der Anforderungen, Engine, Requirements nach dem Muster der Actions, Chat-Port |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-30 | Erstfassung (Entwurf) aus der offiziellen Doku und dem Plan, ohne Code des Originals |
| 2026-10-01 | Vom Projektinhaber geprüft und akzeptiert. Die offenen Fragen bleiben bis zur Prüfung am Original offen; bis dahin gilt das hier beschriebene Verhalten. |
