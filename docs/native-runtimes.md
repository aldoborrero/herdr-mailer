# Native Claude Code and Pi delivery

The package includes `herdr-mailer-runtime`, a Claude Channels MCP server and a Pi extension. Both connect automatically to the private mailer daemon. No model polls its inbox, and no terminal text is pasted. Codex continues to use `codex queue`.

## Register and launch a new session

Start the initialized mailer daemon as usual. Choose a **new runtime session UUID**, register the logical recipient and save its scoped token in a private file. Replace these absolute example paths with your own. Keep the daemon and runtimes under the same trusted Unix UID.

```sh
session=$(cat /proc/sys/kernel/random/uuid) # or uuidgen on macOS
state="$HOME/.local/state/herdr-mailer"
setup="$HOME/.local/state/herdr-mailer-runtime-test"
mkdir -m 700 "$setup"
herdr-mailer --state "$state" register reviewer \
  --runtime pi --location "$session" --output "$setup/reviewer.token"
jq -n --arg state "$state" --arg location "$session" \
  --arg credential "$setup/reviewer.token" --arg receipts "$setup/receipts" \
  '{runtime:"pi",state:$state,location:$location,credential:$credential,receipts:$receipts}' \
  > "$setup/config.json"
HERDR_MAILER_CONFIG="$setup/config.json" herdr-mailer-runtime
```

For Claude Code, use `--runtime claude` and `runtime:"claude"` in the configuration, then explicitly opt into the development channel:

```sh
HERDR_MAILER_CONFIG="$setup/config.json" \
  herdr-mailer-runtime --development-channel
```

The launcher sets `--session-id` to the registered runtime UUID. For Pi it passes the packaged extension with `-e`. For Claude it supplies an in-memory `--mcp-config` entry and the official `--dangerously-load-development-channels server:herdr-mailer` flag. That flag is the channel allowlist opt-in, **not a sandbox or tool-permission bypass**. The launcher never adds permission-bypass flags and writes no global configuration. Review Claude's channel consent prompt. The user must authenticate the runtime normally and select a working model; runtime executables are supplied by the user, not bundled or activated by the mailer package.

Send to `reviewer` from another registered mailer credential. Keep the runtime process open for automatic reception. Additional ordinary runtime arguments can follow the launcher command. Identity/channel override arguments are rejected. Starting, resuming or forking a different conversation requires a new registration/configuration. A Pi lifecycle change stops its connection; a mismatching session UUID cannot silently inherit the old route.

The launcher sets the child runtime's `HERDR_MAILER_CREDENTIAL` and `HERDR_MAILER_STATE` from the receiver configuration, overriding any sender values inherited from its parent. It preserves `HERDR_MAILER_CONFIG` for the inbound bridge. Consequently, outgoing `herdr-mailer send` and `reply` calls use the receiver's scoped identity by default. Only the credential file path is inherited; the token is never placed in argv or logs.

To resume the same Claude conversation after stopping it, keep its configuration, credential and receipt directory and run `herdr-mailer-runtime --development-channel --resume-session`. The launcher supplies only the configured UUID to `--resume`. Pi's `--session-id` reopens the exact registered session when it already exists. Neither path chooses a different or most-recent conversation implicitly.

The components are also available at `$out/share/herdr-mailer/runtimes/`: `claude.js`, `pi.js` and `bridge.js`. The launcher is the supported Claude session-binding path: MCP does not authenticate a Claude conversation UUID, so a bare MCP config alone cannot verify which conversation loaded it. Same-UID administrators remain trusted. No arbitrary external Claude socket is assumed.

## Prerequisites and official contracts

Developed against Claude Code 2.1.284, Pi 0.87.1 (`@earendil-works/pi-coding-agent`) and MCP SDK 1.32.0, locked in `runtimes/package-lock.json`.

- [Claude Channels reference](https://code.claude.com/docs/en/channels-reference): the MCP server advertises `capabilities.experimental["claude/channel"] = {}` and emits `notifications/claude/channel` only after initialization. It declares no permission-relay capability.
- [Claude Channels requirements](https://code.claude.com/docs/en/channels): research-preview availability, supported authentication and organization policy apply. A development allowlist bypass cannot override organization policy. Claude may silently discard notifications when the channel is not enabled.
- [Pi extension lifecycle and API](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md): sockets start on `session_start`, stop on `session_shutdown`, and messages enter through `pi.sendMessage` with `triggerTurn: true` and `deliverAs: "followUp"`. The extension factory has no connection side effects.

The extension only subscribes to session lifecycle. It does not echo assistant responses, message events or its own custom messages back into the delivery stream. Agents can use the existing scoped CLI to send/reply. Ask authorization and unique resolution remain enforced transactionally by SQLite.

## Durability, fences and receipts

SQLite remains authoritative for messages and pending jobs. Each bridge authenticates with the registered session token and exact runtime/location. Only one connection per mailer session is active. Reconnection uses bounded backoff; offline jobs remain pending until reconnect or expiry, including after daemon restart. Reconnection wakes pending jobs without waiting for the previous retry delay. Expiration, destination session and epoch are checked by the daemon and bridge before injection.

A delivery carries a fresh attempt UUID and a short injection lease. Registration is serialized with delivery. On disconnect the daemon retains the routing fence until the lease expires; the bridge checks the lease immediately before the runtime call. ACKs are bound to that attempt and accepted only for the runtime's supported status. Session rotation closes its old connection and old addressed messages remain stale.

The private receipt directory stores origin+message-ID+recipient-session hashes with a content fingerprint, fsyncing an intent before injection and the observed outcome afterward. A replay of a completed receipt sends another ACK without reinjecting; conflicting content is rejected. A crash between intent and outcome produces `native:ambiguous`, preserving the inbox record without automatically risking another injection. Do not delete receipt files to force retries; there is no exactly-once model-effect guarantee or automatic recovery for ambiguous outcomes. Disk failures fail closed. Protect and retain the receipt directory alongside the mailer state. Tokens are read from private same-UID files, never put in process arguments, receipts or logs.

Receipt meanings:

| State | Evidence |
| --- | --- |
| `broker:published` | JetStream accepted publication; independent of runtime delivery |
| `native:pending` | Awaiting a bridge/retry; message remains durable |
| `native:accepted` for Pi | The extension called the runtime's public `sendMessage` API successfully; that API returns void, so later provider errors or model responses are separate |
| `native:channel_written` for Claude | MCP notification written to the transport; Claude supplies no notification receipt, so this cannot assert runtime acceptance or processing |
| `native:ambiguous` | An injection may have occurred; automatic reinjection is stopped |
| `native:stale` / `native:expired` | Delivery stopped before injection because the destination changed or TTL elapsed |

Neither native status marks the inbox read or resolves an ask. A model response must be a separately authorized message/reply. Body limits remain 65,536 UTF-8 bytes, including escaped JSON; bridge frames are bounded. The trust boundary is the same local UID and configured owner, not cryptographically verified remote model identity.

## Build and validation

```sh
npm ci --prefix runtimes --ignore-scripts
npm --prefix runtimes test
go test ./...
go test -race ./...
go vet ./...
nix fmt
nix build .#herdr-mailer
nix flake check
```

The Nix package includes bridge dependencies and runs the official MCP client/server protocol tests and Pi SDK extension-loader tests without network access. Go tests cover the real Unix socket delivery protocol; model effects are mocked in automated tests. See [validation evidence](validation.md) for live-session results and precise limits. Optional Home Manager/NixOS modules continue to configure only the daemon and default to disabled; runtime launch remains explicit per session.

Existing placeholder Pi/Claude registrations from the initial release should be registered again with an exact runtime UUID and a new credential file. Their old metadata-only locations cannot identify a native destination. Registration rotates the mailer session/epoch; messages explicitly addressed to the old session remain stale.
