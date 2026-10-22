export default {
  extends: ['@commitlint/config-conventional'],
  // Dependabot's commit bodies carry long release-note lines.
  rules: { 'body-max-line-length': [0] },
}
