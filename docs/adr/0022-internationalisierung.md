# ADR-0022: Internationalisierung

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-10-02 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §6.22, §7.1; Roadmap Phase 3.4 (aus 3.6 vorgezogen), 3.6, Phase 6; [ADR-0001](0001-neuimplementierung-und-nutzung-des-originals.md), [ADR-0010](0010-api-protokoll.md); [Code-ADR-0013](code/0013-typ-registry.md), [Code-ADR-0017](code/0017-klare-signale-statt-magischer-werte.md), [Code-ADR-0018](code/0018-json-v2.md); Spezifikationen [`requirements.md`](../spec/requirements.md) (B70–B72), [`template.md`](../spec/template.md) (B41) |

## Kontext

- **Plan §6.22:**
  - Alle Texte werden neu geschrieben, mit Englisch als Quellsprache und Deutsch.
  - Der Core liefert Schlüssel mit Parametern, die Frontends lokalisieren selbst.
  - Chat-Ausgaben des Bots, etwa die Meldungen der Anforderungen, erscheinen in der Sprache des Profils und sind anpassbar.
- **Plan §7.1:** Begriffe und i18n-Schlüssel sind in allen Frontends gleich.
- **Konvention der Roadmap:** Neue Texte entstehen immer auf Englisch und Deutsch.
- **Erste Nutzer im Core:**
  - die Meldungen der Anforderungen in Roadmap 3.4: Schlüssel mit Werten wie Rolle, Restzeit, Verwendung oder Betrag, in der Sprache des Profils ([`requirements.md`](../spec/requirements.md), B70–B72)
  - die i18n-Schlüssel der Action-Typen ([Code-ADR-0013](code/0013-typ-registry.md))
  - später Fehler der API ([ADR-0010](0010-api-protokoll.md)), Spiele, Währung und weitere Chat-Ausgaben
- **Sprache des Profils:** Die Roadmap plant die Settings-Sektion „locale“ mit Sprache und Formaten für 3.6. Die Meldungen der Anforderungen brauchen die Sprache aber schon in 3.4.
- **Formate:** Datum, Uhrzeit und Zahlen in Templates folgen der Locale des Profils, sobald es sie gibt ([`template.md`](../spec/template.md), B41).
- **Herkunft:** Texte, Übersetzungen und Ressourcendateien des Originals werden nicht übernommen ([ADR-0001](0001-neuimplementierung-und-nutzung-des-originals.md)).
- **Format der Texte:** Der Projektinhaber will ICU MessageFormat. Es ist der verbreitete Standard für Plural und Auswahl in Meldungen. Die Frontends lesen es mit FormatJS (`intl-messageformat`) oder i18next mit `i18next-icu`.
- **Bibliotheken für Go**, geprüft am 2026-10-02 mit Probeprogrammen auf Englisch und Deutsch:
  - **`kaptinlin/go-i18n`** (v0.6.3):
    - Zahlen bleiben unformatiert, weil es `messageformat-go/mf1` in v0.8.2 einbindet; 1234567.5 wird zu `1.2345675e+06`.
    - Mit `mf1` v0.8.6, das richtig formatiert, lässt es sich nicht bauen, weil sich die API zwischen Patch-Versionen geändert hat.
    - Fehlende Schlüssel und Werte bleiben still.
    - Es nutzt `go-json-experiment/json` statt `encoding/json/v2` ([Code-ADR-0018](code/0018-json-v2.md)).
  - **`kaptinlin/messageformat-go/mf1`** (v0.8.6), direkt genutzt:
    - funktioniert
    - Die Kette mit `agentable/go-intl` (öffentlich seit Mai 2026, 2 Sterne) stammt von einem einzigen Entwickler.
    - API-Brüche in Patch-Versionen, rund 5,7 MB mehr im Binary
  - **`fullpipe/icu-mf`** (v1.0.1, April 2025):
    - Das Escaping mit Apostroph ist fehlerhaft.
    - Ein fehlender Wert ersetzt die ganze Meldung durch den Schlüssel.
    - Kataloge nur als YAML; es nutzt das archivierte `github.com/pkg/errors`.
  - **`romshark/icumsg`** (v0.3.3): nur ein Tokenizer, korrekt, aber ein Entwickler mit einem Stern.
  - **Nicht mehr gepflegt oder ohne Lizenz:** `gotnospirit/messageformat` (2023), `modern-go/msgfmt` (2018), `sjansen/messageformat`, `altipla-consulting/messageformat` (archiviert), `go-slim/icu` (keine Lizenz).
  - **`golang.org/x/text`** (v0.42.0) liefert, was ein eigener Renderer braucht, und ist schon eine indirekte Abhängigkeit des Cores:
    - `language`: Sprach-Tags und Abgleich
    - `feature/plural`: Pluralregeln nach CLDR für Grund- und Ordinalzahlen
    - `number` mit `message`: Zahlen und Prozent nach Sprache, etwa `1.234.567,5` und `25 %`

## Entscheidung

1. **Sprachen:** Englisch ist Quell- und Rückfallsprache, dazu kommt Deutsch. Eine weitere Sprache kommt nur mit vollständigem Katalog und ihren Pluralformen dazu.
2. **Format: ICU MessageFormat v1, im Teilumfang aus Punkt 3.** Die Kataloge sind flache JSON-Objekte, eine Datei je Sprache, im Binary eingebettet: `internal/i18n/locales/en.json` und `de.json`.
   - **Schlüssel:** Zeichenketten aus Teilen mit Punkten dazwischen, etwa `requirement.cooldown.user`. Teile bestehen aus Kleinbuchstaben, Ziffern und `_`.
   - **Werte:** ICU-Meldungen.
   - FormatJS liest diese Dateien unverändert, i18next mit `i18next-icu` und ohne Trennzeichen für Schlüssel.
3. **Teilumfang:**
   - Text mit dem Escaping von ICU (seit ICU 4.8):
     - `''` ergibt einen Apostroph.
     - Ein Apostroph direkt vor `{` oder `}`, in `plural` und `selectordinal` auch vor `#`, beginnt wörtlichen Text bis zum nächsten einzelnen Apostroph.
     - Jeder andere Apostroph ist ein gewöhnliches Zeichen, etwa in `it's`.
     - Lesbarer Text in den Katalogen nutzt den typografischen Apostroph (’), wie es das ICU-Handbuch empfiehlt.
   - `{name}`: der Wert als Text. Zahlen erscheinen hier ohne Formatierung nach Sprache; dafür steht `{name, number}`.
   - `{name, number}`, `{name, number, integer}` und `{name, number, percent}`: Zahlen nach der Sprache.
   - `{name, plural, …}`: Fälle `=N` und die Pluralformen von CLDR für die Sprache, `other` ist Pflicht. `#` steht für die Zahl im Format der Sprache.
   - `{name, selectordinal, …}`: wie `plural`, mit den Ordinalformen.
   - `{name, select, …}`: Fälle aus Bezeichnern, `other` ist Pflicht.
   - Verschachtelung in den Fällen von `plural`, `selectordinal` und `select`.
   - **Nicht unterstützt** und beim Laden abgelehnt:
     - `date`, `time`, `spellout`, `ordinal`, `duration`
     - `currency`, Zahlen-Skeletons (`::…`) und eigene Zahlenmuster
     - `offset:` in `plural`
4. **Eigenes Paket `internal/i18n`** mit einem eigenen Parser und Renderer. Es nutzt nur die Standardbibliothek und `golang.org/x/text` (`language`, `feature/plural`, `number`, `message`); eine weitere Bibliothek kommt nicht dazu.
   - Der Parser erzeugt einen Syntaxbaum.
   - Beim Laden prüft das Paket den Teilumfang und die Fälle von `plural` und `selectordinal` gegen die Pluralregeln der Sprache. Eine Pluralform, die die Sprache nicht kennt, etwa `few` im Deutschen, ist ein Fehler.
   - Ein Fuzz-Test sichert den Parser ab.
5. **Meldungen sind Werte:** `i18n.Message` mit Schlüssel und benannten Werten.
   - Gerendert wird erst dort, wo der Text den Core verlässt.
   - **Chat-Ausgaben des Bots** rendert der Core in der Sprache des Profils.
   - **Über die API** gehen Schlüssel und Werte, die Frontends rendern selbst (Plan §6.22). Die Kataloge des Cores liefert die API mit (Phase 6), damit die Frontends sie nicht kopieren.
   - **Logs, CLI und `doctor`** bleiben englisch und ohne Katalog. Sie dienen der Diagnose und sollen in jedem Supportfall gleich lauten.
6. **Werte in Meldungen:** Text, ganze Zahlen und Zahlen mit Nachkommastellen. Dauern schreibt das Paket selbst aus Katalogmeldungen je Einheit, etwa `{n, plural, one {# minute} other {# minutes}}`. So entstehen „2 minutes 5 seconds“ oder „2 Minuten 5 Sekunden“.
7. **Sprache des Profils:** Die Settings-Sektion „locale“ beginnt schon in Roadmap 3.4, mit `language` (`en` oder `de`, Standard `en`). Die Formate kommen in 3.6 als neue Version der Sektion. Eine Sprache ohne Katalog lehnt das Speichern ab.
8. **Schlüssel im Code** sind Konstanten eines eigenen Typs `i18n.Key`, keine verstreuten Zeichenketten. Tests prüfen:
   - jede Konstante hat in jeder Sprache einen Eintrag
   - jede Sprache hat dieselben Schlüssel
   - jede Meldung hat in jeder Sprache dieselben Werte, und jeder Wert dieselbe Art (Text, Zahl, Auswahl)
   - jedes `plural` und `selectordinal` hat alle Formen, die seine Sprache braucht, nicht nur `other`

   Deshalb kann zur Laufzeit kein Schlüssel fehlen.
9. **Fehler beim Rendern:** Ein fehlender oder unpassender Wert ist ein Programmierfehler. Das Rendern meldet ihn als Fehler; der Aufrufer schreibt ihn ins Log und sendet die Meldung nicht (Code-ADR-0017: klare Signale statt halber Texte). Fehlt eine Meldung in der Sprache des Profils, gilt Englisch.
10. **Texte der Typen:** Namen und Beschreibungen der Action-Typen und Anforderungsarten (Code-ADR-0013) stehen ebenfalls im Katalog des Cores. Alle Frontends zeigen so dieselben Texte.
11. **Anpassbare Chat-Meldungen** (Plan §6.22):
    - Sie überschreiben je Profil einzelne Schlüssel, im selben Teilumfang.
    - Das Speichern prüft sie wie den Katalog, auch, ob sie nur Werte nutzen, die der Schlüssel kennt.
    - Sie kommen als eigene Aufgabe (P1); bis dahin gilt der Katalog.
12. **Herkunft:** Alle Texte sind eigene Worte. Keine stammen aus dem Original oder seinen Übersetzungen (ADR-0001).

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| `kaptinlin/go-i18n` | Zahlen bleiben unformatiert; mit der aktuellen Version seiner MessageFormat-Bibliothek lässt es sich nicht bauen; Fehler bleiben still; zweite JSON-Bibliothek neben `encoding/json/v2` (siehe Kontext). |
| `kaptinlin/messageformat-go/mf1` direkt | Funktioniert, aber die Kette stammt von einem einzigen Entwickler, bricht ihre API in Patch-Versionen und bringt rund 5,7 MB ins Binary. |
| `romshark/icumsg` als Tokenizer, eigener Renderer | Weniger eigener Code, aber eine Fremdabhängigkeit eines einzelnen Entwicklers mit einem Stern; Entscheidung des Projektinhabers für den eigenen Parser. |
| `fullpipe/icu-mf` | Fehlerhaftes Escaping, stille Fehler, Kataloge nur als YAML, archivierte Abhängigkeit `github.com/pkg/errors`. |
| `nicksnyder/go-i18n/v2` | Meldungen als Go-Templates, die die Frontends nicht lesen können; kein ICU. |
| `golang.org/x/text/message` mit `gotext` | Platzhalter nach Position; Kataloge als erzeugter Go-Code, für Frontends nicht lesbar. |
| JSON-Format v4 von i18next ohne ICU | Für die Frontends lesbar, aber Plural und Auswahl stehen in getrennten Schlüsseln; der Projektinhaber will ICU. |
| gettext (PO-Dateien) | Werkzeuge außerhalb von Go; die Frontends bräuchten eine Umwandlung. |
| Texte als Go-Code ohne Katalog | Für Frontends und Übersetzer nicht lesbar; die Vollständigkeit ließe sich nur mühsam prüfen. |
| Profilsprache erst mit der ganzen Sektion „locale“ in 3.6 | Die Meldungen der Anforderungen kämen bis dahin nur auf Englisch. |

## Konsequenzen

**Positiv:**

- **Ein verbreiteter Standard:** Core und Frontends nutzen dieselben Kataloge in ICU MessageFormat.
- **Keine neue Abhängigkeit:** nur `golang.org/x/text`, das der Core schon indirekt nutzt; das Binary wächst nur wenig.
- **Volle Kontrolle:** Parser und Prüfungen sind so streng, wie es der Core braucht. Fehler fallen beim Laden und in den Tests auf, nicht beim Zuschauer.
- **Meldungen als Werte:** Schlüssel und Werte lassen sich in Tests, Logs und Ereignissen prüfen, ohne Texte zu vergleichen.

**Negativ und Risiken:**

- **Eigener Parser:** etwa 400 bis 500 Zeilen, die gepflegt werden müssen. Der Fuzz-Test und Tests mit den Beispielen der ICU-Doku halten das Risiko klein.
- **Nur ein Teilumfang:** Die Frontends verstehen mehr ICU als der Core. Übersetzer müssen beim Teilumfang bleiben; das Laden und die Tests lehnen alles andere ab.
- **Englische Diagnose:** Nutzer ohne Englisch lesen Logs und CLI schwerer. Das ist für die Diagnose in Kauf genommen.

**Folgearbeiten:**

- [x] `internal/i18n`: Parser und Renderer für den Teilumfang, Kataloge EN und DE, Dauern, Rückfall, die Tests aus Punkt 8 und ein Fuzz-Test (Roadmap 3.4), erledigt 2026-10-02; der Rückfall auf Englisch wird erst mit den anpassbaren Meldungen nötig, weil die Kataloge vollständig sein müssen
- [ ] Settings-Sektion „locale“ mit `language` (Roadmap 3.4); Formate als neue Version (Roadmap 3.6)
- [ ] Meldungen der Anforderungen als `i18n.Message` (Roadmap 3.4)
- [ ] Kataloge über die API (Phase 6); Web- und Desktop-Frontend lesen sie
- [ ] Namen und Beschreibungen der Action-Typen und Anforderungsarten im Katalog, mit dem Typkatalog der API (Roadmap 3.5 und Phase 6)
- [ ] Anpassbare Chat-Meldungen je Profil (P1)
