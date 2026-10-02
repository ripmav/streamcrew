# ADR-0022: Internationalisierung

| | |
|---|---|
| **Status** | Vorgeschlagen |
| **Datum** | 2026-10-02 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §6.22, §7.1; Roadmap Phase 3.4 (aus 3.6 vorgezogen), 3.6, Phase 6; [ADR-0001](0001-neuimplementierung-und-nutzung-des-originals.md), [ADR-0010](0010-api-protokoll.md); [Code-ADR-0013](code/0013-typ-registry.md), [Code-ADR-0017](code/0017-klare-signale-statt-magischer-werte.md); Spezifikationen [`requirements.md`](../spec/requirements.md) (B70–B72), [`template.md`](../spec/template.md) (B41) |

## Kontext

- **Plan §6.22:**
  - Alle Texte werden neu geschrieben, mit Englisch als Quellsprache und Deutsch.
  - Der Core liefert Schlüssel mit Parametern, die Frontends lokalisieren selbst.
  - Chat-Ausgaben des Bots, etwa die Meldungen der Anforderungen, erscheinen in der Sprache des Profils und sind anpassbar.
  - Als Bibliotheken nennt der Plan `nicksnyder/go-i18n/v2` und `golang.org/x/text`.
- **Plan §7.1:** Begriffe und i18n-Schlüssel sind in allen Frontends gleich.
- **Konvention der Roadmap:** Neue Texte entstehen immer auf Englisch und Deutsch.
- **Erste Nutzer im Core:**
  - die Meldungen der Anforderungen in Roadmap 3.4: Schlüssel mit Werten wie Rolle, Restzeit, Verwendung oder Betrag, in der Sprache des Profils ([`requirements.md`](../spec/requirements.md), B70–B72)
  - die i18n-Schlüssel der Action-Typen ([Code-ADR-0013](code/0013-typ-registry.md))
  - später Fehler der API ([ADR-0010](0010-api-protokoll.md)), Spiele, Währung und weitere Chat-Ausgaben
- **Sprache des Profils:** Die Roadmap plant die Settings-Sektion „locale“ mit Sprache und Formaten für 3.6. Die Meldungen der Anforderungen brauchen die Sprache aber schon in 3.4.
- **Formate:** Datum, Uhrzeit und Zahlen in Templates folgen der Locale des Profils, sobald es sie gibt ([`template.md`](../spec/template.md), B41). Bis dahin gelten die Formate des Originals.
- **Herkunft:** Texte, Übersetzungen und Ressourcendateien des Originals werden nicht übernommen ([ADR-0001](0001-neuimplementierung-und-nutzung-des-originals.md)).
- **Die Kandidaten** (geprüft 2026-10-02):
  - `github.com/nicksnyder/go-i18n/v2` (v2.6.1, Januar 2026):
    - Meldungen sind Go-Templates (`{{.Name}}`).
    - Pluralformen nach CLDR, eine Pluralzahl je Meldung.
    - Dateien in JSON, TOML oder YAML; das Werkzeug `goi18n` extrahiert und führt zusammen.
    - Das Paket selbst braucht nur die Standardbibliothek und `golang.org/x/text/language`.
  - `golang.org/x/text/message` (v0.42.0, September 2026):
    - Formate wie bei `fmt.Printf` mit Platzhaltern nach Position.
    - Pluralauswahl über `plural.Selectf`.
    - Kataloge entstehen als Go-Code mit dem Werkzeug `gotext`.
  - Unabhängig davon bietet `golang.org/x/text`:
    - `language`: Sprach-Tags und Abgleich
    - `feature/plural`: die Pluralregeln von CLDR (`plural.Cardinal.MatchPlural`)
    - `golang.org/x/text` ist schon eine indirekte Abhängigkeit des Cores.
  - Die Syntax beider Bibliotheken können die Frontends (TypeScript im Web, Plan §7) nicht lesen.

## Entscheidung

1. **Sprachen:** Englisch ist Quell- und Rückfallsprache, dazu kommt Deutsch. Eine weitere Sprache kommt nur mit vollständigem Katalog dazu.
2. **Eigenes kleines Paket `internal/i18n`** auf `golang.org/x/text/language` und `golang.org/x/text/feature/plural`. Keine weitere Bibliothek.
3. **Katalog:** eine JSON-Datei je Sprache, im Binary eingebettet: `internal/i18n/locales/en.json`, `de.json`.
   - **Schlüssel:** verschachtelte Objekte, angesprochen mit Punkten, etwa `requirement.cooldown.user`. Teile bestehen aus Kleinbuchstaben, Ziffern und `_`.
   - **Platzhalter:** benannt als `{{name}}`.
   - **Pluralformen:** eigene Schlüssel mit der Endung der CLDR-Form (`_one`, `_other`; Englisch und Deutsch brauchen nur diese beiden). Gewählt wird nach dem Parameter `count`.
   - Das Format entspricht dem JSON-Format v4 von i18next. So können Web- und Desktop-Frontend die Kataloge des Cores unverändert lesen.
4. **Meldungen sind Werte:** `i18n.Message` mit Schlüssel und benannten Parametern.
   - Gerendert wird erst dort, wo der Text den Core verlässt.
   - **Chat-Ausgaben des Bots** rendert der Core in der Sprache des Profils.
   - **Über die API** gehen Schlüssel und Parameter, die Frontends rendern selbst (Plan §6.22). Die Kataloge des Cores liefert die API mit (Phase 6), damit die Frontends sie nicht kopieren.
   - **Logs, CLI und `doctor`** bleiben englisch und ohne Katalog. Sie dienen der Diagnose und sollen in jedem Supportfall gleich lauten.
5. **Werte in Meldungen:**
   - Text erscheint unverändert.
   - Dauern schreibt das Paket mit eigenen Katalogschlüsseln je Einheit, etwa „2 minutes 5 seconds“ oder „2 Minuten 5 Sekunden“.
   - Zahlen bleiben ohne Tausendertrennzeichen, bis die Formate der Locale kommen (Roadmap 3.6).
6. **Sprache des Profils:** Die Settings-Sektion „locale“ beginnt schon in Roadmap 3.4, mit `language` (`en` oder `de`, Standard `en`). Die Formate kommen in 3.6 als neue Version der Sektion. Eine Sprache ohne Katalog lehnt das Speichern ab.
7. **Schlüssel im Code** sind Konstanten eines eigenen Typs `i18n.Key`, keine verstreuten Zeichenketten. Ein Test prüft:
   - jede Konstante hat in jeder Sprache einen Eintrag
   - jede Sprache hat dieselben Schlüssel
   - jeder Eintrag hat dieselben Platzhalter wie im Englischen
   - jede Pluralmeldung hat alle Formen, die ihre Sprache braucht
   
   Deshalb kann zur Laufzeit kein Schlüssel fehlen; Werkzeuge zum Extrahieren sind nicht nötig.
8. **Rückfall:** Fehlt eine Sprache, gilt Englisch. Fehlt ein Parameter beim Rendern, bleibt sein Platzhalter sichtbar stehen, und das Log warnt. Eine Meldung geht so nie ganz verloren (Code-ADR-0017: Fehler werden sichtbar, statt still verschluckt).
9. **Texte der Typen:** Namen und Beschreibungen der Action-Typen und Anforderungsarten (Code-ADR-0013) stehen ebenfalls im Katalog des Cores. Alle Frontends zeigen so dieselben Texte.
10. **Anpassbare Chat-Meldungen** (Plan §6.22) überschreiben je Profil einzelne Schlüssel, mit denselben Platzhaltern. Sie kommen als eigene Aufgabe (P1); bis dahin gilt der Katalog.
11. **Herkunft:** Alle Texte sind eigene Worte. Keine stammen aus dem Original oder seinen Übersetzungen (ADR-0001).

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `nicksnyder/go-i18n/v2` | Bewährt und klein. Die Go-Templates in den Meldungen können die Frontends aber nicht lesen; sie bräuchten eigene Kopien der Texte, entgegen Plan §7.1. Bei zwei Sprachen mit je zwei Pluralformen spart die Bibliothek wenig. |
| `golang.org/x/text/message` mit `gotext` | Platzhalter nach Position sind für Übersetzer fehleranfällig. Kataloge als erzeugter Go-Code sind für Frontends nicht lesbar. `gotext` ist ein zusätzlicher Schritt im Build. |
| ICU MessageFormat | Mächtiger als nötig (Auswahl, verschachtelte Plurale). Für Go gibt es dafür keine gepflegte Implementierung. |
| gettext (PO-Dateien) | Werkzeuge außerhalb von Go; die Frontends bräuchten eine Umwandlung. |
| Texte als Go-Code ohne Katalog | Für Frontends und Übersetzer nicht lesbar; die Vollständigkeit ließe sich nur mühsam prüfen. |
| Profilsprache erst mit der ganzen Sektion „locale“ in 3.6 | Die Meldungen der Anforderungen kämen bis dahin nur auf Englisch. |

## Konsequenzen

**Positiv:**

- **Eine Quelle für Texte:** Core und Frontends nutzen dieselben Kataloge in einem Format, das beide lesen.
- **Wenig Abhängigkeiten:** nur Pakete aus `golang.org/x/text`, das der Core schon indirekt nutzt.
- **Keine Laufzeitfehler durch fehlende Texte:** Die Tests stellen die Vollständigkeit sicher.
- **Meldungen als Werte:** Schlüssel und Parameter lassen sich in Tests, Logs und Ereignissen prüfen, ohne Texte zu vergleichen.

**Negativ und Risiken:**

- **Eigener Code für Rendern und Plurale:** Er ist klein, muss aber gepflegt werden.
- **Format an i18next angelehnt:** Bricht i18next sein Format, müssen die Frontends nachziehen. Dagegen hilft, dass der Core nur den einfachen Teil nutzt: Platzhalter und Pluralendungen.
- **Englische Diagnose:** Nutzer ohne Englisch lesen Logs und CLI schwerer. Das ist für die Diagnose in Kauf genommen.

**Folgearbeiten:**

- [ ] `internal/i18n` mit den Katalogen EN und DE, Pluralen, Dauern, Rückfall und den Tests aus Punkt 7 (Roadmap 3.4)
- [ ] Settings-Sektion „locale“ mit `language` (Roadmap 3.4); Formate als neue Version (Roadmap 3.6)
- [ ] Meldungen der Anforderungen als `i18n.Message` (Roadmap 3.4)
- [ ] Kataloge über die API (Phase 6); Web- und Desktop-Frontend lesen sie
- [ ] Namen und Beschreibungen der Action-Typen und Anforderungsarten im Katalog, mit dem Typkatalog der API (Roadmap 3.5 und Phase 6)
- [ ] Anpassbare Chat-Meldungen je Profil (P1)
