# Spezifikation: Command-Engine

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-10-02 |
| **Bezug** | Roadmap Phase 3.2; [ADR-0001](../adr/0001-neuimplementierung-und-nutzung-des-originals.md), [Code-ADR-0004](../adr/code/0004-nebenlaeufigkeit-und-supervisor.md), [Code-ADR-0011](../adr/code/0011-event-bus.md), [Code-ADR-0012](../adr/code/0012-template-engine.md); Plan §5, §6.8, §6.9; [`commands.md`](commands.md), [`template.md`](template.md), [`events.md`](events.md), [`users-and-roles.md`](users-and-roles.md) |
| **Umsetzung** | Settings-Sektion `commands` in `internal/settings` (B90); Fehlerpolitik im Datenmodell der Commands (B71, `internal/domain/command`, Migration 0005). `internal/engine`: Instanzen und Zustände (B1–B4), Auslösen mit Anforderungen, Fehler-Cooldown und Grenze der Warteschlange (B10–B15), Sperren (B20–B29), Pause und Begrüßungen (B40–B43, mit `engine.Run.PlaybackEnds` und `engine.Engine.CancelEntrance`), Abbrechen, Wiederholen und Herunterfahren (B50, B51, B53–B55), Verlauf und Ereignisse (B60–B62), Ausführung mit Fehlerpolitik und Zeitlimit (B70–B72), dazu Schalter „aktiv“, Kind-Actions über `engine.Run.PerformChild` und Capabilities nach Code-ADR-0013, Parameter, Zielnutzer und Runner-Parameter (B80–B82). Rücknahme der Anforderungen eines Commands, der nicht eingereiht wird, über `engine.Decision.Revert` (B15). Aufrufe anderer Commands (B30–B36, B52, B73) über `engine.Run.Call`; Abbrechen aller Instanzen, Pause und Fortsetzen (B40–B42, B51) sowie das Starten von Cooldowns aus Actions über `engine.Run.CancelAll`, `Pause`, `Resume` und `StartCooldown`. Die einzelnen Anforderungen prüft der Requirement-Service (Roadmap 3.4) hinter dem Port `engine.Requirements`; die Grenze für Wiederholungen (B74) setzen die Actions `repeat`, `random` und `conditional` um (`internal/action/flow`). |

## Zweck und Umfang

Beschreibt, wie der Core Commands ausführt: Instanzen und ihre Zustände, wann Anforderungen geprüft werden, die Warteschlange mit den Sperrmodi, Commands, die andere Commands starten, Pause, Abbrechen, Wiederholen, den Verlauf mit seinen Ereignissen, die Parameter eines Durchlaufs und die Schutzmechanismen. Dazu kommt die Settings-Sektion `commands`.

Nicht Teil dieser Spezifikation:

- das Verhalten der einzelnen Action-Typen, etwa Warten oder Wiederholen, und die Typ-Registry (Roadmap 3.3, Code-ADR-0013)
- die Prüfung der einzelnen Anforderungsarten und ihre Fehlermeldungen (Roadmap 3.4)
- die Erkennung von Triggern im Chat und die Zuordnung von Ereignissen zu Ereignis-Commands (Roadmap 3.6)
- wann Timer-Commands laufen (Roadmap 5.4)
- die Settings-Sektion `locale` nach [ADR-0022](../adr/0022-internationalisierung.md) (Sprache in Roadmap 3.4, Formate in 3.6)

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
| Begrüßung | der Entrance-Command eines Nutzers oder ein Ereignis-Command auf `chat.user.entrance` (B41) |

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
| B12 | Fehlermeldungen haben einen eigenen Cooldown, damit der Chat nicht vollläuft. Die Settings-Sektion `commands` (B90) wählt die Art: `per_command` (je Anforderung und Command, Standard), `global` (eine gemeinsame Sperrzeit für alle Fehlermeldungen), `off` (jede Ablehnung meldet sich) oder `silent` (keine Fehlermeldungen). Während der Sperrzeit und bei `silent` wird die Ablehnung nur geloggt. | Q1, QP (§6.8), A16 |
| B13 | Sind alle Anforderungen eines Commands erfüllt und wird er eingereiht, beginnen die Fehler-Cooldowns seiner Anforderungen von vorn. | Q1 |
| B14 | Ein inaktiver Command wird nie automatisch ausgelöst ([`commands.md`](commands.md), B3). Ein Start von Hand (Oberfläche, API) ist bei jedem Command möglich, auch einem inaktiven, und läuft ohne Prüfung der Anforderungen, ohne Kosten und ohne Cooldown. | Q1 („Play“ zum Testen), A2 |
| B15 | Es warten höchstens so viele Instanzen gleichzeitig, wie die Größe der Warteschlange in den Settings sagt (B90, Standard 1 000, von 1 bis 10 000). Eine kleinere Größe, als gerade Instanzen warten, nimmt keine neuen an, bis genug gestartet sind. Ist die Warteschlange voll, wird ein ausgelöster Command verworfen, bevor seine Anforderungen geprüft werden, und der Core schreibt eine Warnung ins Log. Wird ein Command nach erfüllten Anforderungen doch nicht eingereiht, etwa weil der Core inzwischen herunterfährt oder bei einem Aufruf ohne Warten die Warteschlange voll ist, nimmt der Requirement-Service Kosten und Cooldowns zurück: Ein Command, der es nicht in die Warteschlange schafft, hat keinen Cooldown. | A3 |

### Sperren und Warteschlange

| ID | Regel | Quellen |
|---|---|---|
| B20 | Der Sperrmodus ist eine globale Einstellung (B90) mit fünf Werten; Standard ist `per_command_type`. | Q1, QP (§6.8) |
| B21 | `per_command_type`: je Command-Art (Chat, Ereignis, Timer, Action-Gruppe …) eine Sperre. Zwei Chat-Commands laufen nacheinander, ein Chat- und ein Ereignis-Command nebeneinander. | Q1 |
| B22 | `per_action_type`: je Action-Typ eine Sperre. Eine Instanz braucht die Sperren aller Action-Typen ihres Commands, auch der Actions in verschachtelten Actions wie Bedingung oder Wiederholung und der Commands, die sie mit Warten aufruft (B31), samt deren Aufrufen mit Warten; ein Zyklus zählt jeden Command einmal. Aufrufe ohne Warten zählen nicht. | Q1, Q9 |
| B23 | `visual_audio`: Commands mit mindestens einer Bild- oder Ton-Action (etwa Sound, Overlay, Sprachausgabe) teilen eine Sperre, auch wenn die Action in einem Command steckt, den sie mit Warten aufrufen (B22); alle anderen laufen sofort. Welche Action-Typen dazugehören, steht im Descriptor ihres Typs (Code-ADR-0013). | Q1, QP (§6.9) |
| B24 | `singular`: eine Sperre für alle Commands. | Q1 |
| B25 | `none`: keine Sperren; jede Instanz startet sofort. | Q1 |
| B26 | Eine Instanz startet erst, wenn sie alle Sperren, die sie braucht, zugleich bekommt. Instanzen, die um eine Sperre konkurrieren, starten in der Reihenfolge ihres Einreihens; eine später eingereihte Instanz überholt keine frühere, die auf eine ihrer Sperren wartet. | Q1, A4 |
| B27 | Ein freigegebener Command braucht keine Sperre und startet sofort nach dem Einreihen, sofern die Engine nicht pausiert ist (B40). | Q1, Q9 |
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

### Pause und Begrüßungen

| ID | Regel | Quellen |
|---|---|---|
| B40 | Die Engine lässt sich pausieren und fortsetzen, über Oberfläche, API und Command-Action. Während der Pause startet keine eingereihte Instanz, auch keine freigegebene; laufende Instanzen laufen weiter. Ausgelöste Commands werden weiter geprüft und eingereiht und starten nach dem Fortsetzen in ihrer Reihenfolge. | Q2, QP (§6.8), Q9 |
| B41 | Begrüßungen sind der Entrance-Command eines Nutzers ([`users-and-roles.md`](users-and-roles.md), B8) und die Ereignis-Commands auf `chat.user.entrance` ([`events.md`](events.md)). Sie lösen bei der ersten Nachricht des Nutzers in der Sitzung aus, die er schreibt, während der Stream auf mindestens einer Plattform live ist. Begrüßungen haben eine eigene Pause: Während sie gilt, werden Begrüßungen weiter geprüft und eingereiht, starten aber erst nach dem Fortsetzen, in ihrer Reihenfolge; andere Instanzen warten nicht auf sie. Die Nachricht zählt auch während der Pause als die erste. Geht der Stream offline, auch nur für eine kurze Unterbrechung ([`events.md`](events.md), B8), bricht der Core alle Begrüßungen ab, die noch nicht beendet sind (B50), auch eingereihte und solche, die auf ihren Abstand warten (B43). | Q2, Q6, A7 |
| B42 | Beide Pausen gelten bis zum Fortsetzen oder bis der Core endet. Nach einem Start ist nichts pausiert. | A8 |
| B43 | Mindestabstand bei Begrüßungen: In einer Begrüßung wartet jede Action, die Bild oder Ton ausgibt (B23), bis die Wiedergabe der vorigen solchen Action aus einer Begrüßung beendet und danach der Abstand vergangen ist; Standard 5 s, einstellbar von 1 s bis 60 s (B90). Das gilt auch zwischen zwei solchen Actions derselben Begrüßung. Die Actions kommen in der Reihenfolge an die Reihe, in der sie zu warten begannen. Das Ende der Wiedergabe meldet die Action; meldet sie keines, gilt das Ende der Action. Die Wartezeit zählt nicht zum Zeitlimit der Action (B72). Andere Actions, etwa Chat-Nachrichten, warten nicht. Eine wartende Begrüßung behält ihre Sperren (B20), sodass je nach Sperrmodus andere Instanzen auf sie warten. Commands, die eine Begrüßung mit Warten aufruft (B31), gehören zu ihr; Aufrufe ohne Warten, Starts von Hand und Wiederholungen sind keine Begrüßungen. | A7 |

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
| B90 | Die Sektion `commands` enthält den Sperrmodus (B20, Standard `per_command_type`), die Art des Fehler-Cooldowns (B12, Standard `per_command`, auch `silent`), seine Dauer (Standard 10 s) das Trennzeichen der getrennten Argumente (Standard `\|`, [`template.md`](template.md), `$argdelimited…`) den Mindestabstand für Bild und Ton bei Begrüßungen (B43, Standard 5 s, von 1 s bis 60 s) und die Größe der Warteschlange (B15, Standard 1 000, von 1 bis 10 000). Änderungen gelten ab dem nächsten Einreihen. | Q1, QP (§6.8), Code-ADR-0009 |

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
| B110 | Begrüßungen und alle Commands sind pausiert; nur die Pause der Begrüßungen endet | Die Begrüßungen bleiben eingereiht, bis auch die Pause aller Commands endet. | B40, B41 |
| B111 | Eine Begrüßung schreibt in den Chat, zeigt dann ein Bild und spielt einen Sound | Die Nachricht geht sofort raus; Bild und Sound kommen nacheinander, mit dem Abstand dazwischen. | B43 |
| B112 | 20 Nutzer schreiben kurz nacheinander ihre erste Nachricht; jede Begrüßung spielt einen Sound von 8 s | Die Sounds folgen einander, je 8 s Wiedergabe und 5 s Abstand, ohne am Zeitlimit zu scheitern; wann die Nachrichten der Begrüßungen rausgehen, hängt vom Sperrmodus ab. | B20, B43, B72 |
| B113 | Der Stream endet, während Begrüßungen eingereiht sind oder auf ihren Abstand warten | Sie enden als `canceled`. | B41, B50 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | nicht dokumentiert, welche Fassung eines geänderten Commands eine wartende Instanz ausführt | die Fassung beim Einreihen (B3, B28) | vorhersehbar; keine halb geänderten Abläufe |
| A2 | „Play“ fragt nach Nutzer, Plattform und Argumenten und wendet Anforderungen, Kosten und Cooldowns nur mit einer Option an; ohne sie leert es danach alle Cooldowns des Commands, auch die anderer Nutzer (Q7) | Start von Hand ohne Anforderungen, Kosten und Cooldowns; laufende Cooldowns bleiben (B14) | Testen soll keine Währung kosten und die Cooldowns der Zuschauer nicht ändern; mit Prüfung lässt sich ein Command über einen Aufruf mit Prüfung starten (B33); Entscheidung des Projektinhabers (2026-10-02) |
| A3 | keine dokumentierte Grenze der Warteschlange | höchstens so viele wartende Instanzen, wie eingestellt, Standard 1 000 (B15) | Schutz vor Überlast, etwa bei einer Flut von Ereignissen; einstellbar, weil der Bedarf je Kanal verschieden ist (Entscheidung des Projektinhabers, 2026-10-02) |
| A4 | Im Modus „je Action-Typ“ startet die erste wartende Instanz, deren Sperren alle frei sind; spätere überholen frühere (Q9) | Einreihungsreihenfolge, kein Überholen (B26) | kein Verhungern einzelner Instanzen, nachvollziehbare Reihenfolge; Entscheidung des Projektinhabers (2026-10-02) |
| A6 | nicht dokumentiert, ob aufgerufene Commands Werte mit dem Aufrufer teilen | Teilen bei Warten, Kopie ohne Warten (B35) | Action-Gruppen sollen Werte liefern können; ohne Warten gäbe es Wettläufe |
| A7 | Begrüßungen werden in ihrer Pause gesammelt und laufen nach dem Fortsetzen in Ankunftsreihenfolge; „nur wenn live“ ist eine Option; kein Abstand zwischen Medien (Q6) | ebenso eingereiht (B41); immer nur live, beim Stream-Ende abgebrochen (B41); Mindestabstand für Bild und Ton (B43) | Entscheidung des Projektinhabers (2026-10-02): Begrüßungen sollen laufen, solange der Stream live ist; eine Flut von Chat-Nachrichten ist hinnehmbar, eine Flut von Sounds und Videos nicht |
| A8 | nicht dokumentiert, ob eine Pause einen Neustart übersteht | nein (B42) | eine vergessene Pause soll den Core nicht dauerhaft stummschalten |
| A9 | nicht dokumentiert, ob Kosten beim Abbrechen erstattet werden | keine Erstattung (B53) | Abbrechen ist eine Entscheidung des Streamers; Erstattung ließe sich ausnutzen |
| A10 | Wiederholen dokumentiert, Anforderungen dabei nicht | ohne Anforderungen, Kosten und Cooldowns, mit der aktuellen Fassung (B54) | Wiederholen ist eine Handlung des Streamers wie der Start von Hand |
| A11 | Verlauf dokumentiert, Umfang und Speicherung nicht | 200 Instanzen im Speicher (B60) | genügt für den laufenden Stream; dauerhafte Auswertung liefert das Ereignisprotokoll (Plan §6.13) |
| A12 | keine Ereignisse für Command-Instanzen dokumentiert | Ereignisse für Frontends, nicht für Ereignis-Commands (B61, B62) | Frontends zeigen Warteschlange und Verlauf live; keine Schleifen |
| A13 | keine Fehlerpolitik: Die meisten Dienste fangen Fehler selbst ab, und der Command läuft weiter; entkommt eine Ausnahme, endet die aktuelle Action-Liste, und die Instanz bleibt im Verlauf „Running“ (Q9) | Fehlerpolitik je Command, Standard `continue` (B71) | Plan §6.8; `continue` entspricht dem üblichen Verhalten, einzelne Fehler nicht den ganzen Command stoppen zu lassen; jeder Fehler endet in einem klaren Zustand; Entscheidung des Projektinhabers (2026-10-02) |
| A14 | keine Zeitlimits je Action dokumentiert | Zeitlimit je Action, Standard 60 s (B72) | Eine hängende Action soll ihre Sperre nicht dauerhaft blockieren |
| A15 | keine Grenzen für Aufrufketten und Wiederholungen dokumentiert | 10 Ebenen, keine Zyklen, 1 000 Wiederholungen (B73, B74) | Schutz vor Endlosschleifen, auch durch Fehler beim Einrichten |
| A16 | Fehler-Cooldown je Anforderung jedes Commands (Standard, 10 s), je Command, je Anforderungsart über alle Commands, global oder aus; „aus“ heißt dort: keine Meldungen. Nach einem neu gesetzten Cooldown schweigt seine Meldung 5 s (Q8) | `per_command`, `global`, `off` (jede Ablehnung meldet sich) und `silent` (keine Meldungen) (B12) | Abschalten der Meldungen ist möglich, ohne `off` umzudeuten; die übrigen Arten des Originals braucht es nicht; Entscheidung des Projektinhabers (2026-10-02) |
| A17 | Abgelehnte Auslösungen erscheinen im Verlauf, mit Meldung als „Failed“, ohne als „Completed“; Commands ohne Actions und inaktive werden gar nicht eingereiht (Q9) | Eine Ablehnung erzeugt keine Instanz (B11); ein Command ohne Actions endet sofort als `completed` (B29) | Der Verlauf zeigt, was lief; Ablehnungen stehen im Log; Entscheidung des Projektinhabers (2026-10-02) |
| A18 | „Play“ in der Command-Liste startet keine inaktiven Commands; nur der Test im Editor läuft immer (Q9) | Der Start von Hand geht bei jedem Command, auch einem inaktiven (B14) | Ein Command lässt sich testen, bevor er aktiviert wird; Entscheidung des Projektinhabers (2026-10-02) |

## Akzeptanzkriterien

- [x] B1–B4: Zustandsautomat mit allen erlaubten Übergängen; ein Endzustand ändert sich nicht; Tests für Ändern und Löschen während des Wartens.
- [x] B10–B15: Anforderungen vor dem Einreihen, Cooldown ab dem Einreihen, Fehler-Cooldown in allen drei Arten und sein Zurücksetzen, Start von Hand ohne Anforderungen, volle Warteschlange; Zeit mit `testing/synctest`.
- [x] B20–B29, B104: je Sperrmodus ein Test mit mehreren Instanzen und der Reihenfolge ihrer Starts; kein Überholen; freigegebene Commands; Wechsel des Modus.
- [ ] B22, B23: Sperrmenge mit Aufrufen mit Warten, verschachtelt und mit Zyklus; Aufrufe ohne Warten zählen nicht.
- [x] B30–B36, B105, B106: Aufrufe mit und ohne Warten, Sperren, Pause, geteilte und kopierte Werte, Fehler des Aufgerufenen.
- [x] B40–B43, B110–B113: Pause und Fortsetzen; Pause der Begrüßungen mit Einreihen, ohne andere aufzuhalten, auch für Ereignis-Commands auf `chat.user.entrance`; Abbrechen beim Stream-Ende; Mindestabstand für Bild und Ton mit Ende der Wiedergabe, Reihenfolge, Sperren, Abbruch beim Warten, Aufrufen und Wartezeit außerhalb des Zeitlimits; Zeit mit `testing/synctest`.
- [x] B50–B55, B109: Abbrechen eingereihter und laufender Instanzen samt wartender Aufrufe, alle abbrechen, Wiederholen, Herunterfahren.
- [x] B60–B62: Verlauf als Ringpuffer; Ereignisse je Zustandswechsel in der richtigen Reihenfolge.
- [x] B70–B74: Fehlerpolitik, Zeitlimit, Tiefe und Zyklen von Aufrufen.
- [x] B80–B82: Parameter und Zielnutzer im Scope der Templates.
- [x] B90: Settings-Sektion mit Standardwerten, Prüfung und Migration nach Code-ADR-0010.
- [x] Nebenläufigkeit: alle Tests der Engine mit `-race`; ein Lasttest mit vielen gleichzeitig ausgelösten Commands in jedem Sperrmodus.

## Offene Fragen

Keine.

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q1 | Doku | <https://mixitup.bot/docs/commands> | Sperrmodi und ihre Wirkung, freigegebene Commands, Cooldown ab dem Einreihen, Sperren begrenzen laufende, nicht eingereihte Commands, Fehlermeldungen und ihr Cooldown je Anforderung und Command samt Zurücksetzen, Verlauf mit Abbrechen und Wiederholen, „Play“ zum Testen; abgerufen 2026-09-30 |
| Q2 | Doku | <https://mixitup.bot/docs/actions/command-action> | Command starten, standardmäßig mit Warten; beim Warten ohne Sperren gegen Verklemmungen; Anforderungen standardmäßig ignoriert; Argumente und Nutzer des Aufrufers; alle abbrechen; Pause aller Commands und der Entrance-Commands; aktuellen Command beenden; abgerufen 2026-09-30 |
| Q3 | Doku | <https://mixitup.bot/docs/users> | Entrance-Command bei der ersten Nachricht im Chat; abgerufen 2026-09-30 |
| Q4 | Doku | <https://mixitup.bot/docs/reference/special-identifiers> | Parameter eines Durchlaufs als Identifier, Zielnutzer; abgerufen 2026-09-29 |
| Q5 | Doku | <https://mixitup.bot/docs/actions/repeat-action> | Wiederholen mit Anzahl aus Identifiern; keine Grenze dokumentiert; abgerufen 2026-09-30 |
| Q6 | Original (Hilfestellung) | `MixItUp.Base/Services/CommandService.cs @ v1.8.200`, `MixItUp.Base/Services/ChatService.cs @ v1.8.200`, `MixItUp.Base/Services/EventService.cs @ v1.8.200`, `MixItUp.Base/Model/Actions/CommandActionModel.cs @ v1.8.200` | Beide Pausen sammeln ausgelöste Commands und führen sie nach dem Fortsetzen in Ankunftsreihenfolge aus; das gilt für den Entrance-Command des Nutzers und die allgemeine Begrüßung. Die erste Nachricht zählt sofort, auch in der Pause; mit der Option „nur wenn live“ zählt eine Nachricht offline nicht. Anforderungen gelten beim Auslösen. Gelesen 2026-10-02 von einem eigenen Recherche-Agenten, der nur das Verhalten in eigenen Worten weitergab |
| Q8 | Original (Hilfestellung) | `MixItUp.Base/Model/Requirements/RequirementModelBase.cs @ v1.8.200`, `MixItUp.Base/Model/Settings/SettingsV3Model.cs @ v1.8.200` | Arten und Standard des Fehler-Cooldowns (A16); gelesen 2026-10-02 von einem eigenen Recherche-Agenten, der nur das Verhalten in eigenen Worten weitergab |
| Q7 | Original (Hilfestellung) | `MixItUp.Base/ViewModel/Commands/CommandEditorWindowViewModelBase.cs @ v1.8.200`, `MixItUp.WPF/Controls/Dialogs/EditTestCommandParametersDialogControl.xaml.cs @ v1.8.200` | Start von Hand mit optionaler Prüfung, ohne sie werden danach die Cooldowns des Commands geleert (A2); gelesen 2026-10-02 von einem eigenen Recherche-Agenten, der nur das Verhalten in eigenen Worten weitergab |
| Q9 | Original (Hilfestellung) | `MixItUp.Base/Services/CommandService.cs @ v1.8.200`, `MixItUp.Base/Model/Commands/CommandModelBase.cs @ v1.8.200`, `MixItUp.Base/Model/Commands/CommandInstanceModel.cs @ v1.8.200`, `MixItUp.Base/Model/Actions/ActionModelBase.cs @ v1.8.200`, `MixItUp.Base/Model/Actions/CommandActionModel.cs @ v1.8.200` | Die Pause hält auch freigegebene Commands an (B27, B40); die Sperrmenge enthält verschachtelte Actions und Aufrufe mit Warten (B22); Überholen (A4), Fehler (A13), Verlauf (A17) und „Play“ (A18); gelesen 2026-10-02 von einem eigenen Recherche-Agenten, der nur das Verhalten in eigenen Worten weitergab |
| QP | Projekt | [Plan](../plan.md) §5, §6.6, §6.8, §6.9, §6.13 | Command-Engine: Begriffe, Zustände, Sperrmodi, Steuerung, Schutzmechanismen, Fehler; Verlauf als P0 |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-09-30 | Erstfassung (Entwurf) aus der offiziellen Doku und dem Plan, ohne Code des Originals |
| 2026-09-30 | Vom Projektinhaber geprüft und akzeptiert. Die offenen Fragen bleiben bis zur Prüfung am Original offen; bis dahin gilt das hier beschriebene Verhalten. |
| 2026-09-30 | Settings-Sektion `commands` und Fehlerpolitik im Datenmodell umgesetzt. Festlegungen dabei: Die Sektion hat die Felder `lockMode`, `errorCooldown`, `errorCooldownDuration` (Go-Dauer, Code-ADR-0009) und `argDelimiter`. Eine Dauer von 0 hält keine Fehlermeldung zurück; negative Dauern sind ungültig. Das Trennzeichen darf nicht leer sein und keine Steuerzeichen enthalten, sonst ist es frei. Die Fehlerpolitik ist ein Pflichtfeld ohne leeren Wert (keine magischen Werte, Code-ADR-0017 vorgeschlagen); bestehende Commands bekommen mit Migration 0005 `continue`. |
| 2026-09-30 | Warteschlange und Ausführung umgesetzt (`internal/engine`). Festlegungen dabei: Die Engine nimmt Instanzen nur an, solange sie als Runnable läuft; davor und nach dem Herunterfahren lehnt sie ab. Die Einstellungen liest sie bei jedem Einreihen; sind sie nicht lesbar oder ungültig, etwa ohne Sperrmodus oder Zeitzone, wird nicht eingereiht. Ebenso lehnt sie einen Command mit unbekannter Art oder Fehlerpolitik ab und Parameter, die sich widersprechen: Argumente ohne den Text, aus dem sie stammen, oder Emotes ohne Nachricht. Kein Wert fällt still auf einen Standard (Vorgabe des Projektinhabers: keine magischen Werte, Code-ADR-0017 vorgeschlagen). Bei `per_action_type` bekommt jeder Action-Typ eine Sperre, auch ein unbekannter; welche Typen zu `visual_audio` gehören, sagt eine Funktion, die die Typ-Registry (Code-ADR-0013) liefern wird. Ein Command ohne Actions wartet während der Pause wie ein freigegebener. Eine Action ohne eigenes Zeitlimit hat 60 s; ein eigenes muss positiv sein, sonst scheitert die Action, ohne zu laufen. Eine Action, die ihr Zeitlimit überschreitet, gilt als gescheitert, auch wenn sie danach ohne Fehler zurückkehrt; eine Panic in einer Action ist deren Fehler. Ohne Zielnutzer vom Aufrufer setzt die Engine den auslösenden Nutzer ausdrücklich als Ziel (B81). Im Verlauf zählt die Position einer Action ab 1; der Nutzer steht dort mit ID und dem Anzeigenamen auf der Plattform des Durchlaufs, ohne Identität dort mit dem der ersten. Ereignisse zur Pause gibt es nur beim Wechsel; erneutes Pausieren oder Fortsetzen wirkt nicht. Beim Wiederholen mehrerer Instanzen läuft jede für sich, mit einem Ergebnis je Instanz; eine, die nicht wiederholt werden kann, hält die anderen nicht auf. In den Ereignissen sind Listen nie `null`, sondern leer; Felder, die nicht gelten, fehlen. Abbrechen einer Instanz, die weder wartet, läuft noch im Verlauf steht, ist ein Fehler. Laufende Instanzen bekommen beim Herunterfahren standardmäßig 10 s; die Composition Root leitet den Wert aus dem Shutdown-Timeout ab. |
| 2026-09-30 | Auslösen umgesetzt. Festlegungen dabei: Automatisch auslösen lässt sich nur mit den Quellen `chat`, `event` und `timer`. Die Reihenfolge ist: aktiv (B14), Pause der Entrance-Commands (B41), ein Platz in der Warteschlange wird reserviert (B15), Zielnutzer (B81), Anforderungen (B10); so verwirft eine volle Warteschlange den Command, bevor er etwas kostet. Die Anforderungen prüft ein Port, den der Requirement-Service (Roadmap 3.4) umsetzt; bis dahin werden sie nicht geprüft. Seine Entscheidung ist ausdrücklich (Code-ADR-0017 vorgeschlagen): erfüllt mit mindestens einem Durchlauf, wartend (etwa unter einer Schwelle; keine Ablehnung, nichts wird abgebucht) oder abgelehnt mit Anforderung, Begründung und der Angabe, ob der Nutzer sie erfährt. Eine widersprüchliche Entscheidung ist ein Fehler. Das Auslösen liefert ein Ergebnis mit benanntem Ausgang: eingereiht, wartend, abgelehnt, inaktiv, Entrance-Pause; ein Fehler heißt nur, dass die Engine die Anfrage nicht bearbeiten konnte, etwa bei voller Warteschlange. Jede Ablehnung wird auf Debug-Stufe geloggt. Der Fehler-Cooldown beginnt beim Senden der Meldung; das Zurücksetzen nach dem Einreihen (B13) gilt bei `per_command` für alle Anforderungen des Commands, die gemeinsame Sperrzeit bei `global` bleibt. Der Zielnutzer wird auch beim Start von Hand bestimmt, nur mit bekannter Plattform; scheitert die Suche, bleibt es beim auslösenden Nutzer, und die Suche wird geloggt. Eine Wiederholung behält den Zielnutzer von damals. Passen nicht alle Instanzen einer Schwelle mit Runner-Parametern in die Warteschlange, werden die übrigen verworfen; das Ergebnis zählt sie, und abgebuchte Kosten bleiben abgebucht (B53). Während der Pause der Entrance-Commands reiht die Engine sie nicht ein und meldet das als Ausgang; ob die Nachricht als erste zählt, entscheidet der Chat-Service (B41). |
| 2026-09-30 | Aufrufe umgesetzt. Festlegungen dabei: Ein Aufruf lädt den Command in der aktuellen Fassung; einen inaktiven Command ruft er nicht auf, weil ein Aufruf wie ein automatisches Auslösen zählt (B14). Ob ein Aufruf wartet und ob er eigene Argumente mitgibt, sind ausdrückliche Angaben; Argumente ohne diese Angabe sind ein Fehler (keine magischen Werte, Code-ADR-0017 vorgeschlagen). Das Ergebnis ist ein benannter Ausgang: abgeschlossen, eingereiht, inaktiv, abgelehnt oder wartend; nur ein Aufruf mit Warten, der scheitert oder abgebrochen wird, lässt die aufrufende Action scheitern (B36). Der aufgerufene Command bekommt außer Nutzer, Plattform, Zielnutzer und Argumenten (B34) auch die auslösende Nachricht; eigene Argumente der Action ersetzen auch den Text nach dem Trigger, er ist dann die Argumente mit Leerzeichen dazwischen. Ein Aufruf mit Warten hat die Quelle `call`, seine Ereignisse `queued` und `started` folgen direkt aufeinander, er zählt nicht zur Grenze der Warteschlange und läuft auch beim Herunterfahren als Teil seines Aufrufers; einen Aufruf ohne Warten lehnt die Engine dann ab (B55). Er läuft innerhalb des Zeitlimits der aufrufenden Action, das die Command-Action (Roadmap 3.3) passend setzt. Der erste Command einer Kette hat die Tiefe 0; ein Aufruf in Tiefe 11 scheitert (B73). Die Kette geht auch an Aufrufe ohne Warten über; eine Wiederholung beginnt eine neue. Prüft ein Aufruf die Anforderungen, gelten Meldung und Fehler-Cooldown wie beim Auslösen (B11, B12); liefert eine Schwelle mehrere Nutzer, läuft je Nutzer ein Aufruf, bei Warten nacheinander. B74 (Grenze der Wiederholungen) setzt die Action „repeat“ um (Roadmap 3.3). |
| 2026-10-01 | Anschluss der Actions nach [Code-ADR-0013](../adr/code/0013-typ-registry.md), Punkt 8, umgesetzt; das Verhalten folgt [`actions.md`](actions.md), B1 und B7–B9. Festlegungen dabei: Jede ausführbare Action hat einen Schalter „aktiv“ (`engine.Performer.Enabled`); eine inaktive läuft nicht, samt ihrer Kind-Actions, und zählt bei `per_action_type` und `visual_audio` für keine Sperre (B22, B23). Unbekannte Typen behalten ihre eigene Sperre. Das Zeitlimit (B72) gilt für die eigene Zeit einer Action: Es steht, während sie eine Kind-Action ausführt oder auf einen aufgerufenen Command wartet, und läuft danach mit der verbleibenden Zeit weiter. Läuft es ab, bricht es den Kontext der Action mit `engine.ErrTimeLimit` als Ursache ab; eine Deadline hat der Kontext nicht. Eine Action setzt ihr Limit mit `engine.Run.LimitTo`, gemessen ab dem Aufruf; das bisherige statische `engine.TimeLimiter` entfällt. Kind-Actions laufen über `engine.Run.PerformChild` wie Actions auf oberster Ebene, mit Schalter, Capability-Prüfung, eigenem Zeitlimit und Fehlerpolitik; der Ausgang ist `next` oder `end`, und wie die Instanz endet, entscheidet die Engine, nicht die Rückgabe der umgebenden Action. Im Verlauf steht statt der Position (`position`) der Pfad (`path`), etwa `[3, 2]`; im Log steht er als `3.2`. Vor jeder Action fragt die Engine den Port `engine.ActionTypes`, welche Capabilities dem Typ fehlen; fehlt eine, scheitert die Action mit `engine.ErrCapability`, ohne zu laufen. Der Port ersetzt `engine.WithVisualAudio` und ist ein Pflichtparameter von `engine.New`, weil nur die Typ-Registry weiß, was ein Typ braucht. |
| 2026-10-01 | B74 umgesetzt: `repeat` und `random` lehnen eine Anzahl über 1 000 ab, bevor sie eine Kind-Action ausführen; die Grenze gilt je Action, verschachtelte Wiederholungen dürfen sich vervielfachen ([`actions.md`](actions.md), B15, B202). |
| 2026-10-01 | Die auslösende Nachricht unter den Parametern (B80) hat ihre ID auf der Plattform (`engine.Params.MessageID`), für Antworten der Chat-Action ([`actions.md`](actions.md), B64) und das Löschen der Auslösenachricht ([`requirements.md`](requirements.md)). Eine ID ohne Nachricht oder ohne Plattform lehnt die Engine ab (`engine.ErrInvalidParams`); aufgerufene Commands bekommen sie mit der Nachricht (B34). |
| 2026-10-02 | B41 geändert, B43 und die Randfälle B110–B113 neu (Entscheidung des Projektinhabers): Begrüßungen, also der Entrance-Command eines Nutzers und die Ereignis-Commands auf `chat.user.entrance`, werden in ihrer Pause eingereiht statt verworfen und laufen nach dem Fortsetzen, wie im Original (Q6). Sie lösen nur aus, solange der Stream live ist, und werden beim Stream-Ende abgebrochen. Zwischen Bild- und Ton-Actions von Begrüßungen liegt ein Mindestabstand ab dem Ende der Wiedergabe, Standard 5 s, einstellbar von 1 s bis 60 s in der Settings-Sektion `commands` (B90). Die offenen Fragen zu B40 und B41 sind am Original geklärt: Es sammelt in beiden Pausen. Die Engine meldet keinen eigenen Ausgang „Begrüßungen pausiert“ mehr; Actions melden das Ende ihrer Wiedergabe mit `engine.Run.PlaybackEnds`, und `engine.Engine.CancelEntrance` bricht die Begrüßungen beim Stream-Ende ab. Welche Nachricht als erste zählt und wann der Stream endet, erkennt der Event-Service (Roadmap 3.6). |
| 2026-10-02 | Offene Fragen zu B14 und B53 geklärt (Entscheidungen des Projektinhabers): Der Start von Hand bleibt ohne Anforderungen und lässt laufende Cooldowns stehen, abweichend vom Original (A2, Q7); beim Abbrechen gibt es weiter keine Erstattung, wie im Original. Die offene Frage zum Fehler-Cooldown (B12) klärt der PR mit der neuen Art „keine Meldungen“. |
| 2026-10-02 | B12 und B90: neue Art `silent` des Fehler-Cooldowns, die keine Fehlermeldungen sendet und Ablehnungen nur loggt (Entscheidung des Projektinhabers; A16, Q8). Die offene Frage zur Standarddauer ist geklärt: 10 s wie im Original. |
| 2026-10-02 | Eine Ablehnung trägt ihren Grund als Meldung mit Schlüssel und Werten (`engine.Rejection.Reason` ist eine `i18n.Message`), die der Requirement-Service erst beim Melden in der Sprache des Profils rendert ([ADR-0022](../adr/0022-internationalisierung.md), Punkt 5). |
| 2026-10-02 | B15 ergänzt (Entscheidung des Projektinhabers): Wird ein Command nach erfüllten Anforderungen nicht eingereiht, nimmt der Requirement-Service Kosten und Cooldowns über `engine.Decision.Revert` zurück, auch wenn die Anfrage inzwischen abgebrochen ist. Das betrifft Auslösungen, während der Core herunterfährt, und Aufrufe ohne Warten bei voller Warteschlange; ein Aufruf mit Warten, der lief und scheiterte, behält sie. Wurde von den Durchläufen einer Schwelle mindestens einer eingereiht, bleiben Kosten und Cooldowns (B82). Eine widersprüchliche Entscheidung wird ebenfalls zurückgenommen. |
| 2026-10-02 | B15 und B90: Die Größe der Warteschlange ist einstellbar, Standard 1 000, von 1 bis 10 000 (Entscheidung des Projektinhabers; die Grenzen nach oben, weil jede wartende Instanz eine Goroutine hält). Die Settings-Sektion `commands` hat dafür die Version 3 mit `queueSize`; ältere Fassungen bekommen den Standard. Eine kleinere Größe nimmt nur keine neuen Instanzen an; wartende bleiben. Die Engine liest die Settings beim Auslösen jetzt vor der Reservierung des Platzes. |
| 2026-10-02 | Offene Fragen am Original geklärt (Q9) und entschieden (Entscheidungen des Projektinhabers). Geändert: Die Sperrmenge von `per_action_type` enthält auch die Actions der Commands, die mit Warten aufgerufen werden (B22); dasselbe gilt für Bild und Ton bei `visual_audio` (B23). Übereinstimmend: Die Pause hält auch freigegebene Commands an (B27, B40, die Zeile A5 entfällt); „aktuellen Command beenden“ endet als `completed` (B4). Geblieben, mit dem Verhalten des Originals unter „Abweichungen“: kein Überholen (A4), Fehlerpolitik (A13), Verlauf ohne Ablehnungen (A17), Start von Hand auch bei inaktiven Commands (A18). |
