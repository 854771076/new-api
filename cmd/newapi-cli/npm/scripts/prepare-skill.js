'use strict';

const fs = require('node:fs');
const path = require('node:path');

const packageDir = path.resolve(__dirname, '..');
const sourceRoot = path.resolve(packageDir, '..', '..', '..', '.agents', 'skills');
const targetRoot = path.join(packageDir, 'skills');

const SKILLS = ['newapi-cli', 'new-api-oauth-integration'];

// Local-only artifacts of the reference app must not ship in the package.
const EXCLUDED = new Set(['node_modules', 'dist', '.temp', '.env', '.env.local', '.git']);

function shouldCopy(source) {
  return !EXCLUDED.has(path.basename(source));
}

fs.rmSync(targetRoot, { recursive: true, force: true });
for (const name of SKILLS) {
  const source = path.join(sourceRoot, name);
  if (!fs.existsSync(path.join(source, 'SKILL.md'))) {
    console.error(`newapi-cli: skill source not found at ${source}`);
    process.exit(1);
  }
  fs.cpSync(source, path.join(targetRoot, name), { recursive: true, filter: shouldCopy });
}
