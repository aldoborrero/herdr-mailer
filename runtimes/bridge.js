import net from "node:net";
import fs from "node:fs";
import path from "node:path";
import { createHash, randomUUID } from "node:crypto";
import { setTimeout as delay } from "node:timers/promises";

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
export const maxFrameBytes = 6 * 65536 + 16384;
export function privateFile(file) {
  const st = fs.lstatSync(file);
  if (!st.isFile() || st.mode & 0o077 || st.uid !== process.getuid())
    throw new Error(
      "credential/receipt must be a private same-UID regular file",
    );
}
export function readConfig(file = process.env.HERDR_MAILER_CONFIG) {
  if (!file) throw new Error("HERDR_MAILER_CONFIG is required");
  const config = JSON.parse(fs.readFileSync(file, "utf8"));
  if (!["pi", "claude"].includes(config.runtime) || !uuid.test(config.location))
    throw new Error("invalid runtime or exact session UUID");
  for (const key of ["state", "credential", "receipts"])
    if (!path.isAbsolute(config[key] ?? ""))
      throw new Error(`${key} must be an absolute path`);
  privateFile(config.credential);
  return config;
}
function privateDir(dir) {
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const st = fs.lstatSync(dir);
  if (!st.isDirectory() || st.mode & 0o077 || st.uid !== process.getuid())
    throw new Error("receipts directory must be private and same-UID");
}
const hash = (value) => createHash("sha256").update(value).digest("hex");
function syncDir(dir) {
  const fd = fs.openSync(dir, "r");
  try {
    fs.fsyncSync(fd);
  } finally {
    fs.closeSync(fd);
  }
}

// A durable started record fences ambiguous crash windows. Never silently replay
// an injection whose outcome cannot be reconstructed. No token is stored here.
export class Receipts {
  constructor(dir) {
    privateDir(dir);
    this.dir = dir;
  }
  async deliver(session, message, inject, valid) {
    const id = `${message.owner}/${message.author}/${message.id}`;
    const file = path.join(this.dir, hash(`${session.id}/${id}`) + ".json");
    const contentHash = hash(JSON.stringify(message));
    let previous;
    try {
      privateFile(file);
      previous = JSON.parse(fs.readFileSync(file, "utf8"));
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
    if (previous) {
      if (previous.hash !== contentHash)
        throw new Error("delivery ID collision");
      return previous.status === "started" ? "ambiguous" : previous.status;
    }
    if (!valid()) throw new Error("stale, cancelled or expired delivery");
    const fd = fs.openSync(file, "wx", 0o600);
    try {
      fs.writeFileSync(
        fd,
        JSON.stringify({ hash: contentHash, status: "started" }),
      );
      fs.fsyncSync(fd);
    } finally {
      fs.closeSync(fd);
    }
    syncDir(this.dir);
    // No await occurs between the final fence and calling the runtime API.
    if (!valid()) return "ambiguous";
    let status;
    try {
      status = await inject(message);
    } catch {
      return "ambiguous";
    }
    if (!["accepted", "channel_written"].includes(status)) return "ambiguous";
    const tmp = `${file}.${randomUUID()}`;
    const out = fs.openSync(tmp, "wx", 0o600);
    try {
      fs.writeFileSync(out, JSON.stringify({ hash: contentHash, status }));
      fs.fsyncSync(out);
    } finally {
      fs.closeSync(out);
    }
    fs.renameSync(tmp, file);
    syncDir(this.dir);
    return status;
  }
}

export function validDelivery(frame, session, config) {
  const m = frame.message;
  return (
    frame.type === "delivery" &&
    Date.parse(frame.deadline) > Date.now() &&
    uuid.test(frame.attempt) &&
    frame.session?.id === session.id &&
    frame.session?.epoch === session.epoch &&
    session.runtime === config.runtime &&
    session.location === config.location &&
    m?.owner === session.owner &&
    m.recipient === session.agent &&
    uuid.test(m.id) &&
    /^[A-Za-z0-9_-]{1,64}$/.test(m.author) &&
    (!m.recipientSession ||
      (m.recipientSession === session.id &&
        m.recipientEpoch === session.epoch)) &&
    typeof m.body === "string" &&
    Buffer.byteLength(m.body, "utf8") <= 65536 &&
    m.body.length > 0 &&
    Date.parse(m.expiresAt) > Date.now()
  );
}

export function startBridge(
  config,
  inject,
  { isCurrent = () => true, onState = () => {} } = {},
) {
  const controller = new AbortController();
  const receipts = new Receipts(config.receipts);
  let socket;
  const done = (async () => {
    let backoff = 100;
    while (!controller.signal.aborted && isCurrent()) {
      try {
        await new Promise((resolve, reject) => {
          let session,
            buffer = Buffer.alloc(0),
            chain = Promise.resolve();
          socket = net.createConnection(path.join(config.state, "daemon.sock"));
          const connection = socket;
          const abort = () => connection.destroy();
          controller.signal.addEventListener("abort", abort, { once: true });
          connection.on("connect", () => {
            try {
              privateFile(config.credential);
              const token = fs.readFileSync(config.credential, "utf8").trim();
              connection.write(
                JSON.stringify({
                  op: "runtime-connect",
                  token,
                  runtime: config.runtime,
                  location: config.location,
                }) + "\n",
              );
            } catch {
              connection.destroy();
            }
          });
          connection.on("error", () =>
            reject(new Error("bridge connection failed")),
          );
          connection.on("close", () => {
            controller.signal.removeEventListener("abort", abort);
            resolve();
          });
          connection.on("data", (chunk) => {
            buffer = Buffer.concat([buffer, chunk]);
            let end;
            while ((end = buffer.indexOf(10)) !== -1) {
              if (end > maxFrameBytes) {
                connection.destroy();
                return;
              }
              const line = buffer.subarray(0, end);
              buffer = buffer.subarray(end + 1);
              chain = chain
                .then(async () => {
                  const response = JSON.parse(line);
                  if (response.error)
                    throw new Error(
                      "bridge authentication or protocol rejected",
                    );
                  const frame = response.data;
                  if (!session) {
                    if (
                      frame?.type !== "ready" ||
                      !uuid.test(frame.session?.id) ||
                      frame.session.runtime !== config.runtime ||
                      frame.session.location !== config.location
                    )
                      throw new Error("invalid bridge handshake");
                    session = frame.session;
                    backoff = 100;
                    onState("connected");
                    return;
                  }
                  const valid = () =>
                    !controller.signal.aborted &&
                    !connection.destroyed &&
                    isCurrent() &&
                    validDelivery(frame, session, config);
                  if (!valid()) throw new Error("invalid delivery");
                  const status = await receipts.deliver(
                    session,
                    frame.message,
                    inject,
                    valid,
                  );
                  if (!connection.destroyed)
                    connection.write(
                      JSON.stringify({ attempt: frame.attempt, status }) + "\n",
                    );
                })
                .catch(() => connection.destroy());
            }
            if (buffer.length > maxFrameBytes) connection.destroy();
          });
        });
      } catch {
        /* No tokens, paths or remote error text in logs. */
      }
      onState("disconnected");
      try {
        await delay(backoff, undefined, { signal: controller.signal });
      } catch {
        break;
      }
      backoff = Math.min(backoff * 2, 5000);
    }
  })();
  return {
    done,
    stop() {
      controller.abort();
      socket?.destroy();
    },
  };
}

export function content(m) {
  return `[herdr-mailer ${m.id}; ${m.kind} from ${m.author}; origin=${m.owner}/${m.author}; correlation=${m.correlation ?? ""}]\n${m.body}`;
}
