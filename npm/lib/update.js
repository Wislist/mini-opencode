// Update notification for the npm wrapper.
//
// The native binary cannot know how it was installed, but this Node shim can:
// it compares the version recorded in its own package.json against the `latest`
// dist-tag on the registry, prints a notice, and offers to run the global
// install command. Everything here is best effort — a failed check must never
// keep the app from starting.

const { spawnSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const readline = require("node:readline");

const PACKAGE_NAME = "@wislist/mini-opencode";
const DEFAULT_REGISTRY = "https://registry.npmjs.org";
const DEFAULT_TIMEOUT_MS = 1500;
const DEFAULT_INTERVAL_MS = 24 * 60 * 60 * 1000;
const PROMPT = "Update now? [y/N] ";

const VERSION_PATTERN = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/;

function parseVersion(value) {
  if (value === undefined || value === null) {
    return null;
  }
  const match = VERSION_PATTERN.exec(String(value).trim());
  if (!match) {
    return null;
  }
  return {
    major: Number(match[1]),
    minor: Number(match[2]),
    patch: Number(match[3]),
    prerelease: match[4] ? match[4].split(".") : [],
  };
}

function comparePrerelease(left, right) {
  if (left.length === 0 && right.length === 0) return 0;
  if (left.length === 0) return 1;
  if (right.length === 0) return -1;

  const length = Math.min(left.length, right.length);
  for (let index = 0; index < length; index += 1) {
    const a = left[index];
    const b = right[index];
    if (a === b) continue;
    const aNumeric = /^\d+$/.test(a);
    const bNumeric = /^\d+$/.test(b);
    if (aNumeric && bNumeric) {
      return Number(a) < Number(b) ? -1 : 1;
    }
    if (aNumeric !== bNumeric) {
      return aNumeric ? -1 : 1;
    }
    return a < b ? -1 : 1;
  }
  if (left.length === right.length) return 0;
  return left.length < right.length ? -1 : 1;
}

function compareVersions(left, right) {
  const a = parseVersion(left);
  const b = parseVersion(right);
  if (!a || !b) {
    // Unparseable input cannot be ordered; parseVersion/isNewerVersion decide
    // whether a version is usable at all.
    return 0;
  }
  for (const key of ["major", "minor", "patch"]) {
    if (a[key] !== b[key]) {
      return a[key] < b[key] ? -1 : 1;
    }
  }
  return comparePrerelease(a.prerelease, b.prerelease);
}

function isNewerVersion(current, latest) {
  const currentParsed = parseVersion(current);
  const latestParsed = parseVersion(latest);
  if (!currentParsed || !latestParsed) {
    return false;
  }
  return compareVersions(latest, current) > 0;
}

// Development builds report a placeholder version (or carry a prerelease tag)
// and must never be nagged about a published release.
function isComparableVersion(value) {
  const parsed = parseVersion(value);
  if (!parsed) return false;
  if (parsed.prerelease.length > 0) return false;
  if (parsed.major === 0 && parsed.minor === 0 && parsed.patch === 0) return false;
  return true;
}

function shouldCheckForUpdate({ lastChecked, now, intervalMs = DEFAULT_INTERVAL_MS }) {
  if (!Number.isFinite(lastChecked)) {
    return true;
  }
  const elapsed = now - lastChecked;
  // A timestamp in the future means the clock moved backwards; re-check.
  if (elapsed < 0) {
    return true;
  }
  return elapsed >= intervalMs;
}

function defaultStateFile() {
  return path.join(os.homedir(), ".mini-opencode", "update-check.json");
}

function readState(file = defaultStateFile()) {
  try {
    const parsed = JSON.parse(fs.readFileSync(file, "utf8"));
    if (parsed && typeof parsed === "object") {
      return parsed;
    }
  } catch {
    // Missing, unreadable, or corrupt state simply means "check again".
  }
  return {};
}

function writeState(file = defaultStateFile(), state = {}) {
  try {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, `${JSON.stringify(state)}\n`);
    return true;
  } catch {
    return false;
  }
}

function updateState(file, patch) {
  const state = readState(file);
  writeState(file, { ...state, ...patch });
}

function markDeclined(file, version) {
  updateState(file, { declinedVersion: version });
}

function registryUrl(registry, packageName) {
  const base = String(registry || DEFAULT_REGISTRY).replace(/\/+$/, "");
  return `${base}/${encodeURIComponent(packageName)}/latest`;
}

async function fetchLatestVersion({
  registry = process.env.npm_config_registry || DEFAULT_REGISTRY,
  packageName = PACKAGE_NAME,
  fetchImpl = globalThis.fetch,
  timeoutMs = DEFAULT_TIMEOUT_MS,
} = {}) {
  if (typeof fetchImpl !== "function") {
    return null;
  }

  const controller = new AbortController();
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => {
      controller.abort();
      reject(new Error("update check timed out"));
    }, timeoutMs);
    if (timer && typeof timer.unref === "function") {
      timer.unref();
    }
  });

  try {
    const response = await Promise.race([
      fetchImpl(registryUrl(registry, packageName), {
        headers: { accept: "application/json" },
        signal: controller.signal,
      }),
      timeout,
    ]);
    if (!response || !response.ok) {
      return null;
    }
    const body = await response.json();
    const version = body && body.version;
    return parseVersion(version) ? String(version).trim().replace(/^v/, "") : null;
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

async function checkForUpdate({
  currentVersion,
  packageName = PACKAGE_NAME,
  registry,
  stateFile = defaultStateFile(),
  now = Date.now(),
  intervalMs = DEFAULT_INTERVAL_MS,
  fetchLatest,
  fetchImpl,
  timeoutMs = DEFAULT_TIMEOUT_MS,
} = {}) {
  if (!isComparableVersion(currentVersion)) {
    return { status: "skipped", reason: "unknown-current-version" };
  }

  const state = readState(stateFile);
  if (!shouldCheckForUpdate({ lastChecked: state.lastChecked, now, intervalMs })) {
    return { status: "skipped", reason: "fresh-cache" };
  }

  const fetchLatestFn = fetchLatest || ((options) => fetchLatestVersion(options));
  let latest = null;
  try {
    latest = await fetchLatestFn({ registry, packageName, fetchImpl, timeoutMs });
  } catch {
    latest = null;
  }

  // Record the attempt even when the registry is unreachable: otherwise every
  // offline start would hit the network again.
  updateState(stateFile, { lastChecked: now });

  if (!latest) {
    return { status: "skipped", reason: "unavailable" };
  }
  if (!isNewerVersion(currentVersion, latest)) {
    return { status: "current", latest };
  }
  if (state.declinedVersion && !isNewerVersion(state.declinedVersion, latest)) {
    return { status: "skipped", reason: "declined", latest };
  }
  return { status: "update-available", current: currentVersion, latest };
}

// The invoking package manager only shows up in the user agent when the process
// is started from an npm/pnpm/yarn script, which is not how users launch the
// binary from a shell. The install location is the more reliable hint.
function detectPackageManager({ userAgent, scriptPath, pnpmHome } = {}) {
  const match = /^\s*([a-zA-Z]+)/.exec(String(userAgent || ""));
  const name = match ? match[1].toLowerCase() : "";
  if (name === "pnpm" || name === "yarn") {
    return name;
  }
  const location = String(scriptPath || "");
  if (location !== "") {
    if (pnpmHome && location.startsWith(String(pnpmHome))) {
      return "pnpm";
    }
    if (/(^|[\\/])pnpm([\\/]|$)/i.test(location)) {
      return "pnpm";
    }
  }
  return "npm";
}

function updateCommand({ packageManager = "npm", packageName = PACKAGE_NAME, version } = {}) {
  const specifier = `${packageName}@${version}`;
  switch (packageManager) {
    case "pnpm":
      return ["pnpm", ["add", "--global", specifier]];
    case "yarn":
      return ["yarn", ["global", "add", specifier]];
    default:
      return ["npm", ["install", "--global", specifier]];
  }
}

function formatUpdateNotice({ current, latest }) {
  return `A new version of mini-opencode is available: ${current} -> ${latest}`;
}

function installUpdate({
  packageManager = "npm",
  packageName = PACKAGE_NAME,
  version,
  spawnSyncImpl = spawnSync,
  stdout = process.stdout,
  stderr = process.stderr,
} = {}) {
  const [command, args] = updateCommand({ packageManager, packageName, version });
  const printable = [command, ...args].join(" ");
  stdout.write(`Running ${printable}\n`);

  let result;
  try {
    result = spawnSyncImpl(command, args, { stdio: "inherit", windowsHide: false });
  } catch (error) {
    stderr.write(`Update failed: ${error.message}\nRun "${printable}" manually to update.\n`);
    return false;
  }

  if (result && result.error) {
    stderr.write(`Update failed: ${result.error.message}\nRun "${printable}" manually to update.\n`);
    return false;
  }
  if (!result || result.status !== 0) {
    stderr.write(`Update failed.\nRun "${printable}" manually to update.\n`);
    return false;
  }
  stdout.write(`Updated to ${version}.\n`);
  return true;
}

function promptForUpdate({ input = process.stdin, output = process.stdout, question = PROMPT } = {}) {
  if (!input || !input.isTTY) {
    return Promise.resolve(false);
  }
  return new Promise((resolve) => {
    let settled = false;
    let rl;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      try {
        rl.close();
      } catch {
        // The interface may already be closed by an EOF.
      }
      resolve(value);
    };

    try {
      rl = readline.createInterface({ input, output });
    } catch {
      resolve(false);
      return;
    }
    rl.on("close", () => finish(false));
    rl.question(question, (answer) => {
      const reply = String(answer || "").trim().toLowerCase();
      finish(reply === "y" || reply === "yes");
    });
  });
}

module.exports = {
  DEFAULT_INTERVAL_MS,
  PACKAGE_NAME,
  checkForUpdate,
  compareVersions,
  defaultStateFile,
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
};
