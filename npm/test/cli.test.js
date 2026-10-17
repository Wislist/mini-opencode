const assert = require("node:assert/strict");
const fs = require("node:fs");
const { PassThrough } = require("node:stream");
const test = require("node:test");

const { main, notifyUpdateIfAvailable, run } = require("../lib/cli");
const { readState, writeState } = require("../lib/update");

function ttyStream(content) {
  const stream = new PassThrough();
  stream.isTTY = true;
  if (content !== undefined) {
    stream.end(content);
  }
  return stream;
}

function collect(stream) {
  const state = { text: "" };
  stream.on("data", (chunk) => {
    state.text += chunk.toString();
  });
  return state;
}

function runOverrides(extra = {}) {
  return {
    platform: "darwin",
    arch: "arm64",
    resolveBinary: () => "/tmp/mini-opencode",
    spawnSync: () => ({ status: 0, signal: null, error: null }),
    stdin: ttyStream(),
    stdout: ttyStream(),
    env: {},
    currentVersion: "1.0.0",
    now: 1000,
    checkForUpdate: async () => ({ status: "current", latest: "1.0.0" }),
    promptForUpdate: async () => false,
    installUpdate: () => true,
    ...extra,
  };
}

test("main forwards arguments and inherited stdio to the native binary", () => {
  const calls = [];
  const code = main(["--example", "value"], {
    platform: "darwin",
    arch: "arm64",
    resolveBinary: () => "/tmp/mini-opencode",
    spawnSync: (...args) => {
      calls.push(args);
      return { status: 7, signal: null, error: null };
    },
  });

  assert.equal(code, 7);
  assert.deepEqual(calls, [[
    "/tmp/mini-opencode",
    ["--example", "value"],
    { stdio: "inherit", windowsHide: false },
  ]]);
});

test("main reports spawn failures", () => {
  assert.throws(
    () => main([], {
      platform: "win32",
      arch: "x64",
      resolveBinary: () => "C:\\pkg\\mini-opencode.exe",
      spawnSync: () => ({ status: null, signal: null, error: new Error("access denied") }),
    }),
    /access denied/,
  );
});

test("main converts a terminating signal into a conventional exit code", () => {
  const code = main([], {
    platform: "darwin",
    arch: "x64",
    resolveBinary: () => "/tmp/mini-opencode",
    spawnSync: () => ({ status: null, signal: "SIGTERM", error: null }),
  });

  assert.equal(code, 143);
});

test("run installs an accepted update before launching the binary", async () => {
  const calls = [];
  const spawnSyncImpl = (...args) => {
    calls.push(args);
    return { status: 0, signal: null, error: null };
  };
  const installs = [];
  const stdout = ttyStream();
  const rendered = collect(stdout);

  const code = await run(["--flag"], runOverrides({
    spawnSync: spawnSyncImpl,
    stdout,
    checkForUpdate: async () => ({ status: "update-available", current: "1.0.0", latest: "1.1.0" }),
    promptForUpdate: async () => true,
    installUpdate: (options) => {
      installs.push(options);
      return true;
    },
  }));

  assert.equal(code, 0);
  assert.equal(installs.length, 1);
  assert.equal(installs[0].version, "1.1.0");
  assert.match(rendered.text, /1\.0\.0/);
  assert.match(rendered.text, /1\.1\.0/);
  assert.deepEqual(calls, [[
    "/tmp/mini-opencode",
    ["--flag"],
    { stdio: "inherit", windowsHide: false },
  ]]);
});

test("run launches the binary without installing when the user declines", async () => {
  const calls = [];
  const installs = [];

  const code = await run([], runOverrides({
    spawnSync: (...args) => {
      calls.push(args);
      return { status: 3, signal: null, error: null };
    },
    checkForUpdate: async () => ({ status: "update-available", current: "1.0.0", latest: "1.1.0" }),
    promptForUpdate: async () => false,
    installUpdate: (options) => {
      installs.push(options);
      return true;
    },
  }));

  assert.equal(code, 3);
  assert.equal(installs.length, 0);
  assert.equal(calls.length, 1);
});

test("run skips the prompt when no update is available", async () => {
  let prompted = 0;

  const code = await run([], runOverrides({
    promptForUpdate: async () => {
      prompted += 1;
      return false;
    },
  }));

  assert.equal(code, 0);
  assert.equal(prompted, 0);
});

test("run does not check for updates when stdin is not a terminal", async () => {
  let checked = 0;
  const stdin = new PassThrough();

  const code = await run([], runOverrides({
    stdin,
    checkForUpdate: async () => {
      checked += 1;
      return { status: "update-available", current: "1.0.0", latest: "1.1.0" };
    },
  }));

  assert.equal(code, 0);
  assert.equal(checked, 0);
});

test("run honors the update opt-out environment variables", async () => {
  for (const env of [{ NO_UPDATE_NOTIFIER: "1" }, { MINI_OPENCODE_NO_UPDATE_CHECK: "true" }, { CI: "true" }]) {
    let checked = 0;
    const code = await run([], runOverrides({
      env,
      checkForUpdate: async () => {
        checked += 1;
        return { status: "update-available", current: "1.0.0", latest: "1.1.0" };
      },
    }));

    assert.equal(code, 0);
    assert.equal(checked, 0, `expected no check for ${JSON.stringify(env)}`);
  }
});

test("run still launches the binary when the update check fails", async () => {
  const calls = [];
  const code = await run([], runOverrides({
    spawnSync: (...args) => {
      calls.push(args);
      return { status: 0, signal: null, error: null };
    },
    checkForUpdate: async () => {
      throw new Error("registry exploded");
    },
  }));

  assert.equal(code, 0);
  assert.equal(calls.length, 1);
});

test("notifyUpdateIfAvailable reports why it stayed quiet", async () => {
  const disabled = await notifyUpdateIfAvailable({
    currentVersion: "1.0.0",
    env: { NO_UPDATE_NOTIFIER: "1" },
    stdin: ttyStream(),
    stdout: ttyStream(),
    checkForUpdate: async () => {
      throw new Error("must not be called");
    },
  });
  assert.deepEqual(disabled, { status: "skipped", reason: "disabled" });

  const quiet = await notifyUpdateIfAvailable({
    currentVersion: "1.0.0",
    env: {},
    stdin: ttyStream(),
    stdout: ttyStream(),
    checkForUpdate: async () => ({ status: "current", latest: "1.0.0" }),
  });
  assert.deepEqual(quiet, { status: "current", latest: "1.0.0" });
});

test("notifyUpdateIfAvailable records a declined version", async () => {
  const stateFile = `/tmp/mini-opencode-declined-${process.pid}.json`;
  fs.rmSync(stateFile, { force: true });
  writeState(stateFile, { lastChecked: 1 });

  const result = await notifyUpdateIfAvailable({
    currentVersion: "1.0.0",
    env: {},
    stdin: ttyStream(),
    stdout: ttyStream(),
    now: 4242,
    stateFile,
    checkForUpdate: async () => ({ status: "update-available", current: "1.0.0", latest: "1.1.0" }),
    promptForUpdate: async () => false,
  });

  assert.equal(result.accepted, false);
  assert.deepEqual(readState(stateFile), { lastChecked: 1, declinedVersion: "1.1.0" });
  fs.rmSync(stateFile, { force: true });
});
