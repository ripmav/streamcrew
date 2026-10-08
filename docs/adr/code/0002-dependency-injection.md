# Code-ADR-0002: Dependency Injection und Composition Root

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | [ADR-0006](../0006-core-als-bibliothek-fuer-selbststart.md); Plan §6.1, §6.3, §6.6, §11.1; Roadmap Phase 1.2 und 1.3; [Code-ADR-0003](0003-fehler-und-logging.md), [Code-ADR-0004](0004-nebenlaeufigkeit-und-supervisor.md), [Code-ADR-0005](0005-konfiguration.md) |

## Kontext

- Plan §6.1 verlangt explizite Abhängigkeiten: eine Composition Root in `internal/app`, keinen Service Locator, kein `init()` und keine veränderlichen Paketvariablen. `gochecknoinits` und `gochecknoglobals` prüfen das bereits ([Code-ADR-0001](0001-go-toolchain-und-linting.md)).
- Der Core wächst auf viele Komponenten: Plattformen, Integrationen, Engine, Speicher, API. Sie müssen sich einzeln testen und durch Fakes ersetzen lassen, etwa die Mock-Plattform in Phase 3.
- [ADR-0006](../0006-core-als-bibliothek-fuer-selbststart.md) verlangt eine schmale öffentliche Start-API `core.Run(ctx, Options)`, die `streamcrew serve` ebenfalls nutzt. Beide brauchen deshalb denselben Einstieg in die Verdrahtung.
- Die Standardbibliothek und die globalen Go-Regeln des Projektinhabers bevorzugen Konstruktor-Injektion und schließen `google/wire` aus, das seit August 2025 archiviert ist.

## Entscheidung

1. **Konstruktor-Injektion von Hand.** Abhängigkeiten sind Parameter von `NewX(…)` und werden in Feldern gespeichert. Es gibt kein DI-Framework, keinen Container, keinen Service Locator und keine Registry mit globalem Zustand.
2. **Eine Composition Root:** `internal/app` baut den gesamten Objektgraphen.
   - `app.New(cfg config.Config, opts ...app.Option) (*app.App, error)` erzeugt alle Komponenten, `(*App).Run(ctx)` betreibt sie bis zum Abbruch des Kontexts.
   - `cmd/streamcrew` parst nur die Kommandozeile und die Konfiguration ([Code-ADR-0005](0005-konfiguration.md)) und ruft dann `app`. Die spätere Start-API `core.Run` (Phase 6) ist eine dünne Hülle um denselben Aufruf. *Präzisiert am 2026-09-29, Entscheidung des Projektinhabers: `cmd/streamcrew` enthält nur `main.go` mit Signalen, Umgebung, kong-Initialisierung, Parsen und Exit-Code; die Definition der Kommandozeile (kong-Struktur `cli.Root`, Unterkommandos, Abbildung auf Exit-Codes) liegt in `internal/cli`.*
   - Wird die Verdrahtung groß, wird sie in `internal/app` auf mehrere Dateien je Bereich aufgeteilt, nicht auf weitere Pakete.
3. **Konstruktoren:**
   - Pflichtabhängigkeiten sind Parameter. Bei mehr als drei Abhängigkeiten oder bei optionalen Einstellungen kommen funktionale Optionen dazu, benannt mit `With…` (`WithLogger`, `WithStatusFunc`).
   - Ein Konstruktor gibt einen konkreten Typ zurück, bei Validierung oder I/O zusammen mit einem Fehler.
   - Fehlt eine optionale Abhängigkeit, gilt ein brauchbarer Standard, z. B. ein Logger, der nichts ausgibt. Der Nullwert eines Typs ist nach Möglichkeit nutzbar.
4. **Interfaces beim Konsumenten:** Ein Paket definiert die kleinen Interfaces, die es selbst braucht (ein bis drei Methoden). Interfaces entstehen nur an Grenzen, an denen ein Fake oder eine zweite Implementierung nötig ist, nicht vorsorglich für jeden Typ.
5. **Komponenten kennen `internal/config` nicht.** Jedes Paket hat seine eigene kleine `Config`-Struktur bzw. Optionen. Die Composition Root übersetzt die globale Konfiguration in diese Strukturen. So bleiben Pakete unabhängig testbar, und nur `cmd/streamcrew` (seit 2026-09-29: `internal/cli`) und `internal/app` hängen von der Konfiguration ab.
6. **Erlaubt auf Paketebene** sind nur Konstanten, Sentinel-Fehler, kompilierte reguläre Ausdrücke und `//go:embed`-Daten. Braucht ein anderer Fall eine Ausnahme von `gochecknoglobals`, entscheidet der Projektinhaber, und `nolintlint` verlangt eine Begründung.
7. **Lebenszyklus:**
   - Hintergrundarbeit ist ein Runnable, das die Composition Root beim Supervisor registriert ([Code-ADR-0004](0004-nebenlaeufigkeit-und-supervisor.md)).
   - Ressourcen mit `Close` schließt die Composition Root in umgekehrter Reihenfolge ihrer Erzeugung, nachdem der Supervisor gestoppt ist.
   - Den Logger erzeugt die Composition Root; jede Komponente bekommt ihn mit dem Attribut `component` ([Code-ADR-0003](0003-fehler-und-logging.md)).
8. **Zeit und Zufall** werden nicht über Interfaces injiziert. Zeitabhängiger Code nutzt das Paket `time` und wird mit `testing/synctest` getestet ([Code-ADR-0006](0006-teststrategie.md)). Ob IDs und Uhren später injiziert werden, klärt das Code-ADR zu IDs und Zeit (Phase 2, ADR-Backlog in Plan §12.2).

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `google/wire` (Codegenerierung) | seit August 2025 archiviert; zusätzlicher Generierungsschritt für eine Verdrahtung, die von Hand überschaubar bleibt |
| `uber-go/fx` bzw. `dig` | Verdrahtung per Reflection: Fehler erst zur Laufzeit, schwer nachvollziehbare Startreihenfolge, eigener Lebenszyklus neben dem Supervisor |
| Service Locator oder globale Registry | versteckte Abhängigkeiten und globaler Zustand; widerspricht Plan §6.1 |
| Globale Konfiguration an alle Pakete durchreichen | koppelt jedes Paket an die Struktur der Startkonfiguration; Tests müssten die ganze Konfiguration aufbauen |

## Konsequenzen

**Positiv:**

- Abhängigkeiten sind im Code sichtbar und werden vom Compiler geprüft.
- Jede Komponente lässt sich isoliert mit Fakes testen.
- `streamcrew serve` und die spätere Start-API nutzen denselben Weg in die Verdrahtung, wie ADR-0006 es verlangt.

**Negativ und Risiken:**

- Die Composition Root wächst mit jeder Komponente und braucht Pflege.
- Etwas Boilerplate für Optionen und die Übersetzung der Konfiguration.

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen und den Index in [`README.md`](README.md) anpassen, erledigt 2026-09-29
- [x] `internal/app` als Composition Root anlegen (Roadmap Phase 1.3), erledigt 2026-09-29
