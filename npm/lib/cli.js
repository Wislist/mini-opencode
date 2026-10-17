const os = require("node:os");
const { spawnSync } = require("node:child_process");

const { resolveBinary } = require("./platform");
const update = require("./update");

function signalExitCode(signal) {
  const number = os.constants.signals[signal];
  return Number.isInteger(number) ? 128 + number : 1;
}

function envFlag(value) {
  if (value === undefined || value === null) {
    return false;
  }
  const text = String(value).trim().toLowerCase();
  return !(text === "" || text === "0" || text === "false" || text === "no");
}

function updateChecksDisabled(env = process.env) {
  return Boolean(
    env && (envFlag(env.MINI_OPENCODE_NO_UPDATE_CHECK) || envFlag(env.NO_UPDATE_NOTIFIER) || envFlag(env.CI)),
  );
}

async function notifyUpdateIfAvailable({
  currentVersion,
  env = process.env,
  stdin = process.stdin,
  stdout = process.stdout,
  stderr = process.stderr,
  now,
  stateFile,
  packageName = update.PACKAGE_NAME,
  spawnSyncImpl,
  checkForUpdate = update.checkForUpdate,
  promptForUpdate = update.promptForUpdate,
  installUpdate = update.installUpdate,
} = {}) {
  if (updateChecksDisabled(env)) {
    return { status: "skipped", reason: "disabled" };
  }
  if (!stdin?.isTTY || !stdout?.isTTY) {
    return { status: "skipped", reason: "not-a-terminal" };
  }

  const result = await checkForUpdate({
    currentVersion,
    packageName,
    now,
    stateFile,
    registry: env && env.npm_config_registry,
  });
  if (!result || result.status !== "update-available") {
    return result || { status: "skipped", reason: "unavailable" };
  }

  stdout.write(`\n${update.formatUpdateNotice(result)}\n`);
  const accepted = await promptForUpdate({
    input: stdin,
    output: stdout,
    current: result.current,
    latest: result.latest,
  });
  if (!accepted) {
    update.markDeclined(stateFile, result.latest);
    return { ...result, accepted: false };
  }

  const updated = installUpdate({
    packageManager: update.detectPackageManager({
      userAgent: env && env.npm_config_user_agent,
      scriptPath: process.argv[1],
      pnpmHome: env && env.PNPM_HOME,
    }),
    packageName,
    version: result.latest,
    spawnSyncImpl: spawnSyncImpl || spawnSync,
    stdout,
    stderr,
  });
  return { ...result, accepted: true, updated };
}

async function run(args, overrides = {}) {
  try {
    await notifyUpdateIfAvailable({
      currentVersion: overrides.currentVersion || require("../package.json").version,
      env: overrides.env,
      stdin: overrides.stdin,
      stdout: overrides.stdout,
      stderr: overrides.stderr,
      now: overrides.now,
      stateFile: overrides.stateFile,
      spawnSyncImpl: overrides.spawnSync,
      checkForUpdate: overrides.checkForUpdate,
      promptForUpdate: overrides.promptForUpdate,
      installUpdate: overrides.installUpdate,
    });
  } catch {
    // A broken update check must never prevent the app from starting.
  }
  return main(args, overrides);
}

function main(args, overrides = {}) {
  const platform = overrides.platform || process.platform;
  const arch = overrides.arch || process.arch;
  const findBinary = overrides.resolveBinary || resolveBinary;
  const run = overrides.spawnSync || spawnSync;
  const binary = findBinary(platform, arch);
  const result = run(binary, args, {
    stdio: "inherit",
    windowsHide: false,
  });

  if (result.error) {
    throw result.error;
  }
  if (Number.isInteger(result.status)) {
    return result.status;
  }
  if (result.signal) {
    return signalExitCode(result.signal);
  }
  return 1;
}

module.exports = { main, notifyUpdateIfAvailable, run };
