import test from 'node:test'
import assert from 'node:assert/strict'
import { approvedForRelease } from './release-policy.mjs'

const pr = { merged_at: '2026-09-05', base: { ref: 'main' }, merge_commit_sha: 'release', head: { sha: 'head' }, user: { login: 'author' } }
const review = { id: 1, state: 'APPROVED', commit_id: 'head', user: { login: 'reviewer', type: 'User' } }
test('requires a merged main PR and independent current-head approval', () => {
  assert.equal(approvedForRelease(pr, [review], 'release'), true)
  for (const candidate of [
    { ...pr, merged_at: null },
    { ...pr, base: { ref: 'feature' } },
    { ...pr, merge_commit_sha: 'elsewhere' }
  ]) assert.equal(approvedForRelease(candidate, [review], 'release'), false)
  for (const candidate of [
    { ...review, commit_id: 'old-head' },
    { ...review, user: { login: 'author', type: 'User' } },
    { ...review, user: { login: 'bot', type: 'Bot' } },
    { ...review, state: 'DISMISSED' }
  ]) assert.equal(approvedForRelease(pr, [candidate], 'release'), false)
  assert.equal(approvedForRelease(pr, [], 'release'), false)
})
test('a subsequent dismissal or change request revokes approval; comments do not', () => {
  for (const state of ['DISMISSED', 'CHANGES_REQUESTED']) {
    assert.equal(approvedForRelease(pr, [review, { ...review, id: 2, state }], 'release'), false)
  }
  assert.equal(approvedForRelease(pr, [review, { ...review, id: 2, state: 'COMMENTED' }], 'release'), true)
})
