# Code-ADR-0009: IDs und Zeit

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-09-29 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §6.7, §6.13, §6.22, §8; Roadmap Phase 2.2; [Code-ADR-0002](0002-dependency-injection.md), [Code-ADR-0006](0006-teststrategie.md), [Code-ADR-0008](0008-datenbankzugriff.md) |

## Kontext

- Entitäten (Nutzer, Commands, Währungen …) und Ereignisse brauchen IDs, die ohne zentrale Vergabe eindeutig sind, sich chronologisch sortieren lassen und in Datenbank, API und Commands als Code gleich aussehen (Plan §6.7: Ereignis-ID als UUIDv7).
- Go 1.27 bringt das Paket `uuid` in der Standardbibliothek mit (geprüft 2026-09-29). `uuid.NewV7` liefert UUIDv7, die aufsteigend sortiert sind, solange die Systemuhr nicht zurückspringt. Plan §8 nannte noch `github.com/google/uuid` oder eine eigene Implementierung.
- Die Roadmap sah eine injizierbare Uhr vor. [Code-ADR-0002](0002-dependency-injection.md) hat bereits entschieden, Zeit nicht über Interfaces zu injizieren, und die Frage an dieses ADR verwiesen. Zeitabhängige Tests laufen in `testing/synctest` ([Code-ADR-0006](0006-teststrategie.md)).
- Jedes Profil hat eine IANA-Zeitzone; `time/tzdata` ist eingebettet (Plan §6.22).

## Entscheidung

1. **IDs sind UUIDv7 aus dem Standardpaket `uuid`.**
   - Ein eigener Typ `id.ID` im Paket `internal/domain/id` kapselt `uuid.UUID`. Er ergänzt, was das Standardpaket nicht mitbringt: `database/sql`-Scanner und -Valuer (als Text) sowie einen nutzbaren Nullwert mit `IsZero`.
   - `id.New()` erzeugt eine UUIDv7, `id.Parse` liest die Textform.
   - Wo Verwechslungen naheliegen, bekommen Entitäten eigene Typen auf dieser Basis (`type UserID id.ID`); sonst genügt `id.ID`.
   - In Datenbank, JSON und YAML stehen IDs in der kanonischen Textform mit Kleinbuchstaben (36 Zeichen). Sie sortiert chronologisch wie die UUID selbst.
2. **IDs der Plattformen bleiben, was die Plattform liefert:** Zeichenketten wie die Twitch-Nutzer-ID, ohne Umwandlung. Sie stehen neben der eigenen ID (Plan §6.13, `user_identities`).
3. **Keine injizierbare Uhr.** Code ruft `time.Now()` direkt auf, Dauern misst er mit `time.Since`.
   - Tests für Zeitverhalten laufen in `testing/synctest`, dessen Uhr auch `time.Now()` und Timer umfasst; auch `uuid.NewV7` nutzt in einer Bubble diese Uhr.\*
   - Eine Uhr als Interface käme erst für Code in Frage, der außerhalb einer Bubble laufen muss und trotzdem eine feste Zeit braucht; das entscheidet dann ein ergänzendes Code-ADR.
4. **Speicherung von Zeit:**

   | Ort | Form |
   |---|---|
   | Go | `time.Time` in UTC, bei der Anzeige in die Zeitzone des Profils umgerechnet |
   | Datenbank | `INTEGER`, Unix-Millisekunden in UTC ([Code-ADR-0008](0008-datenbankzugriff.md)) |
   | JSON, YAML, API | RFC 3339 in UTC mit Millisekunden, z. B. `2026-09-29T10:00:00.000Z` |
   | Dauern | in Go `time.Duration`; in der Datenbank `INTEGER` in Millisekunden; in YAML und Konfiguration als Go-Dauer (`30s`, `5m`) |

   Millisekunden reichen für Chat und Ereignisse und bleiben in JavaScript-Frontends verlustfrei.
5. **Zeitzonen:** Jedes Profil speichert eine IANA-Zeitzone (Standard: die des Systems beim Anlegen, sonst UTC). Zeitpläne wie tägliche Backups oder Timer laufen in dieser Zone; auch Sommerzeitwechsel richten sich nach ihr.

*\* Redaktionell ergänzt am 2026-09-29: Der UUIDv7-Generator der Standardbibliothek ist prozessweit. Entsteht zwischen zwei IDs in einer Bubble eine ID mit echter Zeit, etwa in einem parallelen Test, wirkt das für den Generator wie ein Rücksprung der Uhr, und die IDs der Bubble sind nicht mehr aufsteigend (gemessen: 351 von 2000 Paaren). Tests, die die Reihenfolge von IDs prüfen, laufen deshalb nicht parallel zu anderen Tests, die IDs erzeugen. Im Betrieb gibt es keine Bubbles; die Entscheidung ändert sich nicht.*

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `github.com/google/uuid` | verbreitet, aber seit Go 1.27 durch die Standardbibliothek ersetzbar |
| fortlaufende Integer-IDs | kurz und schnell, aber nur innerhalb einer Datenbank eindeutig; beim Import, bei Commands als Code und bei verknüpften Konten entstünden Konflikte |
| ULID, KSUID, Snowflake | ähnliche Eigenschaften wie UUIDv7, aber nicht standardisiert und nicht in der Standardbibliothek |
| UUID als 16-Byte-`BLOB` in der Datenbank | halb so groß, aber im SQLite-Werkzeug unlesbar; bei den erwarteten Datenmengen unerheblich |
| injizierbare Uhr (`Clock`-Interface) | ein Parameter mehr in vielen Konstruktoren; `synctest` deckt dieselben Tests ohne Umbau ab |
| Zeit als Text in der Datenbank | lesbar, aber größer, langsamer zu vergleichen und anfällig für gemischte Formate |
| Unix-Sekunden oder -Nanosekunden | Sekunden sind für Chat-Reihenfolgen zu grob; Nanosekunden verlieren in JavaScript Genauigkeit |

## Konsequenzen

**Positiv:**

- Keine Abhängigkeit für IDs; IDs sind überall gleich lesbar und chronologisch sortierbar.
- Zeitabhängiger Code braucht keinen Umbau für Tests.
- Einheitliche Zeitformate in Datenbank, API und Dateien.

**Negativ und Risiken:**

- Springt die Systemuhr zurück, sind neue UUIDv7 kurzzeitig nicht mehr aufsteigend. IDs bleiben trotzdem eindeutig; nur die Sortierung nach ID weicht dann von der Erzeugungsreihenfolge ab.
- Eigene Typen je Entität bedeuten Umwandlungen an den Grenzen (sqlc liefert Zeichenketten).
- Zeitpunkte vor 1970 oder mit Nanosekunden-Genauigkeit lassen sich nicht verlustfrei speichern; beides wird nicht gebraucht.

**Folgearbeiten:**

- [x] Nach der Annahme den Status setzen und den Index in [`README.md`](README.md) anpassen, erledigt 2026-09-29
- [x] `internal/domain/id` umsetzen (Roadmap Phase 2.2), erledigt 2026-09-29; Plan §8 (IDs) steht auf der Standardbibliothek
- [x] Zeitzone als Profileinstellung (Roadmap Phase 2.2, Settings-Sektion „Zeit“), erledigt 2026-09-29
