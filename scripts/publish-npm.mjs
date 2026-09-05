import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { manifest, npm, packageFor, tarball, targets, validateDistribution } from './npm-distribution.mjs'

const registry = ['--registry', 'https://registry.npmjs.org/']
export function publishDistribution(packages, runNpm = npm) {
  for (const pkg of packages) {
    let published
    try {
      published = JSON.parse(runNpm(['view', `${pkg.name}@${manifest.version}`, 'dist.integrity', '--json', ...registry], { stdio: ['ignore', 'pipe', 'pipe'] }))
    } catch (error) {
      let code
      try { code = JSON.parse(error.stdout).error?.code } catch { /* network/auth errors must not be ignored */ }
      if (code !== 'E404') throw error
    }
    if (published) {
      assert.equal(published, pkg.integrity, `Refusing to reuse ${pkg.name}@${manifest.version} with different contents`)
      console.log(`Already published identical artifact: ${pkg.name}@${manifest.version}`)
    } else {
      runNpm(['publish', pkg.file, '--ignore-scripts', '--provenance', '--access', 'public', '--tag', 'next', ...registry], { stdio: 'inherit' })
      assert.equal(JSON.parse(runNpm(['view', `${pkg.name}@${manifest.version}`, 'dist.integrity', '--json', ...registry])), pkg.integrity)
    }
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  validateDistribution()
  assert.ok(manifest.version.includes('-'), 'This workflow only publishes prereleases')
  // Read all seven artifacts before performing any external write.
  const packages = [...targets.map(target => packageFor(target).name), manifest.name].map(name => ({
    name, file: tarball(name), integrity: `sha512-${createHash('sha512').update(readFileSync(tarball(name))).digest('base64')}`
  }))
  publishDistribution(packages)
}
