#!/usr/bin/env node
'use strict';
// Runs the StudyHomie binary for this platform, downloading it first if postinstall did not.
const { spawn } = require('node:child_process');
const { ensure } = require('../lib/install.js');

ensure()
  .then((bin) => {
    const child = spawn(bin, process.argv.slice(2), { stdio: 'inherit' });
    // Ctrl+C reaches the binary too (same console): wait for it to finish instead of dying first
    for (const sig of ['SIGINT', 'SIGTERM', 'SIGHUP']) process.on(sig, () => {});
    child.on('error', (err) => { console.error(`studyhomie: ${err.message}`); process.exit(1); });
    child.on('exit', (code, signal) => process.exit(signal ? 1 : code ?? 0));
  })
  .catch((err) => {
    console.error(`studyhomie: ${err.message}`);
    process.exit(1);
  });
