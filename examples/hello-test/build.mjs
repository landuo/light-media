// Developer tooling only. The installed plugin is the compiled Go executable.
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../../', import.meta.url));
const directory = resolve(process.argv[2] || resolve(root, 'work/hello-test-repo'));
const platform = process.argv[3] || 'linux-amd64';
if (!/^(linux|darwin)-(amd64|arm64)$/.test(platform)) {
  throw Error('platform must be linux-amd64/linux-arm64 (darwin supported for development tests)');
}
const nativeEnv = { ...process.env };
delete nativeEnv.GOOS;
delete nativeEnv.GOARCH;
const manifest = JSON.parse(execFileSync('go', ['run', './cmd/hello-test', '--manifest'], { cwd: root, env: nativeEnv, encoding: 'utf8' }));
mkdirSync(directory, { recursive: true });
const filename = `hello-test-${manifest.version}-${platform}`;
const [GOOS, GOARCH] = platform.split('-');
const binary = resolve(directory, filename);
execFileSync('go', ['build', '-trimpath', '-o', `${binary}.tmp`, './cmd/hello-test'], { cwd: root, env: { ...process.env, GOOS, GOARCH, CGO_ENABLED: '0' }, stdio: 'inherit' });
renameSync(`${binary}.tmp`, binary);
const bytes = readFileSync(binary);
const digest = createHash('sha256').update(bytes).digest('hex');
const index = {
  schema_version: 1,
  plugin_id: manifest.plugin_id,
  current_version: manifest.version,
  releases: [{
    version: manifest.version, manifest,
    artifacts: [{ platform, url: filename, format: 'executable', size: bytes.length, sha256: digest, executable_sha256: digest, host_contract: 'light-media.sdk-base/v1' }],
  }],
};
const indexPath = resolve(directory, 'go-plugins.v1.json');
writeFileSync(`${indexPath}.tmp`, JSON.stringify(index, null, 2) + '\n');
renameSync(`${indexPath}.tmp`, indexPath);
console.log(JSON.stringify({ plugin_id: manifest.plugin_id, version: manifest.version, platform, directory, index: indexPath, sha256: digest }, null, 2));
