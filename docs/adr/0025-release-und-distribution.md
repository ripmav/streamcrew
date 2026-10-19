# ADR-0025: Release und Distribution

| | |
|---|---|
| **Status** | Vorgeschlagen |
| **Datum** | 2026-10-17 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Konkretisiert [ADR-0007](0007-release-artefakte-des-cores.md); [ADR-0003](0003-betriebsmodi.md) (Server-Modus), [ADR-0005](0005-core-in-desktop-builds.md)/[ADR-0006](0006-core-als-bibliothek-fuer-selbststart.md) (Desktop), [ADR-0013](0013-sicherheitsmodell.md); Plan §11.3; Roadmap Phase 6.7 |

## Kontext

- Laut [ADR-0007](0007-release-artefakte-des-cores.md) enthält jedes Core-Release (Git-Tag `vX.Y.Z`) beide Artefakte derselben Version: die Binaries aller Zielplattformen und das Go-Modul als Quellarchiv im Proxy-Layout. Wie die Release-Pipeline das realisiert, war offen (Plan §12.1, „Release-ADR“).
- Der MVP-Zustand (M2) verlangt: Binaries, ein Container-Image, ein Beispiel für eine systemd-Unit, Prüfsummen und eine SBOM für alle Artefakte, und einen Prüfschritt, der ein Testprogramm per `GOPROXY=file://…` gegen das Quellarchiv baut.
- Der Server-Modus (ADR-0003) ist der Hauptbetrieb des Headless-MVPs: Container oder bare Metal mit Token-Authentifizierung und abgeschalteten Host-Capabilities. Eine systemd-Unit ist dafür der Standard-Weg.
- ADR-0007 verweist die Signaturen an dieses ADR.

## Entscheidung

1. **`goreleaser` ist der Builder des Releases.** Er läuft auf dem Tag `vX.Y.Z` in einer GitHub-Workflow-Datei (`release.yml`), die das Release auf diesem Repository anlegt. Das Release enthält:
   - die **Binaries** `streamcrew` für linux/darwin/windows × amd64/arm64 (CGO-frei, wie ADR-0007), je als `.tar.gz` (linux) bzw. `.zip` (windows) und das einzelne Binary,
   - das **Container-Image** (mehrplatformig, multi-stage Build: `golang`-Stufe baut, `scratch`-Stufe läuft), das als manifest-orientierte Referenz und als Image-Artefakt (`.tar`) ins Release kommt,
   - die **systemd-Unit-Beispiele** `streamcrew.service` (Daemon-Modus, `User=`-Laufzeitbenutzer, `EnvironmentFile=` für Token und Key) und `streamcrew-timer.service` nicht (Timer sind Kernfunktionen, keine OS-Timer),
   - **`checksums.txt`** (SHA-256 für alle Artefakte) und die **SBOM** (CycloneDX, je eine für das Binary-Projekt und eine für das Container-Image),
   - das **Modul-Quellarchiv** `streamcrew-vX.Y.Z-src.zip` im Layout des Go-Modul-Proxys (siehe Punkt 2).
2. **Das Modul-Quellarchiv** erzeugt ein eigener Pipeline-Schritt mit `golang.org/x/mod/zip` (`zip.Create` auf dem Repository-Verzeichnis des Tags, ohne `testdata`-Unnötigkeiten, aber mit allen Paketen, die `core` braucht). Das Zip ist bit-stabil für dieselbe Tree-Version und entspricht dem Format, dessen Prüfsumme (`h1:`) in einer `go.sum` steht.
3. **Prüfschritt des Quellarchivs:** Die Release-Pipeline baut nach der Erzeugung ein kleines Testprogramm (`_example/embedded/main.go`-Analogon in einem temporären Modul, `require github.com/ripmav/streamcrew vX.Y.Z`) per `GOPROXY=file://<cache>` dagegen. Der Schritt schlägt fehl, wenn das Archiv nicht auflösbar oder nicht kompilierbar ist. Dieselbe Prüfung läuft als CI-Job bei jeder Änderung an `go.mod` (mit dem dann aktuellen Commit als Pseudo-Version), damit das Archivformat nicht erst im Release kaputtgeht.
4. **Signaturen:** Im MVP werden die Artefakte **nicht signiert**. Die Integrität kommt aus (a) dem privaten Repository mit Zugriff per Token/SSH, (b) `checksums.txt` und (c) der SBOM. Kryptographische Signierung der Artefakte (z. B. `cosign` oder SignStore) ist Folgearbeit für die Phase nach dem Open-Sourcing, wenn es öffentliche Download-Pfade gibt.
5. **Kein eigener Update-Server.** Updates laufen über die GitHub-Releases (Binary-Download, `go get`, Container-Registry). Ein Update-Mechanismus im Core (Plan §12.1 „Updates“) ist kein MVP-Thema.
6. **Versionsnummern:** SemVer `vX.Y.Z` (Maßgebend ist der Tag). Zwischen-Commits tragen `version.txt`/Build-Info `(devel)`. Das Release-Tag entsteht manuell nach M2-Abnahme, nicht automatisch.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `goreleaser` nur für die Binaries, Container-Image per eigener Workflow | zwei getrennte Build-Stätten für dasselbe Release; ADR-0007 verlangt ein gemeinsames Artefakt-Set |
| Modul-Zip per `go mod download`-Cache statt `x/mod/zip` | funktioniert, ist aber an die lokale Modul-Cache-Layout-Kopplung gebunden und liefert keine stabile, prüfbare Artefakt-Datei mit definierter Prüfsummen-Beziehung |
| Artefakte sofort signieren (`cosign`) | erhöht den Betriebsaufwand (Key-Management) ohne Nutzen, solange das Repository privat ist; wird bei Öffnung nachgezogen |
| Echter Update-Server (z. B. `go-releaser`-Update-Endpoint) | eigener Dienst, den kein MVP-Nutzer braucht; GitHub-Releases reichen (ADR-0009: Hosting auf GitHub) |

## Konsequenzen

**Positiv:**

- Ein Tag, ein `release.yml`-Lauf, ein komplettes und geprüftes Artefakt-Set — die M2-Exit-Kriterien „Release-Artefakte und Container-Image stehen bereit“ sind direkt erfüllbar.
- Das Quellarchiv ist durch den CI-Prüfschritt permanent verifiziert, nicht erst im Release.
- Die systemd-Unit gibt Server-Nutzern (ADR-0003) ein kopierbares Standard-Setup für Token, Key und Datenverzeichnis.

**Negativ und Risiken:**

- Kein Artefakt-Signing: die Integrität ruht auf der Repository-Zugriffskontrolle. Wer das Repository sieht, sieht auch die Artefakte — akzeptiert für den Privatbetrieb, bei Öffnung nachziehen (Punkt 4).
- Das `scratch`-Image hat kein Shell; Fehlsuche im Container erfordert `docker run --rm --entrypoint /bin/sh`-Alternativen oder ein `busybox`-Tool — im Image dokumentiert.
- `goreleaser`-Konfiguration muss an Toolchain-Änderungen angepasst werden (Go-Bump ⇒ Build-Image-Digest, s. Projektregrn für Dockerfiles).

**Folgearbeiten:**

- [ ] Release-Tag `v1.0.0-mvp` nach M2-Abnahme und ersten echten Release-Lauf
- [ ] Artefakt-Signierung (`cosign`/SignStore), wenn das Repository geöffnet wird
- [ ] Container-Registry-Publish (aktuell: Image nur als Release-Artefakt)
- [ ] Desktop-Pakete (`fyne-cross`) — eigene Phase, nicht Teil dieses MVP-Releases
