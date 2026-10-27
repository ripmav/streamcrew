# Code Review 2026-10-24: code quality and ADR/spec compliance

- **Scope:** `main` at `313b633`, about 127k lines of Go, all ADRs (`docs/adr/`), Code-ADRs (`docs/adr/code/`) and specs (`docs/spec/`).
- **Method:** seven read-only Sonnet subagents (architecture ADRs, Code-ADRs 0001–0006, Code-ADRs 0007–0021, command/action specs, Twitch/chat/moderation specs, users/counters plus API/CLI/TUI, tool run). The main agent merged duplicates, re-rated severities and re-checked the most important findings in the code itself.
- **Status column:**
  - **✔** re-checked in the code by the main agent
  - **V** read in the code by the subagent
  - **P** plausible, not fully traced

Legal and provenance topics were deliberately left out (deferred to Gate O).

## Tooling: all green

| Step | Result |
|---|---|
| `go version` / `go.mod` | go1.27.3 / `go 1.27.3`, which is the latest stable release |
| `golangci-lint` 2.14.0 | 0 issues |
| `gofmt -l`, `go fix -diff`, `go vet` | no findings |
| `govulncheck` | No vulnerabilities found |
| `scripts/sandbox.sh go test ./...` | all packages pass |
| `sqlc diff` / `buf generate` | not checked locally (tools not installed); CI checks both |

The working tree stayed unchanged.

## Priorities

1. **Make the API secure by default (security):** S1, S2, S3, S4, plus the confirmed security findings in [section 7](#7-security-review-whole-codebase) (V1, V2).
2. **Twitch production bugs:** T1 (mass gifts), T2/T3 (bot account in additional channels), T4 (IRC emotes).
3. **Data correctness:** U1 (watch time), U2 (rankings), U3 (title gets cleared).
4. **Code-ADR-0018 JSON v2 migration:** C1, 8 files.
5. **Bring doc status in line with the code:** D1–D6.

---

## 1. Security and ADR-0013 / ADR-0010

| # | Sev. | Status | Finding | Location | Reference |
|---|---|---|---|---|---|
| S1 | **Critical** | ✔ | **The API fails open.** While no token is stored, `tokenAuth` lets every request through, admin RPCs (Shutdown, Config) included. `Enabled()` returns `err == nil && len(tokens) > 0`, so a corrupt or unreadable `api-tokens.json` also turns authentication off, as does revoking the last token. Server mode listens on `:8740` (all interfaces) by default, and the Dockerfile sets `STREAMCREW_MODE=server` with `EXPOSE 8740`. Daemon mode never creates a local token (`ensureDesktopToken` runs in desktop mode only). | `internal/api/token.go:25-30`, `internal/apitoken/apitoken.go:197-200`, `internal/config/config.go:169-177`, `internal/app/app.go:~588` | ADR-0013 §5 ("Jede Anfrage braucht einen Token"), ADR-0003 (server: token required), ADR-0010 §4 |
| S2 | High | ✔ | **`STREAMCREW_TOKEN` in the systemd unit and docker docs does not protect the server.** The variable binds only the CLI client flag `--token` (`internal/cli/commands.go:33`), and the server never reads it. Following `streamcrew.env.example` or `usage.md` therefore yields an open API (see S1). | `packaging/systemd/streamcrew.env.example:6`, `docs/usage.md:131-170` | ADR-0025 §1, ADR-0013 §5 |
| S3 | Medium | V | No throttling of failed logins per source address. On top of that, `Validate()`/`Enabled()` read and parse the token file on every request, which makes cheap DoS amplification possible. | `internal/api/token.go`, `internal/apitoken` | ADR-0013 §5 |
| S4 | Medium | V | No warning when server mode listens on a public address without TLS or proxy, neither in the log nor in `doctor`. `doctor.probeCore` probes plain HTTP only, so with TLS a running core is reported as FAIL. | `internal/doctor/doctor.go` | ADR-0013 §7 |
| S5 | Medium | V | API tokens live in a global `<data-dir>/api-tokens.json` (hashes, 0600), not in the profile DB. No amending ADR records this; it is only noted in `roadmap.md:695`. README.md:375 claims "pro Profil, verschlüsselt … `secret rotate`", which is wrong. | `internal/apitoken/apitoken.go:3-17` | ADR-0012 §6, ADR-0013 §5. Per project rule, ask the owner before deviating from an ADR. |
| S6 | Medium | V | The CLI API commands (`counter`, `user`, `command`, `chat`, `event`) ignore the Unix socket, `runtime.json` and the desktop token. Only `tui` uses them. In desktop mode this means 401 or "connection refused". The file is named `desktop-token`, while ADR-0013 names it `api.token`. | `internal/cli/api.go:18-36`, `internal/cli/tui.go:25-31` | ADR-0005 §4, ADR-0010 §4, ADR-0013 §5, `docs/usage.md` |
| S7 | Medium | P | The EventSub access token is put into the WebSocket URL (`?access_token=`), and dial errors are logged unmasked (`ws.go:181`). A `url.Error` can contain the full URL including the token. | `internal/twitch/ws.go:292`, `:181` | Code-ADR-0003 (no secrets in logs), Code-ADR-0014 (`maskSecrets`) |
| S8 | Low | V | The `overlay` scope counts as `read`, so it can read `UserService/List`, `ChatService/History` and `CommandService/Export` (personal data and all command definitions). ADR-0013 describes overlay as display-only. | `internal/api/scopes.go` | ADR-0013 |
| S9 | Low | V | Own CORS/CSRF handler instead of `http.CrossOriginProtection`. Origin is never compared with Host, so same-origin POSTs are rejected with 403 when the allowlist is empty. | `internal/httpserver/cors.go` | ADR-0010 §5 |
| S10 | Low | V | No h2c (`Protocols.SetUnencryptedHTTP2`), so native gRPC clients cannot connect without TLS. | `internal/httpserver/server.go` | ADR-0010 §4 |
| S11 | Low | V | The handshake has no API version, neither in `StatusResponse` nor in `RuntimeInfo`. The mode comment in the proto omits `daemon`. | `api/proto/.../system.proto:41-52`, `internal/app/runtime.go:18-25` | ADR-0010 §4, ADR-0005 §6 |
| S12 | Low | V | apitoken: duplicate names are allowed and `Revoke(name)` removes all of them. Read-modify-write is locked only within the process, so the CLI and the core can race. Open server streams survive a revocation. | `internal/apitoken/apitoken.go` | — |

## 2. Twitch, chat, events, moderation

| # | Sev. | Status | Finding | Location | Reference |
|---|---|---|---|---|---|
| T1 | **High** | ✔ | **Twitch mass gifts are always rejected.** `community_sub_gift` sets `Gift.Count = Total` but no `Recipients` (Twitch does not send them). The Event Service requires `len(Recipients) == Gift.Count` and returns `ErrShape`. The individual `sub_gift` notices then fire regardless of the threshold, which violates "nie beide". | `internal/twitch/map.go:~593-640`, `internal/eventservice/receive.go:343-346` | events.md B5/B26, twitch-events.md B21 |
| T2 | **High** | ✔/P | **The bot sender is refused.** `chat.Send` refuses `From == AccountBot` with "bot account is not connected" (a stale phase-4 comment). `Status.Sender()` returns `AccountBot` as soon as `Bot == true`, i.e. as soon as an additional-channel or IRC session runs (`platform.go:456,476`). With additional channels configured, every bot send to the home channel then fails. | `internal/twitch/chat.go:45-47`, `internal/connector/platform.go:91` | chat-service.md B6, twitch-multi-channel.md B4 |
| T3 | **High** | V | Delete, Timeout, Ban and ClearChat in additional channels run with the streamer token and use the streamer as `ModeratorID`. Helix answers 403. This breaks mute deletes, the strike follow-up commands and ClearChat there. | `internal/twitch/ops.go`, `mode.go`, `chat.go:78-92` | twitch-multi-channel.md B4/B6, chat-service B21/B23, moderation B10/B11 |
| T4 | Medium | ✔ | **IRC emote ranges are cut off by one.** The Twitch range is inclusive and counted in code points, but the code slices bytes with an exclusive end, so the last character is lost and non-ASCII text is mis-sliced. The test (`irc_test.go:236`, `0-5`) encodes the bug. | `internal/twitch/irc.go:621-638` | moderation B7 (emote filter), template |
| T5 | Medium | V | Twitch cannot send whispers or replies: there is no Helix whisper endpoint and no `Replier`/`Whisperer`. The scope `user:manage:whispers` is requested but never used, yet roadmap 5.1 is marked done. | `internal/twitch`, `internal/helix` | chat-service.md B8, B10, B34, B35 |
| T6 | Medium | V | The stream state is not re-reported after an EventSub reconnect; `streamState` is called only once per `channelSession`. | `internal/twitch/platform.go:728-733` | events.md B11 |
| T7 | Medium | V | EventSub loop: the reader goroutine blocks on `in` (cap 1) without cancellation, so one goroutine leaks per reconnect. While waiting for the new welcome, frames of the old session are discarded, which contradicts the comment "no event is lost". | `internal/twitch/ws.go:386-396`, `:457-499` | Code-ADR-0004, Code-ADR-0015 |
| T8 | Medium | V | `channel.moderate` is mapped only for ban and timeout. Unban, untimeout, mod, vip and the others end up logged at Warn as "unmapped", and the event types are missing from the catalog. `chat.notification` raid, announcement and similar notices are logged at Warn as well. | `internal/twitch/map.go:~176-196` | twitch-events.md B7 |
| T9 | Medium | V | The Event Service holds its global mutex during network I/O (Helix delete, `Moderation.Check`). One slow Helix call blocks every platform. | `internal/eventservice/receive.go:55-56,123` | — |
| T10 | Medium | P | Anonymous cheers are probably dropped: the empty identity fails `checkAccount`. | `internal/twitch/map.go`, `eventservice/receive.go:336-339` | events.md B6/B29 |
| T11 | Medium | P | Follow-time participation reads only cached `FollowedAt` from the home channel. New chatters are discarded ("unknown is not met"), and additional channels use the home-channel follow. | `internal/moderation/moderation.go:206-209`, `twitch/platformdata.go:19` | moderation.md B3 |
| T12 | Medium | P | One EventSub WebSocket per additional channel on the bot token with 22 specs exceeds Twitch's limits of 3 connections and `max_total_cost` 10. The spec lists this as an open question. | `internal/twitch/platform.go:~452` | twitch-multi-channel.md |
| T13 | Low | V | IRC lifecycle: the per-connection close goroutine is never stopped. The backoff is never reset and can exceed the cap (32 s > 30 s). The welcome timeout is overwritten. `Run` stays dead after a token error. `streamStatePoll` runs on the parent context, so a new poller is added on every reconnect. | `internal/twitch/irc.go:187-273`, `platform.go:~484,~548` | Code-ADR-0004 |
| T14 | Low | P | IRC: tag values are not unescaped. `Send` does not strip CR/LF, so a template newline could inject an IRC command. Only the 20/30 s bucket exists, not the additional 1/3 s. | `internal/twitch/irc.go:~357`, `chat.go:23` | twitch-multi-channel.md B18 |
| T15 | Low | V | `EventSub Manager.Reconcile` can run concurrently (ticker, session and revocation callbacks) and has no mutex. | `internal/twitch/subscriptions.go:230`, `platform.go` | — |
| T16 | Low | V | Moderation filters: the link regex matches "yes.Thanks", and the word filter compiles its regex per message and ignores punctuation. | `internal/moderation/filter.go:71` | moderation.md B5 |
| T17 | Low | V | `timer.Run` loads all commands and groups from the store every second. Mute exemption by role also exempts other channels' broadcasters. `Event.ID = string(Body)`. | `internal/timer/timer.go`, `eventservice/receive.go:73` | chat-service B21 |

## 3. Users, counters, API, CLI, TUI

| # | Sev. | Status | Finding | Location | Reference |
|---|---|---|---|---|---|
| U1 | **High** | ✔ | **Watch time is overcounted.** `chat.user.leave` comes only from the mock connector, and `Active.Last` is set but never read. Once a user has chatted, they get +1 minute per live tick until the core restarts. Regular promotion (B26) is skewed the same way. | `internal/userservice/userservice.go:208-210,294,519`, `connector/mock/simulate.go:193` | users-and-roles.md B9, B26 |
| U2 | **High** | ✔ | **Rankings include the streamer and the bot.** The API adapter calls `store.TopUsers` directly. `userservice.Service.Rankings` (which excludes the channel accounts) has no production caller, so it is dead code. | `internal/app/api_users.go:86,206`, `internal/userservice/userservice.go:269` | users-and-roles.md B12/A5 |
| U3 | Medium | V | `streamcrew user set` without `--title` clears the custom title. The CLI help says "empty keeps it", but the adapter sets `x.Title = title` unconditionally (data loss). `--strikes 0` cannot be set either. | `internal/cli/user_api.go:~115-135`, `internal/app/api_users.go:136-150` | `docs/usage.md` |
| U4 | Medium | V | `CounterService.Create` does not call `CheckReserved`, so a counter named `date` shadows `$date`. | `internal/app/api_wiring.go:~100`, `domain/counter/counter.go:117` | counters-and-quotes.md B7/A3 |
| U5 | Medium | V | Error mapping gaps: decimal syntax/range, `id.Parse`, `role.Parse`, unknown statistic, settings validation, `ErrReserved` and `ErrLoginInProgress` all end up as `CodeInternal` (HTTP 500). | `internal/api/counter.go:50-66` and callers | ADR-0010 |
| U6 | Medium | V | TUI stream: no reconnect, silent drops once 64 events are queued, and `ctx` is `context.Background()` although the comment says "canceled on quit". `runtimePath` uses string concatenation. | `internal/tui/model.go:113-151,289-306`, `tui/client.go` | Code-ADR-0004 |
| U7 | Low | V | `userservice.refreshData` makes a network call without a timeout on the single event-loop goroutine, which contradicts the comment "never blocks". `watchTick` scans all users every minute, even when nothing is live. | `internal/userservice/userservice.go:~403-440` | Code-ADR-0004 |
| U8 | Low | V/P | Merge leaves the dropped ID in `active`, so its watch time is lost. `Users.Update` writes roles into every identity, non-atomically, which the next platform sync overwrites (P). | `internal/userservice`, `internal/app/api_users.go:~121-135` | users-and-roles.md B42 |
| U9 | Low | V | `DefaultTitle` uses an unstable `slices.SortFunc`. `RegularHours` has no upper bound, so `*60` can overflow. `FollowedAt` is set to year 1 when unknown. | `domain/user/title.go`, `userservice.evaluateRegular`, `api/user.go` | — |

## 4. Commands, actions, templates, requirements

| # | Sev. | Status | Finding | Location | Reference |
|---|---|---|---|---|---|
| A1 | Medium | ✔ | `command_admin update` reports success without changing anything when the command has no chat action. | `internal/action/commandadmin/commandadmin.go:180-202` | actions.md B123 |
| A2 | Medium | ✔ | `command_admin find` returns `false` for "none", "several" and a failed listing alike. `add` treats a listing error as "free", and `update`/`disable` report "no command" when there are several matches. | `commandadmin.go:218-237` | actions.md B124, Code-ADR-0017 |
| A3 | Medium | V | The threshold requirement can be saved and imported, but every trigger fails with `ErrNotSupported` (the command silently never runs). There is no save-time warning. | `internal/requirement/requirement.go:185` | requirements.md B50–B54 (status "in Arbeit") |
| A4 | Medium | V | `user_command` keys stored answers by command name, so a rename (also a case-only rename, B7) orphans all answers. | `internal/action/usercommand/usercommand.go:~146` | actions.md B127/B129 |
| A5 | Low | V | Time-limit race: an action that returns `nil` just as the timer fires is recorded as failed. | `internal/engine/run.go:~318` | command-engine.md |
| A6 | Low | V | The template cache key is case-sensitive, so `$FollowAge` and `$followage` are resolved twice. | `internal/template/engine.go:~104` | template.md B20/B21 |
| A7 | Low | V | The bits requirement and its order (role, then bits, then cooldown) are not in the spec. | `internal/requirement/bits.go` | requirements.md B2 |
| A8 | Low | V | Unbounded state: the random draw memory is never pruned, and the size of a single global value is not capped. | `internal/action/flow/random.go`, `internal/template/globals.go` | actions.md B13, B57 |
| A9 | Low | P | `addcmd !hello` probably creates `!!hello` (leading `!` not stripped). `find` and `Save` are not atomic. | `commandadmin.go:add` | actions.md B123 |
| A10 | Low | V | Small items: the body-read timeout is not reported as `ErrTimeout` (B73). Duplicate JSON keys count as an error (not specified in B76). File locks without path normalization (B109). The engine waits forever after the shutdown grace period for an action that ignores its context. | `action/network/webrequest.go:~270,~300`, `action/host/file.go:~470`, `engine/engine.go` | actions.md |

## 5. Code-ADR compliance (cross-cutting)

| # | Sev. | Status | Finding | Location | Reference |
|---|---|---|---|---|---|
| C1 | Medium | ✔ | **`encoding/json` v1 is still imported in 8 non-test files**, with no exception comment. These include the security-relevant files `apitoken.go` and `appcreds.go`, which use the lenient decoder, and `chat/history.go`, which writes `null` for empty lists. | `internal/app/runtime.go`, `internal/twitch/twitchmock/rpc.go`, `internal/appcreds/appcreds.go`, `internal/emote/bttvws.go`, `internal/chat/history.go`, `internal/api/token.go`, `internal/apitoken/apitoken.go`, `internal/tui/client.go` | Code-ADR-0018 §1/§2/§4, Code-ADR-0017 |
| C2 | Low | P | Some v2 `json.Marshal` calls lack `Deterministic(true)`. A shared helper would fix it. | `domain/command/requirement.go:104`, `settings/settings.go:720ff`, `auth/auth.go:483/494`, `helix/helix.go:80` | Code-ADR-0018 §4 |
| C3 | Medium | V | **Bare `go` statements** outside the supervisor or `WaitGroup.Go`/`errgroup`, with no one waiting for them. Most important: `app/api_auth.go:79` (login), `app/api_stream.go:120,136`, `app/app.go:933`. Also several in `twitch/platform.go`, `ws.go`, `irc.go`, `emote/bttvws.go:149`, `auth/twitch.go:455`, `httpserver/server.go:171,214` and `tui/model.go:167`. `host/program.go:283` (reap) is a deliberate, commented exception. | see list | Code-ADR-0004 §1 |
| C4 | Low | V | `context.Context` is stored in structs (`app.go:170` `runCtx`, `tui/model.go:114`), and `context.Background()` is used in production paths (`app.go:691,1084`). | `internal/app`, `internal/tui` | Code-ADR-0004, Go guidelines |
| C5 | Low | P | Errors are logged and returned in `App.Run` (`app.go:956,969,975`), so `main` reports them a second time. | `internal/app/app.go` | Code-ADR-0003 §5 |
| C6 | Low | V | `errors.As` is used where the ADR prescribes `errors.AsType`, in about 8 places (`twitch/ops.go:51,60`, `subscriptions.go:329`, `ws.go:213`, `emote/bttv.go:88`, `emote/ffz.go:71`, `action/host/program.go:332`, `twitchmock.go:81`). | see list | Code-ADR-0003 §3 |
| C7 | Low | V | `internal/twitch/twitchmock` imports `internal/config`. | `internal/twitch/twitchmock` | Code-ADR-0002 §5 |
| C8 | Low | V | Package-level state with `//nolint:gochecknoglobals` and a reason, but no recorded owner approval. The mutable `sync.Once` in `appcreds.go:66` is real global state. | `appcreds.go:59-66`, `cli/completion.go:27,46`, `auth/twitch.go:70`, `twitch/subscriptions.go:68`, `config/file.go:234` | Code-ADR-0002 §6 |
| C9 | Low | V | Remaining magic values: `expr.closing()` returns `-1`, `max: -1` means variadic, `api.Command.Channel` uses `""` for "all". | `internal/expr/expr.go:249`, `parse.go:121`, `internal/api/command.go:29` | Code-ADR-0017 |
| C10 | Low | V | Missing SPDX headers in generated code (`appcreds/embedded.go`, `store/sqlcgen/*`) and in `scripts/modulecheck.sh`. | — | ADR-0002 |
| C11 | Low | V | Release: `checksums.txt` does not cover all artifacts (the source zip, the systemd files and the image tar plus its SBOM are uploaded after goreleaser runs). | `.goreleaser.yaml`, `.github/workflows/release.yml:63-92` | ADR-0007 §2, ADR-0025 §1 |
| C12 | Low | P | BTTV and FFZ are always contacted (channel IDs go to third parties) with no setting. Not telemetry, but not "set up by the user" either. | `internal/app/app.go:365-370` | ADR-0011 §3 |
| C14 | Medium | ✔ | **On-the-fly configuration does not work in `serve`.** `core.Run` passes `config.NewFileResolver(opts.ConfigFile)` without calling `Load` (only `cli/mock.go:133` and tests call it). The watcher therefore has no path and no allowed keys: it never watches the file, and `ConfigService/Update` rejects every key as unknown. Without a config file at startup, Update is read-only anyway. Fix this only together with V3 (block widening of rights). | `core/core.go:158-160`, `internal/config/file.go:85`, `internal/config/watch.go` | Code-ADR-0021 |
| C13 | Info | V | `core.Options` has about 30 fields, wider than the "bewusst minimal" start API in ADR-0006. | `core/core.go` | ADR-0006 §1 |

**Compliant (checked by the subagents):**
- **Code-ADR-0001:** lint configuration matches; all 21 `//nolint` directives are specific and carry a reason.
- **Code-ADR-0002:** no `init()`, no `slog.Default()`.
- **Code-ADR-0005:** env access only in `cmd`/`config` (TUI `$EDITOR` is the documented exception).
- **Code-ADR-0006:** no testify mock or gomock; `t.Parallel` in 176 of 203 test files; 55 files use `synctest`.
- **Code-ADR-0007:** breaker per API in the composition root.
- **Code-ADR-0008:** SQLite pragmas, read/write pools, goose with Down migrations, `sqlc diff` in CI.
- **Code-ADR-0009:** UUIDv7; direct `time.Now()` is allowed.
- **Code-ADR-0010:** `RejectUnknownMembers`.
- **Code-ADR-0011:** event bus with lag signal.
- **Code-ADR-0012:** template engine without `text/template`.
- **Code-ADR-0013 and 0019:** capability source, `os.OpenRoot`.
- **Code-ADR-0014:** httpclient with retry, Retry-After and pagination.
- **Code-ADR-0015:** coder/websocket.
- **Code-ADR-0016:** pinned generators plus a CI diff.
- **Code-ADR-0020:** apd, no float in counters.
- **Code-ADR-0021:** watcher with ApplyFuncs.
- **ADR-0005:** `/readyz`, `/healthz`, lock file.
- **ADR-0012:** profiles, backup, AES-GCM vault, key order.
- **ADR-0013:** SSRF dialer.
- **ADR-0023/0024:** auth code flow, ML-KEM sealing.
- **ADR-0022:** EN/DE catalogs identical.

## 6. Documentation drift

| # | Sev. | Status | Finding |
|---|---|---|---|
| D1 | Medium | ✔ | README.md:82 and :165 name `desktop` as the default mode, but the code default is `daemon` (`config.go:46`, ADR-0003). README.md:336 says "nur Loopback-Standard", while server mode listens on `:8740`. `docs/usage.md:128` says "Die API verlangt einen Bearer-Token", which is not true without a token (S1). |
| D2 | Medium | V | `docs/usage.md:162-168` refers to `ghcr.io/ripmav/streamcrew:v1.0.0`. No such registry image exists, because the release builds only an OCI tar (the ADR-0025 follow-up for registry publishing is open). README.md:18 still calls release 6.7 open. The README data-dir layout lacks `api-tokens.json` and `streamcrew.lock`. README.md documents `profile channels … chat`, which does not exist. |
| D3 | Medium | V | `moderation.md` and `twitch-multi-channel.md` say "Entwurf" and "Umsetzung: noch offen", although both are implemented. `spec/README.md` lists moderation as "geplant" with no link. The acceptance boxes are unchecked. |
| D4 | Low | V | twitch-events.md names differ from the catalog and code (goal, charity, `chat.user.moderate.*`, shared_chat). events.md A4 (poll/prediction events exist) contradicts twitch-events.md B17/B22. |
| D5 | Low | ✔/V | `docs/adr/README.md:53` lists ADR-0025 as "Vorgeschlagen" although the file says "Akzeptiert"; roadmap.md:784 has the same lag. The Code-ADR index has no row for 0016. Code-ADRs 0015, 0016 and 0017 are still "Vorgeschlagen" although they are implemented and referenced as binding. Code-ADR-0001 says "go 1.27.1". ADR-0024 contradicts itself ("Refresh ohne Secret"). |
| D6 | Low | V | Stale follow-up boxes: ADR-0022 (requirement messages are already `i18n.Message`), ADR-0006 (`core` exists). The requirements.md acceptance boxes lag behind the code (B1–B8, B10–B12, B70–B72). |
| D7 | Low | V | Weak B-ID traceability in tests. No B-ID references for: actions B118–B129, B92, B216, B24, B53; command-engine B21, B24, B42, B53, B62, B74, B100; template B11, B21, B30, B31, B40, B72, B77; commands B44–B46, B68; `twitch/ws_test.go` has none at all; moderation B12, B13, B26. |
| D8 | Info | V | Test matrices: 17 files in `docs/tests/` against 203 `_test.go` files. The global rule calls for one matrix per test file. |

## 7. Security review (whole codebase)

**Scope and method:** this is a separate pass over the existing code, not a diff.
- **Finding:** three read-only Sonnet subagents looked for vulnerabilities in three areas:
  - API and auth
  - Host actions and untrusted input
  - Platform and data
- **Filtering:** each candidate then got its own false-positive check.
- **Threshold:** only findings with confidence ≥ 8 are reported as vulnerabilities. The rest are listed below as "Below threshold".
- **Excluded:** DoS, rate limiting, secrets on disk and missing hardening without a concrete exploit.

### Confirmed vulnerabilities

#### V1: Unauthenticated admin API by default in server mode: `internal/api/token.go:25-28`

- **Severity:** High in server mode and Docker. Medium in daemon mode (loopback, so any local user or process).
- **Confidence:** 8–9
- **Category:** `auth_bypass`
- **Same as:** S1 and S2
- **Description:**
  - `tokenAuth` passes every request through while `apitoken.Enabled()` is false. That is the case whenever no token is stored, and also on any read or parse error of `api-tokens.json` (`internal/apitoken/apitoken.go:197-200`).
  - `tokenAuth` is the only auth layer in front of the whole API mux (`internal/api/server.go:139-141`).
  - Server mode listens on `:8740`, all interfaces (`internal/config/config.go:169-179`). The Dockerfile sets `STREAMCREW_MODE=server`, `EXPOSE 8740` and `CMD serve`.
  - Only desktop mode creates a token at startup (`internal/app/app.go:~587-591`).
  - The documented `STREAMCREW_TOKEN` in the systemd and Docker recipes is only the CLI client flag (`internal/cli/commands.go:33`). Nothing refuses to start, warns, or bootstraps a token.
  - This contradicts ADR-0003 (server: token required) and ADR-0013 §5.
- **Exploit scenario:**
  1. An operator runs `docker run -p 8740:8740 …` following `docs/usage.md`.
  2. A network attacker POSTs Connect JSON (`Content-Type: application/json`, no `Origin` header, so CORS does not interfere) to:
     - `ConfigService/Update`
     - `CommandService/Create` and `CommandService/Import`
     - `AuthService`, `BackupService` and `StreamService`
     - `SystemService/Shutdown`
  3. The attacker controls the bot: chat and stream actions on the streamer's channel, plus Script and `net:outbound` actions (the server-mode defaults). Host rights cannot be obtained today; see V3.
- **Not exploitable:** browser CSRF or DNS rebinding against a daemon on `127.0.0.1`. `internal/httpserver/cors.go` rejects every request that carries an `Origin` header not on the allow-list, and all Connect RPCs are POST.
- **Recommendation:**
  - Make `Enabled()` fail closed: return an error and answer 503 or 401.
  - In daemon and server mode, create a bootstrap admin token on first start, or refuse a non-loopback listen address without a token.
  - Warn in the log and in `doctor` (ADR-0013 §7).
  - Fix the systemd and Docker docs.

#### V2: Twitch OAuth token written to the log in plaintext: `internal/twitch/ws.go:292` → `:181`

- **Severity:** Medium
- **Confidence:** 8
- **Category:** `sensitive_data_exposure`
- **Same as:** S7, now confirmed
- **Description:**
  - On a fresh EventSub dial, `dialAndWelcome` puts the user OAuth token into the URL as `?access_token=` (`ws.go:285-300`). It then dials with `websocket.Dial(ctx, u.String(), …)` (`ws.go:313`) over a plain `http.Client` (`app.go:322`).
  - `coder/websocket` v1.8.15 (`dial.go:~224`) wraps the `http.Client.Do` error. Its `*url.Error` contains the full URL with the query: `stripPassword` only masks the userinfo password.
  - The error is logged as `"error", err` at three places:
    - `ws.go:181`
    - `internal/twitch/platform.go:368`
    - `internal/twitch/platform.go:450`
  - The slog redactor (`internal/logging/redact.go:44-52`) masks only by attribute key. `maskSecrets` in `internal/httpclient` is not on this path.
  - The code knew the risk: `ws.go:318-320` deliberately logs only host and path. The error path was missed.
- **Exploit scenario:**
  1. Any transport error on a fresh dial (offline, DNS, TLS, timeout, Twitch outage) writes the live streamer or bot token to the Warn log.
  2. `streamcrew diag bundle` copies the log files unfiltered (`internal/cli/diag.go:142-155`), although it promises that secrets are masked.
  3. Whoever receives a bug report with the bundle can use the token for Helix and chat calls as that account.
- **Recommendation:**
  - Strip the URL query from the error: `errors.As` to `*url.Error`, then replace `URL` with a redacted form.
  - Better: send the token as an `Authorization` header, if Twitch allows that.
  - Additionally, run `maskSecrets` over error values in `redact`.

#### V3: Latent: the config API could grant host rights: `internal/config/watch.go:257-361`

- **Status:** not exploitable in the shipped `serve` path (false-positive check, confidence 7). It is still a design contradiction that must be fixed before the watcher wiring is repaired (see C14).
- **Severity:** if reachable, High (admin token leads to host RCE). Effectively none today.
- **Design flaw:**
  - `Watcher.Update` accepts `grant`, `revoke`, `file_root` and `outbound_allow` with no check against widening:
    - `watch.go:31-37` lists `grant` and `revoke` as hot keys.
    - `parse` accepts them (`watch.go:344-361`).
    - `check` stores the new rights live (`watch.go:164-172`).
  - The registry reads the live rights on every check (`internal/action/registry.go:163-170`, `engine/run.go:272`).
  - Code-ADR-0021 (line 56) explicitly calls the rights keys hot. This contradicts ADR-0013 §3: "über die API lassen sich Capabilities nur einschränken, nie erweitern".
- **Why not exploitable today:**
  - Without a config file at startup, `serve` passes no file to the core, so `ConfigService/Update` answers `ErrConfigReadOnly`.
  - With a config file, `core.Run` (`core/core.go:158-160`) builds a fresh `config.NewFileResolver(...)` and never calls `Load`, which happens only in `cli/mock.go:133` and in tests. The watcher therefore has no path and an empty allow-list, and `Update` rejects every key as unknown.
  - Server mode by default grants only `net:outbound` and `script`; `host:process` is not included.
- **Recommendation:**
  - Settle the ADR conflict between ADR-0013 §3 and Code-ADR-0021 with the owner (rule "ADR-Abweichung vorher fragen").
  - Have `Watcher.Update` reject any widening relative to the startup rights. That covers new `grant`, `file_root` or `outbound_allow` entries and removed `revoke` entries.

### Below the threshold (confidence < 8, not reported as vulnerabilities)

| Candidate | Confidence | Assessment |
|---|---|---|
| **Cross-channel roles:** the `broadcaster`/`moderator` badge from an additional channel maps to `role.Streamer`/`Moderator` (`internal/twitch/map.go:420-427`). `checkRole` ignores the channel (`internal/requirement/role.go:26-34`), and the role is persisted globally (`eventservice/receive.go:570-583`). | 7 | A real role-scoping flaw, and it contradicts users-and-roles.md B25. With the default presets, a foreign mod can only add, change or disable chat commands through `addcmd`/`updatecmd`/`disablecmd`. It becomes worse if the streamer has privileged commands without a channel scope. Fix: derive roles per channel. |
| **IRC CRLF injection:** `IRCSession.Send` (`internal/twitch/irc.go:373`, `writeLine` `:326-330`) does not strip `\r`/`\n` (see T14). | 7 | Confirmed; the most realistic path is a moderator using `addcmd x ok$linebreakPRIVMSG #other :…`. It only matters if the bot is in other channels. Fix: replace CR/LF in `Send` and `writeLine`. |
| **Overlay scope equals read** (S8) | 8 for "false positive" | Overlay tokens are accepted only as a bearer header and created only through the CLI. No overlay endpoint with a URL token exists, so the semi-public URL scenario does not apply today. It becomes relevant once overlay endpoints exist. |
| **Fail-open on token-file read error** (part of V1) | 6 | Triggering it remotely would need file-descriptor exhaustion, so it is hard. It is fixed together with V1. |

### Checked and clean

- **External programs (`internal/action/host/program.go`, `words.go`):**
  - Words are split before rendering, so a chat value cannot add or split arguments.
  - There is no shell.
  - The environment is filtered.
  - Rights are checked per action (`engine/run.go:272`).
- **File action:** `os.OpenRoot` for every operation. `local()` rejects absolute paths and `..` after rendering.
- **Templates and expressions:** single-pass rendering, with no re-parsing of inserted values. `expr` and `textfunc` have no I/O.
- **SSRF (`internal/netguard`):**
  - The dialed IP is checked in `Control`, which covers DNS rebinding and redirects.
  - Only http and https are allowed, and no proxy is used.
  - IPv4-mapped addresses are unmapped. Minor gap: NAT64 and 6to4 are not on the blocklist.
- **Backup restore:** only fixed names are read from the zip, so there is no zip slip.
- **Command import (`internal/commandfile`):** parsing only, no execution.
- **SQL:** sqlc with bound parameters. `VACUUM INTO ?` is parameterized.
- **Chat identity:** roles come only from server-set Twitch badges. `FromBot` compares IDs.
- **API authorization:**
  - Scope mapping fails closed, so unknown paths require admin.
  - No token RPC exists.
  - Tokens are compared with `subtle.ConstantTimeCompare`.
  - CORS rejects every foreign `Origin`.
- **OAuth callback:**
  - The state is 32 random bytes and is checked.
  - The callback page is static, with no reflection.
  - It binds to loopback only.
- **Vault:** AES-256-GCM with a random nonce for each seal and the record name as AAD.

## Statistics

| Severity | Count |
|---|---|
| Critical | 1 |
| High | 6 |
| Medium | 28 |
| Low | 33 |
| Info | 2 |
