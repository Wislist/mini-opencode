import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  packageDefinitions,
  prepareRelease,
} from "../../scripts/prepare-npm-release.mjs";

test("prepareRelease generates publishable platform and wrapper packages", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "mini-opencode-npm-"));
  const artifacts = path.join(root, "artifacts");
  const output = path.join(root, "out");
  await mkdir(artifacts);

  for (const definition of packageDefinitions) {
    await writeFile(path.join(artifacts, definition.artifact), definition.packageName);
  }

  await prepareRelease({
    version: "1.2.3",
    artifactsDir: artifacts,
    outputDir: output,
  });

  const wrapper = JSON.parse(await readFile(path.join(output, "wrapper", "package.json"), "utf8"));
  assert.equal(wrapper.name, "@wislist/mini-opencode");
  assert.equal(wrapper.version, "1.2.3");
  assert.equal(wrapper.private, undefined);
  assert.deepEqual(Object.keys(wrapper.optionalDependencies).sort(), packageDefinitions.map((item) => item.packageName).sort());
  assert.ok(Object.values(wrapper.optionalDependencies).every((version) => version === "1.2.3"));
  // The published wrapper must ship the update shim it requires at runtime.
  assert.match(await readFile(path.join(output, "wrapper", "lib", "update.js"), "utf8"), /checkForUpdate/);

  for (const definition of packageDefinitions) {
    const packageDir = path.join(output, definition.directory);
    const manifest = JSON.parse(await readFile(path.join(packageDir, "package.json"), "utf8"));
    assert.equal(manifest.name, definition.packageName);
    assert.equal(manifest.version, "1.2.3");
    assert.deepEqual(manifest.os, [definition.platform]);
    assert.deepEqual(manifest.cpu, [definition.arch]);
    assert.equal(
      await readFile(path.join(packageDir, "bin", definition.executable), "utf8"),
      definition.packageName,
    );
  }
});

test("prepareRelease rejects invalid versions before writing packages", async () => {
  await assert.rejects(
    prepareRelease({ version: "v1.2", artifactsDir: "/missing", outputDir: "/unused" }),
    /valid semantic version/i,
  );
});

test("prepareRelease fails when a required platform artifact is missing", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "mini-opencode-npm-missing-"));
  await assert.rejects(
    prepareRelease({
      version: "1.2.3",
      artifactsDir: path.join(root, "artifacts"),
      outputDir: path.join(root, "out"),
    }),
    /missing release artifact/i,
  );
});
