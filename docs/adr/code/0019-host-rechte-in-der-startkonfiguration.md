# Code-ADR-0019: Host-Rechte in der Startkonfiguration

| | |
|---|---|
| **Status** | Vorgeschlagen |
| **Datum** | 2026-10-01 |
| **Entscheidung durch** | … (Abnahme durch den Projektinhaber ausstehend) |
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

## Entscheidung

1. **Capabilities je Betriebsmodus:** `internal/config` bildet die Menge der Capabilities aus dem Modus und den Abweichungen aus Punkt 2. Die Standards folgen ADR-0013, Punkt 2:

   | Capability | `desktop`, `daemon` | `server` |
   |---|---|---|
   | `host:fs` | an | aus |
   | `host:process` | an | aus |
   | `host:input` | aus | aus |
   | `host:audio` | an | aus |
   | `net:outbound` | an | an, mit SSRF-Schutz (Punkt 4) |
   | `script` | an | an |

   - `host:input` läuft laut ADR-0013 über den Agent. Bis es ihn gibt, ist die Capability in keinem Modus an; sie lässt sich trotzdem freigeben.
   - Die Menge entsteht einmal beim Start und gilt für alle Profile des Cores. Eine Änderung braucht einen Neustart.

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
   - **Fehlende Verzeichnisse:** Fehlt ein Verzeichnis beim Start oder ist es keines, warnt der Core im Log und in `streamcrew doctor`, startet aber, etwa wenn ein externes Laufwerk nicht eingehängt ist. Die Datei-Action öffnet die Wurzel bei jedem Zugriff neu mit `os.OpenRoot` und scheitert, solange sie fehlt.
   - **Ohne `host:fs`:** Wurzeln sind dann erlaubt, wirken aber nicht; der Core warnt.

4. **Allowlist für Netzziele** im Server-Modus (ADR-0013, Punkt 4; B77):

   | Flag | Umgebungsvariable | Schlüssel in der Datei |
   |---|---|---|
   | `--outbound-allow ZIEL` | `STREAMCREW_OUTBOUND_ALLOW` | `outbound_allow` |

   - **Einträge:** eine IP-Adresse (`192.168.1.10`), ein Netz in CIDR-Schreibweise (`10.0.0.0/8`) oder ein Hostname (`homeassistant`, `nas.local`). Hostnamen gelten genau, ohne Beachtung der Schreibweise und ohne Platzhalter. Ports schränkt die Allowlist nicht ein. Ein ungültiger Eintrag ist ein Fehler.
   - **Listen:** wie in Punkt 2, mit Komma in der Umgebungsvariable.
   - **Gesperrt sind ohne Allowlist:**
     - Loopback
     - private Netze (RFC 1918, `fc00::/7`)
     - Link-Local, darunter die Metadaten-Adresse von Cloud-Anbietern `169.254.169.254`
     - Multicast
     - die unspezifizierte Adresse, `0.0.0.0/8` und `100.64.0.0/10`
     - IPv4-Adressen in IPv6 (`::ffff:…`) zählen als IPv4.
   - **Prüfung beim Verbindungsaufbau:** Ein eigener Dialer prüft die tatsächlich verbundene Adresse jeder Verbindung, auch bei Weiterleitungen. Erlaubt ist sie, wenn sie in keinem gesperrten Netz liegt, in einem Netz der Allowlist liegt oder der angefragte Hostname in der Allowlist steht.
   - **Außerhalb des Server-Modus** gibt es keinen SSRF-Schutz, wie ADR-0013 es vorsieht. Eine gesetzte Allowlist wirkt dort nicht; der Core warnt.

5. **Umgebung für externe Programme** (B117): `internal/config` liest die Umgebung des Prozesses einmal beim Start und gibt sie ohne die Variablen `STREAMCREW_*` an die Composition Root, die sie der Action `external_program` übergibt. Sie gehört nicht zur angezeigten Konfiguration, weil sie Secrets enthalten kann.

6. **Prüfen und Anzeigen:**
   - **Fehler:** Alle Konfigurationsfehler werden gesammelt (Code-ADR-0005, Punkt 8); `streamcrew` endet mit Exit-Code `2`.
   - **`streamcrew config show`:** zeigt `grant`, `revoke`, `file_root` und `outbound_allow` so, wie sie gesetzt sind, im Format der Datei.
   - **Start-Log:** Die wirksamen Capabilities, die Wurzeln und die Allowlist stehen beim Start im Log (Info).
   - **`streamcrew doctor`** nennt sie ebenfalls und warnt:
     - bei Host-Capabilities (`host:*`), die im Server-Modus freigegeben sind
     - bei fehlenden Wurzeln und bei Wurzeln ohne `host:fs`
     - bei einer Allowlist außerhalb des Server-Modus

7. **Anschluss im Code:**
   - **Composition Root:** gibt die Menge an `action.NewRegistry` (Engine und Speichern), die Wurzeln an den Port `command.Roots` und an die Datei-Action, die Allowlist an den HTTP-Client der Action `web_request`.
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
| SSRF-Schutz auch in Desktop und Daemon | sperrte Ziele im Heimnetz, die dort gewollt sind; ADR-0013 sieht den Schutz nur im Server-Modus vor |
| Umgebung für Programme über eine Allowlist von Variablen | mehr Konfiguration; B117 legt fest, dass Programme alles außer `STREAMCREW_*` bekommen |

## Konsequenzen

**Positiv:**

- **Sicherer Standard:** Der Server-Modus bleibt ohne Einstellungen sicher; auf dem Streaming-PC ist nichts einzustellen außer den Wurzeln.
- **Ausdrückliche Freigaben:** Jede Abweichung steht in der Startkonfiguration, ist in `config show` sichtbar und lässt sich nur mit lokalem Zugriff ändern.
- **Übertragbare Commands:** Sie nennen Wurzeln über Namen und bleiben ohne Pfade des Rechners.
- **Container:** Dienstnamen lassen sich freigeben, ohne Adressen festzuschreiben.

**Negativ und Risiken:**

- **Neustart:** Änderungen an Rechten, Wurzeln und Allowlist brauchen einen Neustart des Cores.
- **Allowlist mit Hostnamen:** Ein Hostname gibt alle Adressen frei, in die er aufgelöst wird. Wer einen Namen freigibt, vertraut seiner Namensauflösung.
- **Gesperrte Netze:** Die Liste ist ein Entwurf. Die Bedrohungsanalyse in Phase 11 kann sie erweitern, ebenso die Prüfung der Wurzeln gegen weitere sensible Verzeichnisse.
- **Neue Einstellungen:** Sie müssen in README, `config show` und `doctor` nachgezogen werden.

**Folgearbeiten:**

- [ ] Nach der Annahme den Status setzen und den Index in [`README.md`](README.md) anpassen; Code-ADR-0005 und ADR-0013 als ergänzt vermerken
- [ ] `internal/config`: `grant`, `revoke`, `file_root`, `outbound_allow` mit Prüfung, die Menge der Capabilities je Modus und die Umgebung für Programme. Composition Root, Start-Log, `doctor` und README-Tabelle (Roadmap 3.3, „Capability-Prüfung je Betriebsmodus“)
- [ ] Wurzeln in der Datei-Action und hinter `command.Roots` (Roadmap 3.3, `file`)
- [ ] Umgebung ohne `STREAMCREW_*` in der Action `external_program` (Roadmap 3.3)
- [ ] SSRF-Dialer mit gesperrten Netzen und Allowlist (Roadmap 3.3, `web_request`)
