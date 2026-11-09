'use strict';

const path = require('node:path');

const OS_NAMES = { win32: 'windows', darwin: 'darwin', linux: 'linux' };
const ARCH_NAMES = { x64: 'amd64', arm64: 'arm64' };
const EXECUTABLE_EXT = { windows: '.exe', darwin: '', linux: '' };

// resolveTarget maps Node's platform/arch to the release asset naming used by
// the GitHub release workflow. Returns null for combinations without a build.
function resolveTarget(platform = process.platform, arch = process.arch) {
  const os = OS_NAMES[platform];
  const cpu = ARCH_NAMES[arch];
  if (!os || !cpu) {
    return null;
  }
  if (os === 'windows' && cpu !== 'amd64') {
    return null;
  }
  return { os, arch: cpu, ext: EXECUTABLE_EXT[os] };
}

function executableName(target) {
  return `newapi-cli-${target.os}-${target.arch}${target.ext}`;
}

function executablePath(packageDir, target) {
  return path.join(packageDir, 'vendor', executableName(target));
}

module.exports = { executableName, executablePath, resolveTarget };
