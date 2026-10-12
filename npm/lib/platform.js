const TARGETS = new Map([
  ["darwin-arm64", {
    packageName: "@wislist/mini-opencode-darwin-arm64",
    executable: "mini-opencode",
  }],
  ["darwin-x64", {
    packageName: "@wislist/mini-opencode-darwin-x64",
    executable: "mini-opencode",
  }],
  ["win32-arm64", {
    packageName: "@wislist/mini-opencode-win32-arm64",
    executable: "mini-opencode.exe",
  }],
  ["win32-x64", {
    packageName: "@wislist/mini-opencode-win32-x64",
    executable: "mini-opencode.exe",
  }],
]);

function platformPackage(platform = process.platform, arch = process.arch) {
  const target = TARGETS.get(`${platform}-${arch}`);
  if (!target) {
    throw new Error(
      `Unsupported platform ${platform}-${arch}. ` +
      "mini-opencode currently supports macOS and Windows on arm64 or x64.",
    );
  }
  return target;
}

function resolveBinary(platform = process.platform, arch = process.arch, requireResolve = require.resolve) {
  const target = platformPackage(platform, arch);
  const specifier = `${target.packageName}/bin/${target.executable}`;
  try {
    return requireResolve(specifier);
  } catch (error) {
    if (error && error.code !== "MODULE_NOT_FOUND") {
      throw error;
    }
    throw new Error(
      `The native package ${target.packageName} is missing. ` +
      "Reinstall @wislist/mini-opencode with optional dependencies enabled " +
      "(npm install -g @wislist/mini-opencode or pnpm add -g @wislist/mini-opencode).",
      { cause: error },
    );
  }
}

module.exports = {
  platformPackage,
  resolveBinary,
};
