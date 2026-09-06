import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, readFileSync, readdirSync, rmSync, statSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { createVerificationBundle, redactLog, validateAttempt, verifyVerificationBundle } from './verification-bundle.mjs'

const example = JSON.parse(readFileSync(new URL('../compatibility/attempt.example.json', import.meta.url)))
const cli = fileURLToPath(new URL('./verification-bundle.mjs', import.meta.url))
function fixture(t, changes = {}, log = 'Setup attempted; authentication unavailable.\n') {
  const root = mkdtempSync(join(tmpdir(), 'bus-verification-test-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const metadataPath = join(root, 'attempt.json'), logPath = join(root, 'private.log'), outDir = join(root, 'bundle')
  writeFileSync(metadataPath, JSON.stringify({ ...example, ...changes }))
  writeFileSync(logPath, log)
  return { root, metadataPath, logPath, outDir, env: {} }
}

test('redacts literal, escaped, encoded and labeled credentials plus private home paths', () => {
  const secret = 'private/+secret="quoted"'
  const input = [
    secret, encodeURIComponent(secret), JSON.stringify(secret),
    'Authorization: Bearer abc.def-123', 'Authorization: Basic YWJjZGVm',
    '{"agentToken":"json-secret", "scopeToken": "escaped\\\"secret"}',
    'OCTOBER_BUS_ADMIN_TOKEN="shell secret" TOKEN=bare-secret',
    "CUSTOM_API_KEY='api secret' password=pass-secret",
    'https://user:pass@example.test/?token=query-secret&ok=1',
    '/Users/example/project/file', '\u001b[31mBearer\u001b[0m ansi-secret\r\nnormal result: acknowledged=1'
  ].join('\n')
  const { text, rules } = redactLog(input, { secrets: [secret], homeDirectory: '/Users/example' })
  for (const value of [secret, encodeURIComponent(secret), 'abc.def-123', 'YWJjZGVm', 'json-secret', 'escaped', 'shell secret', 'bare-secret', 'api secret', 'pass-secret', 'user:pass', 'query-secret', '/Users/example', 'ansi-secret']) assert(!text.includes(value), value)
  assert(text.includes('normal result: acknowledged=1'))
  assert(text.includes('[HOME]/project/file'))
  assert.deepEqual(rules, ['authorization', 'credential-fields', 'explicit-secrets', 'home-directory', 'terminal-controls', 'url-credentials'])
})

test('creates private, unreviewed artifacts with a digest of the sanitized log only', t => {
  const f = fixture(t, {}, 'Bearer raw-secret\nKnown: runtime-secret\nSetup not run.\n')
  const before = readFileSync(f.logPath, 'utf8')
  const manifest = createVerificationBundle({ ...f, env: { OCTOBER_BUS_AGENT_TOKEN: 'runtime-secret' } })
  const log = readFileSync(join(f.outDir, 'run.log'))
  assert.equal(manifest.log.resultDigest, `sha256:${createHash('sha256').update(log).digest('hex')}`)
  assert.equal(manifest.verificationStatus, 'unreviewed')
  assert.equal(manifest.metadata.outcome, 'not-run')
  assert.equal(manifest.redaction.manualReviewRequired, true)
  assert.deepEqual(readdirSync(f.outDir).sort(), ['REVIEW.md', 'bundle.json', 'run.log'])
  assert(!log.includes('raw-secret') && !log.includes('runtime-secret'))
  assert.equal(readFileSync(f.logPath, 'utf8'), before, 'raw input must remain unchanged')
  assert.deepEqual(verifyVerificationBundle(f.outDir), manifest)
  if (process.platform !== 'win32') {
    assert.equal(statSync(f.outDir).mode & 0o777, 0o700)
    for (const name of readdirSync(f.outDir)) assert.equal(statSync(join(f.outDir, name)).mode & 0o777, 0o600)
  }
  assert.throws(() => createVerificationBundle(f), /EEXIST/)
  assert.equal(readFileSync(join(f.outDir, 'run.log'), 'utf8'), log.toString())
  writeFileSync(join(f.outDir, 'run.log'), 'edited')
  assert.throws(() => verifyVerificationBundle(f.outDir), /digest or size mismatch/)
})

test('outcome labels never promote an attempt to formal verified evidence', t => {
  for (const outcome of ['passed', 'failed', 'partial', 'not-run']) {
    const f = fixture(t, { outcome })
    const bundle = createVerificationBundle(f)
    assert.equal(bundle.metadata.outcome, outcome)
    assert.equal(bundle.verificationStatus, 'unreviewed')
    assert(!readdirSync(f.outDir).includes('evidence.json'))
  }
  for (const changes of [{ outcome: 'verified' }, { repositoryCommit: 'main' }, { profile: 'local-runtime' }, { attemptedAt: '2026-02-30T00:00:00Z' }, { limitations: 'none' }, { extra: 'unknown' }]) assert.throws(() => validateAttempt({ ...example, ...changes }))
})

test('fails closed on missing redaction secrets, unsafe metadata and malformed inputs', t => {
  const f = fixture(t)
  assert.throws(() => createVerificationBundle({ ...f, redactEnv: ['MISSING'] }), /nonempty environment variable/)
  writeFileSync(f.metadataPath, JSON.stringify({ ...example, limitations: ['private-value'] }))
  assert.throws(() => createVerificationBundle({ ...f, redactEnv: ['PRIVATE_VALUE'], env: { PRIVATE_VALUE: 'private-value' } }), /Metadata contains sensitive/)
  writeFileSync(f.metadataPath, '{"token":"do-not-print-this-secret", broken')
  const failed = spawnSync(process.execPath, [cli, '--metadata', f.metadataPath, '--log', f.logPath, '--out', f.outDir], { encoding: 'utf8' })
  assert.equal(failed.status, 1)
  assert(!failed.stderr.includes('do-not-print-this-secret'))
  writeFileSync(f.metadataPath, JSON.stringify(example))
  for (const log of [Buffer.alloc(0), Buffer.from('\u001b[31m\u001b[0m'), Buffer.from([0xff]), Buffer.alloc(16 * 1024 * 1024 + 1, 65)]) {
    writeFileSync(f.logPath, log)
    assert.throws(() => createVerificationBundle(f), /empty|UTF-8|size limit/)
  }
  writeFileSync(f.logPath, '!'.repeat(2 * 1024 * 1024))
  assert.throws(() => createVerificationBundle({ ...f, redactEnv: ['SHORT_SECRET'], env: { SHORT_SECRET: '!' } }), /Sanitized log exceeds/)
})

test('CLI supports creation, digest verification and strict option parsing', t => {
  const f = fixture(t)
  const args = ['--metadata', f.metadataPath, '--log', f.logPath, '--out', f.outDir]
  for (const extra of [['--unknown', 'x'], ['--log', f.logPath], ['--redact-env']]) {
    assert.equal(spawnSync(process.execPath, [cli, ...args, ...extra]).status, 1)
  }
  assert.equal(spawnSync(process.execPath, [cli, ...args]).status, 0)
  assert.equal(spawnSync(process.execPath, [cli, 'verify', f.outDir]).status, 0)
  assert.equal(spawnSync(process.execPath, [cli, '--help']).status, 0)
  const manifest = JSON.parse(readFileSync(join(f.outDir, 'bundle.json')))
  manifest.log.path = '../private.log'
  writeFileSync(join(f.outDir, 'bundle.json'), JSON.stringify(manifest))
  assert.throws(() => verifyVerificationBundle(f.outDir), /log path/)
})

test('does not follow symbolic-link inputs or bundle files', { skip: process.platform === 'win32' }, t => {
  const f = fixture(t)
  const link = join(f.root, 'linked.log')
  symlinkSync(f.logPath, link)
  assert.throws(() => createVerificationBundle({ ...f, logPath: link }), /regular files/)
  createVerificationBundle(f)
  const bundleLink = join(f.root, 'linked-bundle')
  symlinkSync(f.outDir, bundleLink)
  assert.throws(() => verifyVerificationBundle(bundleLink), /real directory/)
})
