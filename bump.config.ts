// Bumps mygo.json, commits, tags v<version> and pushes; the tag triggers
// .github/workflows/release.yml. Run it with `make release`.
export default {
  files: ['mygo.json'],
  commit: 'chore: release {tag}',
  tag: 'v{version}',
  push: true,
  // Refuse to release from a dirty working tree.
  noGitCheck: false,
}