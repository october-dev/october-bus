import assert from 'node:assert/strict'
import test from 'node:test'
import { checkCompatibility } from './check-compatibility.mjs'

const registry = { schemaVersion: 1, verified: ['evidence/test.json'] }
const adapter = { id: 'test', adapterVersion: '1', harnessFamily: 'Test', status: 'verified', testedVersions: ['1'], platforms: ['linux'], protocolVersions: ['0.1'] }
const record = { adapterId: 'test', adapterVersion: '1', harnessFamily: 'Test', harnessVersion: '1', operatingSystem: 'linux', architecture: 'amd64', protocolVersion: '0.1', runtimeVersion: '0.1.0-rc.4', result: 'passed', profile: 'mcp-adapter', repositoryCommit: 'a'.repeat(40), resultDigest: `sha256:${'b'.repeat(64)}`, verifiedAt: '2026-09-01T00:00:00Z' }
const options = { now: Date.parse('2026-09-06T00:00:00Z') }
test('release metadata requires mode/model and treats those as distinct combinations', () => {
  const current = { ...record, attestation: 'https://example.test/artifact', launchMode: 'config-only', model: 'provider/model' }
  const release = { ...options, runtimeVersion: record.runtimeVersion, requireAttestation: true }
  assert.equal(checkCompatibility(registry, [adapter], () => current, release).verifiedHarnesses, 1)
  assert.throws(() => checkCompatibility(registry, [adapter], () => current, { ...release, sourceCommit: 'c'.repeat(40) }), /candidate source commit/)
  for (const field of ['launchMode', 'model']) {
    for (const value of [undefined, '', 'x\ny', 'x'.repeat(257)]) assert.throws(() => checkCompatibility(registry, [adapter], () => ({ ...current, [field]: value }), release))
  }
  const matrix = { ...registry, verified: ['evidence/one.json', 'evidence/two.json'] }
  assert.equal(checkCompatibility(matrix, [adapter], path => ({ ...current, model: path }), release).verifiedCombinations, 2)
  assert.throws(() => checkCompatibility(matrix, [adapter], () => current, release), /duplicate/)
  assert.throws(() => checkCompatibility(registry, [adapter], () => current, { ...release, requiredAdapters: ['missing-host'] }), /requires missing-host/)
})
test('cross-checks evidence, exact manifest versions, freshness and release scope', () => {
  assert.equal(checkCompatibility(registry, [adapter], () => record, options).verifiedHarnesses, 1)
  for (const change of [{ result: 'failed' }, { runtimeVersion: 'dev' }, { adapterVersion: 'old' }, { harnessVersion: 'unknown' }, { verifiedAt: '2020-01-01' }, { verifiedAt: '2099-01-01' }, { profile: 'local-runtime' }]) {
    assert.throws(() => checkCompatibility(registry, [adapter], () => ({ ...record, ...change }), options))
  }
  assert.throws(() => checkCompatibility(registry, [{ ...adapter, status: 'experimental' }], () => record, options))
  assert.throws(() => checkCompatibility(registry, [adapter], () => record, { ...options, runtimeVersion: '0.1.0-next.14' }))
  assert.throws(() => checkCompatibility(registry, [adapter], () => record, { ...options, requireAttestation: true }))
  assert.throws(() => checkCompatibility({ ...registry, verified: ['../secret.json'] }, [adapter], () => assert.fail('must not read external paths'), options))
  assert.throws(() => checkCompatibility({ ...registry, verified: [] }, [adapter], () => record, options))
})
