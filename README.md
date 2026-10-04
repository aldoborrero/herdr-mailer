# herdr-mailer

Durable inter-agent messaging for Herdr. A private local daemon stores messages in SQLite and automatically delivers them to registered Codex sessions with `codex queue`. Agents can also subscribe to recipient-scoped events without checking an inbox repeatedly.

This is a new project with a small hmail-compatible command surface. The executable, state directory and credentials are separate from hmail. Version 0.1 supports one trusted local owner, optional private NATS JetStream, and native Codex delivery. Pi and Claude return explicit unsupported delivery status.

## Quick start

```sh
nix develop
just build
export PATH="$PWD/result/bin:$PATH"
herdr-mailer init
herdr-mailer daemon
```

In another terminal, register sessions. Registration is an owner operation, and each registration rotates that agent's session ID, epoch and token. Replace the UUID below with the exact existing Codex thread UUID. The daemon must run as the same user as that Codex app server, with its `codex` executable on PATH.

```sh
herdr-mailer register coordinator --runtime codex \
  --location 00000000-0000-4000-8000-000000000001 \
  --output /tmp/coordinator-mailer.token
herdr-mailer register worker --output /tmp/worker-mailer.token
export HERDR_MAILER_CREDENTIAL=/tmp/worker-mailer.token
herdr-mailer send coordinator 'Implementation is ready for review.'
herdr-mailer ask coordinator 'Should the next batch use the same schema?'
```

Use a new private credential filename; existing files are never overwritten. Give each worker only its token file. Do not give workers `owner.json`. Same-UID processes are trusted by the OS boundary; session credentials enforce attribution through the API, not isolation from a malicious process that can read the owner's files.

```sh
herdr-mailer --credential /tmp/coordinator-mailer.token inbox --unread --json
herdr-mailer --credential /tmp/coordinator-mailer.token read MESSAGE_UUID
herdr-mailer --credential /tmp/coordinator-mailer.token reply ASK_UUID 'Yes, keep it.'
herdr-mailer events --follow --cursor 0
herdr-mailer status
herdr-mailer doctor
```

`ask` returns immediately with the durable message record. `reply` correlates with an ask addressed to the authenticated agent. All v0.1 command output is JSON. Reading marks a message read, which does not establish model processing. Native queue acceptance also does not mark it read.

State defaults to `$XDG_STATE_HOME/herdr-mailer` or `~/.local/state/herdr-mailer`. Override with `--state` or `HERDR_MAILER_STATE`; directory permissions must be 0700. `--credential` or `HERDR_MAILER_CREDENTIAL` names a token file. No global aliases, hmail migrations, shared-server restarts or agent configuration edits are performed.

## Optional JetStream

Local mode needs no broker. See [operations](docs/operations.md) for a private broker configuration and ACL requirements. After provisioning one:

```sh
herdr-mailer daemon --nats-url nats://127.0.0.1:4222 \
  --nats-stream HERDR_MAILER --nats-prefix herdr_mailer \
  --nats-credentials /private/path/bridge.creds
```

The bridge keeps a separate durable broker outbox. Broker outages do not prevent local persistence. A broker ACK means persistence at that layer; it is never treated as a native-delivery receipt. SQLite suppresses repeated `(owner, author, message ID)` records even after JetStream's deduplication window ends and rejects conflicting content for that key.

## Development

```sh
nix develop
just test
just test -race
just lint
just fmt
just check
just build
just run --help
```

`nats-server` is mandatory for tests. Tests start isolated loopback servers and temporary state directories; no real Codex messages are sent by tests. Outside the devshell, put `nats-server` on PATH or set `NATS_SERVER` to its executable. The Nix package runs all Go tests, the race detector and `go vet` without external network access. The flake's formatter check is also a CI gate.

## Design and limits

- [Architecture, identity and security](docs/architecture.md)
- [CLI compatibility subset](docs/compatibility.md)
- [Operations, retry behavior and optional service modules](docs/operations.md)
- [Acceptance evidence](docs/validation.md)
- [Versioned Lexicons](lexicons/)

The records use ATProto-inspired conventions, with a deliberately provisional `com.example.herdr` namespace. This is not an ATProto implementation: did:key, private session credentials and SQLite replace its identity/repository stack. No federation, PDS, registry, firehose or exactly-once model effects are claimed.

MIT licensed; see [LICENSE](LICENSE). No hmail source code was copied. The basic command shapes and earlier failure reports informed this implementation.
