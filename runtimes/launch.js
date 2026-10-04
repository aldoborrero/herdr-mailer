#!/usr/bin/env node
import { readConfig } from "./bridge.js";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const args = process.argv.slice(2);
let development = false,
  resume = false;
while (["--development-channel", "--resume-session"].includes(args[0])) {
  const flag = args.shift();
  if (flag === "--development-channel") development = true;
  if (flag === "--resume-session") resume = true;
}
const config = readConfig();
const dir = path.dirname(fileURLToPath(import.meta.url));
let command, runtimeArgs;
if (config.runtime === "claude") {
  if (!development)
    throw new Error(
      "Claude Channels requires explicit --development-channel opt-in; see docs/native-runtimes.md",
    );
  command = "claude";
  const mcp = {
    mcpServers: {
      "herdr-mailer": {
        command: process.execPath,
        args: [path.join(dir, "claude.js")],
        env: { HERDR_MAILER_CONFIG: process.env.HERDR_MAILER_CONFIG },
      },
    },
  };
  runtimeArgs = [
    resume ? "--resume" : "--session-id",
    config.location,
    "--mcp-config",
    JSON.stringify(mcp),
    "--dangerously-load-development-channels",
    "server:herdr-mailer",
  ];
} else {
  command = "pi";
  runtimeArgs = [
    "--session-id",
    config.location,
    "-e",
    path.join(dir, "pi.js"),
  ];
}
// Disallow overriding identity/channel configuration after binding the bridge.
if (
  args.some(
    (arg) =>
      /^--(session|session-id|resume|continue|fork|fork-session|teleport|from-pr|mcp-config|channels|dangerously-load-development-channels)(=|$)/.test(
        arg,
      ) || /^-[rc]/.test(arg),
  )
)
  throw new Error("session and channel flags are managed by the launcher");
const child = spawn(command, [...runtimeArgs, ...args], {
  stdio: "inherit",
  env: {
    ...process.env,
    // Outgoing CLI calls must use the same recipient identity as the bridge,
    // even when another mailer worker starts this runtime.
    HERDR_MAILER_CREDENTIAL: config.credential,
    HERDR_MAILER_STATE: config.state,
    HERDR_MAILER_CONFIG: process.env.HERDR_MAILER_CONFIG,
  },
});
for (const signal of ["SIGINT", "SIGTERM"])
  process.on(signal, () => child.kill(signal));
child.on("error", () => {
  process.stderr.write("herdr-mailer: runtime executable unavailable\n");
  process.exitCode = 1;
});
child.on("exit", (code, signal) => {
  process.exitCode = code ?? (signal ? 1 : 0);
});
