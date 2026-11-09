'use strict';

const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');

const { executableName, executablePath, resolveTarget } = require('./platform');

const packageDir = path.resolve(__dirname, '..');
const pkg = require('../package.json');

const DEFAULT_DOWNLOAD_BASE = 'https://github.com/QuantumNous/new-api/releases/download';
const CHECKSUM_ASSET = 'checksums-cli.txt';
const DOWNLOAD_TIMEOUT_MS = 120000;

function log(message) {
  console.log(`newapi-cli: ${message}`);
}

function warn(message) {
  console.warn(`newapi-cli: ${message}`);
}

function downloadBase() {
  return (process.env.NEWAPI_CLI_DOWNLOAD_BASE || (pkg.config && pkg.config.downloadBase) || DEFAULT_DOWNLOAD_BASE).replace(/\/+$/, '');
}

// releaseTags lists the tag shapes to try. Published packages normally use
// "v<version>", but releases tagged without the prefix must work too.
function releaseTags() {
  const tags = [];
  if (process.env.NEWAPI_CLI_RELEASE_TAG) {
    tags.push(process.env.NEWAPI_CLI_RELEASE_TAG);
  }
  tags.push(`v${pkg.version}`, pkg.version);
  return [...new Set(tags)];
}

async function download(url) {
  const response = await fetch(url, {
    redirect: 'follow',
    signal: AbortSignal.timeout(DOWNLOAD_TIMEOUT_MS),
  });
  if (!response.ok) {
    throw new Error(`GET ${url} returned HTTP ${response.status}`);
  }
  return Buffer.from(await response.arrayBuffer());
}

async function tryDownload(url) {
  try {
    return await download(url);
  } catch {
    return null;
  }
}

// parseChecksums reads the "<sha256>  <file>" format produced by sha256sum.
function parseChecksums(text) {
  const checksums = new Map();
  for (const line of text.split('\n')) {
    const [hash, file] = line.trim().split(/\s+\*?/);
    if (!hash || !file) {
      continue;
    }
    checksums.set(file, hash.toLowerCase());
  }
  return checksums;
}

function fatalError(message) {
  return Object.assign(new Error(message), { fatal: true });
}

async function installBinary(target) {
  const base = downloadBase();
  const name = executableName(target);
  let lastError = new Error(`no release found for ${pkg.version}`);

  for (const tag of releaseTags()) {
    try {
      const checksums = await tryDownload(`${base}/${tag}/${CHECKSUM_ASSET}`);
      if (!checksums) {
        throw new Error(`release ${tag} has no ${CHECKSUM_ASSET}; refusing to install an unverified binary`);
      }
      const binary = await download(`${base}/${tag}/${name}`);
      const expected = parseChecksums(checksums.toString('utf8')).get(name);
      if (!expected) {
        throw fatalError(`${CHECKSUM_ASSET} for ${tag} has no entry for ${name}`);
      }
      const actual = crypto.createHash('sha256').update(binary).digest('hex');
      if (actual !== expected) {
        throw fatalError(`checksum mismatch for ${name} in ${tag}: expected ${expected}, got ${actual}`);
      }

      const destination = executablePath(packageDir, target);
      fs.mkdirSync(path.dirname(destination), { recursive: true });
      fs.writeFileSync(destination, binary, { mode: 0o755 });
      log(`installed ${name} from ${tag}`);
      return;
    } catch (err) {
      if (err.fatal) {
        throw err;
      }
      lastError = err;
    }
  }

  throw lastError;
}

// installSkill copies the bundled agent skills into the project that runs
// `npm install`, so agents can use the CLI and the integration guide without a
// manual setup step.
function installSkill() {
  if (process.env.NEWAPI_CLI_SKIP_SKILL === '1' || process.env.npm_config_global === 'true') {
    return;
  }
  const sourceRoot = path.join(packageDir, 'skills');
  if (!fs.existsSync(sourceRoot)) {
    warn('bundled skills are missing; skipping skill installation');
    return;
  }
  const names = fs
    .readdirSync(sourceRoot, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && fs.existsSync(path.join(sourceRoot, entry.name, 'SKILL.md')))
    .map((entry) => entry.name);
  if (names.length === 0) {
    warn('bundled skills are missing; skipping skill installation');
    return;
  }
  const projectDir = process.env.NEWAPI_CLI_PROJECT_DIR || process.env.INIT_CWD;
  if (!projectDir) {
    warn('cannot determine the project directory; skipping skill installation (set NEWAPI_CLI_PROJECT_DIR)');
    return;
  }
  for (const name of names) {
    const destination = path.join(projectDir, '.agents', 'skills', name);
    fs.mkdirSync(destination, { recursive: true });
    fs.cpSync(path.join(sourceRoot, name), destination, { recursive: true, force: true });
    log(`skill installed to ${destination}`);
  }
}

async function main() {
  installSkill();

  if (process.env.NEWAPI_CLI_SKIP_DOWNLOAD === '1') {
    log('skipping binary download (NEWAPI_CLI_SKIP_DOWNLOAD=1)');
    return;
  }

  const target = resolveTarget();
  if (!target) {
    warn(`no prebuilt binary for ${process.platform}/${process.arch}; build from source with "go build ./cmd/newapi-cli"`);
    return;
  }

  try {
    await installBinary(target);
  } catch (err) {
    console.error(`newapi-cli: failed to install the binary: ${err.message}`);
    console.error(`newapi-cli: retry with "npm rebuild ${pkg.name}", or set NEWAPI_CLI_SKIP_DOWNLOAD=1 to install without it.`);
    process.exitCode = 1;
  }
}

main();
