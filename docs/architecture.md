# Architecture and security

## Identity

| Identity | Representation | Lifetime |
| --- | --- | --- |
| Owner | Local Ed25519 did:key | State bootstrap; back up privately |
| Logical agent | Owner DID plus local agent name | Stable across registrations |
| Session | UUID and monotonically increasing epoch | One registration |
| Runtime location | Exact Codex thread UUID | Explicit owner registration |
| Herdr pane | Not used as an identity or route | UI detail outside the protocol |

`init` uses Go's standard Ed25519 key generator. The DID encodes the public key with the standard `ed 01` multicodec varint and base58btc multibase. The private key and administrator token remain in mode-0600 `owner.json`; worker files contain only random 256-bit opaque session tokens. SQLite stores hashes of worker tokens. Public session records contain no secrets.

The application borrows typed records, stable identities and Lexicon descriptions from ATProto. The [Lexicon specification](https://atproto.com/specs/lexicon) defines the record schema format used under `lexicons/`. The provisional namespace is not owned, resolved or published. UUID record keys use the `any` key declaration; semantic validation additionally checks UUIDs, agent names, body size, kind, expiry and reply correlation. Records include `$type`; schemas describe the other properties because the discriminator is implicit in Lexicon records.

This system is not ATProto-compatible. ATProto currently supports [did:plc and did:web](https://atproto.com/specs/did), not did:key. ATProto signs [repository commits](https://atproto.com/specs/repository), not every individual message. This MVP does neither repository signing nor message signing, and implements no XRPC, MST, CID addressing, PDS, federation or firehose. did:key identifies the owner locally; it is not proof that a message was signed. See the [did:key method](https://w3c-ccg.github.io/did-key-spec/) and [Ed25519 Multikey encoding](https://www.w3.org/TR/vc-di-eddsa/#multikey) for the encoding convention.

## Local boundary

A mode-0700 state directory contains the mode-0600 Unix socket and database. OS directory/socket permissions define a same-UID trust domain. This protects against other UIDs, not an adversarial same-UID process with unrestricted filesystem access. Use separate OS users or sandbox filesystem access when stronger isolation is needed.

Only the administrator token can register an agent or change its native target. Worker requests derive their author, session and epoch from a registered token; a `fromAgent` field is rejected. A send rechecks the credential within the transaction that persists it. Worker credentials permit sending to any logical name in this owner domain, and reading/replying/subscribing only to their own inbox. There is no cross-owner messaging or per-sender recipient allowlist in v0.1.

Re-registering an agent revokes its old token. A send to an existing recipient writes `recipientSession` and `recipientEpoch` into the message itself, preserving the intended session across broker delays and daemon restarts. Both fields must be present together. The delivery worker checks the current UUID and epoch under the same lock used by registration, immediately before injection; mismatches become `stale` rather than jumping to the replacement session. Jobs for an unregistered recipient have no explicit target and bind the first session they encounter. Do not run multiple active routing authorities for the same logical agent.

## Persistence and delivery

One SQLite transaction validates reply authorization and inserts a message, native-delivery job, optional broker-publication job and recipient event. WAL and `synchronous=FULL` preserve committed state. An outbox failure rolls the entire transaction back. Incoming broker delivery uses the same persistence path but creates no broker job, preventing loops.

Persistent deduplication uses `(origin, UUID)`, where origin is the configured owner DID plus the stable logical author (`OWNER/AUTHOR`). Session rotation does not create a new origin. Identical records from the same origin are idempotent; different content for that key is rejected. Two origins may reuse a UUID independently. Events include the origin; `read` and `reply` reject ambiguous bare UUIDs and accept `--author` to select the intended received record. Internal SQLite keys are distinct from wire UUIDs. An automatic transaction upgrades the initial schema, preserving existing message rows, outbox jobs and event cursors.

A reply must reference a locally persisted `ask` from the reply recipient to the reply author in the same owner domain. This check runs inside the persistence transaction for both local sends and broker receipts, before any new row or subscriber ACK. A missing/out-of-order ask causes retry without persisting or acknowledging the reply. A broker-carried DID, author or session is not independent proof of remote identity: only trusted daemons with restricted publication credentials may publish into this owner domain.

Events have durable increasing cursors and are queried with the authenticated recipient predicate. A follow connection waits on change notifications, then queries after its last cursor. Agent clients do not poll. The daemon's delivery scheduler checks due jobs every 250 ms. Events and messages currently have no automatic pruning; cursors remain valid until an operator removes state.

The Unix protocol accepts one bounded JSON request per connection; unknown fields are rejected. Initial reads and writes have deadlines. Shutdown closes all connections, including clients that never complete a JSON document, and cancels workers. The client closes its socket when its context is cancelled. A daemon lock prevents a second process from replacing a live socket.

`native:accepted`, `broker:published`, unread/read and model processing are separate facts. Only the first three are observable here. Message IDs are included in native text so a receiving agent can recognize retries; the runtime has no application-provided exactly-once idempotency contract.
