import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { randomUUID } from "node:crypto";

test("launcher binds runtime identity, requires channel opt-in and never passes a token", (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hm-launch-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const credential = path.join(dir, "token");
  fs.writeFileSync(credential, "secret-worker-token", { mode: 0o600 });
  for (const runtime of ["claude", "pi"]) {
    fs.writeFileSync(
      path.join(dir, runtime),
      `#!${process.execPath}\nconsole.log(JSON.stringify(process.argv.slice(2)));\n`,
      { mode: 0o700 },
    );
    const location = randomUUID();
    const file = path.join(dir, "config.json");
    fs.writeFileSync(
      file,
      JSON.stringify({
        runtime,
        location,
        state: dir,
        credential,
        receipts: path.join(dir, "receipts"),
      }),
    );
    const env = {
      ...process.env,
      PATH: dir + path.delimiter + process.env.PATH,
      HERDR_MAILER_CONFIG: file,
    };
    const optIn = runtime === "claude" ? ["--development-channel"] : [];
    const call = (args) =>
      spawnSync(process.execPath, [path.resolve("launch.js"), ...args], {
        env,
        encoding: "utf8",
      });
    if (runtime === "claude") assert.notEqual(call([]).status, 0);
    const result = call(optIn);
    assert.equal(result.status, 0, result.stderr);
    const args = JSON.parse(result.stdout);
    assert.deepEqual(args.slice(0, 2), ["--session-id", location]);
    assert.ok(!JSON.stringify(args).includes("secret-worker-token"));
    assert.ok(!args.includes("--dangerously-skip-permissions"));
    if (runtime === "claude") {
      const resumed = call([...optIn, "--resume-session"]);
      assert.equal(resumed.status, 0, resumed.stderr);
      assert.deepEqual(JSON.parse(resumed.stdout).slice(0, 2), [
        "--resume",
        location,
      ]);
      assert.ok(args.includes("--dangerously-load-development-channels"));
      const mcp = JSON.parse(args[args.indexOf("--mcp-config") + 1]);
      assert.equal(
        mcp.mcpServers["herdr-mailer"].env.HERDR_MAILER_CONFIG,
        file,
      );
    } else assert.ok(args.includes("-e"));
    for (const flag of [
      "--session-id=other",
      "--resume",
      "--fork",
      "--fork-session",
      "-r",
    ])
      assert.notEqual(call([...optIn, flag]).status, 0, flag);
  }
});

test("both runtime children override conflicting sender environment and retain bridge config", (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hm-launch-env-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const credential = path.join(dir, "receiver.token");
  const state = path.join(dir, "receiver-state");
  const token = "receiver-token-must-not-appear-in-output";
  fs.writeFileSync(credential, token, { mode: 0o600 });
  const file = path.join(dir, "config.json");
  for (const runtime of ["pi", "claude"]) {
    fs.writeFileSync(
      path.join(dir, runtime),
      `#!${process.execPath}\nconsole.log(JSON.stringify({credential:process.env.HERDR_MAILER_CREDENTIAL,state:process.env.HERDR_MAILER_STATE,config:process.env.HERDR_MAILER_CONFIG,args:process.argv.slice(2)}));\n`,
      { mode: 0o700 },
    );
    fs.writeFileSync(
      file,
      JSON.stringify({
        runtime,
        location: randomUUID(),
        credential,
        state,
        receipts: path.join(dir, "receipts"),
      }),
    );
    const args = [path.resolve("launch.js")];
    if (runtime === "claude") args.push("--development-channel");
    const result = spawnSync(process.execPath, args, {
      encoding: "utf8",
      env: {
        ...process.env,
        PATH: dir + path.delimiter + process.env.PATH,
        HERDR_MAILER_CONFIG: file,
        HERDR_MAILER_CREDENTIAL: "/sender/token",
        HERDR_MAILER_STATE: "/sender/state",
      },
    });
    assert.equal(result.status, 0, result.stderr);
    const child = JSON.parse(result.stdout);
    assert.equal(child.credential, credential);
    assert.equal(child.state, state);
    assert.equal(child.config, file);
    assert.ok(!result.stdout.includes(token));
    assert.ok(!result.stderr.includes(token));
  }
});
