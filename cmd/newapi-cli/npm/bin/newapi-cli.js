'use strict';

const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

const { executablePath, resolveTarget } = require('../lib/platform');

const packageDir = path.resolve(__dirname, '..');
const target = resolveTarget();

if (!target) {
  console.error(`newapi-cli: no prebuilt binary for ${process.platform}/${process.arch}`);
  process.exit(1);
}

const binary = executablePath(packageDir, target);
if (!fs.existsSync(binary)) {
  console.error(`newapi-cli: binary is missing at ${binary}`);
  console.error(`newapi-cli: reinstall the package or run "node ${path.join(packageDir, 'lib', 'install.js')}"`);
  process.exit(1);
}

const result = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit' });
if (result.error) {
  console.error(`newapi-cli: failed to start ${binary}: ${result.error.message}`);
  process.exit(1);
}
if (result.signal) {
  process.kill(process.pid, result.signal);
}
process.exit(result.status ?? 1);
