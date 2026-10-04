# herdr-mailer

New Go project: durable inter-agent messaging for Herdr with ATProto-inspired identity and records, optional NATS JetStream transport, and native runtime delivery. Read IMPLEMENTATION.md before coding.

## Layout

| Path | Purpose |
| --- | --- |
| cmd/ | CLI entry points |
| internal/ | Identity, storage, daemon, transport, delivery adapters |
| lexicons/ | Versioned message and session record definitions |
| nix/ | Blueprint packages, modules, devshell and formatter |
| docs/ | Architecture, compatibility and operations |

## Commands

Implement a justfile providing test, lint, fmt, check, build and run. Go tests must also run in the Nix package build. Use the local nix-project-scaffold, nix-blueprint and nix-treefmt-formatter skills under /home/aldo/.codex/skills/.

## Invariants

SQLite is authoritative. A transport ACK is not proof that a model processed a message. Never conflate owner, logical agent, session and pane identity. Validate authenticated senders and recipient access. Do not claim full ATProto compatibility or exactly-once model effects.

Use a separate application state directory and executable name. Do not replace the installed hmail, read its live message database, change global agent settings, or restart Herdr/shared Codex servers. Tests use isolated state and private test servers.

The user authorized this implementation and local commits, with the existing Codex in Herdr pane wD:p1 coordinating. Work only in the assigned worktree. Do not push, publish, deploy to NixOS or modify nix-system. Preserve attribution for reused MIT source. Do not spawn additional workers without coordinating scope first.
