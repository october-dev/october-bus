import assert from 'node:assert/strict'
import test from 'node:test'
import { distributionManifest, manifest, packageFor, targets } from './npm-distribution.mjs'
import { publishDistribution } from './publish-npm.mjs'

const packages = [...targets.map(target => packageFor(target).name), manifest.name].map(name => ({ name, file: `${name}.tgz`, integrity: `sha512-${name}` }))
const notFound = () => Object.assign(new Error('missing'), { stdout: JSON.stringify({ error: { code: 'E404' } }) })

test('packed SDK pins every native optional package to the parent version', () => {
  assert.deepEqual(distributionManifest().optionalDependencies, Object.fromEntries(packages.slice(0, -1).map(pkg => [pkg.name, manifest.version])))
})

test('publishes all native packages before the parent, with provenance and exact integrity', () => {
  const published = []
  publishDistribution(packages, args => {
    const pkg = packages.find(pkg => args[1] === pkg.file || args[1] === `${pkg.name}@${manifest.version}`)
    assert.ok(pkg)
    if (args[0] === 'view') {
      if (!published.includes(pkg.name)) throw notFound()
      return JSON.stringify(pkg.integrity)
    }
    assert.ok(args.includes('--provenance') && args.includes('--ignore-scripts'))
    assert.equal(args[args.indexOf('--tag') + 1], 'next')
    published.push(pkg.name)
    return ''
  })
  assert.deepEqual(published, packages.map(pkg => pkg.name))
})

test('a native publish failure prevents the parent package from being published', () => {
  const attempts = []
  assert.throws(() => publishDistribution(packages, args => {
    if (args[0] === 'view') throw notFound()
    attempts.push(args[1])
    throw new Error('no publish permission')
  }), /no publish permission/)
  assert.deepEqual(attempts, [packages[0].file])
})

test('reruns skip identical artifacts but reject different contents and network/auth failures', t => {
  t.mock.method(console, 'log', () => {})
  publishDistribution(packages, args => {
    assert.equal(args[0], 'view')
    return JSON.stringify(packages.find(pkg => args[1] === `${pkg.name}@${manifest.version}`).integrity)
  })
  assert.throws(() => publishDistribution(packages, () => JSON.stringify('different')), /Refusing to reuse/)
  assert.throws(() => publishDistribution(packages, () => { throw new Error('offline') }), /offline/)
})
