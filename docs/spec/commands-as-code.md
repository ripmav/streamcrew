# Spezifikation: Commands als Code

| | |
|---|---|
| **Status** | Geprüft |
| **Stand** | 2026-10-03 |
| **Bezug** | Roadmap Phase 3.5; Plan §2 (Ziel Z9), §6.9, §7.2; [Code-ADR-0005](../adr/code/0005-konfiguration.md), [Code-ADR-0010](../adr/code/0010-polymorphe-serialisierung.md), [Code-ADR-0013](../adr/code/0013-typ-registry.md), [Code-ADR-0017](../adr/code/0017-klare-signale-statt-magischer-werte.md), [Code-ADR-0018](../adr/code/0018-json-v2.md), [Code-ADR-0020](../adr/code/0020-dezimalzahlen.md); [`commands.md`](commands.md), [`requirements.md`](requirements.md), [`actions.md`](actions.md) |
| **Umsetzung** | begonnen 2026-10-03 (Roadmap 3.5) in `internal/commandfile`: eindeutige Namen (B22), Schema-Export (B30) mit `streamcrew schema export`, Lesen der Dateien (B5, B37, B60, B66, B67), Umwandeln der Dokumente in Commands und Gruppen mit den IDs ihrer Namen (B1–B5, B10–B14, B20–B24), `streamcrew command validate` (B31, B32) mit `app.CheckCommandFiles`; Import und Export folgen |

## Zweck und Umfang

Commands, Command-Gruppen und Cooldown-Gruppen lassen sich als Dateien schreiben, prüfen, in ein Profil importieren und aus ihm exportieren, in YAML oder JSON. So lässt sich streamcrew ohne Oberfläche einrichten, Commands lassen sich in Git verwalten und zwischen Profilen austauschen (Plan, Ziel Z9). Dazu gehören JSON-Schemas, mit denen Editoren die Dateien prüfen und vervollständigen.

Nicht Teil dieser Spezifikation:

- Counter, Quotes, Nutzer und Settings; sie haben eigene Befehle und die Sicherung des Profils.
- der Import von Daten aus Mix It Up (Roadmap 10.2)
- der Weg über die API (Roadmap Phase 6); bis dahin arbeitet die CLI direkt mit dem Profil.

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Dokument | ein Command, eine Command-Gruppe oder eine Cooldown-Gruppe in der Form dieser Spezifikation |
| Datei | eine YAML- oder JSON-Datei mit einem oder mehreren Dokumenten |
| Art (`kind`) | was ein Dokument beschreibt, etwa `ChatCommand` |
| Verweis | ein Feld, das ein anderes Dokument nennt, etwa der Command, den eine Command-Action aufruft |

## Verhalten

### Format

| ID | Regel | Quellen |
|---|---|---|
| B1 | Ein Dokument hat genau die vier Felder `apiVersion`, `kind`, `metadata` und `spec`. Die einzige `apiVersion` ist `streamcrew/v1alpha1`; eine andere lehnt die Prüfung ab und nennt die unterstützte. | QP (§6.9) |
| B2 | Arten: `ChatCommand`, `EventCommand`, `TimerCommand` und `ActionGroup` für die Command-Arten `chat`, `event`, `timer` und `action_group` ([`commands.md`](commands.md), B2), dazu `CommandGroup` und `CooldownGroup`. Jede Art hat ihr eigenes Schema (B30). | QP (§6.9), Entscheidung des Projektinhabers |
| B3 | `metadata` hat `name`, den Namen des Dokuments (Pflicht), und bei Commands `group`, den Namen ihrer Command-Gruppe (ohne das Feld keine Gruppe). IDs stehen nie in Dateien. | QP (§6.9), Entscheidung des Projektinhabers |
| B4 | Felder, die das Schema der Art nicht kennt, und doppelte Schlüssel sind Fehler. Fehlt ein Feld mit Voreinstellung, gilt die Voreinstellung „beim Anlegen“ ([Code-ADR-0013](../adr/code/0013-typ-registry.md), Punkt 4); fehlt ein Pflichtfeld, ist das Dokument ungültig. | Code-ADR-0013, Code-ADR-0017 |
| B5 | Zahlen werden exakt gelesen ([Code-ADR-0020](../adr/code/0020-dezimalzahlen.md)), Dauern als Text wie `30s` oder `1m30s` (Code-ADR-0009). YAML gilt nach Version 1.2: `no`, `yes`, `on` und `off` sind Text, nur `true` und `false` sind Wahrheitswerte. | Code-ADR-0005, Code-ADR-0020 |

### Commands

| ID | Regel | Quellen |
|---|---|---|
| B10 | `spec` eines Commands hat: `enabled` (Schalter „aktiv“, Voreinstellung an), `unlocked` (Schalter „freigegeben“, Voreinstellung aus), `errorPolicy` (`continue` oder `abort`, Voreinstellung `continue`), `requirements` (B12) und `actions` (B13). | [`commands.md`](commands.md), B1 |
| B11 | Je Art kommen hinzu: bei `ChatCommand` die Trigger `triggers` (Liste, mindestens einer) und `triggerMode` (`exclamation`, `literal` oder `wildcard`, Voreinstellung `exclamation`); bei `EventCommand` der Ereignistyp `event` (Pflicht); `TimerCommand` und `ActionGroup` haben keine weiteren Felder. | [`commands.md`](commands.md), B10–B14, B20 |
| B12 | `requirements` ist eine Map: Schlüssel ist die Art der Anforderung (`role`, `cooldown`, `currency`, `rank`, `inventory`, `arguments`, `threshold`, `settings`), Wert sind ihre Felder ohne `type` und `schemaVersion`, etwa `role: { role: follower }`. Je Art gibt es so höchstens eine Anforderung. Ohne das Feld hat der Command keine Anforderungen. | QP (§6.9), Entscheidung des Projektinhabers |
| B13 | `actions` ist eine Liste von Actions mit `type` und ihren Feldern, ohne `schemaVersion`; Kind-Actions ebenso. Die Version der Dokumente ergibt sich aus `apiVersion`. Ohne das Feld hat der Command keine Actions. | Code-ADR-0010, Code-ADR-0013 |
| B14 | Arten von Actions und Anforderungen, die diese Version nicht kennt, sind in Dateien ein Fehler; anders als gespeicherte Dokumente ([`commands.md`](commands.md), B4) bleiben sie nicht still erhalten. | Code-ADR-0017 |

### Gruppen

| ID | Regel | Quellen |
|---|---|---|
| B20 | `CommandGroup` hat in `spec` das eigene Timer-Intervall `timerInterval` als Dauer; ohne das Feld hat die Gruppe keines ([`commands.md`](commands.md), B31). | [`commands.md`](commands.md), B30, B31 |
| B21 | `CooldownGroup` hat in `spec` die positive Dauer `duration` (Pflicht). | [`commands.md`](commands.md), B33 |

### Namen und Verweise

| ID | Regel | Quellen |
|---|---|---|
| B22 | Ein Dokument ist an Art und Name erkennbar. Die Namen von Commands sind im Profil ohne Rücksicht auf die Schreibweise eindeutig, wie die Namen von Cooldown-Gruppen ([`commands.md`](commands.md), B33); die Namen von Command-Gruppen sind es schon (B30). [`commands.md`](commands.md) übernimmt die Regel mit ihrer Umsetzung (Roadmap 3.5). | Entscheidung des Projektinhabers |
| B23 | Verweise stehen in Dateien als Namen: der Command einer Command-Action, die Gruppe beim Schalten einer Gruppe ([`actions.md`](actions.md), B31, B34), die Cooldown-Gruppe eines Cooldowns ([`requirements.md`](requirements.md), B20), die Währung, der Rang und der Gegenstand der Anforderungen ([`commands.md`](commands.md), B42–B44) und die Command-Gruppe in `metadata.group`. Counter und freigegebene Wurzeln stehen ohnehin als Name. Der Import löst Namen in IDs auf, der Export schreibt Namen. | Entscheidung des Projektinhabers |
| B24 | Ein Verweis zeigt auf ein Dokument derselben Prüfung oder desselben Imports oder auf eines, das es im Profil schon gibt. Ein Name, den keines davon trägt, ist ein Fehler. | [`actions.md`](actions.md), B31 |

### Prüfen, Import und Export

| ID | Regel | Quellen |
|---|---|---|
| B30 | `streamcrew schema export` schreibt JSON-Schemas nach Entwurf 2020-12 in ein Verzeichnis, Voreinstellung `schemas/`: eine Datei mit einer Definition je Art sowie je Action- und Anforderungsart, aus denselben Bausteinen wie der Typkatalog ([Code-ADR-0013](../adr/code/0013-typ-registry.md), Punkt 6). Verweise stehen darin als Namen (B23). Das Verzeichnis `schemas/` im Repository enthält die erzeugte Datei; ein Test hält sie aktuell. | QP (§7.2), Code-ADR-0013 |
| B31 | `streamcrew command validate <pfade>` prüft Dateien, ohne das Profil zu ändern: Syntax, die Form der Art, dieselben Prüfungen wie beim Speichern ([`commands.md`](commands.md); [`requirements.md`](requirements.md), B80; [Code-ADR-0013](../adr/code/0013-typ-registry.md), Punkt 7) und die Verweise (B24). Es liest das Profil dabei nur, darf also laufen, während der Core läuft. | QP (§7.2) |
| B32 | Jeder Fehler nennt Datei, Zeile und Spalte und den Pfad im Dokument, etwa `hug.yaml:12:9: spec.actions[1].message: …`. Syntaxfehler in YAML nennen nur die Zeile ([Code-ADR-0005](../adr/code/0005-konfiguration.md), Punkt 3). Die Prüfung meldet die Fehler aller Dateien und Dokumente, je Dokument den ersten, und endet mit einem Status ungleich 0, wenn es welche gibt. Warnungen beim Speichern ([`requirements.md`](requirements.md), B81; [`actions.md`](actions.md), B7) stehen ebenso, ändern den Status aber nicht. Mit `--output json` gibt sie das maschinenlesbar aus. | Code-ADR-0005 |
| B33 | `streamcrew command import <pfade>` prüft wie `validate` und übernimmt danach alle Dokumente in einer Transaktion, oder bei einem Fehler keines. Ein Dokument, dessen Art und Name es im Profil schon gibt, ersetzt das gespeicherte; es behält seine ID und seinen Zeitpunkt der Erstellung. Andere legt der Import neu an. Dokumente, die nicht in den Dateien stehen, bleiben unverändert. Der Import braucht einen gestoppten Core, bis die API ihn übernimmt (Roadmap Phase 6). | QP (§7.2) |
| B34 | Der Import übernimmt zuerst Cooldown-Gruppen, dann Command-Gruppen, dann Commands, damit Verweise auflösbar sind; Commands, die einander aufrufen, legt er gemeinsam an. | — |
| B35 | `streamcrew command export [namen]` schreibt Commands mit den Gruppen und Cooldown-Gruppen, auf die sie verweisen, ohne Namen alle. Voreinstellung ist YAML auf die Standardausgabe; `--file` schreibt in eine Datei, `--dir` je Dokument eine Datei, `--format json` JSON. Er liest das Profil nur und darf laufen, während der Core läuft. | QP (§7.2) |
| B36 | Der Export schreibt alle Felder ausdrücklich, auch die mit Voreinstellung, in fester Reihenfolge: `apiVersion`, `kind`, `metadata`, `spec`, darin die Felder in der Reihenfolge des Schemas. Die Dokumente stehen in der Reihenfolge Cooldown-Gruppen, Command-Gruppen, Commands, je nach Namen. Exportieren und wieder Importieren ändert nichts außer dem Zeitpunkt der Änderung. | Code-ADR-0018 |
| B37 | Dateien: YAML mit der Endung `.yaml` oder `.yml`, mehrere Dokumente getrennt durch `---`; JSON mit der Endung `.json`, ein Dokument oder eine Liste von Dokumenten. Pfade dürfen Verzeichnisse sein; dann zählen alle Dateien mit diesen Endungen darin, auch in Unterverzeichnissen, in der Reihenfolge ihrer Pfade. Die Dateien sind UTF-8. | QP (§6.9) |
| B38 | Bei `--dir` heißt jede Datei nach Art, Name und Version des Formats, etwa `chat-command-hug.v1alpha1.yaml`: der Name in Kleinbuchstaben, jedes Zeichen außer Buchstaben und Ziffern als `-`, die Version aus `apiVersion` ohne `streamcrew/`. Gleiche Dateinamen bekommen eine Nummer vor der Version, etwa `chat-command-hug-2.v1alpha1.yaml`. | Entscheidung des Projektinhabers |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B60 | Zwei Dokumente derselben Art mit Namen, die sich nur in der Schreibweise unterscheiden, in einem Import | Fehler bei beiden, nichts wird übernommen | B22, B33 |
| B61 | Ein Command ruft einen Command auf, den erst derselbe Import anlegt, auch gegenseitig | wird aufgelöst | B24, B34 |
| B62 | Ein Dokument hat einen Fehler, die übrigen nicht | Der Import übernimmt nichts und nennt alle Fehler. | B32, B33 |
| B63 | Ein Command in der Datei heißt `Hug`, im Profil `hug` | Der Import ersetzt den gespeicherten Command; sein Name lautet danach `Hug`. | B22, B33 |
| B64 | Eine gespeicherte Command-Action verweist auf einen gelöschten Command | Der Export dieses Commands scheitert und nennt den Verweis; ohne Namen lässt er sich nicht schreiben. | B23 |
| B65 | Ein gespeicherter Command hat eine Action unbekannten Typs, etwa aus einer neueren Version | Der Export dieses Commands scheitert und nennt die Action (B14). | B14 |
| B66 | Ein doppelter Schlüssel in YAML | Fehler mit Zeile und Spalte des zweiten Schlüssels | B4, B32 |
| B67 | Ein Trigger `no` in YAML | bleibt der Text „no“ | B5 |

## Abweichungen vom Original

Keine. Commands als Code sind eine neue Funktion (Plan §4, „neu“); Commands aus Mix It Up übernimmt der Import aus Roadmap 10.2.

## Akzeptanzkriterien

- [ ] B1–B5, B10–B14, B20, B21: jede Art mit gültigen und ungültigen Dokumenten in YAML und JSON, gegen den Go-Code und gegen das exportierte Schema.
- [x] B22: Namen von Commands sind ohne Rücksicht auf die Schreibweise eindeutig; gespeicherte Namen, die das verletzen, behandelt eine Migration. Erledigt 2026-10-03: Spalte `name_key` (Migration 13), Go-Migration 14 benennt doppelte Namen um.
- [x] B23, B24, B61: Verweise mit Namen auf Dokumente derselben Dateien und des Profils, auch gegenseitige Aufrufe. Erledigt 2026-10-03 mit `commandfile.Convert` und `commandfile.Apply`; der Import nutzt dieselben Schritte.
- [x] B30: Das exportierte Schema stimmt mit `schemas/` im Repository überein. Erledigt 2026-10-03: `TestSchemaExport` in `cmd/streamcrew` vergleicht die Ausgabe mit [`schemas/streamcrew-v1alpha1.schema.json`](../../schemas/streamcrew-v1alpha1.schema.json); der Konformitätstest der Anforderungsarten und die Beispiele in `internal/commandfile` prüfen das Schema gegen den Go-Code.
- [ ] B31, B32, B62, B66: Fehler mit Datei, Zeile, Spalte und Pfad, die aller Dokumente auf einmal, als Text und als JSON. B31, B32 und B66 erledigt 2026-10-03 mit `streamcrew command validate`; B62 folgt mit dem Import.
- [ ] B33, B34, B60, B63: Import neu und ersetzend, alles oder nichts, mit gestopptem Core.
- [ ] B35–B38, B64, B65: Export als YAML und JSON, in eine Datei und je Dokument; Rundlauf aus Export und Import ohne Änderung.

## Offene Fragen

Keine. Die Grundsatzfragen hat der Projektinhaber am 2026-10-03 entschieden (Arten je Command-Art, Erkennung am Namen mit eindeutigen Namen, Verweise mit Namen, Anforderungen als Map).

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| QP | Projekt | [Plan](../plan.md) §2 (Ziel Z9), §6.9 (Beispiel des Formats), §7.2 (CLI) | Commands als Code, Austauschformat, Befehle |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-10-03 | Erstfassung mit den Entscheidungen des Projektinhabers: je Command-Art ein `kind`, Erkennung am Namen mit eindeutigen Command-Namen, Verweise mit Namen, Anforderungen als Map nach Art. Festlegungen dabei: keine Kurzformen, etwa `role: follower`, damit jede Anforderung eine Form hat; unbekannte Arten von Actions und Anforderungen sind in Dateien ein Fehler; der Import ersetzt Dokumente gleichen Namens und löscht keine anderen; der Export schreibt alle Felder ausdrücklich. |
| 2026-10-03 | Umsetzung begonnen mit B22 und B30. Festlegungen dabei: Auch Währung, Rang und Gegenstand der Anforderungen stehen als Namen (B3, B23); bis Phase 8 gibt es keine, ein solcher Name ist also ein Fehler (B24). Das Schema hat die Definitionen `document`, je Art (`ChatCommand` …), `requirements`, `actions`, `action` und je Typ `action.<typ>` und `requirement.<typ>`; in `spec` stehen die Felder der Art vorn, dann `enabled`, `unlocked`, `errorPolicy`, `requirements` und `actions`, und in dieser Reihenfolge schreibt sie der Export (B36). Namen prüft das Schema nur auf Steuerzeichen und Leerzeichen am Rand, weitere Leerraumzeichen am Rand nur der Go-Code ([Code-ADR-0013](../adr/code/0013-typ-registry.md), Punkt 6). |
| 2026-10-03 | B38: Der Export mit `--dir` hängt die Version des Formats an den Dateinamen, etwa `chat-command-hug.v1alpha1.yaml` (Entscheidung des Projektinhabers). Anlass war der Hinweis, dass Dateien keine Version je Action- und Anforderungsart tragen (B13): Bekommt eine Art eine neue Version, liest der Import eine ältere Datei, als hätte sie schon die neue Form. |
| 2026-10-03 | B31, B32: Dateien prüft allein der Go-Code, wie gespeicherte Dokumente; das Schema dient Editoren (Entscheidung des Projektinhabers, [Code-ADR-0013](../adr/code/0013-typ-registry.md), Punkt 6). So liegen die Regeln an einer Stelle, und ein eigener Prüfer gegen das Schema im Programm entfällt. Deshalb meldet die Prüfung je Dokument den ersten Fehler, über alle Dokumente und Dateien hinweg alle; B31 nennt statt des Schemas der Art ihre Form. |
| 2026-10-03 | Lesen der Dateien umgesetzt. Festlegungen dabei: Pfade im Dokument zählen Listen ab 0 (`spec.actions[0]`). Auch YAML darf oben eine Liste von Dokumenten haben; leere YAML-Dokumente zählen nicht. Schlüssel sind Skalare und gelten als ihr Text. Zahlen liest der Import wie `decimal.Parse`, also auch hexadezimal nach `0x` und mit `_` zwischen Ziffern (Code-ADR-0020); oktale Zahlen wie `0o17`, `.inf` und `.nan` sind Fehler. Anker und Aliasse werden aufgelöst, höchstens 2^20 Werte und 256 Ebenen je Dokument; Merge-Schlüssel (`<<`) sind ein Fehler. JSON-Dateien liest `encoding/json/jsontext`, weil YAML v3 das JSON-Escape `\/` nicht kennt. |
| 2026-10-03 | Umwandeln der Dokumente umgesetzt (`commandfile.Convert`). Festlegungen dabei: Anforderungen stehen im Command in der Reihenfolge ihrer Prüfung, gleich in welcher Reihenfolge die Datei sie nennt. Ein Verweis auf ein Dokument mit einem Fehler gilt als aufgelöst, damit ein Fehler keine Folgefehler nach sich zieht. Fehler beim Lesen einer Action oder Anforderung nennen das Feld, das sie betreffen; Fehler ihrer Prüfung die Action oder Anforderung; Fehler der Prüfung eines ganzen Commands, etwa ein Trigger mit `!`, das Feld `spec`. Kind-Actions werden vor ihren Eltern geprüft, damit der Fehler an der Action steht, die ihn hat. Verweise auf Währungen, Ränge und Gegenstände sind bis Phase 8 immer ein Fehler (B24). |
| 2026-10-03 | `command validate` umgesetzt. Festlegungen dabei: Die Prüfung kopiert das Profil wie ein Backup und speichert die Dokumente in die Kopie, mit genau den Prüfungen beim Speichern; danach löscht sie die Kopie. So darf der Core laufen, und Counter, die das Speichern anlegt, entstehen nur in der Kopie. Commands werden zweimal gespeichert, zuerst ausgeschaltet ohne Anforderungen und Actions, dann vollständig; so können Commands derselben Dateien einander aufrufen (B61) und Trigger zwischen ersetzten Commands wandern. Vor dem Speichern prüft sie Trigger (`commands.md`, B14) und Ereignistypen (B20) gegen die Commands des Profils und der Dateien, damit der Fehler den anderen Command nennt. Fehler des Speicherns stehen an der Action oder Anforderung, die sie betreffen. Die Ausgabe als Text endet mit einer Zeile wie `2 documents in 2 files: 1 error, 0 warnings`; Pfade ohne solche Dateien und unbekannte Endungen sind ein Fehler der Kommandozeile (Status 2), ein Profil, das es nicht gibt, ein Fehler (Status 1). |
