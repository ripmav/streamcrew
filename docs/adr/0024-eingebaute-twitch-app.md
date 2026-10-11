# ADR-0024: Eingebaute Twitch-App für den Login

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-10-11 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | [ADR-0012](0012-persistenz.md), [ADR-0013](0013-sicherheitsmodell.md), [ADR-0014](0014-oauth-und-app-credentials.md), [ADR-0023](0023-twitch-login-authorization-code-flow.md); Plan §12 |

## Kontext

[ADR-0023](0023-twitch-login-authorization-code-flow.md) hat den
Authorization Code Flow als Standard-Login entschieden, gebunden an ein
Client-Secret (Confidential Client; Twitch unterstützt kein PKCE). Das Secret
kommt dabei vom Nutzer (BYO-App): Jeder Nutzer muss einmalig eine eigene
App in der Twitch-Developer-Konsole anlegen, die Redirect-URL registrieren
und ID und Secret bei `auth login` angeben. Ohne App bleibt nur der
Device-Code-Flow-Fallback, dessen UX („Gerät aktivieren") der Projektinhaber
verworfen hat — der erste Login des Standardwegs ist also ohne Einrichtung
nicht möglich.

Die Ökosystem-Recherche (2026-10-11) zeigt die zwei etablierten Muster:

- **MixItUp** (lokaler Desktop-Bot, Open Source) liefert eine **eingebaute
  App für alle Nutzer**: die Client-ID liegt im Quellcode, das Secret ist in
  der verteilten Binary eingebettet (geschlossene Build-Ebene), der Flow ist
  der Authorization Code Flow mit Loopback-Redirect; der Refresh läuft ohne
  Secret. Das Secret ist damit an alle Nutzer verteilt, die Binary ist der
  Wertschutz.
- **StreamElements** (Hosted-Service) hält das Secret **nur serverseitig**
  und verteilt keine Credentials — für streamcrew nicht übernehmbar, weil der
  Core local-first ohne Projekt-Backend läuft (ADR-0003).

Der Projektinhaber hat 2026-10-11 entschieden, ebenfalls eine eingebaute
App bereitzustellen, damit der Standard-Login ohne Einrichtung funktioniert;
der BYO-Ansatz bleibt optional erhalten.

## Entscheidung

1. **Eingebaute App:** Die Core-Binary enthält die Credentials (Client-ID
   und Client-Secret) einer Confidential Twitch-App „StreamCrew" (neu
   angelegt, eine App für beide Konten). Sie ist die **Standard-App des
   Authorization Code Flows**: Ein Login ohne `--client-id`/`--client-secret`
   verwendet sie, wenn weder der Vault noch die Flags eine App liefern.
2. **Verschlüsselung:** Die Credentials liegen im Binary nicht als
   Klartext, sondern als hybrider postquantum-fähiger Verschluss, allein mit
   der Go-Standardlibrary (keine neue Abhängigkeit):
   - **ML-KEM-768** (`crypto/mlkem`, NIST-PQC-KEM): kapselt ein einzeitiges
     Shared Secret.
   - **HKDF-SHA256** (`crypto/hkdf`) leitet daraus den Datenkey ab.
   - **AES-256-GCM** verschlüsselt das App-Record (Client-ID + Secret).
   Im Binary ruhen der ML-KEM-Private-Key, der KEM-Ciphertext und der
   GCM-Payload — in keinem Format, das ein Mensch oder ein `strings`-Scan
   als Credential erkennt. Dekodiert wird das Record einmalig beim ersten
   Login nach dem Start (im Speicher geachtet, nie in Logs — das Secret ist
   ein `logging.Secret`).
3. **Threat-Modell (ohne Übertreibung):** Der Verschluss schützt vor
   *kasualer* Extraktion (Repo-, Binary- und Image-Scans, `strings`,
   Diffs) und gegen Harvest-now-decrypt-later für abgegriffene Artefakte.
   Er schützt **nicht** vor einem Angreifer, der die Binary ausführen kann:
   Laufzeit-Extraktion ist möglich. Das Secret ist damit verteilte
   Infrastruktur mit Rotationsplan (Punkt 5) zu behandeln.
4. **Ladereihenfolge** (Login und Refresh):
   `--client-id`/`--client-secret` (BYO; wird wie bisher im Vault
   gespeichert) → Vault (App des letzten Logins) → **eingebaute App** →
   Fehler. Das eingebaute Secret wird **nicht** zusätzlich in den Vault
   geschrieben. Der BYO-Weg ändert sich nicht.
5. **Rotation:** Neues Secret in der Twitch-Konsole → Generator erneut
   laufen lassen → neue Binary/Release. Damit der alte Stand nach einer
   Rotation nicht bricht, läuft der **Refresh des Authorization Code Flows
   ohne `client_secret`** (nur Client-ID; von Twitch akzeptiert, im Feld
   seit Jahren so praktiziert — MixItUp). Eine Rotation betrifft dann nur
   *neue* Logins; bestehende Accounts refreshen mit ihrer Binary weiter.
   Der Generator (intern, `go:generate`) erzeugt aus ID und Secret (Secret
   über Umgebungsvariable/Stdin, nie als CLI-Argument) das `embedded.go`;
   der Wert verlässt die Maschine des Projektinhabers nicht.
6. **Redirect-URL:** fest `http://localhost:8741/oauth/callback` (ersetzt
   ADR-0023, Punkt 4). Die URL trägt einen Pfad, bleibt exakt zwischen
   Konsole-Registrierung und `redirect_uri`-Parameter identisch, Host
   `localhost`, HTTP (Loopback-Ausnahme). Bestehende BYO-Apps tragen die
   alte URL: sie bleiben für Refreshes funktionsfähig (diese senden kein
   `redirect_uri`); ein Re-Login benötigt die neue URL zusätzlich in der
   eigenen App (mehrere Redirects erlaubt).
7. **Unverändert aus ADR-0023:** Flow-Mechanik (state, 10-Min-Fenster,
   Loopback-Bindung an `127.0.0.1`/`::1`), Re-Login mit best-effort-Revoke,
   `auth.*`-Ereignisse, die 41 Pflicht-Scopes, der DCF-Fallback mit der
   öffentlichen Projekt-App, `state`-Prüfung und die `accounts.flow`-Spalte.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| BYO-App nur (ADR-0023, Status quo) | Einrichtungsaufwand für jeden Nutzer vor dem ersten Login; vom Projektinhaber verworfen (2026-10-11). |
| Gemeinsames Secret als Klartext-Config-File | Menschenlesbar an jeder Stelle der Distribution, kein Harvest-now-Schutz, keine bessere Rotationslage als der verschlüsselte Einbau. |
| Hosted Auth-Broker (StreamElements-Muster) | Secret bliebe serverseitig, verlangt aber ein Projekt-Backend und die Token-Übertragung ins lokale Core — widerspricht dem Local-First-Betrieb (ADR-0003), neues Bauteil mit eigenem Sicherheitsdesign. |
| Device Code Flow als Standard | UX vom Projektinhaber verworfen (ADR-0023); bleibt Fallback. |
| Wiederverwendung der öffentlichen DCF-App | Hätte die DCF-App mit dem Secret des Code-Flows vermengt; eine eigene Confidential-App trennt Rotation, Rate-Limit-Budget und Reputationsrisiko. |

## Konsequenzen

**Positiv:**

- Der Standard-Login funktioniert ohne Einrichtung: Browser öffnen,
  Scopes einsehen, autorisieren (BYO bleibt die Option für eigene Apps und
  eigene Rate-Limits).
- Postquantum-fähiger Verschluss (ML-KEM) ohne neue Abhängigkeiten
  (Go-Standardlibrary).
- Refresh ohne Secret: Secret-Rotation bricht keine bestehenden Accounts;
  das Secret wird nach dem ersten Login nie wieder gebraucht.

**Negativ und Risiken:**

- Das Secret ist an alle Nutzer verteilt (Binary): kasuale Extraktion ist
  erschwert, Laufzeit-Extraktion möglich. Konsekutiv:
  - **Code-Interception ohne PKCE wird Account-Übernahme** für jeden mit
    dem Secret — der Loopback-Listener bindet strikt an die Loopback-Adressen
    (ADR-0023, Punkt 5); lokales Malware-Risiko bleibt wie bei MixItUp.
  - Gemeinsames Rate-Limit-Budget und gemeinsame App-Reputation über alle
    Nutzer; Missbrauch kann die App für alle unbrauchbar machen.
- Das Secret existiert beim Generator-Run in der Build-Umgebung des
  Projektinhabers (Variablen/Prozessspeicher).
- Die verschlüsselte Verteilung ist kein Nachweis gegenüber Twitch: die
  App ist eine normale Confidential App mit geteiltem Secret.

**Folgearbeiten:**

- [x] Projektinhaber legt die App „StreamCrew" (Confidential) in der
      Twitch-Konsole an und registriert
      `http://localhost:8741/oauth/callback` (erledigt 2026-10-11).
- [ ] Generator lokal laufen lassen, `embedded.go` committen (Aktivierung),
      E2E: erster Login mit der eingebauten App (beide Konten), Refresh,
      `auth status`, Logout.
- [ ] ADR-0023 mit „Ergänzt durch"-Vermerk versehen (erfolgt mit diesem
      ADR).
