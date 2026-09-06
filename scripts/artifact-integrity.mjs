import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

export const integrity = file => `sha512-${createHash('sha512').update(readFileSync(file)).digest('base64')}`

// Include dirty/new source files, not just HEAD: local rebuilds must also detect
// edits made after a binary was built. Ignored caches/artifacts are excluded.
export function sourceIdentity(root) {
  const git = args => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const commit = git(['rev-parse', 'HEAD']).trim()
  const files = [...new Set(git(['ls-files', '--cached', '--others', '--exclude-standard', '-z']).split('\0').filter(Boolean))].sort()
  const hash = createHash('sha256')
  for (const file of files) {
    const path = join(root, file)
    const bytes = existsSync(path) ? readFileSync(path) : Buffer.from('deleted')
    hash.update(`${file}\0${bytes.length}\0`).update(bytes)
  }
  return { commit, sourceDigest: `sha256:${hash.digest('hex')}` }
}

export function validateArtifact(record, expected, file) {
  assert.equal(record.schemaVersion, 1, 'Unknown npm artifact record format')
  for (const key of ['name', 'version', 'requiredPath']) assert.equal(record[key], expected[key], `Artifact ${key} mismatch; rebuild and repack`)
  assert.deepEqual(record.source, expected.source, 'Artifact source mismatch; rebuild and repack from this checkout')
  assert.equal(record.integrity, integrity(file), 'Artifact contents differ from the checked package; rebuild and repack')
  return { name: record.name, file, integrity: record.integrity }
}
