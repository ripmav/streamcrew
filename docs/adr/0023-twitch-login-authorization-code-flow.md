# ADR-0023: Twitch-Login per Authorization Code Flow

| | |
|---|---|
| **Status** | Akzeptiert |
| **Datum** | 2026-10-08 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §6.12, §7, §10; Roadmap Phase 4 (4.1); [ADR-0003](0003-betriebsmodi.md), [ADR-0004](0004-plattformumfang-zum-start.md), [ADR-0012](0012-persistenz.md), [ADR-0013](0013-sicherheitsmodell.md), [ADR-0014](0014-oauth-und-app-credentials.md); [Code-ADR-0008](code/0008-datenbankzugriff.md), [Code-ADR-0019](code/0019-host-rechte-in-der-startkonfiguration.md) |

## Kontext

[ADR-0014](0014-oauth-und-app-credentials.md) hat den Device Code Flow (RFC 8628)
für die Twitch-Anmeldung entschieden, weil Twitch ihn als einzigen Flow für
öffentliche Clients ohne Secret dokumentiert. Bei der manuellen Prüfung
(2026-10-08, E2E-Vorbereitung) hat der Projektinhaber dieses Erlebnis verworfen:
Der Nutzer wird auf die Seite „Gerät aktivieren“ geschickt, anstatt sich wie bei
üblichen Chatbot-Diensten bei Twitch anzumelden, die angeforderten Rechte auf
einer Autorisierungsseite zu sehen, zu autorisieren und zurückgeleitet zu
werden.

Der gewünschte Standard ist der **Authorization Code Flow** (RFC 6749 §4.1):
Die Anmeldung startet beim Nutzer, und das Ergebnis kommt per Redirect an eine
lokale Adresse zurück. Bei Twitch ist dieser Flow an ein Client-Secret gebunden
(Confidential Client): Die offizielle Doku (Stand 2026-10) gibt `client_secret`
im Token-Austausch als required an, und **PKCE unterstützt Twitch nicht**
(Staff-Bestätigung im Developer-Forum, zuletzt 2024-12 und 2026-01). Der
offizielle `twitch-cli` nutzt genau dieses Muster: Authorization Code Flow mit
Loopback-Redirect (`http://localhost:3000` registriert, lokaler Webserver
empfängt den Callback) und Client-ID sowie -Secret, die der Nutzer selbst
anlegt (BYO); der Device Code Flow ist dort nur eine optionale Flagge.

Die Rahmenbedingungen von ADR-0014 gelten weiter: Headless-Core ohne Browser
(ADR-0003), Tokens AES-256-GCM im Vault (ADR-0012/0013), die 41 Pflicht-Scopes,
eine App für beide Konten, Refresh 15 min vor Ablauf, Widerruf vor dem Löschen
und die Auth-Ereignisse für Frontends.

## Entscheidung

**Der Standard-Login für Twitch ist der Authorization Code Flow mit
Loopback-Redirect, den der Nutzer im Browser abschließt; der Device Code Flow
bleibt als Fallback.**

1. **Flow:** `auth login twitch` baut die Twitch-Autorisierungs-URL
   (`https://id.twitch.tv/oauth2/authorize` mit `response_type=code`) und zeigt
   sie an; ein lokaler HTTP-Listener empfängt den Redirect, prüft den
   `state`-Parameter und übergibt den Code an den Core. Der Core tauscht den
   Code am Token-Endpunkt gegen Access- und Refresh-Token und speichert Account
   und Token wie in ADR-0014.
2. **Kein PKCE:** Twitch unterstützt es nicht (Kontext). Die Sicherheit des
   Code-Austauschs trägt das Client-Secret und die Loopback-Bindung des
   Listeners; der Code verlässt den Rechner nicht.
3. **Client-Credentials BYO:** Der Nutzer betreibt eine eigene **Confidential
   App** bei Twitch (eine App für beide Konten), registriert dort die
   Redirect-URL und gibt Client-ID und -Secret einmal bei `auth login twitch`
   (`--client-id`, `--client-secret`). Das Secret ruht verschlüsselt im Vault
   (je Plattform ein Eintrag `auth/<platform>/client`, gemeinsam für
   Streamer- und Bot-Konto); es kommt nie ins Binary, nie in Logs und nie in
   die `accounts`-Tabelle. Die öffentliche Projekt-App „StreamCrew“
   (Client-ID als Konstante) dient nur noch dem Device-Code-Flow-Fallback.
4. **Redirect-URL:** fest `http://127.0.0.1:8741` — exakt so in der App des
   Nutzers zu registrieren („OAuth Redirect URLs“). Der Listener bindet nur an
   `127.0.0.1` (nicht `localhost`, um die `::1`-Auflösung von Browsern zu
   vermeiden). Port und Pfad sind in v1 nicht konfigurierbar; eine Änderung
   erfordert die Neuregistrierung der Redirect-URL in der App des Nutzers.
5. **Sicherheitsdetails:** `state` (32 zufällige Bytes, hex, nur im Speicher,
   Abgleich beim Redirect — CSRF-Schutz). Das Login-Fenster hat 10 Minuten
   (`auth.action_required.expires_at`), danach muss der Login neu gestartet
   werden. Ein Re-Login widerruft die bisherige Anmeldung best-effort, bevor
   die neue gespeichert wird.
6. **Fallback:** `auth login twitch --device-flow` führt den Device Code Flow
   aus ADR-0014 unverändert aus (öffentlicher Client, Projekt-App) — für
   Headless- und Server-Betrieb, wo der Loopback-Redirect nicht erreichbar ist
   (ADR-0003), und für Nutzer ohne eigene App.
7. **Kopplung Account ↔ Flow:** Die Tabelle `accounts` kennt die Spalte
   `flow` (`authorization_code` oder `device_code`). Refresh (Core-Loop und auf
   Abruf) und Widerruf konstruieren daraus den passenden Client — Confidential
   (mit dem gespeicherten Secret) oder public (nur Client-ID).
8. **Übernommen aus ADR-0014:** die 41 Pflicht-Scopes (Tabelle dort),
   Token-Speicherung im Vault unter `auth/<platform>/<role>`, Refresh-Loop
   (60 s Takt, 15 min vor Ablauf) und Scope-Abgleich, die `auth.*`-Ereignisse,
   der Revoke-Endpunkt (400 = Erfolg) und der Helix `Client-Id`-Header.
   `auth.action_required` trägt im Code-Flow die Autorisierungs-URL in `url`,
   ein leeres `code` und das Fensterende in `expires_at`.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Device Code Flow als Standard (ADR-0014) | UX „Gerät aktivieren“ statt Nutzer-Login mit Autorisierungsseite; vom Projektinhaber verworfen (2026-10-08). Bleibt der Fallback. |
| Authorization Code Flow mit PKCE | Twitch unterstützt PKCE nicht (Stand 2026-10); ohne Secret keinen Code-Flow. |
| Authorization Code Flow mit öffentlichem Projekt-Client (ohne Secret) | Von Twitch nicht unterstützt: öffentliche Clients sind laut Doku auf den Device Code Flow beschränkt, `client_secret` ist required. |
| Implicit Grant Flow | Kein Refresh-Token; jeder Refresh bräuchte eine neue manuelle Anmeldung. |
| Projekt-gehostete Confidential-App (gemeinsames Secret) | Das Secret läge in der verteilten Software und wäre damit öffentlich — genau das Risiko, das das BYO-Secret vermeidet. |

## Konsequenzen

**Positiv:**

- Die Anmeldung ist der Standardweg der Chatbot-Dienste: Login bei Twitch,
  Scopes auf der Autorisierungsseite einsehbar, Autorisieren, zurück zum Core.
- Der Refresh-Token des Code-Flows ist (anders als der des DCF) nicht einmalig
  und ohne 30-Tage-Limit — die Anmeldung überlebt längere Offline-Phasen (in
  der E2E zu verifizieren).
- Das Secret bleibt beim Nutzer (BYO); streamcrew verteilt und verwahrt keine
  App-Credentials im Binary.

**Negativ und Risiken:**

- Jeder Nutzer muss einmalig eine eigene Confidential-App anlegen und die
  Redirect-URL registrieren; ohne App ist nur der DCF-Fallback möglich.
- Ohne PKCE ist der Code-Flow nur so sicher wie sein Secret: Der Listener bindet
  strikt an `127.0.0.1`, das Secret erreicht weder Logs noch Vault-nach außen;
  die Terminal-Historie des `--client-secret`-Aufrufs liegt beim Nutzer.
- Der Loopback-Redirect funktioniert nur, wenn der Browser auf dem Rechner des
  Listeners läuft; Server-Betrieb braucht den DCF-Fallback (oder später:
  Token-Import).

**Folgearbeiten:**

- [ ] Manuelle E2E mit eigener Confidential-App: Code-Flow-Login beider Konten,
  `auth status`, Logout, Connected Apps ohne Tokens; dazu ein DCF-Login
  (`--device-flow`) als Fallback-Prüfung (Plan §7.2).
- [ ] Spätere Phasen: Browser-Auto-Open und konfigurierbarer Redirect-Port
  (Frontends), Token-Import für Server-Betrieb ohne Browser (Backlog).
