# ADR-0010: API-Protokoll: ConnectRPC mit Protobuf

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-28 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | [ADR-0003](0003-betriebsmodi.md), [ADR-0005](0005-core-in-desktop-builds.md), [ADR-0009](0009-repositories-und-hosting.md); Plan §6.14, §6.15; Roadmap Phasen 1 und 6 |

## Kontext

- Alle Frontends sprechen ausschließlich über eine öffentliche API mit dem Core: CLI/TUI, Desktop-App und Weboberfläche.
- Gebraucht werden gewöhnliche Anfragen (lesen, ändern, ausführen) und Live-Daten (Chat, Events, Command-Instanzen, Verbindungsstatus, Logs, Aufforderungen).
- Clients entstehen in Go (TUI, Desktop) und TypeScript (Web).
- Der Core läuft lokal neben der Desktop-App ([ADR-0005](0005-core-in-desktop-builds.md)) oder als Server-Anwendung ([ADR-0003](0003-betriebsmodi.md)).

## Entscheidung

1. **ConnectRPC mit Protobuf** (`connectrpc.com/connect`).
2. **Ein Vertrag:**
   - Die Protobuf-Dateien in `api/proto/streamcrew/v1alpha1/` im Core-Repository sind die einzige Quelle.
   - `buf` übernimmt Linting, Breaking-Change-Prüfung und Codegenerierung.
   - Der generierte Go-Code liegt öffentlich in `api/gen`.
   - Die Weboberfläche erzeugt ihren TypeScript-Client (`@connectrpc/connect-web`) aus den Protobuf-Dateien eines Core-Tags ([ADR-0009](0009-repositories-und-hosting.md)).
3. **Interaktionsmuster:** einfache Anfragen für alles Gewöhnliche, Server-Streaming für Live-Daten. Bidirektionales Streaming wird nicht verwendet, damit alles auch im Browser funktioniert.
4. **Transport:** Der Core-Server spricht das Connect-Protokoll, gRPC und gRPC-Web, über HTTP/1.1 und HTTP/2.
   - **Lokal** (ADR-0005):
     - Unix-Socket (unter Windows 10+ ebenfalls möglich) oder Loopback-TCP
     - Token in einer Datei im Datenverzeichnis (Rechte 0600)
     - Laufzeitdatei mit Adresse, PID und API-Version
     - Versions-Handshake über den System-Dienst
   - **Server-Modus:** hinter einem Reverse Proxy mit TLS; Authentifizierung ist Pflicht.
5. **Authentifizierung:**
   - Bearer-Tokens mit Scopes (`read`, `control`, `admin`, `overlay`), geprüft per Interceptor.
   - Für die Weboberfläche zusätzlich `http.CrossOriginProtection` und eine CORS-Allowlist.
6. **Versionierung:**
   - `v1alpha1` bis Core 1.0, danach `v1`.
   - Ab `v1` blockiert `buf breaking` in der CI inkompatible Änderungen.
   - Größere Brüche kommen als neues Paket (`v2`), parallel zum alten.
7. **Abgrenzung:**
   - Eine REST-Schnittstelle für Drittanbieter (Developer-API) kann später per Transcoding auf dieselben Dienste folgen (Phase 11).
   - Der MCP-Server bleibt eigenständig.
   - Der Overlay-Server für OBS-Browserquellen ist nicht Teil dieser API.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| REST/OpenAPI mit SSE oder WebSocket | verbreitet und curl-freundlich, aber zwei Mechanismen; Live-Daten schwächer typisiert |
| gRPC klassisch | stark typisiert, im Browser aber nur über einen Proxy (gRPC-Web) nutzbar |

## Konsequenzen

**Positiv:**

- Ein Vertrag, aus dem typisierte Clients für Go und TypeScript entstehen.
- Live-Daten per Streaming ohne zweiten Mechanismus.
- Läuft im Browser ohne Proxy; lokal, remote und im Server-Modus derselbe Weg.

**Negativ und Risiken:**

- Die Protobuf- und `buf`-Werkzeugkette gehört in Build und CI.
- Weniger curl-freundlich als REST. Das Connect-Protokoll erlaubt aber einfache JSON-Anfragen per HTTP-POST.
- Im Browser nur Server-Streaming, was zu diesem Entwurf passt.

**Folgearbeiten:**

- [ ] `buf` einrichten und die Protobuf-Dateien für `v1alpha1` schreiben (Roadmap Phase 6)
- [ ] Code-ADR zur Codegenerierung um `buf` ergänzen (Roadmap Phase 6)
