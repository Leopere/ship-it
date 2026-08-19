# ship-it

`ship-it` is the deliberately boring path from a local working tree to the remote-default branch: fetch, stage everything, commit, merge, calendar-tag, push, and hand the shipped revision to `deploy-it`. It does not invent project gates or create pull requests.

Build/test/review may run first, but a repository work cycle is not complete until `ship-it` stages every change, commits, pushes, and performs its `deploy-it` handoff. Production safety remains the hard gate before shipping.

Install `deploy-it` first. Run `ship-it start` before editing and `ship-it` afterward. An optional positional argument supplies the commit message; otherwise one is generated from the changed paths. Every successful shipping resolution invokes the installed `deploy-it` binary with the exact commit, remote, branch, and tag, including when the revision was already shipped. Repositories without `.deploy-it.json` in that commit skip deployment, while deployable repositories require explicit local trust. Run `ship-it install` to place the binary in `~/.local/bin`, install its embedded skill for Codex, Claude, and Cursor, and register Cursor `sessionStart` / `stop` wrap hooks so Agent prompts finalize with `ship-it`.

The installed skill’s migration steering for `git.nixc.us` is intentionally narrow: it activates only when the repository already contains a `git.nixc.us` remote or registry reference. When triggered, it directs targets to private GitHub repositories and container images to `ghcr.io`, and it should only proceed after confirming external migration targets and production acceptance signals are visible. When work is safe to deploy, ship-it should be used immediately and will stage all repository changes before commit.
