const assert = require("node:assert/strict");
const test = require("node:test");

const { main } = require("../lib/cli");

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
