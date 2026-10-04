import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import net from "node:net";
import { randomUUID } from "node:crypto";
import { setTimeout as delay } from "node:timers/promises";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { z } from "zod";
import {
  loadExtensions,
  createExtensionRuntime,
} from "../node_modules/@earendil-works/pi-coding-agent/dist/core/extensions/loader.js";
import { readConfig, Receipts, startBridge, validDelivery } from "../bridge.js";
import { createChannel } from "../claude.js";

function fixture(t, runtime = "pi") {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hm-runtime-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const config = {
    runtime,
    location: randomUUID(),
    state: dir,
    credential: path.join(dir, "token"),
    receipts: path.join(dir, "receipts"),
  };
  fs.writeFileSync(config.credential, "private-test-token\n", { mode: 0o600 });
  const file = path.join(dir, "config.json");
  fs.writeFileSync(file, JSON.stringify(config));
  const session = {
    id: randomUUID(),
    epoch: 3,
    runtime,
    location: config.location,
    agent: "bob",
    owner: "did:key:test",
  };
  const message = {
    id: randomUUID(),
    owner: session.owner,
    author: "alice",
    recipient: "bob",
    body: "native body",
    kind: "send",
    recipientSession: session.id,
    recipientEpoch: 3,
    expiresAt: new Date(Date.now() + 60000).toISOString(),
  };
  const frame = () => ({
    type: "delivery",
    attempt: randomUUID(),
    deadline: new Date(Date.now() + 2000).toISOString(),
    session,
    message,
  });
  return { config, file, session, message, frame };
}
async function until(predicate) {
  for (let i = 0; i < 200; i++) {
    if (predicate()) return;
    await delay(10);
  }
  assert.fail("timed out");
}
async function broker(t, f, handle) {
  const sockets = new Set();
  const server = net.createServer((socket) => {
    sockets.add(socket);
    socket.on("close", () => sockets.delete(socket));
    socket.on("error", () => {});
    let buffer = "",
      authenticated = false;
    socket.on("data", (chunk) => {
      buffer += chunk;
      let end;
      while ((end = buffer.indexOf("\n")) >= 0) {
        const value = JSON.parse(buffer.slice(0, end));
        buffer = buffer.slice(end + 1);
        if (!authenticated) {
          assert.equal(value.op, "runtime-connect");
          assert.equal(value.token, "private-test-token");
          assert.equal(value.runtime, f.config.runtime);
          assert.equal(value.location, f.config.location);
          authenticated = true;
          socket.write(
            JSON.stringify({ data: { type: "ready", session: f.session } }) +
              "\n",
          );
          handle(socket);
        } else {
          handle(socket, value);
        }
      }
    });
  });
  await new Promise((resolve) =>
    server.listen(path.join(f.config.state, "daemon.sock"), resolve),
  );
  t.after(async () => {
    for (const socket of sockets) socket.destroy();
    await new Promise((resolve) => server.close(resolve));
  });
  return server;
}
const push = (socket, frame) =>
  socket.write(JSON.stringify({ data: frame }) + "\n");

test("durable receipt replay, collision and crash ambiguity", async (t) => {
  const f = fixture(t);
  let calls = 0;
  const inject = () => {
    calls++;
    return "accepted";
  };
  assert.equal(
    await new Receipts(f.config.receipts).deliver(
      f.session,
      f.message,
      inject,
      () => true,
    ),
    "accepted",
  );
  assert.equal(
    await new Receipts(f.config.receipts).deliver(
      f.session,
      f.message,
      inject,
      () => true,
    ),
    "accepted",
  );
  assert.equal(calls, 1);
  await assert.rejects(
    new Receipts(f.config.receipts).deliver(
      f.session,
      { ...f.message, body: "collision" },
      inject,
      () => true,
    ),
    /collision/,
  );
  const unknown = { ...f.message, id: randomUUID() };
  assert.equal(
    await new Receipts(f.config.receipts).deliver(
      f.session,
      unknown,
      () => {
        throw new Error("crash after handoff");
      },
      () => true,
    ),
    "ambiguous",
  );
  assert.equal(
    await new Receipts(f.config.receipts).deliver(
      f.session,
      unknown,
      inject,
      () => true,
    ),
    "ambiguous",
  );
  assert.equal(calls, 1);
  for (const name of fs.readdirSync(f.config.receipts))
    assert.equal(
      fs.statSync(path.join(f.config.receipts, name)).mode & 0o077,
      0,
    );
});

test("strict destination, epoch, lease, expiry and decoded size fences", (t) => {
  const f = fixture(t);
  const frame = f.frame();
  assert.equal(validDelivery(frame, f.session, f.config), true);
  for (const patch of [
    { recipient: "mallory" },
    { owner: "other" },
    { recipientEpoch: 2 },
    { recipientSession: randomUUID() },
    { body: "é".repeat(32769) },
    { expiresAt: new Date(0).toISOString() },
  ]) {
    assert.equal(
      validDelivery(
        { ...frame, message: { ...f.message, ...patch } },
        f.session,
        f.config,
      ),
      false,
    );
  }
  assert.equal(
    validDelivery(
      { ...frame, deadline: new Date(0).toISOString() },
      f.session,
      f.config,
    ),
    false,
  );
  assert.equal(
    validDelivery(
      { ...frame, message: { ...f.message, body: "\0".repeat(65536) } },
      f.session,
      f.config,
    ),
    true,
  );
  fs.chmodSync(f.config.credential, 0o644);
  assert.throws(() => readConfig(f.file), /private/);
});

test("reconnect after lost ACK replays receipt without reinjecting and stops cleanly", async (t) => {
  const f = fixture(t);
  let calls = 0,
    connections = 0,
    ack;
  await broker(t, f, (socket, value) => {
    if (!value) {
      connections++;
      push(socket, f.frame());
    } else if (connections === 1) socket.destroy();
    else ack = value;
  });
  const bridge = startBridge(f.config, () => {
    calls++;
    return "accepted";
  });
  t.after(() => bridge.stop());
  await until(() => ack);
  assert.equal(ack.status, "accepted");
  assert.equal(calls, 1);
  assert.equal(connections, 2);
  bridge.stop();
  await bridge.done;
  await delay(150);
  assert.equal(connections, 2);
});

test("Claude channel uses real MCP handshake, capability and notification schema", async (t) => {
  const f = fixture(t, "claude");
  let inject,
    stopped = false;
  const [clientTransport, serverTransport] =
    InMemoryTransport.createLinkedPair();
  const channel = createChannel(f.config, serverTransport, (_config, fn) => {
    inject = fn;
    return {
      stop() {
        stopped = true;
      },
    };
  });
  const client = new Client({ name: "protocol-test", version: "1" });
  let notification;
  client.setNotificationHandler(
    z.object({
      method: z.literal("notifications/claude/channel"),
      params: z.object({
        content: z.string(),
        meta: z.record(z.string(), z.string()),
      }),
    }),
    (value) => {
      notification = value;
    },
  );
  await channel.start();
  assert.equal(inject, undefined);
  await client.connect(clientTransport);
  assert.deepEqual(
    client.getServerCapabilities().experimental["claude/channel"],
    {},
  );
  assert.equal(await inject(f.message), "channel_written");
  await until(() => notification);
  assert.ok(notification.params.content.endsWith(f.message.body));
  assert.equal(notification.params.meta.message_id, f.message.id);
  assert.equal(notification.params.meta.author, "alice");
  await channel.close();
  await client.close();
  assert.equal(stopped, true);
});

test("Pi official SDK loads extension, automatic triggerTurn, dedup and lifecycle stop", async (t) => {
  const f = fixture(t);
  const old = process.env.HERDR_MAILER_CONFIG;
  process.env.HERDR_MAILER_CONFIG = f.file;
  t.after(() => {
    if (old === undefined) delete process.env.HERDR_MAILER_CONFIG;
    else process.env.HERDR_MAILER_CONFIG = old;
  });
  let connection, ack;
  const submissions = [];
  await broker(t, f, (socket, value) => {
    if (!value) {
      connection = socket;
      push(socket, f.frame());
    } else ack = value;
  });
  const runtime = createExtensionRuntime();
  runtime.sendMessage = (message, options) =>
    submissions.push({ message, options });
  const result = await loadExtensions(
    [path.resolve("pi.js")],
    f.config.state,
    undefined,
    runtime,
  );
  assert.deepEqual(result.errors, []);
  assert.equal(result.extensions.length, 1);
  const extension = result.extensions[0];
  const ctx = {
    sessionManager: { getSessionId: () => f.config.location },
    ui: { notify() {} },
  };
  const emit = async (type) => {
    for (const handler of extension.handlers.get(type) ?? [])
      await handler({ type, reason: "startup" }, ctx);
  };
  t.after(() => emit("session_shutdown"));
  await delay(20);
  assert.equal(connection, undefined, "no socket opened in factory");
  await emit("session_start");
  await until(() => ack);
  assert.equal(ack.status, "accepted");
  assert.equal(submissions.length, 1);
  assert.deepEqual(submissions[0].options, {
    triggerTurn: true,
    deliverAs: "followUp",
  });
  assert.equal(submissions[0].message.customType, "herdr-mailer");
  ack = undefined;
  push(connection, f.frame());
  await until(() => ack);
  assert.equal(submissions.length, 1);
  await emit("session_shutdown");
  await until(() => connection.destroyed);
  ctx.sessionManager.getSessionId = () => randomUUID();
  connection = undefined;
  await emit("session_start");
  await delay(150);
  assert.equal(connection, undefined);
});

test("maximum escaped bridge body crosses real socket without truncation", async (t) => {
  const f = fixture(t);
  f.message.body = "\0".repeat(65536);
  let ack, received;
  await broker(t, f, (socket, value) => {
    if (!value) push(socket, f.frame());
    else ack = value;
  });
  const bridge = startBridge(f.config, (message) => {
    received = message.body;
    return "accepted";
  });
  t.after(() => bridge.stop());
  await until(() => ack);
  assert.equal(ack.status, "accepted");
  assert.equal(received, f.message.body);
  bridge.stop();
  await bridge.done;
});

test("final injection fence refuses a session/lease invalidated after durable intent", async (t) => {
  const f = fixture(t);
  let checks = 0,
    calls = 0;
  const result = await new Receipts(f.config.receipts).deliver(
    f.session,
    f.message,
    () => {
      calls++;
      return "accepted";
    },
    () => ++checks === 1,
  );
  assert.equal(result, "ambiguous");
  assert.equal(calls, 0);
});
