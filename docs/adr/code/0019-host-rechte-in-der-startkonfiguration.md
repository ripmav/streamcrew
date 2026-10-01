# Code-ADR-0019: Host-Rechte in der Startkonfiguration

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-10-01 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Ergänzt [Code-ADR-0005](0005-konfiguration.md) und setzt Punkt 3 und 4 von [ADR-0013](../0013-sicherheitsmodell.md) um; [ADR-0003](../0003-betriebsmodi.md); Plan §6.5, §6.15, §6.21; Roadmap Phase 3.3; [`actions.md`](../../spec/actions.md) B7, B77, B100–B102, B117; [Code-ADR-0013](0013-typ-registry.md), [Code-ADR-0017](0017-klare-signale-statt-magischer-werte.md) |

## Kontext

- [ADR-0013](../0013-sicherheitsmodell.md) legt fest:
  - Jede Funktion, die über den Core hinausgreift, braucht eine Capability (Punkt 1).
  - Der Betriebsmodus bestimmt, welche an sind (Punkt 2).
  - Abweichungen davon stehen in der Startkonfiguration, nicht in der Profildatenbank; über die API lassen sich Rechte nur einschränken (Punkt 3).
  - Im Server-Modus gilt für `net:outbound` ein SSRF-Schutz mit einer Allowlist in der Startkonfiguration (Punkt 4).
  - Wie diese Einstellungen heißen und aussehen, lässt ADR-0013 offen.
- Die Actions brauchen weitere Angaben aus der Startkonfiguration:
  - Die Datei-Action arbeitet nur unter **freigegebenen Wurzeln**, die sie über einen Namen anspricht (B100, B102).
  - Externe Programme bekommen die Umgebung des Cores ohne die Variablen `STREAMCREW_*` (B117).
- [Code-ADR-0005](0005-konfiguration.md) regelt die Startkonfiguration:
  - kong ist die einzige Quelle.
  - Vorrang: Flag vor Umgebungsvariable vor Datei vor Standardwert.
  - Die Schlüssel in der YAML-Datei sind die Flag-Namen in `snake_case`; unbekannte Schlüssel sind ein Fehler.
  - Nur `internal/config` und `cmd/streamcrew` lesen Umgebungsvariablen; `forbidigo` erzwingt das, auch für `os.Environ`.
- Was das Action-Framework aus Roadmap 3.3 schon mitbringt:
  - Die Typ-Registry kennt die Capabilities jedes Action-Typs ([Code-ADR-0013](0013-typ-registry.md), Punkt 8).
  - Die Engine verweigert eine Action, deren Capability fehlt (B7).
  - Das Speichern warnt bei fehlenden Capabilities und bei unbekannten Wurzeln (Port `command.Roots`).
- Es fehlen die Menge der Capabilities, die die Composition Root an die Registry gibt, und die Wurzeln hinter `command.Roots`.
- Vorgaben des Projektinhabers vom 2026-10-01:
  - Dienste wie Twitch sind ohne Eintrag in einer Allowlist erreichbar.
  - Die Allowlist nimmt IP-Adressen, Netze in CIDR-Schreibweise und Hostnamen.
  - Änderungen der Allowlist greifen möglichst sofort, ohne Neustart; ebenso Änderungen der Rechte und Wurzeln.
  - Die Flags heißen kurz `--grant` und `--revoke`.

## Entscheidung

1. **Capabilities je Betriebsmodus:** `internal/config` bildet die Menge der Capabilities aus dem Modus und den Abweichungen aus Punkt 2. Die Standards folgen ADR-0013, Punkt 2:

   | Capability | erlaubt | `desktop`, `daemon` | `server` |
   |---|---|---|---|
   | `host:fs` | Dateien lesen und schreiben, nur unter freigegebenen Wurzeln (Punkt 3): Datei-Action, später lokale Overlay-Dateien | an | aus |
   | `host:process` | Programme starten: Action `external_program`, später Python | an | aus |
   | `host:input` | Tastatur, Maus und globale Hotkeys, über den Agent | aus | aus |
   | `host:audio` | Ton auf dem Rechner des Cores ausgeben | an | aus |
   | `net:outbound` | Anfragen an andere Rechner aus Commands: Action `web_request` (Punkt 4) | an | an, mit SSRF-Schutz |
   | `script` | Skripte in einer Sandbox: JavaScript-Action | an | an |

   - `host:input` läuft laut ADR-0013 über den Agent. Bis es ihn gibt, ist die Capability in keinem Modus an; sie lässt sich trotzdem freigeben.
   - Die Menge gilt für alle Profile des Cores. Ändern sich `grant` oder `revoke` in der Datei, gilt die neue Menge ohne Neustart (Punkt 5). Den Modus selbst ändert nur ein Neustart.

2. **Abweichungen:** zwei Listen von Capabilities.

   | Flag | Umgebungsvariable | Schlüssel in der Datei | Bedeutung |
   |---|---|---|---|
   | `--grant CAPABILITY` | `STREAMCREW_GRANT` | `grant` | zusätzlich an |
   | `--revoke CAPABILITY` | `STREAMCREW_REVOKE` | `revoke` | zusätzlich aus |

   - **Wirksame Menge:** die Standards des Modus, dazu `grant`, ohne `revoke`.
   - **Listen:** Die Flags lassen sich wiederholen. In der Umgebungsvariable trennt ein Komma, in der Datei steht eine YAML-Liste.
   - **Vorrang:** Er gilt für die ganze Liste, wie für jede Einstellung nach Code-ADR-0005: Ein Flag ersetzt die Liste aus Umgebung und Datei, statt sie zu ergänzen.
   - **Prüfung:**
     - Ein unbekannter Name ist ein Fehler.
     - Eine Capability in beiden Listen ist ein Fehler, weil sie sich widerspricht (Code-ADR-0017).
     - Eine Freigabe, die der Modus schon gewährt, oder ein Entzug, den er schon vorsieht, ist erlaubt und ändert nichts.

3. **Freigegebene Wurzeln** für die Datei-Action (B100, B102):

   | Flag | Umgebungsvariable | Schlüssel in der Datei |
   |---|---|---|
   | `--file-root NAME=DIR` | `STREAMCREW_FILE_ROOT` | `file_root` |

   - **Form:** eine Map wie `--log-component-level`. Das Flag lässt sich wiederholen. In der Umgebungsvariable trennt `;` die Einträge, in der Datei steht ein YAML-Objekt. Standard: keine Wurzel.
   - **Name:** 1 bis 32 Kleinbuchstaben, Ziffern und `_`. Commands nennen die Wurzel nur über ihren Namen, nie über den Pfad. So lassen sie sich zwischen Rechnern und Betriebssystemen übertragen.
   - **Verzeichnis:** ein absoluter Pfad. Relative Pfade und `~` sind ein Fehler, weil das Arbeitsverzeichnis eines Dienstes nicht feststeht. Eine Wurzel darf das Datenverzeichnis weder sein noch enthalten noch darin liegen; sonst könnten Commands aus Importen die Profildatenbank, Schlüssel oder den API-Token lesen ([ADR-0013](../0013-sicherheitsmodell.md), Kontext).
   - **Fehlende Verzeichnisse:** Fehlt ein Verzeichnis beim Start oder beim Nachladen oder ist es keines, warnt der Core im Log und in `streamcrew doctor`, startet aber, etwa wenn ein externes Laufwerk nicht eingehängt ist. Die Datei-Action schlägt den Namen bei jedem Zugriff in der aktuellen Liste nach (Punkt 5), öffnet die Wurzel neu mit `os.OpenRoot` und scheitert, solange sie fehlt.
   - **Ohne `host:fs`:** Wurzeln sind dann erlaubt, wirken aber nicht; der Core warnt.

4. **Netzziele** (ADR-0013, Punkt 4; B77):
   - **Ohne Eintrag erlaubt:**
     - in jedem Modus alle öffentlichen Adressen, etwa die von `twitch.tv`
     - in Desktop und Daemon jedes Ziel; nur der Server-Modus sperrt, und nur die internen Netze unten
   - **Dienste mit festen Zielen:**
     - Die Adapter der Plattformen (Twitch, YouTube, Kick) und andere Teile des Cores mit Zielen, die im Code feststehen, fallen nicht unter `net:outbound` und den SSRF-Schutz. Sie brauchen keinen Eintrag.
     - `net:outbound` und der SSRF-Schutz gelten für Ziele aus Commands, also für die Action `web_request`.
     - Integrationen mit Zielen, die der Nutzer einstellt, etwa OBS im Heimnetz, regelt ihr eigenes ADR (Phase 7).
   - **Allowlist:** öffnet im Server-Modus einzelne interne Ziele.

     | Flag | Umgebungsvariable | Schlüssel in der Datei |
     |---|---|---|
     | `--outbound-allow ZIEL` | `STREAMCREW_OUTBOUND_ALLOW` | `outbound_allow` |

   - **Einträge:**
     - eine IP-Adresse (`192.168.1.10`)
     - ein Netz in CIDR-Schreibweise (`10.0.0.0/8`)
     - ein Hostname (`homeassistant`, `nas.local`); öffentliche Namen wie `twitch.tv` sind erlaubt, aber ohnehin offen
     - Hostnamen gelten genau, ohne Beachtung der Schreibweise und ohne Platzhalter. Ports schränkt die Allowlist nicht ein. Ein ungültiger Eintrag ist beim Start ein Fehler.
   - **Listen:** wie in Punkt 2, mit Komma in der Umgebungsvariable.
   - **Gesperrt sind im Server-Modus ohne Eintrag:**
     - Loopback
     - private Netze (RFC 1918, `fc00::/7`)
     - Link-Local, darunter die Metadaten-Adresse von Cloud-Anbietern `169.254.169.254`
     - Multicast
     - die unspezifizierte Adresse, `0.0.0.0/8` und `100.64.0.0/10`
     - IPv4-Adressen in IPv6 (`::ffff:…`) zählen als IPv4.
   - **Prüfung beim Verbindungsaufbau:** Ein eigener Dialer prüft die tatsächlich verbundene Adresse jeder Verbindung, auch bei Weiterleitungen. Erlaubt ist sie, wenn sie in keinem gesperrten Netz liegt, in einem Netz der Allowlist liegt oder der angefragte Hostname in der Allowlist steht. Er liest dabei die jeweils aktuelle Allowlist (Punkt 5).
   - **Außerhalb des Server-Modus** gibt es keinen SSRF-Schutz, wie ADR-0013 es vorsieht. Eine gesetzte Allowlist wirkt dort nicht; der Core warnt.

5. **Einstellungen ohne Neustart ändern:** `grant`, `revoke`, `file_root` und `outbound_allow` greifen nach einer Änderung der Datei ohne Neustart.
   - **Nachladen:** Der Core prüft die Konfigurationsdatei jede Sekunde auf einen geänderten Inhalt; gemeint ist die Datei aus `--config` oder `config.yaml` im Standard-Datenverzeichnis. Eine Datei, die erst nach dem Start entsteht, zählt ebenso.
   - **Ganz oder gar nicht:** Der Core prüft die geänderte Datei wie beim Start (Punkte 2 bis 4) und übernimmt die vier Einstellungen nur gemeinsam. So passen Rechte, Wurzeln und Allowlist immer zueinander.
   - **Fehler:** Ist die Datei ungültig, unlesbar oder fehlt sie, bleiben die bisherigen Werte gültig. Log (Warnung) und `streamcrew doctor` nennen den Grund. Das gilt auch für eine gelöschte Datei, weil manche Editoren beim Speichern die Datei kurz entfernen. Wer eine Einstellung zurücknehmen will, ändert sie, etwa auf `outbound_allow: []`, oder entfernt den Schlüssel aus der Datei.
   - **Wirkung:**
     - **Rechte:** Engine und Speichern lesen die Menge bei jeder Prüfung neu (B7). Eine Action, die schon läuft, läuft zu Ende; die nächste Action, auch in derselben Instanz, wird gegen die neue Menge geprüft.
     - **Wurzeln:** Der nächste Zugriff der Datei-Action und das nächste Speichern nutzen die neue Liste. Eine entfernte Wurzel lässt die nächste Datei-Action scheitern (B102); ein Zugriff, der schon läuft, endet normal.
     - **Allowlist:** Sie gilt für jede danach aufgebaute Verbindung. Der HTTP-Client der Action `web_request` schließt dazu seine ungenutzten offenen Verbindungen, damit keine alte Verbindung an der neuen Liste vorbeiführt.
     - Das Log nennt jede übernommene Änderung mit altem und neuem Wert (Info).
   - **Vorrang:** Er bleibt wie in Code-ADR-0005 und gilt je Einstellung. Was per Flag oder Umgebungsvariable gesetzt ist, gilt fest für die Laufzeit; Änderungen dieses Schlüssels in der Datei wirken dann nicht, und der Core sagt das beim Start einmal im Log.
   - **Übrige Einstellungen:** Für die Einstellungen aus Code-ADR-0005, etwa Modus, Datenverzeichnis, Profil, Adresse und Logging, bleibt es beim Neustart. Geänderte Schlüssel davon nennt das Log mit dem Hinweis, dass sie erst nach einem Neustart gelten.
   - **Sicherheit:** Die Datei liegt lokal. Wer sie ändern kann, hat lokalen Zugriff, wie ADR-0013, Punkt 3, es für das Erweitern von Rechten verlangt. Über die API lassen sich diese Einstellungen nicht ändern.
   - **Umsetzung:**
     - ein Runnable unter dem Supervisor ([Code-ADR-0004](0004-nebenlaeufigkeit-und-supervisor.md)) in `internal/config`
     - Es vergleicht den Inhalt der Datei, nicht ihre Änderungszeit.
     - Es gibt eine geprüfte Momentaufnahme der vier Einstellungen über einen atomar getauschten Wert an ihre Nutzer. Die Typ-Registry bekommt dafür eine Quelle der aktuellen Menge statt einer festen Menge.

6. **Umgebung für externe Programme** (B117): `internal/config` liest die Umgebung des Prozesses einmal beim Start und gibt sie ohne die Variablen `STREAMCREW_*` an die Composition Root, die sie der Action `external_program` übergibt. Sie gehört nicht zur angezeigten Konfiguration, weil sie Secrets enthalten kann.

7. **Prüfen und Anzeigen:**
   - **Fehler:** Alle Konfigurationsfehler werden gesammelt (Code-ADR-0005, Punkt 8); `streamcrew` endet mit Exit-Code `2`.
   - **`streamcrew config show`:** zeigt `grant`, `revoke`, `file_root` und `outbound_allow` so, wie sie gesetzt sind, im Format der Datei.
   - **Log:** Die wirksamen Capabilities, die Wurzeln und die Allowlist stehen beim Start und nach jeder übernommenen Änderung im Log (Info).
   - **`streamcrew doctor`** nennt sie ebenfalls, so wie sie gerade gelten, und warnt:
     - bei Host-Capabilities (`host:*`), die im Server-Modus freigegeben sind
     - bei fehlenden Wurzeln und bei Wurzeln ohne `host:fs`
     - bei einer Allowlist außerhalb des Server-Modus
     - wenn die Datei beim letzten Nachladen ungültig war

8. **Anschluss im Code:**
   - **Composition Root:** gibt die Quelle der aktuellen Menge an `action.NewRegistry` (Engine und Speichern), die aktuellen Wurzeln an den Port `command.Roots` und an die Datei-Action, die aktuelle Allowlist an den Dialer im HTTP-Client der Action `web_request`. Die Registry nimmt damit statt einer festen Menge eine Quelle ([Code-ADR-0013](0013-typ-registry.md), Punkt 3, wird dort vermerkt).
   - **Komponenten:** bekommen nur ihre eigenen Werte, nicht `config.Config` ([Code-ADR-0002](0002-dependency-injection.md)).

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| eine Map `--capability NAME=on\|off` | `on` und `off` sind in YAML 1.1 Wahrheitswerte und je nach Parser mehrdeutig (Code-ADR-0005); eine Map mit drei Zuständen (an, aus, Standard) ist schwerer zu lesen als zwei Listen |
| eine vollständige Liste `--capabilities` statt Abweichungen | jede Konfiguration müsste die Standards des Modus wiederholen; eine neue Capability einer späteren Version wäre in alten Konfigurationen stillschweigend aus |
| Capabilities und Wurzeln in der Profildatenbank | widerspricht ADR-0013, Punkt 3: Über die API dürfen Rechte nicht erweitert werden |
| Wurzeln als Verzeichnisse ohne Namen | Commands enthielten absolute Pfade des Rechners und ließen sich nicht übertragen |
| eine fehlende Wurzel beim Start als Fehler; Wurzeln beim Start öffnen und offen halten | ein nicht eingehängtes Laufwerk verhinderte den Start des ganzen Cores; offene Verzeichnisse ließen sich nicht verschieben |
| Allowlist nur aus Adressen und Netzen | Dienstnamen in Container-Netzen, etwa `http://homeassistant:8123`, haben wechselnde Adressen |
| Allowlist mit Platzhaltern (`*.local`) oder Ports | mehr als bisher nötig und schwerer zu überblicken; lässt sich später ergänzen |
| Einstellungen nur beim Start lesen | jede Änderung bräuchte einen Neustart des Cores, also eine Unterbrechung während des Streams (Vorgabe des Projektinhabers) |
| längere Flags `--capability-allow` und `--capability-deny` | eindeutiger, aber länger; die Vorgabe des Projektinhabers sind kurze Flags |
| Dateiänderungen über Ereignisse des Betriebssystems (`fsnotify`) | eine Abhängigkeit mehr; Ereignisse fehlen auf Netzlaufwerken und bei Dateien, die per Symlink getauscht werden, etwa ConfigMaps in Kubernetes. Den Inhalt jede Sekunde zu vergleichen ist billig und auf jedem System gleich |
| Neu laden auf `SIGHUP` | gibt es unter Windows nicht, und man muss daran denken, das Signal zu schicken |
| Allowlist über die API ändern | widerspricht ADR-0013, Punkt 3 |
| jede Einstellung einzeln übernehmen, auch wenn eine andere ungültig ist | Rechte, Wurzeln und Allowlist könnten kurz nicht zueinander passen, etwa eine neue Wurzel ohne das zugehörige `host:fs` |
| SSRF-Schutz auch in Desktop und Daemon | sperrte Ziele im Heimnetz, die dort gewollt sind; ADR-0013 sieht den Schutz nur im Server-Modus vor |
| Umgebung für Programme über eine Allowlist von Variablen | mehr Konfiguration; B117 legt fest, dass Programme alles außer `STREAMCREW_*` bekommen |

## Konsequenzen

**Positiv:**

- **Sicherer Standard:** Der Server-Modus bleibt ohne Einstellungen sicher; auf dem Streaming-PC ist nichts einzustellen außer den Wurzeln.
- **Ausdrückliche Freigaben:** Jede Abweichung steht in der Startkonfiguration, ist in `config show` sichtbar und lässt sich nur mit lokalem Zugriff ändern.
- **Übertragbare Commands:** Sie nennen Wurzeln über Namen und bleiben ohne Pfade des Rechners.
- **Container:** Dienstnamen lassen sich freigeben, ohne Adressen festzuschreiben.
- **Plattformen ohne Einstellung:** Twitch und andere Dienste mit festen Zielen brauchen keinen Eintrag; öffentliche Ziele aus Commands sind in jedem Modus offen.
- **Ohne Unterbrechung:** Änderungen an Rechten, Wurzeln und Allowlist greifen nach höchstens etwa einer Sekunde, ohne Neustart.

**Negativ und Risiken:**

- **Feste Werte:** Was per Flag oder Umgebungsvariable gesetzt ist, lässt sich nur mit einem Neustart ändern. Modus, Datenverzeichnis und die übrigen Einstellungen aus Code-ADR-0005 brauchen weiter einen Neustart.
- **Nachladen:** Der Core liest die Datei jede Sekunde. Nach einem Tippfehler gelten die alten Werte weiter, bis die Datei wieder gültig ist; nur Log und `doctor` zeigen das.
- **Rechte im laufenden Command:** Ändern sie sich während einer Instanz, prüft die Engine ihre späteren Actions schon gegen die neuen Rechte. Eine Instanz kann so teils mit, teils ohne ein Recht laufen.
- **Registry:** `action.NewRegistry` nimmt statt einer festen Menge eine Quelle; das ändert eine Schnittstelle aus Code-ADR-0013.
- **Allowlist mit Hostnamen:** Ein Hostname gibt alle Adressen frei, in die er aufgelöst wird. Wer einen Namen freigibt, vertraut seiner Namensauflösung.
- **Gesperrte Netze:** Die Liste ist ein Entwurf. Die Bedrohungsanalyse in Phase 11 kann sie erweitern, ebenso die Prüfung der Wurzeln gegen weitere sensible Verzeichnisse.
- **Neue Einstellungen:** Sie müssen in README, `config show` und `doctor` nachgezogen werden.

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen und den Index in [`README.md`](README.md) anpassen; Code-ADR-0005 und ADR-0013 als ergänzt vermerken, erledigt 2026-10-01
- [ ] `internal/config`: `grant`, `revoke`, `file_root`, `outbound_allow` mit Prüfung, die Menge der Capabilities je Modus, das Nachladen der Datei und die Umgebung für Programme. Typ-Registry mit einer Quelle der Menge, Composition Root, Log, `doctor` und README-Tabelle (Roadmap 3.3, „Capability-Prüfung je Betriebsmodus“)
- [ ] Wurzeln in der Datei-Action und hinter `command.Roots` (Roadmap 3.3, `file`)
- [ ] Umgebung ohne `STREAMCREW_*` in der Action `external_program` (Roadmap 3.3)
- [ ] SSRF-Dialer mit gesperrten Netzen und der aktuellen Allowlist, ungenutzte Verbindungen nach einer Änderung schließen (Roadmap 3.3, `web_request`)
