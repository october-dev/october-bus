import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { manifest, npm, packageFor, readArtifact, root, targets, validateDistribution } from './npm-distribution.mjs'
import { sourceIdentity } from './artifact-integrity.mjs'

const registry = ['--registry', 'https://registry.npmjs.org/']
export function publishDistribution(packages, runNpm = npm) {
  assert.deepEqual(packages.map(pkg => pkg.name), [...targets.map(target => packageFor(target).name), manifest.name], 'Expected exactly six native packages followed by the parent')
  // Preflight every immutable version before the first write. A conflict or
  // registry outage on the last package must not partially publish the first six.
  const existing = new Set()
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
      existing.add(pkg.name)
    }
  }
  for (const pkg of packages) {
    if (existing.has(pkg.name)) {
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
  // Validate all seven checked artifacts and their source before registry writes.
  const source = sourceIdentity(root)
  const packages = [...targets.map(target => packageFor(target).name), manifest.name].map(name => readArtifact(name, source))
  publishDistribution(packages)
}
