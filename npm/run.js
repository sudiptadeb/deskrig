#!/usr/bin/env node
// Runs deskrig. The first run downloads the release this package names into ~/.local/bin,
// where install.sh puts it, checks it against the sha256 pinned in assets.json, and from then
// on deskrig found there or in /usr/local/bin is the one that runs, keeping itself current.
'use strict';
const fs = require('fs');
const os = require('os');
const path = require('path');
const crypto = require('crypto');
const { execFileSync, spawnSync } = require('child_process');
const { version, assets } = require('./assets.json');

const tool = 'deskrig';
const home = path.join(os.homedir(), '.local', 'bin');

function platform() {
  let arch = { x64: 'amd64', arm64: 'arm64' }[process.arch] || process.arch;
  // Under Rosetta node reports x64; prefer the native binary.
  if (process.platform === 'darwin' && arch === 'amd64') {
    try {
      if (execFileSync('sysctl', ['-n', 'sysctl.proc_translated'], { encoding: 'utf8' }).trim() === '1') arch = 'arm64';
    } catch {}
  }
  return `${process.platform}/${arch}`;
}

function installed() {
  const self = fs.realpathSync(__filename);
  for (const dir of [home, '/usr/local/bin']) {
    const file = path.join(dir, tool);
    try {
      // npm's own shim can sit in /usr/local/bin too; that is this script, not the binary.
      if (fs.realpathSync(file) !== self) return file;
    } catch {}
  }
}

async function install() {
  const want = assets[platform()];
  if (!want) throw new Error(`no ${tool} release for ${platform()}`);
  console.error(`${tool}: downloading ${version} for ${platform()}`);
  const res = await fetch(want.url);
  if (!res.ok) throw new Error(`download failed: ${res.status} ${want.url}`);
  const body = Buffer.from(await res.arrayBuffer());
  const got = crypto.createHash('sha256').update(body).digest('hex');
  if (got !== want.sha256) throw new Error(`checksum mismatch for ${want.url}`);
  fs.mkdirSync(home, { recursive: true });
  const file = path.join(home, tool);
  const tmp = `${file}.${process.pid}.tmp`;
  fs.writeFileSync(tmp, body, { mode: 0o755 });
  // Replace, don't rewrite: macOS can kill a binary changed in place.
  fs.renameSync(tmp, file);
  console.error(`${tool}: installed ${file}`);
  if (!(process.env.PATH || '').split(path.delimiter).includes(home)) {
    console.error(`${tool}: ${home} is not on your PATH; add: export PATH="${home}:$PATH"`);
  }
  return file;
}

async function main() {
  const bin = installed() || (await install());
  const run = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
  if (run.error) throw run.error;
  if (run.signal) process.kill(process.pid, run.signal);
  process.exit(run.status);
}

main().catch((err) => {
  console.error(`${tool}: ${err.message}`);
  process.exit(1);
});
