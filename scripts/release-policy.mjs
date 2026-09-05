import { execFileSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

export function approvedForRelease(pr, reviews, sha) {
  if (!pr.merged_at || pr.base?.ref !== 'main' || pr.merge_commit_sha !== sha) return false
  const latest = new Map()
  for (const review of [...reviews].sort((a, b) => a.id - b.id)) {
    // Comments do not dismiss an approval; explicit review states do.
    if (review.state !== 'COMMENTED') latest.set(review.user?.login, review)
  }
  return [...latest.values()].some((review) =>
    review.state === 'APPROVED' &&
    review.commit_id === pr.head?.sha &&
    review.user?.type === 'User' &&
    review.user.login !== pr.user?.login
  )
}

function verifyRelease() {
  const repository = process.env.GITHUB_REPOSITORY
  const sha = process.env.GITHUB_SHA
  if (!/^[\w.-]+\/[\w.-]+$/.test(repository ?? '') || !/^[a-f0-9]{40}$/.test(sha ?? '')) {
    throw new Error('Expected repository and full commit SHA from GitHub Actions')
  }
  execFileSync('git', ['fetch', 'origin', 'main'], { stdio: 'inherit' })
  execFileSync('git', ['merge-base', '--is-ancestor', sha, 'origin/main'], { stdio: 'inherit' })
  const get = (path) => JSON.parse(execFileSync('gh', ['api', path], { encoding: 'utf8' }))
  const pulls = get(`repos/${repository}/commits/${sha}/pulls?per_page=100`)
  for (const candidate of pulls) {
    const pr = get(`repos/${repository}/pulls/${candidate.number}`)
    if (pr.merge_commit_sha !== sha) continue
    const pages = JSON.parse(execFileSync('gh', ['api', '--paginate', '--slurp', `repos/${repository}/pulls/${candidate.number}/reviews?per_page=100`], { encoding: 'utf8' }))
    if (approvedForRelease(pr, pages.flat(), sha)) return
  }
  throw new Error('Release must tag a merged main PR with an independent approval of its final head')
}

if (process.argv[1] === fileURLToPath(import.meta.url)) verifyRelease()
