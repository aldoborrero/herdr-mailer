# First implementation assignment

## User decision and purpose

The user chose a NEW project with compatibility for hmail's basic CLI, rather than a fork. Build a useful, tested initial release. The existing Codex in pane wD:p1 is coordinator; you are the implementation worker in your own worktree. Implement, test and commit; do not stop at an architecture proposal.

The user wants agents to receive information automatically, without repeatedly checking an inbox. Reuse useful ideas from ATProto but NO federation, PDS, public registry or firehose. NATS means NATS JetStream, not NAT traversal.

## Deliverable

1. Go CLI named herdr-mailer, local Unix-socket daemon and durable SQLite inbox. Preserve basic command shapes send <recipient> <body>, ask <recipient> <body>, reply <ask-id> <body>, inbox --unread --json, read <id>, status, doctor, events --follow. Explicitly document compatibility subset and differences rather than claiming drop-in parity. Additional identity/session registration and daemon commands are expected. Never install an hmail alias globally.
2. Separate owner identity, stable logical agent ID, session ID/epoch, and runtime location. Provide practical local identity bootstrap without external network dependence (e.g. standard did:key using a maintained implementation or well-tested multicodec encoding). Record an explicit ATProto-inspired subset and its differences: ATProto does not accept did:key identities and does not sign each individual message. Publish actual valid Lexicon definitions and typed records with $type, stable message ID, author/recipient, timestamps, kind, correlation. Use a namespace that is clearly provisional if no owned namespace is known. No fake did:web domain resolution or invented cryptography.
3. Local access uses a documented same-UID trust boundary, private state/socket permissions and real registered credentials for per-session attribution. An arbitrary fromAgent JSON field must not impersonate another session. Keep bootstrap owner keys out of worker credentials; scoped opaque tokens are acceptable for the MVP. Protect inbox/read/reply boundaries. Stable delivery routing must refuse a stale session epoch.
4. SQLite message + outbox writes in one transaction. Durable retry state, bounded backoff, explicit expiration checked BEFORE any injection. A recipient-specific live event stream/cursor and cancellation-safe socket handling. Received messages survive daemon restart; no busy-loop polling by agents.
5. Optional NATS JetStream bridge using nats.go. A configured private stream and durable recipient consumer, stable Nats-Msg-Id, dedup/inbox idempotency and ACK only AFTER local persistence. Never use broker ACK as native-delivery receipt. Local mode works without NATS; broker outages preserve the local outbox and reconnect. Retention must preserve intended offline delivery. Document routing and broker authentication/subject permissions; avoid network-open defaults. Test with a real isolated NATS server.
6. Native Codex delivery first: inspect installed codex queue --help and use exact registered session UUID, with executable arguments (no shell concatenation). codex queue uses the shared app-server IPC; terminal text injection is not native delivery. Document what queue acceptance proves, timeout/ambiguous-crash behavior, retry/duplicate limits and security boundary. Keep an adapter interface for Pi and Claude. Do not pretend stubs deliver: unsupported adapters must return explicit unsupported status. If practical, add a Pi extension subscribing to recipient events; Claude Channels needs explicit opt-in and can be documented as a next milestone.
7. House-style Nix flake: numtide/blueprint prefix=nix, package, devshell, treefmt/check, lock, justfile, pinned CI action hashes verified from primary sources, README, license and Home Manager/NixOS service module suitable for later integration. Follow /home/aldo/.codex/skills/nix-project-scaffold/SKILL.md and referenced skills/templates. Module must not enable external NATS access by default. Do not apply it to the live host.

## Acceptance evidence

Run meaningful tests for send/ask/reply/read; unauthorized sender/recipient; duplicate receipt; restart durability/outbox replay; broker disconnect/reconnect/offline recipient; ACK-before-persist prevention; stale epoch; expired delivery; an incomplete socket request not blocking shutdown; and Codex adapter argument correctness/failure propagation. Use fakes for model side effects and real isolated broker integration. Report separately which native interactions were live-tested and which were mocked.

Run go test ./..., go test -race ./... where available, go vet, nix fmt, nix flake show, a devshell smoke check, nix build and nix flake check on the host system. Fix failures. Tests requiring NATS must not silently skip in the Nix package/check; include server in check inputs. Keep network access out of build tests.

Make coherent local commits on the assigned branch. Report results, exact commands, limitations and remaining work to the coordinator. You may use hmail send wD:p1 for milestones (the CLI is available; installed MCP is not configured), or the coordinator will inspect Herdr. Do not send messages to unrelated agents. Checkpoints: architecture established; core/local delivery works; NATS + Nix checks green.

## Audited references and useful local tools

Existing MIT hmail source: /tmp/herdr-messaging.wWEw52/mail (upstream husniadil/herdr-mail, commit 5d32a38476ed5f02493f1e6991f3dcf5d3fca917). Reuse selectively with attribution. Known hazards: incomplete JSON connections block shutdown; parked list leaks other recipients; delivery deadline checked after attempt. Do not inherit these bugs.

NATS experiments: /tmp/hmail-nats-research.eYnFMj (isolated tests previously passed; these are evidence, not the new implementation).
NATS server available: /nix/store/6bns5h7y5png9ska6aan6hax12szzmvf-nats-server-2.14.7/bin/nats-server.
Pinned nixpkgs available: /nix/store/s7cw0cq2w6qdqixcif23fsacbka4cv0j-source.

Primary references to verify as needed:
- https://atproto.com/specs/lexicon
- https://atproto.com/specs/did
- https://atproto.com/specs/repository
- https://docs.nats.io/learn/jetstream
- https://learn.chatgpt.com/docs/app-server
- https://code.claude.com/docs/en/channels-reference
- Pi extensions: current upstream packages/coding-agent/docs/extensions.md in pi-mono.

Keep the implementation focused. Deliver a real local + broker + Codex vertical slice before adding further runtimes, UI, federation or orchestration features.
