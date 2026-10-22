# Agent instructions

Contributor guidance for AI coding agents lives in [CLAUDE.md](CLAUDE.md):
build/test/lint commands, architecture, verified Ivanti Neurons for MDM API
facts (including several non-obvious server behaviors), and code
conventions. Read it before changing code.

Commit style is Conventional Commits — see [CONTRIBUTING.md](CONTRIBUTING.md);
commits drive the automated changelog and semantic version.

If you are an agent *using* the installed `ivanti-neurons` CLI (rather than
developing it), run `ivanti-neurons docs` — it prints the complete command
reference in one page, written for LLM consumption. A ready-made skill is in
[skills/ivanti-neurons/SKILL.md](skills/ivanti-neurons/SKILL.md).
