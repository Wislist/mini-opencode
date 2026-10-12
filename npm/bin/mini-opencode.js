#!/usr/bin/env node

const { main } = require("../lib/cli");

try {
  process.exitCode = main(process.argv.slice(2));
} catch (error) {
  const message = error instanceof Error ? error.message : String(error);
  console.error(`mini-opencode: ${message}`);
  process.exitCode = 1;
}
