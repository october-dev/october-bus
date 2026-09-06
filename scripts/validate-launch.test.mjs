import assert from 'node:assert/strict'
import test from 'node:test'
import { validationPlan, runValidation } from './validate-launch.mjs'

test('combined validation includes install and integration but never installs tools or publishes', () => {
  const plan = validationPlan({ allPlatforms: true, upgrade: true })
  assert.ok(plan.some(step => step.args.includes('test:distribution')))
  assert.ok(plan.some(step => step.args.includes('test:integration')))
  assert.ok(plan.some(step => step.args.includes('scripts/rehearse-upgrade.mjs')))
  assert.ok(plan.every(step => !step.args.some(arg => ['install', 'ci', 'publish', 'publish:distribution'].includes(arg))))
  assert.ok(!validationPlan().some(step => step.args.includes('scripts/rehearse-upgrade.mjs')))
})

test('failed stages return failure and do not pretend later checks passed', t => {
  t.mock.method(console, 'log', () => {})
  t.mock.method(console, 'error', () => {})
  let calls = 0
  const plan = [{ label: 'first', command: 'fake', args: [] }, { label: 'later', command: 'fake', args: [] }]
  assert.equal(runValidation(plan, () => { calls++; throw new Error('failed') }), 1)
  assert.equal(calls, 1)
  assert.equal(runValidation(plan, () => {}), 0)
})
