# Offene Findings: Nachprüfung des Fix-Branches (Code-Review 2026-10-24)

- **Grundlage:** [`code-review-2026-10-24.md`](code-review-2026-10-24.md) und
  [`fix-plan-2026-10-24.md`](fix-plan-2026-10-24.md)
- **Geprüft:** Branch `fix/code-review-2026-10-24` bei `d10dfe6` (6 Commits über
  `main` bei `313b633`)
- **Tools:** Alle Prüfungen waren grün, Details im Abschnitt
  [„Tools bei der Nachprüfung“](#tools-bei-der-nachprüfung).

Die Prioritäten 1–5 des Reviews sind weitgehend umgesetzt. Die Punkte unten
sind noch offen: Entweder ist der Fix unvollständig, oder der Fix hat einen
neuen Befund mitgebracht.

## 1. Unvollständig umgesetzt

| # | Sev. | Befund | Ort | Bezug |
|---|---|---|---|---|
| S3 | Mittel | **Der Throttle für fehlgeschlagene Logins wirkt nicht.** `authLimiter.record` zählt je `r.RemoteAddr`, und dieser Wert ist `ip:port`. Jede neue TCP-Verbindung ist damit ein neuer Schlüssel. Hinzu kommt: Ab 1024 Einträgen setzt sich die Map komplett zurück, ein Angreifer leert sie also mit wechselnden Ports. Der Test merkt das nicht, weil `httptest` immer dieselbe Adresse liefert. **Fix:** den Schlüssel per `net.SplitHostPort` auf die IP kürzen, alte Einträge gezielt entfernen statt die ganze Map zu leeren, und im Test zwei Ports derselben IP prüfen. | `internal/api/token.go:39-60`, `:80` | ADR-0013 §5 |
| T1 | Mittel | **„Nie beide“ wird bei Twitch-Massengeschenken weiter verletzt.** Das `ErrShape` ist behoben. Ab der Schwelle löst jetzt aber das Sammelereignis aus, und die einzelnen `sub_gift`-Meldungen derselben Aktion lösen trotzdem aus. `subGiftNotice` liest `community_gift_id` nicht, und nichts unterdrückt die Einzelmeldungen. **Fix:** `community_gift_id` mappen und die Einzelgeschenke einer Sammelaktion ab der Schwelle unterdrücken. Unter der Schwelle laufen sie weiter. | `internal/twitch/map.go:585-591`, `:629-645`, `internal/eventservice/receive.go:356-380` | events.md B5, twitch-events.md B21 |
| U3 | Niedrig | **`--strikes 0` lässt sich weiter nicht setzen**, weil `user_api.go:118` nur bei `c.Strikes > 0` sendet. Außerdem lässt sich der Titel über die CLI nicht mehr leeren: `--title ""` behält ihn, eine Option zum Leeren gibt es nicht. **Fix:** Pointer-Flags oder kongs `IsSet` nutzen und ein eigenes `--clear-title` anbieten. | `internal/cli/user_api.go:96-120` | `docs/usage.md` |
| T4 | Niedrig | Ein Emote aus einem Zeichen (`id:5-5`) bekommt keinen Text, weil die Bedingung noch `end > start` aus der exklusiven Zählung trägt. Richtig wäre `end >= start`. Nebenbei wird `[]rune(text)` in der Schleife für jedes Emote neu gebildet. | `internal/twitch/irc.go:633-635` | moderation B7 |
| U2 | Info | Die Ranglisten filtern jetzt in SQL. `userservice.Service.Rankings` hat weiter keinen Aufrufer außerhalb der Tests und ist toter Code. Entweder löschen oder vom API-Adapter nutzen lassen. | `internal/userservice/userservice.go:257` | users-and-roles.md B12 |

## 2. Neue Befunde aus den Fixes

| # | Sev. | Befund | Ort | Bezug |
|---|---|---|---|---|
| N1 | Mittel | **Die CLI schickt das lokale Admin-Token an fremde Server.** `Env.token()` liest `desktop-token` bzw. `api.token` auch dann, wenn `--server`/`STREAMCREW_SERVER` auf einen entfernten Host zeigt, notfalls über http. **Fix:** die Token-Dateien nur lesen, wenn der Server lokal ist (Loopback, Unix-Socket oder `runtime.json` des Datenverzeichnisses). | `internal/cli/api.go:42-68` | ADR-0013 §5 |
| N2 | Niedrig | **Eine Rücknahme des lokalen Tokens hält nur bis zum nächsten Neustart.** Ist der Wert aus `api.token` nicht mehr im Store, registriert `ensureLocalToken` ihn als „Store gelöscht“ neu. Das hebt auch ein bewusstes `streamcrew token revoke admin` wieder auf. **Fix:** nur neu registrieren, wenn der Store leer ist, oder ein zurückgenommenes Token an seiner ID erkennen. | `internal/app/runtime.go` (`ensureLocalToken`) | ADR-0013 §5 |
| N3 | Niedrig | **Zwei Texte stimmen nicht.** Die Hilfe zu `--bootstrap-token` sagt „Default: a generated token, shown once in the log“. Tatsächlich schreibt der Daemon die Datei und loggt nur den Pfad, der Server erzeugt gar kein Token. Außerdem loggt `registerToken` immer „from STREAMCREW_TOKEN“, auch beim selbst erzeugten lokalen Token. | `internal/config/config.go:48`, `internal/app/runtime.go` (`registerToken`) | — |
| N4 | Niedrig | **`doctor` mit TLS und eigenem Zertifikat:** `probeCore` nutzt `http.DefaultClient`. Mit einem selbstsignierten Zertifikat scheitert die https-Probe, und ein laufender Core gilt weiter als FAIL. | `internal/doctor/doctor.go` (`probeCore`) | ADR-0013 §7 |

## 3. Zurückgestellt mit Entscheidung

| # | Stand |
|---|---|
| V3/C14 | Am 2026-10-24 entschieden, festgehalten im Branch `docs/config-api-rechte-nur-einschraenken` (Nachtrag zu Code-ADR-0021). `ConfigService/Update` darf Rechte, `file-root`, `outbound-allow`, `mode`, `cors-allow`, `listen`, TLS und `dev` nur einschränken, gemessen am aktuell wirksamen Stand. Danach wird `serve` verdrahtet. Die Umsetzung ist offen (Roadmap 6.5). |
| V3, Rückfrage | Ebenfalls offen: Soll `log-file` über die API änderbar bleiben? Ein Admin-Token könnte damit Log-Zeilen an einen beliebigen Pfad schreiben, auf den der Core Schreibrechte hat. Darin stehen auch Inhalte, die Angreifer steuern, etwa Chat-Texte. |

## 4. Nicht angegangen (laut Fix-Plan bewusst außerhalb der Prioritäten)

Die Einzelheiten stehen im Review. Offen sind:

- **Sicherheit:** S5, S6 (nur minimal umgesetzt: die CLI nutzt den Unix-Socket und `runtime.json` weiter nicht), S8–S12
- **Twitch:** T5–T17
- **Nutzer und API:** U4–U9
- **Commands und Actions:** A1–A10
- **Code-ADRs:** C2–C13
- **Doku:** D7, D8
- **Unterhalb der Schwelle (Review §7):** kanalübergreifende Rollen, IRC-CRLF-Injection (zusammen mit T14)

## 5. Prozess

- **Signaturen:** Die 6 Commits des Branches sind nicht GPG-signiert (`git log --format=%G?` zeigt `N`).
- **Stack:** Alles liegt auf einem Branch statt in einem Stack mit einem PR je Bucket, wie der Fix-Plan es vorsieht. Der Doku-Commit (D1–D6) liegt oben statt ganz unten im Stack.
- **Footer:** `Assisted-by: Claude (high) via Pi` nennt kein Modell, und `Co-Authored-By` fehlt.
- **Test-Matrix:** Für die neue Datei `internal/api/token_auth_test.go` gibt es keine Matrix in `docs/tests/`.
- **Commit-Titel:** In `docs:Doku…` und `refactor:encoding…` fehlt nach dem Doppelpunkt das Leerzeichen.

## Tools bei der Nachprüfung

| Schritt | Ergebnis |
|---|---|
| `gofmt -l`, `go fix -diff`, `go vet` | keine Befunde |
| `golangci-lint` | 0 issues |
| `govulncheck` | No vulnerabilities found |
| `scripts/sandbox.sh go test ./...` | alle Pakete grün |
