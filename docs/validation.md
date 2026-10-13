# Acceptance evidence

Validated locally on 2026-10-04 in the assigned `feat/initial-mailer` worktree, on x86_64-linux. No push, deployment, NixOS activation, global configuration change, installed hmail replacement or shared-server restart was performed.

## Commands

The native Go runs used isolated cache/server settings:

```sh
export GOPATH="$PWD/.go"
export GOMODCACHE="$PWD/.go/pkg/mod"
export GOCACHE="$(mktemp -d)"
export NATS_SERVER="$(command -v nats-server)"

go test ./...
go test -race ./...
go vet ./...
nix fmt
nix flake show
nix develop --command bash -c 'go version && nats-server --version && just --list && test "$GOPATH" = "$PRJ_ROOT/.go" && test "$GOMODCACHE" = "$PRJ_ROOT/.go/pkg/mod"'
nix build .#herdr-mailer --print-build-logs
nix flake check --print-build-logs
```

All listed checks passed. The host Go toolchain was 1.26.7; the locked Nix toolchain is 1.26.8 and NATS is 2.14.7. The Nix build independently runs tests, race tests and vet with mandatory `nats-server` in `nativeCheckInputs`. It accesses only private loopback/Unix sockets during checks. Dependency downloads occur in the separate fixed-output vendor derivation. CI has not run remotely.

Blueprint exposes the package, devshell, formatter and formatter check; its additional output names cause harmless Nix warnings. Checks ran for the host system only. aarch64-linux and aarch64-darwin outputs are declared but were not built. x86_64-darwin is excluded because the pinned nixpkgs no longer supports it.

## Behavioral coverage

| Evidence | Tests |
| --- | --- |
| CLI init/register/send/ask/reply/inbox/read/status/doctor/events | `internal/cli` |
| Token-derived author, unauthorized read/reply/register, unknown `fromAgent` rejection | `internal/store`, `internal/daemon` |
| Revoked session tokens and event subscriptions | `internal/store`, `internal/daemon` |
| Atomic message/outbox rollback on simulated outbox failure | `internal/store` |
| Duplicate receipt and conflicting-ID rejection | `internal/store`, `internal/transport` |
| Durable messages, events, retry jobs and restart replay | `internal/store`, `internal/daemon` |
| Recipient-specific cursor replay, including more than 100 events | `internal/daemon` |
| Stale epoch, expired-before-injection, offline first binding | `internal/daemon` |
| Retry delay, eight-attempt limit, explicit unsupported status, ambiguous cancellation persistence | `internal/daemon` |
| Incomplete JSON does not block shutdown; stream client cancellation | `internal/daemon` |
| Exact Codex argv, body stays one literal argument, failure/timeout propagation | `internal/delivery` |
| Private bootstrap, owner database binding, Ed25519 Multikey vector | `internal/identity`, `internal/store` |
| Typed record semantics and published Lexicon consistency | `internal/record` |
| Real JetStream offline recipient, safe per-subject retention, dedup, disconnect/reconnect and outbox replay | `internal/transport`, `internal/daemon` |
| No ACK on persistence failure, redelivery then ACK; short fetch deadline does not cancel disk persistence | `internal/transport` |

Broker integration tests fail rather than skip when `nats-server` is unavailable. Every server binds `127.0.0.1` on a private temporary port with isolated storage. Model-side effects are mocked in the automated suite. JWT/TLS account policy, remote broker topology and Home Manager activation have not been live-tested. Pi and Claude follow-up evidence is recorded below.

## Native live interaction

The installed `codex queue --help` was inspected. A separate temporary mailer daemon, using the Nix-built binary and fresh owner/session credentials, sent one authorized milestone to coordinator thread `01a0f9be-cb70-7390-bcad-7c50b52940be` through the actual Codex adapter.

- Message: `dfd4c97b-d3af-4174-90d3-a7f22e758e4f`.
- Observed recipient status: `{"native:accepted":1}`.
- The private daemon was stopped after the check; the shared Codex app server was untouched.

This proves successful queue-command acceptance through the real adapter. It does not prove model processing or exactly-once effects. Failure, ambiguous timeout, injection argument safety and delivery retry behavior were verified with fakes, not forced failures against the shared runtime.

## Current limitations

One trusted same-UID owner domain and one local routing authority; no cross-owner federation, PDS, public identity resolution or multi-host identity provisioning. Durable storage has no automatic pruning, quotas, schema migration command or terminal-job replay command. Broker poison records need operator investigation. Native bridges support Pi and Claude in addition to Codex, subject to the prerequisites below. Session credential isolation depends on the documented OS trust boundary. Codex crash windows may duplicate delivery; bridge intent/outcome windows are retained as ambiguous without automatic reinjection.

The NixOS service module was also evaluated with `nixpkgs.lib.nixosSystem` against a synthetic user and a temporary state path. The resulting unit selects the mailer executable, the explicit user, umask 0077 and restart-on-failure; no host configuration was activated.

## Coordinator invariants follow-up

Implementation worker session UUID: `01a1074c-41e3-7210-99b4-319fe85549c8` (from the session environment). This follow-up keeps the same assigned worktree and uses no additional agents.

| Invariant | Concrete tests |
| --- | --- |
| Trusted single-owner NATS domain, strict payload/route checks, restricted publishing credentials | `TestUntrustedEnvelopeNeverPersistsOrAcknowledges`, `TestPrivateBrokerRejectsUnauthorizedPublishers` (real server with distinct bridge/worker credentials and subject ACLs) |
| UUID/epoch preserved through delayed broker receipt; registration serialized with injection | `TestDelayedBrokerRecordKeepsExplicitRecipientSession`, `TestRegistrationSerializedWithNativeInjection`, `TestNativeEpochExpiryAndDurableRetries` |
| Persistent origin+ID dedup, conflicting content rejected, correct origin selection | `TestOriginScopedDedupRestartAndAmbiguousRead`, `TestBrokerDedupIncludesOrigin`, `TestUpgradePreservesLegacyRowsJobsAndEventCursors` |
| Reply correlation and recipient authorization before persistence and ACK | `TestIncomingReplyAuthorizationBeforePersistence`, `TestReplyNotAcknowledgedUntilAuthorizedAskExists` (real server, pending ACK until the ask exists) |
| Broker ACK, native acceptance and unread/model state kept separate | `TestBrokerAckDoesNotClaimNativeAcceptanceOrRead`, `TestShutdownPersistsAmbiguousOutcome`, `TestTimeoutAmbiguousAndExpiredNoInvocation` |
| Exact queue arguments; no sandbox or approval override flags | `TestCodexArgumentsAndFailure`; state/IPC prerequisites and scoped Auto approval documented in operations |

The broker ACL test uses test-only username/password credentials on isolated loopback. It verifies the permission boundary, not production NKey/JWT/TLS provisioning. No new live messages to model runtimes were needed for these regression tests. The initial live Codex evidence above remains limited to queue acceptance. An additive SQLite migration now upgrades the initial schema automatically; no separate general-purpose migration or replay command is provided.

## Unique ask resolution review

The coordinator's three original reproductions are included in `internal/store/coordinator_review_test.go`. On revision `924135a`, forged and unknown-ask broker replies were already rejected, while `TestCoordinatorRejectSecondReply` reproduced the missing single-resolution constraint. All three pass with the resolution registry.

Additional regressions:

- `TestReplyRejectsInvalidRoutesWithoutPartialState` and `TestInvalidReplyRoutesAndCorrelationNeverACK`: wrong sender, wrong recipient, missing ask and non-ask correlation; message/job/event/resolution counts remain unchanged and ACK is never called.
- `TestSingleResolutionAndExactReplay`: first local response succeeds; distinct local/broker responses and modified replays fail; identical replay is idempotent.
- `TestConcurrentRepliesChooseOneResolution`: 16 simultaneous local, broker or mixed attempts produce one resolution and no partial rows.
- `TestConcurrentReplayIsIdempotent`: 16 identical deliveries insert one reply/job/event/resolution.
- `TestResolutionRollsBackWithJobsAndEvents`: failed job/event inserts also roll back the reply and resolution, allowing a later valid retry.
- `TestBrokerReplyResolutionRejectsSecondButAcknowledgesReplay`: real isolated JetStream accepts/ACKs exact replay and leaves a distinct second response unacknowledged; SQLite remains unchanged.
- `TestResolutionSurvivesRestartAndUpgrade`, `TestUpgradeRejectsConflictingLegacyReplies` and `TestResolutionsScopedToAskOrigin`: upgrade/restart durability, preserved evidence on inconsistent legacy history, and independent asks sharing a UUID across origins.

These guarantees concern durable ask resolution in SQLite. Native queue acceptance and model effects retain the separately documented ambiguity and duplicate limits.

## Escaped request body review

The coordinator's `TestCoordinatorAcceptMaxEscapedBody` reproduced `unexpected EOF` with 65,536 newline bytes under the old 128 KiB document cap. It now passes with the 409,600-byte cap. `TestRequestBodyByteLimit` additionally checks maximum six-byte escapes, multibyte UTF-8 preservation, and rejection of oversized decoded bodies. `TestRequestDocumentLimit` checks acceptance at the exact document boundary, rejection one byte beyond it, and abusive metadata/leading whitespace. Rejected requests leave no messages, jobs, events or resolutions.

All four coordinator reproductions, the complete Go test/race/vet suite, formatting, Nix package build and host flake checks passed after this correction. These are local validation results; coordinator review and integration approval remain separate.

## Native Claude Code and Pi follow-up

Base revision: `87c3b5b`; implementation branch: `feat/native-runtimes`. The README support table is updated and its SVG banner is unchanged. Worker session UUID remains `01a1074c-41e3-7210-99b4-319fe85549c8`.

Automated evidence:

| Contract | Coverage |
| --- | --- |
| Token + runtime + exact location, one connection per session | `TestNativeRuntimeAuthenticatedDeliveryAndReceipts` |
| Lost connection retains job and replays with a fresh attempt | `TestRuntimeDisconnectRetainsAndReconnectReplays` |
| Offline bridge does not exhaust Codex's retry limit; rotation makes old destination stale | `TestRuntimeOfflineNeverExhaustsAndRotationIsStale` |
| Session registration waits for an in-flight injection lease, including disconnect | `TestRuntimeDisconnectFencesRegistrationAndOldAddress` |
| Wrong attempt cannot acknowledge a job | `TestRuntimeWrongAttemptCannotAcknowledge` |
| Correct channel capability, initialization ordering and notification schema | Official MCP SDK client/server over linked transports in `runtimes/test/bridges.test.js` |
| Pi extension loading, `triggerTurn`, follow-up delivery, shutdown and wrong-session refusal | Official Pi 0.87.1 extension loader, with mocked runtime action and context |
| Persistent replay, content collision and crash ambiguity | Receipt ledger reopened between deliveries; real socket disconnect/reconnect tests |
| Bounded frames, UTF-8 body limits and final lease check | Validation and full 65,536-byte six-character-escaped body over a real socket |
| Explicit Claude channel opt-in and no token or permission bypass in argv | Launcher tests with isolated fake executables |

The Go suite includes all previous coordinator reply/escaped-body regressions and real private NATS integration. Bridge tests use temporary private sockets/receipt directories and never call a model. The Nix package runs Go tests/race/vet and the JavaScript SDK tests. `npmDepsFetcherVersion = 2` accommodates Pi's published shrinkwrap; five omitted nested package integrities are filled from their exact registry versions in the committed lockfile. Runtime production closure omits the Pi SDK test dependency: Pi is supplied by the user's own runtime installation.

Live checks used only **new isolated test sessions**, a temporary daemon, new credentials and temporary Claude/Pi configuration. No existing session, global settings or host service was changed. Both processes and the private daemon were stopped afterward.

- Pi 0.87.1: runtime UUID `222ed1d0-f1f5-490b-80ac-b03218fddc8f`; mailer message `facf790d-3557-4ad7-b93a-c011e46d027d`. The real RPC output contains `message_end` with `role: custom` and that exact message ID/body. SQLite reports `native:accepted`. A subsequent assistant event reports `Provider is not configured: anthropic`, so no successful model response is claimed.
- Claude Code 2.1.284: runtime UUID `cef8bb39-4392-41c6-b02e-fcee8f9c25d2`; mailer message `2f2d1bb3-3a9d-4f35-b434-6ac5e891aa8f`. The real Claude initialization event reports the same session UUID and the `herdr-mailer` MCP server as connected. SQLite reports `native:channel_written`. The isolated runtime reports `Not logged in · Please run /login`, `terminal_reason: api_error`, zero input/output tokens and zero cost. Neither channel consumption nor a model response is claimed.

For these checks Pi ran in RPC mode with tools and resource discovery disabled, and Claude ran in print/stream-JSON mode with a strict MCP configuration, empty setting sources and tools disabled. Claude's development-channel flag was explicitly supplied. Pi's API submission and Claude's transport write remain different evidence from a model response, independently of login/model availability.

## Runtime child identity review

The coordinator's launcher reproduction confirmed that a parent sender's `HERDR_MAILER_CREDENTIAL` and `HERDR_MAILER_STATE` reached the simulated receiver unchanged. Before the fix, `TestRuntimeLauncherReplyUsesReceiverEnvironment` also failed for both Pi and Claude by attempting to connect to the sender's state directory.

The launcher now sets both variables from the receiver configuration and retains `HERDR_MAILER_CONFIG`. The JavaScript launcher regression checks both runtimes under a conflicting parent environment. The Go integration test starts an isolated daemon, creates a real ask, launches each simulated runtime, and invokes the actual CLI entry in a subprocess with only `reply <ask-id> <body>` arguments. It verifies receiver authorship, routing back to the original sender, correlation, persistence in the sender inbox and absence of raw tokens in stdout/stderr. Node is a mandatory Go package check input in Nix so this regression cannot silently skip. No real runtime sessions are started by these tests.
