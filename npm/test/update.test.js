const assert = require("node:assert/strict");
const { mkdtemp, readFile, writeFile } = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const { PassThrough } = require("node:stream");
const test = require("node:test");

const {
  checkForUpdate,
  compareVersions,
  detectPackageManager,
  fetchLatestVersion,
  formatUpdateNotice,
  installUpdate,
  isComparableVersion,
  isNewerVersion,
  markDeclined,
  parseVersion,
  promptForUpdate,
  readState,
  shouldCheckForUpdate,
  updateCommand,
  writeState,
} = require("../lib/update");

async function tempStateFile() {
  const dir = await mkdtemp(path.join(os.tmpdir(), "mini-opencode-update-"));
  return path.join(dir, "update-check.json");
}

function ttyStream(content) {
  const stream = new PassThrough();
  stream.isTTY = true;
  if (content !== undefined) {
    stream.end(content);
  }
  return stream;
}

function eofStream() {
  const stream = new PassThrough();
  stream.isTTY = true;
  stream.resume();
  stream.end();
  return stream;
}

test("parseVersion accepts release and prerelease versions", () => {
  assert.deepEqual(parseVersion("1.2.3"), { major: 1, minor: 2, patch: 3, prerelease: [] });
  assert.deepEqual(parseVersion("v1.2.3"), { major: 1, minor: 2, patch: 3, prerelease: [] });
  assert.deepEqual(parseVersion("1.2.3+build.5"), { major: 1, minor: 2, patch: 3, prerelease: [] });
  assert.deepEqual(parseVersion("1.2.3-rc.1"), { major: 1, minor: 2, patch: 3, prerelease: ["rc", "1"] });
  assert.deepEqual(parseVersion(" 1.2.3 "), { major: 1, minor: 2, patch: 3, prerelease: [] });
});

test("parseVersion rejects malformed versions", () => {
  for (const value of ["", "1.2", "1.2.3.4", "latest", "1.x.3", undefined, null, "1.2.3-"]) {
    assert.equal(parseVersion(value), null, `expected ${JSON.stringify(value)} to be invalid`);
  }
});

test("compareVersions orders releases and prereleases like semver", () => {
  assert.equal(compareVersions("1.2.3", "1.2.3"), 0);
  assert.equal(compareVersions("1.2.4", "1.2.3"), 1);
  assert.equal(compareVersions("1.3.0", "1.2.9"), 1);
  assert.equal(compareVersions("2.0.0", "1.99.99"), 1);
  assert.equal(compareVersions("1.2.3", "1.2.4"), -1);
  assert.equal(compareVersions("1.0.0-alpha", "1.0.0"), -1);
  assert.equal(compareVersions("1.0.0", "1.0.0-rc.1"), 1);
  assert.equal(compareVersions("1.0.0-alpha.1", "1.0.0-alpha.2"), -1);
  assert.equal(compareVersions("1.0.0-alpha.10", "1.0.0-alpha.9"), 1);
  assert.equal(compareVersions("1.0.0-alpha", "1.0.0-alpha.1"), -1);
});

test("compareVersions cannot order malformed input", () => {
  assert.equal(compareVersions("not-a-version", "1.2.3"), 0);
  assert.equal(compareVersions("1.2.3", "not-a-version"), 0);
  assert.equal(compareVersions("weird", "weird"), 0);
});

test("isNewerVersion only reports a strictly newer release", () => {
  assert.equal(isNewerVersion("1.2.3", "1.2.4"), true);
  assert.equal(isNewerVersion("1.2.3", "1.2.3"), false);
  assert.equal(isNewerVersion("1.2.3", "1.2.2"), false);
  assert.equal(isNewerVersion("1.2.3", "latest"), false);
  assert.equal(isNewerVersion("latest", "1.2.3"), false);
  assert.equal(isNewerVersion("", "1.2.3"), false);
});

test("isComparableVersion skips development placeholders", () => {
  assert.equal(isComparableVersion("1.2.3"), true);
  assert.equal(isComparableVersion("0.0.0"), false);
  assert.equal(isComparableVersion("0.0.0-development"), false);
  assert.equal(isComparableVersion("1.2.3-rc.1"), false);
  assert.equal(isComparableVersion("latest"), false);
});

test("shouldCheckForUpdate respects the cache interval", () => {
  const interval = 1000;
  assert.equal(shouldCheckForUpdate({ now: 5000, intervalMs: interval }), true);
  assert.equal(
    shouldCheckForUpdate({ lastChecked: Number.NaN, now: 5000, intervalMs: interval }),
    true,
  );
  assert.equal(shouldCheckForUpdate({ lastChecked: 4500, now: 5000, intervalMs: interval }), false);
  assert.equal(shouldCheckForUpdate({ lastChecked: 4000, now: 5000, intervalMs: interval }), true);
  assert.equal(shouldCheckForUpdate({ lastChecked: 3999, now: 5000, intervalMs: interval }), true);
  assert.equal(shouldCheckForUpdate({ lastChecked: 9000, now: 5000, intervalMs: interval }), true);
});

test("readState tolerates missing and corrupt state files", async () => {
  const file = await tempStateFile();
  assert.deepEqual(readState(file), {});

  await writeFile(file, "not json");
  assert.deepEqual(readState(file), {});

  await writeFile(file, JSON.stringify({ lastChecked: 42, declinedVersion: "1.2.3" }));
  assert.deepEqual(readState(file), { lastChecked: 42, declinedVersion: "1.2.3" });
});

test("writeState creates parent directories and round-trips", async () => {
  const dir = await mkdtemp(path.join(os.tmpdir(), "mini-opencode-update-"));
  const file = path.join(dir, "nested", "deeper", "update-check.json");
  writeState(file, { lastChecked: 7, declinedVersion: "1.0.0" });
  assert.deepEqual(JSON.parse(await readFile(file, "utf8")), {
    lastChecked: 7,
    declinedVersion: "1.0.0",
  });
  assert.deepEqual(readState(file), { lastChecked: 7, declinedVersion: "1.0.0" });
});

test("writeState failures never throw", async () => {
  assert.doesNotThrow(() => writeState("/dev/null/impossible/update.json", { lastChecked: 1 }));
});

test("markDeclined records the version without losing the check timestamp", async () => {
  const file = await tempStateFile();
  writeState(file, { lastChecked: 123 });
  markDeclined(file, "1.4.0");
  assert.deepEqual(readState(file), { lastChecked: 123, declinedVersion: "1.4.0" });
});

test("fetchLatestVersion reads the dist-tag from the registry", async () => {
  const calls = [];
  const version = await fetchLatestVersion({
    registry: "https://registry.example.com/",
    packageName: "@scope/pkg",
    fetchImpl: async (url, options) => {
      calls.push([url, options]);
      return { ok: true, json: async () => ({ version: "2.3.4" }) };
    },
  });

  assert.equal(version, "2.3.4");
  assert.equal(calls.length, 1);
  assert.equal(calls[0][0], "https://registry.example.com/%40scope%2Fpkg/latest");
  assert.equal(calls[0][1].headers.accept, "application/json");
  assert.ok(calls[0][1].signal, "the request must be abortable");
});

test("fetchLatestVersion returns null on any failure", async () => {
  const base = { registry: "https://registry.example.com", packageName: "@scope/pkg" };

  assert.equal(await fetchLatestVersion({ ...base, fetchImpl: async () => ({ ok: false, status: 404 }) }), null);
  assert.equal(await fetchLatestVersion({ ...base, fetchImpl: async () => ({ ok: true, json: async () => ({}) }) }), null);
  assert.equal(await fetchLatestVersion({ ...base, fetchImpl: async () => ({ ok: true, json: async () => ({ version: "nope" }) }) }), null);
  assert.equal(await fetchLatestVersion({ ...base, fetchImpl: async () => ({ ok: true, json: async () => { throw new Error("bad json"); } }) }), null);
  assert.equal(await fetchLatestVersion({ ...base, fetchImpl: async () => { throw new Error("offline"); } }), null);
  assert.equal(await fetchLatestVersion({ ...base, fetchImpl: "not-a-function" }), null);
});

test("fetchLatestVersion gives up when the request hangs", async () => {
  const version = await fetchLatestVersion({
    registry: "https://registry.example.com",
    packageName: "@scope/pkg",
    timeoutMs: 5,
    fetchImpl: (url, options) => new Promise((resolve, reject) => {
      options.signal.addEventListener("abort", () => reject(new Error("aborted")));
      setTimeout(() => resolve({ ok: true, json: async () => ({ version: "9.9.9" }) }), 1000);
    }),
  });

  assert.equal(version, null);
});

test("checkForUpdate skips unknown current versions without touching the network", async () => {
  const file = await tempStateFile();
  let fetched = 0;

  for (const currentVersion of ["0.0.0-development", "", "0.0.0", "1.0.0-rc.1"]) {
    const result = await checkForUpdate({
      currentVersion,
      stateFile: file,
      now: 1000,
      fetchLatest: async () => {
        fetched += 1;
        return "9.9.9";
      },
    });
    assert.equal(result.status, "skipped");
    assert.equal(result.reason, "unknown-current-version");
  }
  assert.equal(fetched, 0);
});

test("checkForUpdate skips a fresh cache entry", async () => {
  const file = await tempStateFile();
  writeState(file, { lastChecked: 1000 });
  let fetched = 0;

  const result = await checkForUpdate({
    currentVersion: "1.0.0",
    stateFile: file,
    now: 1000 + 60_000,
    intervalMs: 24 * 60 * 60 * 1000,
    fetchLatest: async () => {
      fetched += 1;
      return "2.0.0";
    },
  });

  assert.equal(result.status, "skipped");
  assert.equal(result.reason, "fresh-cache");
  assert.equal(fetched, 0);
});

test("checkForUpdate reports an available update and refreshes the cache", async () => {
  const file = await tempStateFile();
  const result = await checkForUpdate({
    currentVersion: "1.0.0",
    stateFile: file,
    now: 5000,
    fetchLatest: async () => "1.1.0",
  });

  assert.equal(result.status, "update-available");
  assert.equal(result.current, "1.0.0");
  assert.equal(result.latest, "1.1.0");
  assert.equal(readState(file).lastChecked, 5000);
});

test("checkForUpdate reports the current version and refreshes the cache", async () => {
  const file = await tempStateFile();
  const result = await checkForUpdate({
    currentVersion: "1.1.0",
    stateFile: file,
    now: 5000,
    fetchLatest: async () => "1.1.0",
  });

  assert.equal(result.status, "current");
  assert.equal(result.latest, "1.1.0");
  assert.equal(readState(file).lastChecked, 5000);
});

test("checkForUpdate skips when the registry is unreachable but still records the attempt", async () => {
  const file = await tempStateFile();
  const result = await checkForUpdate({
    currentVersion: "1.0.0",
    stateFile: file,
    now: 5000,
    fetchLatest: async () => null,
  });

  assert.equal(result.status, "skipped");
  assert.equal(result.reason, "unavailable");
  assert.equal(readState(file).lastChecked, 5000);
});

test("checkForUpdate stays silent for a version the user already declined", async () => {
  const file = await tempStateFile();
  writeState(file, { lastChecked: 0, declinedVersion: "1.2.0" });

  const result = await checkForUpdate({
    currentVersion: "1.0.0",
    stateFile: file,
    now: 24 * 60 * 60 * 1000,
    fetchLatest: async () => "1.2.0",
  });

  assert.equal(result.status, "skipped");
  assert.equal(result.reason, "declined");
  assert.equal(readState(file).declinedVersion, "1.2.0");
});

test("checkForUpdate offers a version newer than the declined one", async () => {
  const file = await tempStateFile();
  writeState(file, { lastChecked: 0, declinedVersion: "1.2.0" });

  const result = await checkForUpdate({
    currentVersion: "1.0.0",
    stateFile: file,
    now: 24 * 60 * 60 * 1000,
    fetchLatest: async () => "1.3.0",
  });

  assert.equal(result.status, "update-available");
  assert.equal(result.latest, "1.3.0");
});

test("checkForUpdate never rejects when the checker itself blows up", async () => {
  const file = await tempStateFile();
  const result = await checkForUpdate({
    currentVersion: "1.0.0",
    stateFile: file,
    now: 5000,
    fetchLatest: async () => {
      throw new Error("boom");
    },
  });

  assert.equal(result.status, "skipped");
  assert.equal(result.reason, "unavailable");
});

test("detectPackageManager reads the invoking package manager", () => {
  assert.equal(detectPackageManager({ userAgent: "pnpm/9.12.0 npm/? node/v22.0.0 darwin arm64" }), "pnpm");
  assert.equal(detectPackageManager({ userAgent: "yarn/1.22.22 npm/? node/v20.0.0 darwin arm64" }), "yarn");
  assert.equal(detectPackageManager({ userAgent: "npm/10.8.2 node/v22.0.0 darwin arm64" }), "npm");
  assert.equal(detectPackageManager({ userAgent: "bun/1.1.0" }), "npm");
  assert.equal(detectPackageManager({}), "npm");
  assert.equal(detectPackageManager(), "npm");
});

test("detectPackageManager falls back to the install location", () => {
  assert.equal(detectPackageManager({ scriptPath: "/Users/me/Library/pnpm/mini-opencode" }), "pnpm");
  assert.equal(
    detectPackageManager({ scriptPath: "/opt/pnpm-home/bin/mini-opencode", pnpmHome: "/opt/pnpm-home" }),
    "pnpm",
  );
  assert.equal(
    detectPackageManager({ scriptPath: "/usr/local/lib/node_modules/@wislist/mini-opencode/bin/mini-opencode.js" }),
    "npm",
  );
  assert.equal(detectPackageManager({ scriptPath: "/opt/notpnpmish/bin/mini-opencode" }), "npm");
});

test("updateCommand targets the global install for each package manager", () => {
  assert.deepEqual(updateCommand({ packageManager: "npm", packageName: "@scope/pkg", version: "1.2.3" }), [
    "npm",
    ["install", "--global", "@scope/pkg@1.2.3"],
  ]);
  assert.deepEqual(updateCommand({ packageManager: "pnpm", packageName: "@scope/pkg", version: "1.2.3" }), [
    "pnpm",
    ["add", "--global", "@scope/pkg@1.2.3"],
  ]);
  assert.deepEqual(updateCommand({ packageManager: "yarn", packageName: "@scope/pkg", version: "1.2.3" }), [
    "yarn",
    ["global", "add", "@scope/pkg@1.2.3"],
  ]);
  assert.deepEqual(updateCommand({ packageName: "@scope/pkg", version: "1.2.3" }), [
    "npm",
    ["install", "--global", "@scope/pkg@1.2.3"],
  ]);
});

test("formatUpdateNotice mentions both versions", () => {
  const notice = formatUpdateNotice({ current: "1.0.0", latest: "1.1.0" });
  assert.match(notice, /1\.0\.0/);
  assert.match(notice, /1\.1\.0/);
});

test("promptForUpdate returns true only for an explicit yes", async () => {
  assert.equal(await promptForUpdate({ input: ttyStream("y\n"), output: new PassThrough() }), true);
  assert.equal(await promptForUpdate({ input: ttyStream("Y\n"), output: new PassThrough() }), true);
  assert.equal(await promptForUpdate({ input: ttyStream("yes\n"), output: new PassThrough() }), true);
  assert.equal(await promptForUpdate({ input: ttyStream("n\n"), output: new PassThrough() }), false);
  assert.equal(await promptForUpdate({ input: ttyStream("\n"), output: new PassThrough() }), false);
  assert.equal(await promptForUpdate({ input: ttyStream("maybe\n"), output: new PassThrough() }), false);
  assert.equal(await promptForUpdate({ input: eofStream(), output: new PassThrough() }), false);
});

test("promptForUpdate writes a yes/no question and defaults to No", async () => {
  const output = new PassThrough();
  let rendered = "";
  output.on("data", (chunk) => {
    rendered += chunk.toString();
  });

  await promptForUpdate({ input: ttyStream("n\n"), output });
  assert.match(rendered, /\[y\/N\]/);
});

test("promptForUpdate declines without prompting when stdin is not a terminal", async () => {
  const input = new PassThrough();
  const output = new PassThrough();
  let rendered = "";
  output.on("data", (chunk) => {
    rendered += chunk.toString();
  });

  assert.equal(await promptForUpdate({ input, output }), false);
  assert.equal(rendered, "");
});

test("installUpdate runs the global install and reports success", () => {
  const calls = [];
  const stdout = new PassThrough();
  const ok = installUpdate({
    packageManager: "pnpm",
    packageName: "@scope/pkg",
    version: "1.2.3",
    stdout,
    stderr: new PassThrough(),
    spawnSyncImpl: (...args) => {
      calls.push(args);
      return { status: 0, signal: null, error: null };
    },
  });

  assert.equal(ok, true);
  assert.deepEqual(calls, [["pnpm", ["add", "--global", "@scope/pkg@1.2.3"], { stdio: "inherit", windowsHide: false }]]);
});

test("installUpdate reports a failing install without throwing", () => {
  const stderr = new PassThrough();
  let rendered = "";
  stderr.on("data", (chunk) => {
    rendered += chunk.toString();
  });

  const failed = installUpdate({
    packageManager: "npm",
    packageName: "@scope/pkg",
    version: "1.2.3",
    stdout: new PassThrough(),
    stderr,
    spawnSyncImpl: () => ({ status: 1, signal: null, error: null }),
  });
  assert.equal(failed, false);
  assert.match(rendered, /npm install --global @scope\/pkg@1\.2\.3/);

  rendered = "";
  const crashed = installUpdate({
    packageManager: "npm",
    packageName: "@scope/pkg",
    version: "1.2.3",
    stdout: new PassThrough(),
    stderr,
    spawnSyncImpl: () => ({ status: null, signal: null, error: new Error("EACCES") }),
  });
  assert.equal(crashed, false);
  assert.match(rendered, /EACCES/);
});
