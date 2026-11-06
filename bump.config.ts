import { execFileSync } from 'node:child_process'

// Bumps mygo.json, writes the version's CHANGELOG.md section, commits, tags
// v<version> and pushes; the tag triggers .github/workflows/release.yml,
// which uses that section as the release notes. Run it with `make release`.
export default {
  files: ['mygo.json'],
  commit: 'chore: release {tag}',
  tag: 'v{version}',
  push: true,
  // Refuse to release from a dirty working tree.
  noGitCheck: false,
  // Commit only the version file and the changelog generated below.
  all: false,
  execute(op) {
    execFileSync(
      'bunx',
      ['changelogen@0.6.2', '--output', 'CHANGELOG.md', '--noAuthors', '-r', op.state.newVersion],
      { stdio: 'inherit' },
    )
    execFileSync('git', ['add', '--', 'CHANGELOG.md'], { stdio: 'inherit' })
    op.update({ updatedFiles: [...new Set([...op.state.updatedFiles, 'CHANGELOG.md'])] })
  },
}
