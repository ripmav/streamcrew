# ADR-0013: Sicherheitsmodell (Entwurf)

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | [ADR-0003](0003-betriebsmodi.md), [ADR-0010](0010-api-protokoll.md), [ADR-0011](0011-keine-telemetrie.md), [ADR-0012](0012-persistenz.md); Plan §6.5, §6.9, §6.15, §13 (R10); Roadmap Phase 2.4, 6.2, 12 |
| **Ergänzt durch** | [Code-ADR-0019](code/0019-host-rechte-in-der-startkonfiguration.md): Einstellungen zu Punkt 3 und 4 (`--grant`, `--revoke`, `--file-root`, `--outbound-allow`), ohne Neustart aus der Konfigurationsdatei nachgeladen; `host:input` ist bis zum Agent in keinem Modus an |

*Entwurf nach Roadmap Phase 2.4. Das endgültige Sicherheitsmodell mit Bedrohungsanalyse folgt in Phase 11 und bestätigt oder ersetzt dieses ADR.*

## Kontext

- Der Core führt Actions aus, die den Rechner betreffen: Dateien schreiben, Programme starten, Tastatur und Maus steuern, Ton ausgeben, beliebige URLs abrufen, Skripte ausführen (Plan §6.15).
- Auf dem Streaming-PC ist das gewollt. Als Server-Anwendung, erreichbar über das Netz, wäre dieselbe Funktion ein Einfallstor: Wer die API bedienen kann, könnte Befehle auf dem Server ausführen oder über Web-Requests interne Dienste erreichen (Plan §13, R10).
- Frontends sprechen nur über die API ([ADR-0010](0010-api-protokoll.md)), lokal wie entfernt. Overlays in OBS rufen Endpunkte des Cores ohne Anmeldung auf.
- Commands kommen auch aus Dateien (Commands als Code) und später aus Importen; ihr Inhalt ist nicht vertrauenswürdig.

## Entscheidung

1. **Capabilities:** Jede Funktion, die über den Core hinausgreift, braucht eine benannte Capability. Jeder Action- und Requirement-Typ nennt die Capabilities, die er braucht, in seinem Descriptor (Plan §6.9). Die Engine prüft vor der Ausführung; fehlt eine, scheitert die Action mit einer klaren Meldung, und der Command folgt seiner Fehlerbehandlung.
2. **Standard je Betriebsmodus** ([ADR-0003](0003-betriebsmodi.md)):

   | Capability | Betrifft | Desktop, Daemon | Server |
   |---|---|---|---|
   | `host:fs` | File-Action, lokale Overlay-Dateien; nur unter freigegebenen Wurzeln über `os.Root` | an | aus |
   | `host:process` | externe Programme, Python | an | aus |
   | `host:input` | Tastatur, Maus, globale Hotkeys | über den Agent | über den Agent |
   | `host:audio` | lokale Tonausgabe | an | aus (Overlay oder Agent) |
   | `net:outbound` | WebRequest-Action | an | an, mit SSRF-Schutz |
   | `script` | JavaScript-Action | an (Sandbox) | an (Sandbox), abschaltbar |

3. **Rechte erweitert nur, wer lokal Zugriff hat.** Welche Capabilities abweichend vom Standard an oder aus sind, steht in der Startkonfiguration (Flag, Umgebungsvariable, Konfigurationsdatei), nicht in der Profildatenbank. Über die API lassen sich Capabilities nur einschränken, nie erweitern. Ein entfernter Angreifer mit API-Zugang kann sich so keine Host-Rechte verschaffen.
4. **SSRF-Schutz für `net:outbound` im Server-Modus:** Ziele in privaten, Loopback-, Link-Local- und Multicast-Netzen sind gesperrt. Geprüft wird die tatsächlich verbundene Adresse beim Verbindungsaufbau, nicht nur der Name, damit DNS-Rebinding nicht hilft. Eine Allowlist in der Startkonfiguration erlaubt einzelne interne Ziele.
5. **Anmeldung an der API:**
   - Jede Anfrage braucht einen Token, außer `/healthz`, `/readyz` und den Overlay-Endpunkten.
   - Tokens sind zufällig (256 Bit), liegen nur als Hash in der Profildatenbank und tragen Scopes: `read`, `control`, `admin`, `overlay`.
   - Desktop und Daemon legen beim ersten Start einen lokalen Admin-Token in `<data-dir>/api.token` an (Rechte `0600`), den CLI, TUI und Desktop-App lesen. Im Server-Modus gibt es keinen automatischen Token; der erste entsteht mit `streamcrew token create` auf dem Server.
   - Fehlgeschlagene Anmeldungen werden je Quelladresse gebremst. Browser-Frontends schützt `http.CrossOriginProtection`, dazu eine CORS-Allowlist für die Web-UI.
6. **Overlay-Endpunkte** liefern nur lesend, was sie anzeigen, optional geschützt durch einen Token in der URL. Eigenes HTML läuft in Sandbox-iframes.
7. **Transport:** Im Server-Modus bleibt TLS Sache des Reverse Proxys ([ADR-0003](0003-betriebsmodi.md)). Lauscht der Core im Server-Modus ohne Proxy auf einer öffentlichen Adresse, warnt er im Log und in `streamcrew doctor`.
8. **Secrets** erscheinen nie in Logs ([Code-ADR-0003](code/0003-fehler-und-logging.md)) und liegen verschlüsselt in Datenbank und Backups ([ADR-0012](0012-persistenz.md)).

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| gleiche Rechte in allen Modi | auf einem Server wäre jede API-Anmeldung eine Shell auf dem Server |
| Host-Funktionen im Server-Modus gar nicht anbieten | schließt Nutzer aus, die den Core bewusst auf einem eigenen Rechner mit Bildschirm betreiben; die Freigabe per Startkonfiguration bleibt möglich |
| Capabilities in der Profildatenbank, über die API änderbar | ein gestohlener API-Token reichte, um Host-Rechte freizuschalten |
| SSRF-Prüfung nur anhand des Hostnamens | per DNS-Rebinding umgehbar |
| Tokens im Klartext speichern | ein Datenbank- oder Backup-Leck gäbe gültige Zugänge preis |
| mTLS für die API | stark, aber für Streamer im Alltag zu umständlich; bleibt eine Option für Phase 11 |

## Konsequenzen

**Positiv:**

- Der Server-Modus ist ohne weitere Einstellungen sicher; der Streaming-PC bleibt voll nutzbar.
- Rechte lassen sich nur mit lokalem Zugriff erweitern.
- Descriptors machen sichtbar, welcher Typ welche Rechte braucht; Frontends können das anzeigen.

**Negativ und Risiken:**

- Die Prüfung vor jeder Action kostet etwas Laufzeit und muss lückenlos sein; Tests prüfen, dass jeder Typ seine Capabilities nennt.
- Der SSRF-Schutz braucht einen eigenen Dialer und kann legitime interne Ziele sperren, bis sie in der Allowlist stehen.
- Ein Entwurf: Bedrohungsanalyse, Penetrationstest und die endgültige Liste der Capabilities fehlen noch (Phase 11).

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen, den Index in [`README.md`](README.md) anpassen und die Backlog-Nummern in Plan und Roadmap nachziehen, erledigt 2026-09-29
- [x] Capabilities in Descriptors und Engine-Prüfung (Roadmap Phase 3), erledigt 2026-10-01: Typ-Registry und Engine nach Code-ADR-0013, Menge je Betriebsmodus nach Code-ADR-0019
- [ ] Startkonfiguration für abweichende Capabilities und die SSRF-Allowlist (Roadmap Phase 3 bzw. mit der WebRequest-Action); Format in [Code-ADR-0019](code/0019-host-rechte-in-der-startkonfiguration.md) (akzeptiert 2026-10-01); Konfiguration mit Nachladen erledigt 2026-10-01 (`internal/config`), es fehlt der SSRF-Dialer mit der WebRequest-Action
- [ ] API-Tokens mit Scopes, lokaler Admin-Token, Bremse für Fehlversuche (Roadmap Phase 6.2)
- [ ] Bedrohungsanalyse und endgültiges Sicherheitsmodell (Roadmap Phase 11)
