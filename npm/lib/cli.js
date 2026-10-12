const os = require("node:os");
const { spawnSync } = require("node:child_process");

const { resolveBinary } = require("./platform");

function signalExitCode(signal) {
  const number = os.constants.signals[signal];
  return Number.isInteger(number) ? 128 + number : 1;
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

module.exports = { main };
