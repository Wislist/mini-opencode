#!/usr/bin/env node

import { constants as fsConstants } from "node:fs";
import {
  access,
  chmod,
  copyFile,
  cp,
  mkdir,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(scriptDir, "..");

export const packageDefinitions = [
  {
    platform: "darwin",
    arch: "arm64",
    directory: "darwin-arm64",
    packageName: "@wislist/mini-opencode-darwin-arm64",
    artifact: "mini-opencode-darwin-arm64",
    executable: "mini-opencode",
  },
  {
    platform: "darwin",
    arch: "x64",
    directory: "darwin-x64",
    packageName: "@wislist/mini-opencode-darwin-x64",
    artifact: "mini-opencode-darwin-x64",
    executable: "mini-opencode",
  },
  {
    platform: "win32",
    arch: "arm64",
    directory: "win32-arm64",
    packageName: "@wislist/mini-opencode-win32-arm64",
    artifact: "mini-opencode-win32-arm64.exe",
    executable: "mini-opencode.exe",
  },
  {
    platform: "win32",
    arch: "x64",
    directory: "win32-x64",
    packageName: "@wislist/mini-opencode-win32-x64",
    artifact: "mini-opencode-win32-x64.exe",
    executable: "mini-opencode.exe",
  },
];

function validVersion(version) {
  return /^\d+\.\d+\.\d+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.test(version);
}

async function writeJSON(filename, value) {
  await writeFile(filename, `${JSON.stringify(value, null, 2)}\n`);
}

export async function prepareRelease({ version, artifactsDir, outputDir }) {
  if (!validVersion(version)) {
    throw new Error(`Version ${JSON.stringify(version)} is not a valid semantic version`);
  }

  for (const definition of packageDefinitions) {
    const artifact = path.join(artifactsDir, definition.artifact);
    try {
      await access(artifact, fsConstants.R_OK);
    } catch (error) {
      throw new Error(`Missing release artifact: ${artifact}`, { cause: error });
    }
  }

  await rm(outputDir, { recursive: true, force: true });
  await mkdir(outputDir, { recursive: true });

  const license = path.join(repoRoot, "LICENSE");
  for (const definition of packageDefinitions) {
    const packageDir = path.join(outputDir, definition.directory);
    const binDir = path.join(packageDir, "bin");
    await mkdir(binDir, { recursive: true });
    await copyFile(path.join(artifactsDir, definition.artifact), path.join(binDir, definition.executable));
    if (definition.platform !== "win32") {
      await chmod(path.join(binDir, definition.executable), 0o755);
    }
    await copyFile(license, path.join(packageDir, "LICENSE"));
    await writeJSON(path.join(packageDir, "package.json"), {
      name: definition.packageName,
      version,
      description: `Native ${definition.platform}-${definition.arch} binary for @wislist/mini-opencode.`,
      license: "MIT",
      repository: {
        type: "git",
        url: "git+https://github.com/Wislist/mini-opencode.git",
      },
      os: [definition.platform],
      cpu: [definition.arch],
      files: ["bin"],
      publishConfig: { access: "public" },
    });
  }

  const wrapperDir = path.join(outputDir, "wrapper");
  await mkdir(wrapperDir, { recursive: true });
  await cp(path.join(repoRoot, "npm", "bin"), path.join(wrapperDir, "bin"), { recursive: true });
  await cp(path.join(repoRoot, "npm", "lib"), path.join(wrapperDir, "lib"), { recursive: true });
  await copyFile(path.join(repoRoot, "npm", "README.md"), path.join(wrapperDir, "README.md"));
  await copyFile(license, path.join(wrapperDir, "LICENSE"));
  await chmod(path.join(wrapperDir, "bin", "mini-opencode.js"), 0o755);

  const wrapper = JSON.parse(await readFile(path.join(repoRoot, "npm", "package.json"), "utf8"));
  wrapper.version = version;
  delete wrapper.private;
  delete wrapper.scripts;
  wrapper.optionalDependencies = Object.fromEntries(
    packageDefinitions.map((definition) => [definition.packageName, version]),
  );
  wrapper.publishConfig = { access: "public" };
  await writeJSON(path.join(wrapperDir, "package.json"), wrapper);
}

function parseArguments(args) {
  const values = {};
  for (let index = 0; index < args.length; index += 2) {
    const key = args[index];
    const value = args[index + 1];
    if (!key?.startsWith("--") || value === undefined) {
      throw new Error("usage: prepare-npm-release.mjs --version <version> --artifacts <dir> --out <dir>");
    }
    values[key.slice(2)] = value;
  }
  return values;
}

async function run() {
  const args = parseArguments(process.argv.slice(2));
  await prepareRelease({
    version: args.version,
    artifactsDir: path.resolve(args.artifacts || "dist"),
    outputDir: path.resolve(args.out || ".npm-release"),
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  run().catch((error) => {
    console.error(`prepare-npm-release: ${error.message}`);
    process.exitCode = 1;
  });
}
