# Operations

## Native delivery

The installed CLI was inspected with `codex queue --help`. The adapter executes `codex`, `queue`, `--thread`, the registered UUID, `--message`, and the formatted body as separate arguments. It never runs a shell or injects terminal keystrokes. Codex uses the existing shared app-server IPC; the daemon must have the same UID, Codex environment and access as the intended runtime. No server is restarted or configured by this project. The [official app-server documentation](https://learn.chatgpt.com/docs/app-server) describes the underlying runtime boundary; the installed command's help is the authority for this build-specific `queue` surface.

Successful exit means the queue command accepted the request. It does not prove that the model saw, understood or acted on it. The native command has a 15-second deadline, shortened to the message expiry. Expiration is checked before invoking the adapter and again immediately before process start. Timeouts/cancellation are recorded as `ambiguous` and not automatically retried. Other failures retry with exponential delays of 1, 2, 4, ... seconds, bounded at 256 seconds, with at most eight native attempts. Exhaustion is `failed`; unsupported adapters and stale epochs are terminal. Jobs without a registered recipient retry until expiry.

A crash after queue acceptance and before recording success can replay delivery on restart. A nonzero exit might also follow a remote acceptance. There is no atomic transaction spanning Codex and SQLite; duplicates remain possible. Include the stable message ID in application-level deduplication and make side effects idempotent. Terminal jobs currently require operator investigation and a deliberate new send; there is no automatic unsafe replay command.

`read` marks a record read and does not cancel a pending native job. `status` reports only jobs for the caller's own inbox. Run `doctor` with that caller's token. Daemon logs report processing errors without logging bodies or tokens.

Pi can later implement the delivery interface or host an extension consuming `events --follow`. No Pi extension is shipped or claimed as native delivery. Claude Channels requires an explicit opt-in and separately reviewed integration; it is a next milestone. No stub reports successful delivery.

## JetStream boundary and retention

The bridge is optional and disabled by default. Use a private NATS account and a unique stream/prefix per owner DID. Workers receive only local session tokens; never give them the bridge's broker credentials. Broker publishers are trusted daemon infrastructure, not individual agents. The daemon authenticates a local sender before publishing, and consumers verify the subject's author and recipient against the typed message and check the configured owner DID before persistence. Anyone with bridge publish authority can forge the private domain, so account and subject permissions are part of the security boundary. Message records are not cryptographically signed.

Subjects are `PREFIX.AUTHOR.RECIPIENT`; both names are single safe tokens. The durable recipient consumer is a deterministic hash of prefix and recipient. Only registered recipients are consumed. The first consumer starts from all retained messages, including messages published before registration. Use one active daemon per logical recipient; two daemons sharing the same durable can distribute deliveries rather than mirror them. Cross-owner trust, remote discovery and multi-host identity provisioning are not implemented. V0.1's supported topology is one local authority with its private durable bridge; the server can be remote through an explicitly configured authenticated endpoint.

The stream uses file storage, limits retention, no maximum age and discard-new. It rejects incompatible preexisting retention settings rather than silently adopting them. The bridge creates an unlimited stream when absent; in production pre-provision storage capacity and monitoring. Per-subject limits additionally require `DiscardNewPerSubject=true`; otherwise the bridge rejects the stream. Finite server/account/stream limits with discard-new preserve old offline messages and reject new publications, which remain in the local outbox. Work-queue/interest retention or discard-old can lose intended offline delivery and are not accepted.

Every publication uses the stable UUID as `Nats-Msg-Id`. The broker deduplication window is two minutes on streams the bridge creates. SQLite's message primary key and content check provide durable idempotency after that window. Consumers ACK synchronously only after local persistence succeeds; persistence failure NAKs for retry. Malformed or untrusted messages remain unacknowledged/NAKed, so investigate repeated receive errors and remove poison records administratively if necessary. A broker ACK never changes `native:*` status.

Outages retain local messages and outbox rows. Initial connection retries every two seconds; the NATS client reconnects indefinitely. Broker publication retries use the durable bounded exponential schedule until message expiration; native delivery continues locally. Retained expired broker records may still arrive but cannot be injected. Broker stream deletion/reconfiguration while running requires operator repair and daemon restart; transparent consumer recreation after administrative deletion is not guaranteed.

For a private development server (never use this unauthenticated setup on a public interface):

```sh
mkdir -m 700 /tmp/herdr-mailer-nats
nats-server -js -a 127.0.0.1 -p 4222 -sd /tmp/herdr-mailer-nats
```

Production should use TLS and an isolated account with a protected NKey/JWT credentials file passed via `--nats-credentials`. A bridge needs publish access to its `PREFIX.*.*`, replies/ACKs under `$JS.ACK.STREAM.>` and its stream/consumer API subjects under `$JS.API`; subscribe access is limited to its private reply inboxes (`_INBOX.>`). Stream/consumer administration should be scoped to the selected stream; pre-provision resources to avoid broad account administration. Never grant wildcard private-prefix publish access to untrusted workers. Authentication and ACL policy are broker/operator responsibilities, not auto-configured by this CLI.

## State and services

Back up the private owner file and SQLite consistently (stop this daemon or use SQLite's backup API; do not copy only the main database while WAL is active). Losing `owner.json` loses the owner identity and admin credential. Do not reinitialize over it or copy it into a worker credential. Tokens are not printed by registration; they are written with exclusive creation and mode 0600. An interrupted registration can rotate a session before its token file is saved; recover by registering again to a fresh output path.

The flake exports `homeModules.herdr-mailer` and `nixosModules.herdr-mailer`. Both default to disabled and expose a package and optional Codex package. They start only a local daemon, with umask 0077, and enable no external NATS listener or firewall rule. Initialize the selected user's state first. Home Manager is preferred when Codex uses that user's login environment. NixOS requires an explicit existing user and absolute state directory; ensure the system service can access the user's Codex IPC environment. These modules are provided for later integration and have not been applied to the host.

```nix
{
  imports = [ inputs.herdr-mailer.homeModules.herdr-mailer ];
  services.herdr-mailer = {
    enable = true;
    package = inputs.herdr-mailer.packages.${pkgs.stdenv.hostPlatform.system}.herdr-mailer;
    # codexPackage = yourExistingCodexPackage;
  };
}
```

## CI provenance

The workflow derives nixpkgs from `flake.lock`. Action tags and full commit hashes were checked against their upstream releases on 2026-10-04: [checkout v7.0.0](https://github.com/actions/checkout/releases/tag/v7.0.0), commit [9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0](https://github.com/actions/checkout/commit/9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0), and [install-nix-action v31.10.6](https://github.com/cachix/install-nix-action/releases/tag/v31.10.6), commit [8aa03977d8d733052d78f4e008a241fd1dbf36b3](https://github.com/cachix/install-nix-action/commit/8aa03977d8d733052d78f4e008a241fd1dbf36b3). CI runs one flake check, including package tests, race tests, vet and formatting. No repository action has been published or triggered during local implementation.
