# Spezifikation: Command-Engine

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-09-30 |
| **Bezug** | Roadmap Phase 3.2; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md), [Code-ADR-0004](../adr/code/0004-nebenlaeufigkeit-und-supervisor.md), [Code-ADR-0011](../adr/code/0011-event-bus.md), [Code-ADR-0012](../adr/code/0012-template-engine.md); Plan §5, §6.8, §6.9; [`commands.md`](commands.md), [`template.md`](template.md), [`events.md`](events.md), [`users-and-roles.md`](users-and-roles.md) |
| **Umsetzung** | Settings-Sektion `commands` in `internal/settings` (B90); Fehlerpolitik im Datenmodell der Commands (B71, `internal/domain/command`, Migration 0005). `internal/engine`: Instanzen und Zustände (B1–B4), Start von Hand und Grenze der Warteschlange (B14, B15), Sperren (B20–B29), Pause (B40, B42), Abbrechen, Wiederholen und Herunterfahren (B50, B51, B53–B55), Verlauf und Ereignisse (B60–B62), Ausführung mit Fehlerpolitik und Zeitlimit (B70–B72). Es folgen: Auslösen mit Anforderungen (B10–B13), Pause der Entrance-Commands (B41), Aufrufe (B30–B36, B52, B73), Zielnutzer und Runner-Parameter (B81, B82). |

## Zweck und Umfang

Beschreibt, wie der Core Commands ausführt: Instanzen und ihre Zustände, wann Anforderungen geprüft werden, die Warteschlange mit den Sperrmodi, Commands, die andere Commands starten, Pause, Abbrechen, Wiederholen, den Verlauf mit seinen Ereignissen, die Parameter eines Durchlaufs und die Schutzmechanismen. Dazu kommt die Settings-Sektion `commands`.

Nicht Teil dieser Spezifikation:

- das Verhalten der einzelnen Action-Typen, etwa Warten oder Wiederholen, und die Typ-Registry (Roadmap 3.3, Code-ADR-0013)
- die Prüfung der einzelnen Anforderungsarten und ihre Fehlermeldungen (Roadmap 3.4)
- die Erkennung von Triggern im Chat und die Zuordnung von Ereignissen zu Ereignis-Commands (Roadmap 3.6)
- wann Timer-Commands laufen (Roadmap 5.4)
- die Settings-Sektion `locale`; sie hängt an der Entscheidung zur Internationalisierung (ADR-0022, Roadmap 3.6)

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Command | die gespeicherte Definition nach [`commands.md`](commands.md) |
| Instanz | eine einzelne Ausführung eines Commands mit ihren Parametern und ihrem Zustand |
| Parameter | die Daten eines Durchlaufs: auslösender Nutzer, Plattform, Zielnutzer, Argumente, Nachricht, Werte des Durchlaufs |
| Auslösen | ein Command soll laufen, etwa weil ein Trigger im Chat erkannt wurde; daraus entsteht eine Instanz, wenn die Anforderungen erfüllt sind |
| Sperre | eine Warteschlange, in der Instanzen nacheinander laufen |
| Sperrmodus | globale Einstellung, welche Sperren eine Instanz braucht |
| Freigegeben (unlocked) | ein Command, der keine Sperre braucht ([`commands.md`](commands.md), B5) |
| Aufruf | eine Instanz startet einen anderen Command, etwa mit der Command-Action |
| Verlauf | die zuletzt eingereihten Instanzen mit ihrem Zustand |
| Wiederholen (Replay) | eine Instanz aus dem Verlauf mit denselben Parametern erneut starten |

## Verhalten

### Instanzen und Zustände

| ID | Regel | Quellen |
|---|---|---|
| B1 | Jede Ausführung eines Commands ist eine Instanz mit eigener ID (UUIDv7), dem Command, den Parametern (B80), der Quelle (`chat`, `event`, `timer`, `call`, `manual`, `replay`) und den Zeitpunkten von Einreihen, Start und Ende. | QP (§6.8), Q1 |
| B2 | Zustände: `pending` (eingereiht), `running`, dann genau einer der Endzustände `completed`, `failed` oder `canceled`. Ein Endzustand ändert sich nie. Aus `pending` führt `canceled` direkt zum Ende, ohne dass eine Action läuft. | QP (§6.8), Q1 |
| B3 | Eine Instanz führt die Fassung des Commands aus, die beim Einreihen galt. Ändern, Deaktivieren oder Löschen des Commands wirkt erst auf Instanzen, die danach eingereiht werden. | A1 |
| B4 | `completed` heißt: Alle Actions sind gelaufen oder die Instanz hat sich selbst beendet (Action „aktuellen Command beenden“). `failed` heißt: Eine Action ist gescheitert und die Fehlerpolitik bricht ab (B71). `canceled` heißt: Die Instanz wurde abgebrochen (B50). | Q2, QP (§6.8) |

### Anforderungen und Einreihen

| ID | Regel | Quellen |
|---|---|---|
| B10 | Wird ein Command ausgelöst, prüft die Engine zuerst die Anforderungen ([`commands.md`](commands.md), B40–B47). Erst wenn alle erfüllt sind, bucht sie Kosten ab (Währung, Inventar), startet die Cooldowns und reiht die Instanz ein. Der Cooldown läuft also ab dem Einreihen, nicht ab dem Start. | Q1 |
| B11 | Ist eine Anforderung nicht erfüllt, entsteht keine Instanz. Der Nutzer bekommt eine Fehlermeldung mit dem Grund; Text und Reihenfolge der Prüfungen legt Roadmap 3.4 fest. | Q1 |
| B12 | Fehlermeldungen haben einen eigenen Cooldown, damit der Chat nicht vollläuft. Die Settings-Sektion `commands` (B90) wählt die Art: `per_command` (je Anforderung und Command, Standard), `global` (eine gemeinsame Sperrzeit für alle Fehlermeldungen) oder `off` (jede Ablehnung meldet sich). Während der Sperrzeit wird die Ablehnung nur geloggt. | Q1, QP (§6.8) |
| B13 | Sind alle Anforderungen eines Commands erfüllt und wird er eingereiht, beginnen die Fehler-Cooldowns seiner Anforderungen von vorn. | Q1 |
| B14 | Ein inaktiver Command wird nie automatisch ausgelöst ([`commands.md`](commands.md), B3). Ein Start von Hand (Oberfläche, API) ist bei jedem Command möglich, auch einem inaktiven, und läuft ohne Prüfung der Anforderungen, ohne Kosten und ohne Cooldown. | Q1 („Play“ zum Testen), A2 |
| B15 | Es warten höchstens 1 000 Instanzen gleichzeitig. Ist die Warteschlange voll, wird ein ausgelöster Command verworfen, bevor seine Anforderungen geprüft werden, und der Core schreibt eine Warnung ins Log. | A3 |

### Sperren und Warteschlange

| ID | Regel | Quellen |
|---|---|---|
| B20 | Der Sperrmodus ist eine globale Einstellung (B90) mit fünf Werten; Standard ist `per_command_type`. | Q1, QP (§6.8) |
| B21 | `per_command_type`: je Command-Art (Chat, Ereignis, Timer, Action-Gruppe …) eine Sperre. Zwei Chat-Commands laufen nacheinander, ein Chat- und ein Ereignis-Command nebeneinander. | Q1 |
| B22 | `per_action_type`: je Action-Typ eine Sperre. Eine Instanz braucht die Sperren aller Action-Typen ihres Commands, auch der Actions in verschachtelten Actions wie Bedingung oder Wiederholung. | Q1 |
| B23 | `visual_audio`: Commands mit mindestens einer Bild- oder Ton-Action (etwa Sound, Overlay, Sprachausgabe) teilen eine Sperre; alle anderen laufen sofort. Welche Action-Typen dazugehören, steht im Descriptor ihres Typs (Code-ADR-0013). | Q1, QP (§6.9) |
| B24 | `singular`: eine Sperre für alle Commands. | Q1 |
| B25 | `none`: keine Sperren; jede Instanz startet sofort. | Q1 |
| B26 | Eine Instanz startet erst, wenn sie alle Sperren, die sie braucht, zugleich bekommt. Instanzen, die um eine Sperre konkurrieren, starten in der Reihenfolge ihres Einreihens; eine später eingereihte Instanz überholt keine frühere, die auf eine ihrer Sperren wartet. | Q1, A4 |
| B27 | Ein freigegebener Command braucht keine Sperre und startet sofort nach dem Einreihen, sofern die Engine nicht pausiert ist (B40). | Q1, A5 |
| B28 | Die Sperren einer Instanz stehen beim Einreihen fest. Ein Wechsel des Sperrmodus gilt für Instanzen, die danach eingereiht werden. | A1 |
| B29 | Ein Command ohne Actions braucht keine Sperre und endet sofort als `completed`. | B22 |

### Aufrufe anderer Commands

| ID | Regel | Quellen |
|---|---|---|
| B30 | Eine laufende Instanz kann einen anderen Command starten (Command-Action, Roadmap 3.3). Standardmäßig wartet sie auf sein Ende. | Q2 |
| B31 | Ein Aufruf mit Warten läuft als Teil der aufrufenden Instanz: Er braucht keine Sperren und ignoriert die Pause, damit sich Aufrufer und Aufgerufener nicht gegenseitig blockieren. Im Verlauf erscheint er als eigene Instanz mit Verweis auf die aufrufende. | Q2 |
| B32 | Ein Aufruf ohne Warten wird eingereiht wie ein ausgelöster Command (Quelle `call`): mit Sperren und Pause. | Q2 |
| B33 | Standardmäßig prüft ein Aufruf die Anforderungen des aufgerufenen Commands nicht. Verlangt die Action die Prüfung, gilt B10–B13 mit dem Nutzer der aufrufenden Instanz. | Q2 |
| B34 | Der aufgerufene Command läuft mit dem Nutzer, der Plattform und dem Zielnutzer der aufrufenden Instanz. Gibt die Action Argumente an, bekommt er diese, sonst die Argumente der aufrufenden Instanz. | Q2 |
| B35 | Ein Aufruf mit Warten teilt die Werte des Durchlaufs mit der aufrufenden Instanz ([`template.md`](template.md), B10): Was der aufgerufene Command setzt, sieht der Aufrufer danach. Ein Aufruf ohne Warten bekommt eine Kopie der Werte zum Zeitpunkt des Aufrufs. | A6 |
| B36 | Endet ein Aufruf mit Warten als `failed` oder `canceled`, scheitert die aufrufende Action; die Fehlerpolitik des Aufrufers entscheidet (B71). | QP (§6.8) |

### Pause

| ID | Regel | Quellen |
|---|---|---|
| B40 | Die Engine lässt sich pausieren und fortsetzen, über Oberfläche, API und Command-Action. Während der Pause startet keine eingereihte Instanz, auch keine freigegebene; laufende Instanzen laufen weiter. Ausgelöste Commands werden weiter geprüft und eingereiht und starten nach dem Fortsetzen in ihrer Reihenfolge. | Q2, QP (§6.8), A5 |
| B41 | Entrance-Commands ([`users-and-roles.md`](users-and-roles.md), B8) haben eine eigene Pause. Während sie gilt, werden Entrance-Commands nicht eingereiht; die erste Nachricht des Nutzers zählt trotzdem als seine erste in dieser Sitzung. | Q2, A7 |
| B42 | Beide Pausen gelten bis zum Fortsetzen oder bis der Core endet. Nach einem Start ist nichts pausiert. | A8 |

### Abbrechen und Wiederholen

| ID | Regel | Quellen |
|---|---|---|
| B50 | Eine eingereihte oder laufende Instanz lässt sich abbrechen, über Oberfläche, API und Command-Action. Eine eingereihte endet sofort als `canceled`. Bei einer laufenden wird ihr Kontext abgebrochen; die aktuelle Action endet an der nächsten Stelle, an der sie das prüft, weitere Actions laufen nicht. | Q1, Q2, QP (§6.8) |
| B51 | „Alle abbrechen“ bricht alle eingereihten und laufenden Instanzen ab. | Q2 |
| B52 | Mit der Instanz enden alle Aufrufe, auf die sie wartet (B31). | B31 |
| B53 | Abgebuchte Kosten werden beim Abbrechen nicht erstattet, gestartete Cooldowns laufen weiter. | A9 |
| B54 | Jede Instanz aus dem Verlauf lässt sich wiederholen, auch mehrere auf einmal. Die Wiederholung ist eine neue Instanz (Quelle `replay`) mit denselben Parametern und der aktuellen Fassung des Commands; Anforderungen, Kosten und Cooldowns entfallen wie bei B14. | Q1, A10 |
| B55 | Beim Beenden des Cores nimmt die Engine keine neuen Instanzen mehr an, außer denen der Ereignisse beim Beenden (`app.stopping`). Eingereihte Instanzen enden als `canceled`; laufende bekommen die Zeit, die der Supervisor zum Herunterfahren gewährt, und werden danach abgebrochen. | QP (§6.6), Code-ADR-0004 |

### Verlauf und Ereignisse

| ID | Regel | Quellen |
|---|---|---|
| B60 | Der Verlauf hält die letzten 200 Instanzen im Speicher, mit Command (ID und Name), Quelle, Nutzer, Plattform, Argumenten, Zustand, Zeitpunkten, aufrufender Instanz und den Fehlern der Actions (Position, Typ, Meldung). Er übersteht keinen Neustart. | Q1, QP (§6.8), A11 |
| B61 | Jeder Zustandswechsel wird als Ereignis auf dem Bus veröffentlicht: `command.instance.queued`, `command.instance.started`, `command.instance.completed`, `command.instance.failed`, `command.instance.canceled`; Pause und Fortsetzen als `command.queue.paused` und `command.queue.resumed`, jeweils mit dem Bereich (alle oder Entrance). | QP (§6.8), Code-ADR-0011 |
| B62 | Diese Ereignisse sind für Frontends und Logs gedacht; Ereignis-Commands können nicht auf sie reagieren, damit keine Schleifen entstehen. | A12 |

### Ausführung, Fehler und Schutz

| ID | Regel | Quellen |
|---|---|---|
| B70 | Die Actions laufen der Reihe nach ([`commands.md`](commands.md), B4). Unbekannte Action-Typen werden übersprungen und im Log gemeldet. | Code-ADR-0010 |
| B71 | Jeder Command hat eine Fehlerpolitik: `continue` (Standard) führt nach einer gescheiterten Action die nächste aus, und die Instanz endet als `completed` mit den Fehlern im Verlauf; `abort` beendet die Instanz als `failed`. | QP (§6.8), A13 |
| B72 | Jede Action hat ein Zeitlimit. Den Wert legt ihr Typ fest (Code-ADR-0013); Actions wie Warten richten es nach ihrer Dauer. Ohne Angabe gilt 60 s. Eine Action, die das Limit überschreitet, gilt als gescheitert. | QP (§6.8), A14 |
| B73 | Aufrufe sind höchstens 10 Ebenen tief. Startet eine Instanz einen Command, der in ihrer Kette von Aufrufen schon vorkommt (A startet B, B startet A), scheitert die aufrufende Action, statt eine Schleife zu bilden. Das gilt auch für Aufrufe ohne Warten. | QP (§6.8), A15 |
| B74 | Eine Action, die andere Actions wiederholt, tut das höchstens 1 000-mal; ein größerer Wert lässt sie scheitern (Roadmap 3.3). | QP (§6.8), A15 |

### Parameter eines Durchlaufs

| ID | Regel | Quellen |
|---|---|---|
| B80 | Die Parameter einer Instanz sind der auslösende Nutzer, die Plattform, der Zielnutzer, die Argumente mit dem Text nach dem Trigger, die auslösende Nachricht, die Werte des Durchlaufs (etwa Ereigniswerte, [`events.md`](events.md), B7) und der Name des Commands. Aus ihnen entsteht der Scope der Templates ([`template.md`](template.md)). | QP (§6.8), Q4 |
| B81 | Zielnutzer ist bei einem Ereignis mit Ziel der Zielnutzer des Ereignisses ([`events.md`](events.md), B6). Sonst ist es der Nutzer, den das erste Argument nennt (mit oder ohne `@`), wenn er auf der Plattform bekannt ist, und andernfalls der auslösende Nutzer ([`template.md`](template.md), B60). | Q4, QP (§6.8) |
| B82 | Läuft ein Command nach einer Schwelle für jeden beteiligten Nutzer einzeln ([`commands.md`](commands.md), B46), entsteht je Nutzer eine Instanz mit dessen Parametern (Runner-Parameter, P1). | QP (§6.8) |

### Settings-Sektion `commands`

| ID | Regel | Quellen |
|---|---|---|
| B90 | Die Sektion `commands` enthält den Sperrmodus (B20, Standard `per_command_type`), die Art des Fehler-Cooldowns (B12, Standard `per_command`), seine Dauer (Standard 10 s) und das Trennzeichen der getrennten Argumente (Standard `\|`, [`template.md`](template.md), `$argdelimited…`). Änderungen gelten ab dem nächsten Einreihen. | Q1, QP (§6.8), Code-ADR-0009 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B100 | Derselbe Command wird zweimal schnell ausgelöst, ohne Cooldown | zwei Instanzen; ob sie nebeneinander laufen, entscheidet der Sperrmodus | B20–B26 |
| B101 | Ein eingereihter Command wird gelöscht | Die Instanz läuft mit der Fassung beim Einreihen (B3). | B3 |
| B102 | Wiederholen einer Instanz, deren Command gelöscht ist | abgelehnt mit Fehler; keine Instanz | B54 |
| B103 | Abbrechen einer beendeten Instanz | keine Wirkung, kein Fehler | B2 |
| B104 | `per_action_type`: Command A braucht die Sperren „Chat“ und „Sound“, B braucht „Sound“, C braucht „Chat“; alle werden eingereiht, während ein anderer Command „Chat“ hält | A wartet auf „Chat“; B wartet hinter A, obwohl „Sound“ frei ist; C wartet hinter A (B26). | B26 |
| B105 | Ein Aufruf mit Warten ruft einen Command, dessen Art gerade gesperrt ist | läuft sofort (B31) | B31 |
| B106 | Die Engine ist pausiert, und ein laufender Command ruft einen anderen mit Warten auf | Der Aufruf läuft (B31); einer ohne Warten bleibt eingereiht bis zum Fortsetzen. | B31, B32, B40 |
| B107 | Ein Command ohne Actions | endet sofort als `completed` (B29) | B29 |
| B108 | Die Warteschlange ist voll, und ein Command mit Kosten wird ausgelöst | verworfen, ohne Kosten und Cooldown (B15) | B15 |
| B109 | Eine Instanz wird abgebrochen, während ihre Action auf eine Plattform-API wartet | Die Action bekommt den abgebrochenen Kontext und endet; die Instanz endet als `canceled`, auch wenn die API danach antwortet. | B50, Code-ADR-0004 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | nicht dokumentiert, welche Fassung eines geänderten Commands eine wartende Instanz ausführt | die Fassung beim Einreihen (B3, B28) | vorhersehbar; keine halb geänderten Abläufe |
| A2 | „Play“ zum Testen; nicht dokumentiert, ob Anforderungen gelten | Start von Hand ohne Anforderungen, Kosten und Cooldowns (B14) | Testen soll keine Währung kosten und keinen Cooldown auslösen; zu prüfen (offene Frage) |
| A3 | keine dokumentierte Grenze der Warteschlange | höchstens 1 000 wartende Instanzen (B15) | Schutz vor Überlast, etwa bei einer Flut von Ereignissen |
| A4 | Reihenfolge beim Warten auf mehrere Sperren nicht dokumentiert | Einreihungsreihenfolge, kein Überholen (B26) | kein Verhungern einzelner Instanzen, nachvollziehbare Reihenfolge |
| A5 | nicht dokumentiert, ob freigegebene Commands in der Pause laufen | Pause hält auch freigegebene Commands an (B27, B40) | Pause soll alles anhalten, was noch nicht läuft; zu prüfen |
| A6 | nicht dokumentiert, ob aufgerufene Commands Werte mit dem Aufrufer teilen | Teilen bei Warten, Kopie ohne Warten (B35) | Action-Gruppen sollen Werte liefern können; ohne Warten gäbe es Wettläufe |
| A7 | nicht dokumentiert, was mit Entrance-Commands in ihrer Pause geschieht | werden nicht eingereiht (B41) | Begrüßungen nach dem Fortsetzen kämen zu spät; zu prüfen |
| A8 | nicht dokumentiert, ob eine Pause einen Neustart übersteht | nein (B42) | eine vergessene Pause soll den Core nicht dauerhaft stummschalten |
| A9 | nicht dokumentiert, ob Kosten beim Abbrechen erstattet werden | keine Erstattung (B53) | Abbrechen ist eine Entscheidung des Streamers; Erstattung ließe sich ausnutzen; zu prüfen |
| A10 | Wiederholen dokumentiert, Anforderungen dabei nicht | ohne Anforderungen, Kosten und Cooldowns, mit der aktuellen Fassung (B54) | Wiederholen ist eine Handlung des Streamers wie der Start von Hand |
| A11 | Verlauf dokumentiert, Umfang und Speicherung nicht | 200 Instanzen im Speicher (B60) | genügt für den laufenden Stream; dauerhafte Auswertung liefert das Ereignisprotokoll (Plan §6.13) |
| A12 | keine Ereignisse für Command-Instanzen dokumentiert | Ereignisse für Frontends, nicht für Ereignis-Commands (B61, B62) | Frontends zeigen Warteschlange und Verlauf live; keine Schleifen |
| A13 | Fehlerverhalten einzelner Actions nicht dokumentiert | Fehlerpolitik je Command, Standard `continue` (B71) | Plan §6.8; `continue` entspricht dem üblichen Verhalten, einzelne Fehler nicht den ganzen Command stoppen zu lassen; zu prüfen |
| A14 | keine Zeitlimits je Action dokumentiert | Zeitlimit je Action, Standard 60 s (B72) | Eine hängende Action soll ihre Sperre nicht dauerhaft blockieren |
| A15 | keine Grenzen für Aufrufketten und Wiederholungen dokumentiert | 10 Ebenen, keine Zyklen, 1 000 Wiederholungen (B73, B74) | Schutz vor Endlosschleifen, auch durch Fehler beim Einrichten |

## Akzeptanzkriterien

- [x] B1–B4: Zustandsautomat mit allen erlaubten Übergängen; ein Endzustand ändert sich nicht; Tests für Ändern und Löschen während des Wartens.
- [ ] B10–B15: Anforderungen vor dem Einreihen, Cooldown ab dem Einreihen, Fehler-Cooldown in allen drei Arten und sein Zurücksetzen, Start von Hand ohne Anforderungen, volle Warteschlange; Zeit mit `testing/synctest`.
- [x] B20–B29, B104: je Sperrmodus ein Test mit mehreren Instanzen und der Reihenfolge ihrer Starts; kein Überholen; freigegebene Commands; Wechsel des Modus.
- [ ] B30–B36, B105, B106: Aufrufe mit und ohne Warten, Sperren, Pause, geteilte und kopierte Werte, Fehler des Aufgerufenen.
- [ ] B40–B42: Pause und Fortsetzen, eigene Pause für Entrance-Commands.
- [ ] B50–B55, B109: Abbrechen eingereihter und laufender Instanzen samt wartender Aufrufe, alle abbrechen, Wiederholen, Herunterfahren.
- [x] B60–B62: Verlauf als Ringpuffer; Ereignisse je Zustandswechsel in der richtigen Reihenfolge.
- [ ] B70–B74: Fehlerpolitik, Zeitlimit, Tiefe und Zyklen von Aufrufen.
- [ ] B80–B82: Parameter und Zielnutzer im Scope der Templates.
- [x] B90: Settings-Sektion mit Standardwerten, Prüfung und Migration nach Code-ADR-0010.
- [x] Nebenläufigkeit: alle Tests der Engine mit `-race`; ein Lasttest mit vielen gleichzeitig ausgelösten Commands in jedem Sperrmodus.

## Offene Fragen

- B12, B90: Welche Dauer hat der Fehler-Cooldown im Original als Standard? streamcrew nimmt 10 s an.
- B14/A2: Prüft der Start von Hand („Play“) im Original die Anforderungen?
- B27/A5: Laufen freigegebene Commands im Original auch während der Pause?
- B40: Werden im Original ausgelöste Commands während der Pause eingereiht oder verworfen?
- B41/A7: Was geschieht im Original mit Entrance-Commands während ihrer Pause?
- B53/A9: Erstattet das Original Kosten beim Abbrechen?
- B71/A13: Läuft ein Command im Original nach einer gescheiterten Action weiter?
- B22: Zählen im Original bei `per_action_type` auch die Actions in verschachtelten Actions?
- B4: Mit welchem Zustand endet eine Instanz im Original nach „aktuellen Command beenden“?

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q1 | Doku | <https://mixitup.bot/docs/commands> | Sperrmodi und ihre Wirkung, freigegebene Commands, Cooldown ab dem Einreihen, Sperren begrenzen laufende, nicht eingereihte Commands, Fehlermeldungen und ihr Cooldown je Anforderung und Command samt Zurücksetzen, Verlauf mit Abbrechen und Wiederholen, „Play“ zum Testen; abgerufen 2026-09-30 |
| Q2 | Doku | <https://mixitup.bot/docs/actions/command-action> | Command starten, standardmäßig mit Warten; beim Warten ohne Sperren gegen Verklemmungen; Anforderungen standardmäßig ignoriert; Argumente und Nutzer des Aufrufers; alle abbrechen; Pause aller Commands und der Entrance-Commands; aktuellen Command beenden; abgerufen 2026-09-30 |
| Q3 | Doku | <https://mixitup.bot/docs/users> | Entrance-Command bei der ersten Nachricht im Chat; abgerufen 2026-09-30 |
| Q4 | Doku | <https://mixitup.bot/docs/reference/special-identifiers> | Parameter eines Durchlaufs als Identifier, Zielnutzer; abgerufen 2026-09-29 |
| Q5 | Doku | <https://mixitup.bot/docs/actions/repeat-action> | Wiederholen mit Anzahl aus Identifiern; keine Grenze dokumentiert; abgerufen 2026-09-30 |
| QP | Projekt | [Plan](../plan.md) §5, §6.6, §6.8, §6.9, §6.13 | Command-Engine: Begriffe, Zustände, Sperrmodi, Steuerung, Schutzmechanismen, Fehler; Verlauf als P0 |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-30 | Erstfassung (Entwurf) aus der offiziellen Doku und dem Plan, ohne Code des Originals |
| 2026-09-30 | Vom Projektinhaber geprüft und akzeptiert. Die offenen Fragen bleiben bis zur Prüfung am Original offen; bis dahin gilt das hier beschriebene Verhalten. |
| 2026-09-30 | Settings-Sektion `commands` und Fehlerpolitik im Datenmodell umgesetzt. Festlegungen dabei: Die Sektion hat die Felder `lockMode`, `errorCooldown`, `errorCooldownDuration` (Go-Dauer, Code-ADR-0009) und `argDelimiter`. Eine Dauer von 0 hält keine Fehlermeldung zurück; negative Dauern sind ungültig. Das Trennzeichen darf nicht leer sein und keine Steuerzeichen enthalten, sonst ist es frei. Die Fehlerpolitik ist ein Pflichtfeld ohne leeren Wert (keine magischen Werte, Code-ADR-0017 vorgeschlagen); bestehende Commands bekommen mit Migration 0005 `continue`. |
| 2026-09-30 | Warteschlange und Ausführung umgesetzt (`internal/engine`). Festlegungen dabei: Die Engine nimmt Instanzen nur an, solange sie als Runnable läuft; davor und nach dem Herunterfahren lehnt sie ab. Die Einstellungen liest sie bei jedem Einreihen; sind sie nicht lesbar, wird nicht eingereiht. Ein leerer oder unbekannter Sperrmodus gilt als `per_command_type`. Bei `per_action_type` bekommt jeder Action-Typ eine Sperre, auch ein unbekannter; welche Typen zu `visual_audio` gehören, sagt eine Funktion, die die Typ-Registry (Code-ADR-0013) liefern wird. Ein Command ohne Actions wartet während der Pause wie ein freigegebener. Eine Action, die ihr Zeitlimit überschreitet, gilt als gescheitert, auch wenn sie danach ohne Fehler zurückkehrt; eine Panic in einer Action ist deren Fehler. Im Verlauf zählt die Position einer Action ab 1; der Nutzer steht dort mit ID und dem Anzeigenamen auf der Plattform des Durchlaufs, ohne Identität dort mit dem der ersten. Ereignisse zur Pause gibt es nur beim Wechsel; erneutes Pausieren oder Fortsetzen wirkt nicht. Beim Wiederholen mehrerer Instanzen läuft jede für sich; eine, die nicht wiederholt werden kann, hält die anderen nicht auf. Abbrechen einer Instanz, die weder wartet, läuft noch im Verlauf steht, ist ein Fehler. Laufende Instanzen bekommen beim Herunterfahren standardmäßig 10 s; die Composition Root leitet den Wert aus dem Shutdown-Timeout ab. |
