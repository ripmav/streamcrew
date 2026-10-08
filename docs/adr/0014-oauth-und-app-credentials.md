# ADR-0014: OAuth und App-Credentials

| | |
|---|---|
| **Status** | Abgelöst durch [ADR-0023](0023-twitch-login-authorization-code-flow.md) |
| **Datum** | 2026-10-07 |
| **Entscheidung durch** | Projektinhaber (ripmav) |
| **Bezug** | Plan §6.12, §6.13, §7, §12.1; Roadmap Phase 4 (4.1); [ADR-0003](0003-betriebsmodi.md), [ADR-0004](0004-plattformumfang-zum-start.md), [ADR-0012](0012-persistenz.md), [ADR-0013](0013-sicherheitsmodell.md); [Code-ADR-0003](code/0003-fehler-und-logging.md), [Code-ADR-0009](code/0009-ids-und-zeit.md), [Code-ADR-0011](code/0011-event-bus.md), [Code-ADR-0019](code/0019-host-rechte-in-der-startkonfiguration.md) |

## Kontext

Phase 4 verbindet den Core mit Twitch (ADR-0004): Der Core arbeitet dabei als User
für zwei Konten — den Streamer und ein optionales Bot-Konto (Plan §6.12) — auf
Chat, Moderation und Events (Roadmap 4.2–4.4). Dafür braucht er OAuth-Access-Tokens,
die ab 4.2 von dem Helix-Client und ab 4.3 von den EventSub-Abonnements genutzt
werden. Die Rahmenbedingungen:

- Der Core läuft headless auf dem Streaming-PC oder als Server-Anwendung
  (ADR-0003). Es gibt keinen Browser, und im Server-Modus ist eine lokale
  Callback-Adresse nicht erreichbar. Der OAuth-Flow muss deshalb
  **frontend-unabhängig** laufen: Der Core startet den Flow, zeigt dem Nutzer
  per Ereignis die URL und den Code, und empfängt das Ergebnis selbst
  (Plan §6.12).
- Access-Tokens sind langlebige Zugangsdaten. Nach dem Sicherheitsmodell
  (ADR-0013) ruhen sie AES-256-GCM-verschlüsselt, der Schlüssel kommt aus dem
  Schlüsselbund, einer Key-Datei oder der Umgebung (ADR-0012, bereits in
  `internal/vault` umgesetzt).
- Twitch unterstützt den Device Code Flow (RFC 8628) für **öffentliche
  Clients ohne Secret** (allgemein verfügbar seit 12/2023). Der
  Authorization-Code-Flow mit Loopback-Redirect ist der Standard für spätere
  Plattformen (YouTube, Kick), die keinen Device Code Flow haben — dort gehört
  das Client-Secret nicht ins Binary, sondern in die Hände des Nutzers
  (*Bring Your Own*, BYO, Plan §6.12).
- Twitch hat die Moderator-Scopes 2026 fein gekörnt (z. B. `moderator:read`
  aufgelöst in `moderator:read:banned_users`, `moderator:manage:warnings`
  u. a.); die Twitch-Dokumentation verlangt, **nur die Scopes anzufordern, die
  die App nutzt** (sonst droht die Sperrung der App). Die Pflicht-Scopes sind
  deshalb eine gepflegte Konstante, kein wachsender Haufen.

## Entscheidung

Wir nutzen für Twitch den Device Code Flow mit `golang.org/x/oauth2`
(`Config.DeviceAuth`/`Config.DeviceAccessToken`) für einen öffentlichen Client
ohne Secret, speichern Accounts in SQLite und Tokens im Vault, refreshen
proaktiv vor Ablauf, weisen fehlende Scopes mit „Anmeldung erforderlich“ aus
und widerrufen beim Abmelden.

*Abgelöst am 2026-10-08 durch [ADR-0023](0023-twitch-login-authorization-code-flow.md):
Der Standard-Login ist der Authorization Code Flow mit Loopback-Redirect, den
der Nutzer im Browser abschließt, mit Client-ID und -Secret, die der Nutzer
seiner eigenen Confidential-App mitbringt (BYO); der Device Code Flow bleibt
als Fallback (`auth login twitch --device-flow`). Die Entscheidungen zu
Speicherung, Scopes, Refresh und Ereignissen gelten weiter, soweit ADR-0023
sie ausdrücklich übernimmt.*

1. **Flow:** Device Code Flow (RFC 8628) gegen `https://id.twitch.tv/oauth2/device`
   (Start) und `https://id.twitch.tv/oauth2/token` (Polling mit
   `grant_type=urn:ietf:params:oauth:grant-type:device_code`). Der Core pollt
   selbst (`x/oauth2` übernimmt Intervall, `slow_down` und Ablauf).
2. **Kein PKCE:** Twitch dokumentiert für den Device Code Flow kein
   `code_challenge`. Der Device-Code ist selbst das Geheimnis — er wird nie
   angezeigt, nur übertragen (TLS) — und macht PKCE überflüssig. (Der
   Authorization-Code-Flow der späteren Plattformen nutzt dagegen PKCE mit
   Loopback-Redirect.)
3. **App-Credentials:** Eine Projekt-App „StreamCrew“ (öffentlicher Client,
   ohne Secret, Client-ID `5yjhnihgh11abxo4f2cck1lfwx9ovg`, registriert
   2026-10-07) ist die Standard-App; ihre Client-ID ist eine Konstante in
   `internal/auth` und übersteuerbar (`--client-id`, später Konfiguration).
   Client-IDs sind öffentlich und dürfen in Binary oder Konfiguration stehen.
   Client-**Secrets** (YouTube, Kick) gehören nie in Binary oder
   Konfiguration, sondern verschlüsselt in den Vault (BYO: Der Nutzer legt
   seine eigene App an, streamcrew verwahrt nur das Secret).
4. **Konten:** Pro Plattform ein Streamer-Konto und ein optionales Bot-Konto
   (Plan §6.12, `connector.Account`). Metadaten — Login, Plattform-User-ID,
   gewährte Scopes, verwendete Client-ID — liegen in der SQLite-Tabelle
   `accounts` (Primärschlüssel `platform`, `role`; Schema-Skizze Plan §6.13).
5. **Token-Speicher:** Access- und Refresh-Token samt Ablaufzeit ruhen
   AES-256-GCM-verschlüsselt im Vault (ADR-0012) unter dem Namen
   `auth/<platform>/<role>` als JSON; der Klartext erreicht nicht die
   Datenbank.
6. **Refresh:** Der Core refresh-t proaktiv 15 Minuten vor Ablauf
   (Takt 60 s) und zusätzlich auf Abruf über den `Token()`-Port, den die
   Plattform-Adapter ab 4.2 nutzen. Der Refresh passiert nur mit
   `client_id` (öffentlicher Client, kein Secret). Verliert der
   Refresh-Token seine Gültigkeit (Twitch: 30 Tage Inaktivität) oder
   schlägt der Refresh dauerhaft fehl, wechselt der Kontenzustand auf
   „Anmeldung erforderlich“ — es erscheint kein modaler Dialog (Plan §6.12).
7. **Scope-Abgleich:** Die Pflicht-Scopes von Twitch sind eine Konstante
   (Tabelle unten). Beim Login werden genau diese angefordert; die gewährten
   Scopes (Antwortfeld `scope`) werden mit dem Konto gespeichert. Fehlt ein
   Pflicht-Scope — etwa weil streamcrew später ein Feature dazugewinnt —,
   zeigt der Status „Anmeldung erforderlich“ an und der nächste Login
   holt die Scopes neu.
8. **Widerruf:** `auth logout` ruft erst `https://id.twitch.tv/oauth2/revoke`
   auf (400 „bereits widerrufen“ zählt als Erfolg) und löscht erst dann
   Account und Vault-Eintrag. Scheitert der Widerruf, bleiben beide bestehen.
9. **Ereignisse für Frontends** (Plan §6.12, Code-ADR-0011):
   - `auth.action_required` — `{platform, role, url, code, expires_at}`:
     Frontends zeigen URL und Code (TUI: Text, Desktop: Browser öffnen,
     Web: Link).
   - `auth.login_completed` — `{platform, role, login}`.
   - `auth.login_failed` — `{platform, role, reason}`.
10. **Server-Modus:** Der HTTP-Client der Auth-Service läuft durch die
    Outbound-Allowlist (Code-ADR-0019). Für die Twitch-Flows müssen
    `id.twitch.tv` und `api.twitch.tv` freigegeben sein
    (`--outbound-allow`); die `doctor`-Prüfung und die Doku nennen das.
11. **Generalisierung:** Die Flow-Schritte (Start, Warten, Refresh, Widerruf,
    Nutzer-Nachschlagen) hängen hinter einem Interface in `internal/auth`;
    der Device Code Flow ist die erste Umsetzung. Weitere Plattformen
    (YouTube, Kick) kommen mit ihren Phasen und dem Authorization-Code-Flow
    plus PKCE; das Schema (Tabelle, Vault-Namen, BYO-Secret) ist dafür
    bereits ausreichend.

### Pflicht-Scopes von Twitch (Stand 2026-10-07, offizielle Scope-Referenz)

| Scope | Feature (Roadmap) |
|---|---|
| `user:read:chat` | EventSub `channel.chat.*` (Nachrichten, Löschungen, Leeren, Benachrichtigungen, AutoMod-Hold) (4.4) |
| `user:write:chat` | Chat-Nachricht senden (4.2) |
| `moderator:read:chat_settings` | Chat-Einstellungen lesen; EventSub `channel.moderate` (4.2/4.4) |
| `moderator:manage:chat_settings` | Chat stumm schalten: Slow, Nur-Follower, Nur-Abonnenten, Emote-only (B80 `disable_chat`) (4.2) |
| `moderator:read:chat_messages` | Gelöschte und gepinnte Nachrichten lesen; EventSub `channel.moderate` (4.2/4.4) |
| `moderator:manage:chat_messages` | Nachrichten löschen (B80 `purge`) (4.2) |
| `moderator:read:banned_users` | EventSub `channel.moderate` (Bann, Timeout) (4.4) |
| `moderator:manage:banned_users` | Timeout, Bann, Entbannen (B80) (4.2) |
| `moderator:read:moderators` | EventSub `channel.moderate` (Mod-Änderungen) (4.4) |
| `channel:manage:moderators` | Mod erteilen und entziehen (B80 `mod`/`unmod`) (4.2) |
| `moderator:read:warnings` | Warn-Ereignisse, Strikes (4.2/4.4) |
| `moderator:manage:warnings` | Warnung senden (B80 `add_strike`) (4.2) |
| `moderator:read:chatters` | Nutzer aus dem Chat: `$randomuser…` ([`template.md`](../spec/template.md), B22) (4.2) |
| `moderator:read:shoutouts` | Shoutout-Ereignisse (ein- und ausgehend) (4.4) |
| `moderator:manage:shoutouts` | Shoutout senden (Twitch-Action) (4.4) |
| `moderator:read:shield_mode` | Shield-Mode-Ereignisse (4.4) |
| `moderator:manage:shield_mode` | Shield-Mode starten und beenden (4.4) |
| `moderator:read:unban_requests` | Unban-Request-Ereignisse (4.4) |
| `moderator:manage:unban_requests` | Unban-Requests entscheiden (4.4) |
| `moderator:read:suspicious_users` | Suspicious-User-Ereignisse (4.4) |
| `moderator:read:vips` | EventSub `channel.moderate` (VIP-Änderungen) (4.4) |
| `channel:read:vips` | VIP-Liste lesen (4.4) |
| `channel:manage:vips` | VIP setzen und entziehen (Twitch-Action) (4.4) |
| `moderator:read:followers` | EventSub `channel.follow`; Follower-Liste (4.4) |
| `user:manage:whispers` | Flüstern senden und empfangen (4.2/4.4) |
| `channel:read:subscriptions` | Abo, Resub, Geschenk, Massengeschenk, Abo-Ende (4.4) |
| `channel:read:charity` | Charity-Ereignisse (4.4) |
| `channel:read:goals` | Ziel-Ereignisse (4.4) |
| `channel:read:hype_train` | Hype-Train-Ereignisse (4.4) |
| `channel:read:ads` | Werbeeinbruch-Ereignisse; Werbeplan lesen (4.4) |
| `channel:edit:commercial` | Werbung auslösen (Twitch-Action) (4.4) |
| `channel:read:polls` | Umfrage-Ereignisse (4.4) |
| `channel:manage:polls` | Umfrage starten und beenden (Twitch-Action) (4.4) |
| `channel:read:predictions` | Vorhersage-Ereignisse (4.4) |
| `channel:manage:predictions` | Vorhersage starten und beenden (Twitch-Action) (4.4) |
| `channel:read:redemptions` | Channel-Points-Ereignisse (Belohnungen, Einlösungen) (4.4) |
| `channel:manage:redemptions` | Belohnungen verwalten, Einlösung abschließen oder erstatten (4.4) |
| `channel:manage:broadcast` | Titel und Kategorie setzen, Stream-Tags, Stream-Marker (4.4) |
| `channel:manage:raids` | Raid starten (Twitch-Action) (4.4) |
| `clips:edit` | Clip erstellen (Twitch-Action) (4.4) |
| `bits:read` | Cheer- und Bits-Ereignisse, Bits-Rangliste, Custom Power-Ups (4.4, P1/P2) |

Nicht angefordert: `channel:read:stream_key` (Streaming-Key: kein Feature),
Automod-Verwaltung (`moderator:manage:automod`, kein Feature),
Schedule-/Video-/Editor-Verwaltung (keine Features). Ereignisse ohne Scope
(Stream Start/Stop, Raid ausgehend, Channel-Update) brauchen keines.

**Anmerkung „Chat leeren“ (B80 `clear_chat`):** In der aktuellen
API-Referenz (2026-10-07) gibt es keinen Helix-Endpunkt, der den gesamten
Chat leert; die Legacy-API ist eingestelt. Die Action existiert seit Phase 3
gegen die Mock-Plattform; die Twitch-Zuordnung (ggf. über
`moderator:manage:chat_messages` oder als Doku-Update der Action) entscheidet
4.2. Ein Scope dafür wird nicht gesondert angefordert.

## Betrachtete Alternativen

| Alternative | Warum nicht |
|---|---|
| Authorization-Code-Flow mit PKCE und Loopback-Redirect | Braucht einen lokalen Webserver und einen erreichbaren Browser; im Server-Modus (ADR-0003) ist `127.0.0.1` nicht erreichbar. Der Device Code Flow ist für genau diesen Fall (Gerät ohne Browser) vorgesehen. Bleibt die Wahl für YouTube und Kick, die keinen Device Code Flow haben. |
| Confidential Client mit Secret | Das Secret müsste verteilt und auf jedem Rechner verwahrt werden; für ein lokal genutztes Werkzeug bringt das keinen Sicherheitseffekt, und der Refresh wäre ans Secret gekoppelt. Twitch empfiehlt für offene Plattformen (Windows) ohnehin Public. |
| Eigener DCF-Client statt `golang.org/x/oauth2` | Die Bibliothek liefert Polling-Intervall, `slow_down`, Ablauf-Deadline und typisierte Token-Fehler; doppelte Wartung für keinen Vorteil. |
| Token im Keyring oder in der Konfiguration | ADR-0012 legt SQLite plus Vault für alle Secrets eines Profils fest: Schlüsselrotation, Backups ohne Klartext und Profil-Isolation sind damit gelöst; Keyring-Größenlimits wären ein zusätzliches Risiko. |

## Konsequenzen

**Positiv:**

- Kein Secret zu verwalten oder zu vertreiben; Login ohne Browser auf dem
  Streaming-PC (30-Minuten-Fenster, Code auf jedem Gerät).
- Tokens ruhen verschlüsselt (ADR-0012), der Klartext bleibt im Speicher.
- Refresh ist transparent: Adapter ab 4.2 fragen einfach `Token()` ab.
- Klare Signale statt Dialoge: „Anmeldung erforderlich“ als Zustandsmeldung
  (Code-ADR-0017, Plan §6.12).
- Schema und BYO-Mechanik sind plattformneutral, YouTube und Kick passen
  darauf auf.

**Negativ und Risiken:**

- Der Login ist interaktiv: Ein Mensch muss den Code eingeben (Fenster
  30 min); bei Ablauf oder Ablehnung muss neu gestartet werden.
- Refresh-Token verfallen nach 30 Tagen Inaktivität (Twitch) — nach langer
  Abwesenheit ist neu anzumelden, auch wenn alles gespeichert ist.
- Neue Features mit neuen Scopes bedeuten für alle Nutzer einen Re-Login,
  bis der Scope-Abgleich erfüllt ist.
- Twitch ändert Scope-Namen (Feinkörnigkeit 2026); die Konstante muss mit
  der API-Entwicklung gepflegt werden. Der Scope-Abgleich macht Abweichungen
  sichtbar, statt sie zu verschweigen.
- Über-Scoping riskiert die Sperrung der App durch Twitch — die Liste oben
  wächst nur mit nachvollziehbaren Features (Punkt 7).

**Folgearbeiten:**

- [ ] 4.2: Helix-Client nutzt den `Token()`-Port; `clear_chat` wird einem Endpunkt zugeordnet (oder in `actions.md` als Mock-only markiert).
- [ ] 4.3: EventSub-Abonnements mit User-Token; Scope-Bedarf pro Subscription hier prüfen.
- [ ] 4.4: Neue Twitch-Actions mit Scope-Bedarf (z. B. `channel:manage:clips` für Clips aus VODs) in die Tabelle aufnehmen.
- [ ] Spätere Phasen: Client-ID und BYO-Secrets in den Settings verwalten (bisher `--client-id`), YouTube/Kick mit Authorization-Code-Flow plus PKCE und Loopback.
