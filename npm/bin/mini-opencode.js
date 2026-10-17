#!/usr/bin/env node

const { run } = require("../lib/cli");

run(process.argv.slice(2))
  .then((code) => {
    process.exitCode = code;
  })
  .catch((error) => {
    const message = error instanceof Error ? error.message : String(error);
    console.error(`mini-opencode: ${message}`);
    process.exitCode = 1;
  });
