import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { chmodSync, copyFileSync, cpSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
export const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
export const sdk = join(root, 'sdk/typescript')
export const artifacts = join(root, 'dist/npm')
export const manifest = JSON.parse(readFileSync(join(sdk, 'package.json'), 'utf8'))
export const { targets, platformPackage } = require('../sdk/typescript/cli/october-bus.cjs')

export function npm(args, options = {}) {
  // Invoked through npm run: this also avoids a shell or npm.cmd on Windows.
  assert.ok(process.env.npm_execpath, 'Run this script through the package.json npm scripts')
  return execFileSync(process.execPath, [process.env.npm_execpath, ...args], { encoding: 'utf8', ...options })
}

export function packageFor(target) {
  assert.ok(targets.includes(target), `Unsupported npm target: ${target}`)
  const [os, cpu] = target.split('-')
  const platform = platformPackage(os, cpu)
  return { ...platform, os, cpu, goos: os === 'win32' ? 'windows' : os, goarch: cpu === 'x64' ? 'amd64' : 'arm64' }
}

export function tarball(name) {
  return join(artifacts, `${name.replace('@', '').replace('/', '-')}-${manifest.version}.tgz`)
}

export function validateDistribution() {
  assert.match(manifest.version, /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/)
  assert.equal(manifest.bin['october-bus'], 'cli/october-bus.cjs')
}

export function distributionManifest() {
  return { ...manifest, optionalDependencies: Object.fromEntries(targets.map(target => [packageFor(target).name, manifest.version])) }
}

function build(target) {
  const platform = packageFor(target)
  const directory = join(artifacts, target)
  mkdirSync(join(directory, 'bin'), { recursive: true })
  const binary = join(directory, 'bin', platform.binary)
  execFileSync('go', ['build', '-trimpath', '-ldflags', `-s -w -X github.com/october-dev/october-bus/bus.Version=${manifest.version}`, '-o', binary, './cmd/october-bus'], {
    cwd: root, stdio: 'inherit', env: { ...process.env, CGO_ENABLED: '0', GOOS: platform.goos, GOARCH: platform.goarch }
  })
  chmodSync(binary, 0o755)
  const pkg = {
    name: platform.name, version: manifest.version, description: `October Bus native Go binary for ${target}.`,
    license: manifest.license, os: [platform.os], cpu: [platform.cpu],
    repository: manifest.repository, homepage: manifest.homepage,
    files: ['bin', 'LICENSE', 'README.md'], publishConfig: manifest.publishConfig
  }
  writeFileSync(join(directory, 'package.json'), `${JSON.stringify(pkg, null, 2)}\n`)
  copyFileSync(join(root, 'LICENSE'), join(directory, 'LICENSE'))
  writeFileSync(join(directory, 'README.md'), `# ${platform.name}\n\nNative Go executable for October Bus on ${target}. Installed automatically by \`${manifest.name}@${manifest.version}\`; use that package rather than installing this internal platform package directly.\n`)
  console.log(`Built ${platform.name}@${manifest.version}`)
}

function pack(directory, expectedBinary) {
  const [result] = JSON.parse(npm(['pack', '--json', '--ignore-scripts', '--pack-destination', artifacts], { cwd: directory }))
  assert.ok(result.files.some(file => file.path === expectedBinary), `Packed binary/launcher missing: ${expectedBinary}`)
  console.log(`Packed ${result.filename}`)
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  validateDistribution()
  const [command, selection = `${process.platform}-${process.arch}`, ...extra] = process.argv.slice(2)
  assert.ok(['build', 'pack'].includes(command) && extra.length === 0, 'Expected build|pack [--all|os-arch]')
  const selected = selection === '--all' ? targets : [packageFor(selection).target]
  mkdirSync(artifacts, { recursive: true })
  for (const target of selected) {
    if (command === 'build') build(target)
    else pack(join(artifacts, target), `bin/${packageFor(target).binary}`)
  }
  if (command === 'pack') {
    npm(['run', 'build'], { cwd: sdk, stdio: 'inherit' })
    // Generate release-only dependencies here. Source npm ci must also work
    // before these exact-version platform packages have been published.
    const stagedSDK = join(artifacts, 'sdk')
    mkdirSync(stagedSDK, { recursive: true })
    for (const path of ['cli', 'dist', 'src', 'LICENSE', 'README.md']) {
      cpSync(join(sdk, path), join(stagedSDK, path), { recursive: true })
    }
    writeFileSync(join(stagedSDK, 'package.json'), `${JSON.stringify(distributionManifest(), null, 2)}\n`)
    pack(stagedSDK, manifest.bin['october-bus'])
  }
}
