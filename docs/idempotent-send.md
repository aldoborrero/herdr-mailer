# Retry-safe sends for supervisors

The authenticated `capabilities` operation reports protocol `2`, `idempotentSend: true` and `recipientPrecondition: true`. Existing send/ask/reply calls remain supported.

```sh
herdr-mailer --credential sender.token send worker 'Review the current task.' \
  --operation-id dispatch-42 \
  --recipient-session RECIPIENT_MAILER_SESSION_UUID \
  --recipient-epoch 3
```

The recipient session is mailer's session ID returned by registration, not the runtime UUID in `location`. Session and epoch must be supplied together. An initial send checks both inside the transaction which stores its message, delivery jobs, event and operation receipt. A stale target fails without writing any of these records.

Retry with the same operation ID and the same request. The namespace is owner plus logical author. The original message, timestamps, expiry and destination are returned without adding jobs or events, including after daemon restart or recipient rotation. A different request using that key fails. Current sender credentials remain mandatory: a retired sender token cannot replay a receipt.

Use a fresh key for a new intent. Do not change the key merely because the reply was lost. Replaying an expired message preserves its expiry; it does not renew delivery. A replay receipt is evidence of the original persisted send, not evidence that the current recipient or model consumed it.

The JSON socket fields are `operationId`, `recipientSession` and `recipientEpoch`. Keys contain 1–128 ASCII letters, digits, `_` or `-`. `ask` and `reply` support the same options; reply resolution remains unique and authorized. Calls without an operation ID deliberately retain the previous behavior of allocating a new message each time.

SQLite migrates by adding `send_operations`; messages, pending jobs, cursors and reply resolutions are preserved. Tests cover concurrent retries, restart, target rotation, changed content, author isolation, retired credentials, transactional rollback, replies, CLI flags and the Unix-socket API.
