import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { appendFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

export function validateCandidateRun(run, repository) {
  assert.equal(run.head_repository?.full_name, repository, 'Candidate must come from this repository')
  assert.equal(run.head_branch, 'main', 'Candidate must be built on main')
  assert.equal(run.path, '.github/workflows/publish-npm-prerelease.yml', 'Unexpected candidate workflow')
  assert.equal(run.event, 'workflow_dispatch', 'Candidate must be deliberately dispatched')
  assert.equal(run.status, 'completed')
  assert.equal(run.conclusion, 'success', 'Candidate checks/publication must have succeeded')
  assert.match(run.head_sha, /^[a-f0-9]{40}$/)
  return run.head_sha
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const repository = process.env.GITHUB_REPOSITORY
  const id = process.env.OCTOBER_BUS_CANDIDATE_RUN_ID
  assert.match(repository ?? '', /^[\w.-]+\/[\w.-]+$/)
  assert.match(id ?? '', /^[1-9][0-9]*$/, 'Supply the successful candidate workflow run ID')
  const run = JSON.parse(execFileSync('gh', ['api', `repos/${repository}/actions/runs/${id}`], { encoding: 'utf8' }))
  const sha = validateCandidateRun(run, repository)
  assert.ok(process.env.GITHUB_OUTPUT)
  appendFileSync(process.env.GITHUB_OUTPUT, `candidate_sha=${sha}\n`)
}
