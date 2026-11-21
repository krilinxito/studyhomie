'use strict';
// Downloads the StudyHomie release that matches this package's version, checks its SHA-256 against
// the release's checksums.txt and unpacks the binary into vendor/. Used by postinstall and, if that
// was skipped (--ignore-scripts), by the first run.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const { execFileSync } = require('node:child_process');

const REPO = 'krilinxito/studyhomie';
const pkg = require('../package.json');
const vendor = path.join(__dirname, '..', 'vendor');
const exeName = process.platform === 'win32' ? 'studyhomie.exe' : 'studyhomie';
const binPath = path.join(vendor, exeName);

function target() {
  const osName = { win32: 'windows', darwin: 'darwin', linux: 'linux' }[process.platform];
  const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
  if (!osName || !arch) throw new Error(`no build for ${process.platform}/${process.arch}`);
  // Windows only has an amd64 build; Windows on ARM runs it under emulation
  return { os: osName, arch: osName === 'windows' ? 'amd64' : arch };
}

async function get(url) {
  const res = await fetch(url, { headers: { 'User-Agent': `studyhomie-npm/${pkg.version}` }, redirect: 'follow' });
  if (!res.ok) throw new Error(`GET ${url}: ${res.status} ${res.statusText}`);
  return Buffer.from(await res.arrayBuffer());
}

async function version() {
  const v = process.env.STUDYHOMIE_VERSION || pkg.version;
  if (!/^0\.0\.0/.test(v)) return v.startsWith('v') ? v : `v${v}`;
  // a development checkout of this package: use the latest release
  const r = JSON.parse((await get(`https://api.github.com/repos/${REPO}/releases/latest`)).toString('utf8'));
  return r.tag_name;
}

async function install() {
  const v = await version();
  const t = target();
  const name = `studyhomie_${v}_${t.os}_${t.arch}`;
  const file = `${name}${t.os === 'windows' ? '.zip' : '.tar.gz'}`;
  const base = `https://github.com/${REPO}/releases/download/${v}`;
  const [archive, sums] = await Promise.all([get(`${base}/${file}`), get(`${base}/checksums.txt`)]);

  const line = sums.toString('utf8').split('\n').map((l) => l.trim().split(/\s+/)).find((p) => p[1] && p[1].replace(/^\*/, '') === file);
  if (!line) throw new Error(`checksums.txt has no entry for ${file}`);
  const got = crypto.createHash('sha256').update(archive).digest('hex');
  if (got !== line[0].toLowerCase()) throw new Error(`checksum mismatch for ${file}`);

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'studyhomie-'));
  try {
    const archivePath = path.join(tmp, file);
    fs.writeFileSync(archivePath, archive);
    // bsdtar (in Windows 10+, macOS and most Linux) unpacks both .zip and .tar.gz
    try {
      execFileSync('tar', ['-xf', archivePath, '-C', tmp], { stdio: 'ignore' });
    } catch (err) {
      if (t.os !== 'windows') throw err;
      execFileSync('powershell.exe', ['-NoProfile', '-Command', `Expand-Archive -LiteralPath '${archivePath}' -DestinationPath '${tmp}' -Force`], { stdio: 'ignore' });
    }
    fs.mkdirSync(vendor, { recursive: true });
    const next = `${binPath}.new`;
    fs.copyFileSync(path.join(tmp, name, exeName), next);
    fs.chmodSync(next, 0o755);
    fs.renameSync(next, binPath);
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
  return v;
}

// npm 11 does not run install scripts of global packages unless allowed, so the first run usually
// is the one that downloads the binary
async function ensure() {
  if (!fs.existsSync(binPath)) {
    process.stderr.write('studyhomie: downloading the binary for this system (only the first time)...\n');
    await install();
  }
  return binPath;
}

module.exports = { ensure, install };

if (require.main === module) {
  install()
    .then((v) => console.log(`studyhomie ${v} installed`))
    .catch((err) => {
      // do not fail the npm install: the first run tries again and explains what went wrong
      console.warn(`studyhomie: could not download the binary now (${err.message}); it will be retried on first run.`);
    });
}
