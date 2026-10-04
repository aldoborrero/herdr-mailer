# Acceptance evidence

Validated locally on 2026-10-04 in the assigned `feat/initial-mailer` worktree, on x86_64-linux. No push, deployment, NixOS activation, global configuration change, installed hmail replacement or shared-server restart was performed.

## Commands

The native Go runs used isolated cache/server settings:

```sh
export GOPATH="$PWD/.go"
export GOMODCACHE="$PWD/.go/pkg/mod"
export GOCACHE=/tmp/herdr-mailer-go-cache
export NATS_SERVER=/nix/store/6bns5h7y5png9ska6aan6hax12szzmvf-nats-server-2.14.7/bin/nats-server

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

Broker integration tests fail rather than skip when `nats-server` is unavailable. Every server binds `127.0.0.1` on a private temporary port with isolated storage. Model-side effects are mocked in the automated suite. JWT/TLS account policy, remote broker topology, Home Manager activation, Pi and Claude delivery have not been live-tested.

## Native live interaction

The installed `codex queue --help` was inspected. A separate temporary mailer daemon, using the Nix-built binary and fresh owner/session credentials, sent one authorized milestone to coordinator thread `01a0f9be-cb70-7390-bcad-7c50b52940be` through the actual Codex adapter.

- Temporary state: `/tmp/herdr-mailer-native.QMSknM`.
- Message: `dfd4c97b-d3af-4174-90d3-a7f22e758e4f`.
- Observed recipient status: `{"native:accepted":1}`.
- The private daemon was stopped after the check; the shared Codex app server was untouched.

This proves successful queue-command acceptance through the real adapter. It does not prove model processing or exactly-once effects. Failure, ambiguous timeout, injection argument safety and delivery retry behavior were verified with fakes, not forced failures against the shared runtime.

## Current limitations

One trusted same-UID owner domain and one local routing authority; no cross-owner federation, PDS, public identity resolution or multi-host identity provisioning. Durable storage has no automatic pruning, quotas, schema migration command or terminal-job replay command. Broker poison records need operator investigation. Supported native delivery is Codex only; Pi/Claude are explicit unsupported adapters. Session credential isolation depends on the documented OS trust boundary. Crashes between native acceptance and receipt persistence may duplicate delivery.

The NixOS service module was also evaluated with `nixpkgs.lib.nixosSystem` against a synthetic user and `/tmp/herdr-mailer-module-eval` state path. The resulting unit selects the mailer executable, the explicit user, umask 0077 and restart-on-failure; no host configuration was activated.
