const assert = require("node:assert/strict");
const test = require("node:test");

const {
  platformPackage,
  resolveBinary,
} = require("../lib/platform");

const cases = [
  ["darwin", "arm64", "@wislist/mini-opencode-darwin-arm64", "mini-opencode"],
  ["darwin", "x64", "@wislist/mini-opencode-darwin-x64", "mini-opencode"],
  ["win32", "arm64", "@wislist/mini-opencode-win32-arm64", "mini-opencode.exe"],
  ["win32", "x64", "@wislist/mini-opencode-win32-x64", "mini-opencode.exe"],
];

test("platformPackage maps every supported platform", () => {
  for (const [platform, arch, packageName, executable] of cases) {
    assert.deepEqual(platformPackage(platform, arch), { packageName, executable });
  }
});

test("platformPackage rejects unsupported operating systems and architectures", () => {
  assert.throws(() => platformPackage("linux", "x64"), /unsupported platform linux-x64/i);
  assert.throws(() => platformPackage("darwin", "ia32"), /unsupported platform darwin-ia32/i);
});

test("resolveBinary resolves the executable exported by the optional package", () => {
  const requested = [];
  const result = resolveBinary("win32", "x64", (specifier) => {
    requested.push(specifier);
    return "C:\\pkg\\bin\\mini-opencode.exe";
  });

  assert.equal(result, "C:\\pkg\\bin\\mini-opencode.exe");
  assert.deepEqual(requested, ["@wislist/mini-opencode-win32-x64/bin/mini-opencode.exe"]);
});

test("resolveBinary explains how to repair a missing optional dependency", () => {
  assert.throws(
    () => resolveBinary("darwin", "arm64", () => {
      const error = new Error("not found");
      error.code = "MODULE_NOT_FOUND";
      throw error;
    }),
    (error) => {
      assert.match(error.message, /@wislist\/mini-opencode-darwin-arm64/);
      assert.match(error.message, /optional dependencies/i);
      assert.match(error.message, /npm|pnpm/i);
      return true;
    },
  );
});
