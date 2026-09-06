import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export function checkCompatibility(registry, adapters, readEvidence, { now = Date.now(), runtimeVersion, requireAttestation = false } = {}) {
  assert.equal(registry.schemaVersion, 1)
  assert.ok(Array.isArray(registry.verified))
  const manifests = new Map(adapters.map(adapter => [adapter.id, adapter]))
  assert.equal(manifests.size, adapters.length, 'Duplicate adapter IDs')
  const active = new Set()
  const combinations = new Set()
  const warnings = []
  for (const path of registry.verified) {
    assert.match(path, /^evidence\/[A-Za-z0-9][A-Za-z0-9._-]*\.json$/, 'Registry entries must be evidence files, not external paths')
    const record = readEvidence(path)
    const adapter = manifests.get(record.adapterId)
    assert.ok(adapter, `No manifest for ${record.adapterId}`)
    assert.equal(adapter.status, 'verified', `${path}: experimental adapters cannot be in the verified registry`)
    assert.equal(record.result, 'passed', `${path}: failed run listed as verified`)
    assert.equal(record.adapterVersion, adapter.adapterVersion, `${path}: stale adapter version`)
    assert.equal(record.harnessFamily, adapter.harnessFamily)
    assert.ok(adapter.testedVersions.includes(record.harnessVersion), `${path}: untested harness version`)
    assert.ok(adapter.protocolVersions.includes(record.protocolVersion), `${path}: unsupported protocol`)
    assert.ok(adapter.platforms.includes(record.operatingSystem), `${path}: unsupported platform`)
    assert.ok(['mcp-adapter', 'native-adapter'].includes(record.profile), `${path}: runtime-only checks cannot verify a harness`)
    assert.match(record.runtimeVersion, /^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/, `${path}: name a released runtime, not dev`)
    assert.match(record.resultDigest, /^sha256:[a-f0-9]{64}$/)
    assert.match(record.repositoryCommit, /^[a-f0-9]{40}$/)
    const verified = Date.parse(record.verifiedAt)
    assert.ok(Number.isFinite(verified) && verified <= now && now - verified < 90 * 86400000, `${path}: future or expired evidence`)
    if (runtimeVersion) assert.equal(record.runtimeVersion.replace(/^v/, ''), runtimeVersion.replace(/^v/, ''), `${path}: evidence is not for the release candidate`)
    if (record.attestation) {
      const url = new URL(record.attestation)
      assert.ok(url.protocol === 'https:' && !url.username && !url.password, `${path}: use an HTTPS artifact link without credentials`)
    } else {
      assert.ok(!requireAttestation, `${path}: public reproducible artifact link required`)
      warnings.push(`${path}: no public artifact link recorded; independent run-log review remains required`)
    }
    const key = [record.adapterId, record.harnessVersion, record.runtimeVersion, record.protocolVersion, record.operatingSystem, record.architecture].join('/')
    assert.ok(!combinations.has(key), `${path}: duplicate verification combination`)
    combinations.add(key)
    active.add(adapter.id)
  }
  for (const adapter of adapters) {
    assert.ok(adapter.status !== 'verified' || active.has(adapter.id), `${adapter.id}: verified manifest has no active evidence`)
  }
  if (runtimeVersion) assert.ok(active.size > 0, 'Release verification requires at least one named harness')
  return { verifiedHarnesses: active.size, verifiedCombinations: combinations.size, warnings }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2)
  const requireAttestation = args.includes('--require-attestation')
  const positional = args.filter(arg => arg !== '--require-attestation')
  assert.ok(positional.length === 0 || (positional.length === 2 && positional[0] === '--runtime'), 'Expected [--runtime VERSION] [--require-attestation]')
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
  const read = path => JSON.parse(readFileSync(path, 'utf8'))
  const adapters = readdirSync(join(root, 'adapters'), { withFileTypes: true }).filter(entry => entry.isDirectory()).map(entry => read(join(root, 'adapters', entry.name, 'adapter.json')))
  const result = checkCompatibility(read(join(root, 'compatibility/registry.json')), adapters,
    path => read(join(root, 'compatibility', path)), { runtimeVersion: positional[1], requireAttestation })
  console.log(JSON.stringify(result, null, 2))
  console.log('Metadata consistency only: this does not execute or certify a named harness.')
}
