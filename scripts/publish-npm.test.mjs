import assert from 'node:assert/strict'
import test from 'node:test'
import { distributionManifest, manifest, packageFor, targets } from './npm-distribution.mjs'
import { publishDistribution, validateChannel } from './publish-npm.mjs'

const packages = [...targets.map(target => packageFor(target).name), manifest.name].map(name => ({ name, file: `${name}.tgz`, integrity: `sha512-${name}` }))
const notFound = () => Object.assign(new Error('missing'), { stdout: JSON.stringify({ error: { code: 'E404' } }) })

test('stable candidate and promotion are explicit, never inferred from a prerelease', () => {
  for (const [version, channel] of [['1.0.0', 'candidate'], ['1.0.0', 'latest'], ['1.0.0-rc.1', 'next']]) validateChannel(version, channel)
  for (const [version, channel] of [['1.0.0', 'next'], ['1.0.0-rc.1', 'latest'], ['1.0.0-rc.1', 'candidate'], ['01.0.0', 'latest'], ['1.0.0', 'other']]) {
    assert.throws(() => validateChannel(version, channel))
  }
})

test('stable promotion preflights all artifacts and promotes parent last; retries are safe', () => {
  for (const missing of [true, false]) {
    const writes = []
    const run = () => publishDistribution(packages, args => {
      if (args[0] === 'view') {
        const pkg = packages.find(pkg => args[1] === `${pkg.name}@1.0.0`)
        if (missing && pkg.name === manifest.name) throw notFound()
        return JSON.stringify(pkg.integrity)
      }
      assert.deepEqual(args.slice(0, 2), ['dist-tag', 'add'])
      assert.equal(args[3], 'latest')
      writes.push(args[2])
      return ''
    }, { version: '1.0.0', channel: 'latest' })
    if (missing) {
      assert.throws(run, /publish candidate first/)
      assert.deepEqual(writes, [])
    } else {
      run()
      run()
      assert.deepEqual(writes, [...packages, ...packages].map(pkg => `${pkg.name}@1.0.0`))
    }
  }
})

test('stable candidate publishes without assigning latest', () => {
  const present = new Set()
  publishDistribution(packages, args => {
    if (args[0] === 'view') {
      const pkg = packages.find(pkg => args[1] === `${pkg.name}@1.0.0`)
      if (!present.has(pkg.name)) throw notFound()
      return JSON.stringify(pkg.integrity)
    }
    assert.equal(args[0], 'publish')
    assert.equal(args[args.indexOf('--tag') + 1], 'candidate')
    present.add(packages.find(pkg => pkg.file === args[1]).name)
    return ''
  }, { version: '1.0.0', channel: 'candidate' })
  assert.equal(present.size, 7)
})

test('packed SDK pins every native optional package to the parent version', () => {
  assert.deepEqual(distributionManifest().optionalDependencies, Object.fromEntries(packages.slice(0, -1).map(pkg => [pkg.name, manifest.version])))
  assert.equal(distributionManifest().scripts, undefined)
  assert.equal(distributionManifest().devDependencies, undefined)
})

test('a conflict or outage on the final preflight performs no publishes', () => {
  for (const failure of ['conflict', 'outage']) {
    assert.throws(() => publishDistribution(packages, args => {
      assert.equal(args[0], 'view', 'preflight must finish before publishing anything')
      if (args[1] !== `${manifest.name}@${manifest.version}`) throw notFound()
      if (failure === 'outage') throw new Error('registry unavailable')
      return JSON.stringify('different')
    }), failure === 'outage' ? /registry unavailable/ : /different contents/)
  }
  assert.throws(() => publishDistribution(packages.slice(1), () => assert.fail('no registry request expected')), /exactly six/)
  for (const value of ['null', '""', '{}']) assert.throws(() => publishDistribution(packages, args => {
    assert.equal(args[0], 'view')
    return value
  }), /invalid artifact integrity/)
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
