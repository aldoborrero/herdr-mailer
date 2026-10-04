# CLI compatibility

This is a new executable and protocol, not a drop-in replacement for hmail. It never opens hmail's live database and installs no hmail alias.

| Command shape | v0.1 behavior |
| --- | --- |
| `send <recipient> <body>` | Persist a message and delivery jobs; return JSON |
| `ask <recipient> <body>` | Persist kind `ask`; return immediately, no blocking answer wait |
| `reply <ask-id> <body>` | Only the ask recipient may reply; route to original author |
| `inbox --unread --json` | Authenticated logical agent's unread records |
| `read <id>` | Read own received message and mark it read |
| `status` | Counts of own inbox jobs by channel and state |
| `doctor` | SQLite check and own session/routing details |
| `events --follow` | Own live events as newline-delimited JSON |

All commands emit JSON in this initial release; `--json` is accepted for command-shape compatibility. Bodies are one quoted argument, limited to 65,536 UTF-8 bytes. Agent names are 1–64 ASCII letters, digits, underscores or hyphens. Herdr pane names, aliases, attachments, broadcasts, priority, parked-message listing, hmail config/env variables and MCP are outside this subset.

Message UUIDs are scoped to the stable logical author within the owner domain. If multiple received records share a UUID, `read ID --author NAME` and `reply ID BODY --author NAME` select its original author. Without that selector an ambiguous ID fails. The flag never changes the authenticated sender. Event records include `origin` as well as `messageId`.

`--ttl` sets native and broker-publication expiry, from a positive duration through 720 hours (default 24h). Expired records remain readable for audit but are never injected. `events --cursor N` resumes after N; persist the last successfully handled cursor. Non-follow events finish with `{"data":{"done":true}}` at the socket protocol level (CLI prints the data object).

The owner must `init`, start `daemon`, then `register` each worker. Use `--runtime codex --location EXACT_THREAD_UUID` for automatic native delivery. `none`, `pi` and `claude` are valid registration metadata but produce explicit `native:unsupported`; their inbox and event stream remain usable.
