// Points a copy of the npm package at a GitHub release; npm/publish.sh runs it on that copy.
// Downloads each binary of v1.2.3, pins its sha256 in assets.json and sets the package version.
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';

const repo = 'sudiptadeb/deskrig';
const platforms = ['darwin/amd64', 'darwin/arm64'];
const assetName = (os, arch, v) => `deskrig-${os}-${arch}`;

const version = process.argv[2]?.replace(/^v/, '');
if (!version) throw new Error('usage: node pin.mjs <version>');

const assets = {};
for (const p of platforms) {
  const [os, arch] = p.split('/');
  const url = `https://github.com/${repo}/releases/download/v${version}/${assetName(os, arch, version)}`;
  const res = await fetch(url);
  if (!res.ok) throw new Error(`${res.status} ${url}`);
  const sha256 = createHash('sha256').update(Buffer.from(await res.arrayBuffer())).digest('hex');
  assets[p] = { url, sha256 };
  console.log(p, sha256);
}

const dir = new URL('.', import.meta.url);
writeFileSync(new URL('assets.json', dir), JSON.stringify({ version, assets }, null, 2) + '\n');
const pkg = JSON.parse(readFileSync(new URL('package.json', dir), 'utf8'));
pkg.version = version;
writeFileSync(new URL('package.json', dir), JSON.stringify(pkg, null, 2) + '\n');
