# ship-it

`ship-it` is the deliberately boring path from a local working tree to the remote-default branch: fetch, stage everything, commit, merge, calendar-tag, push, and hand the shipped revision to `deploy-it`. It does not invent project gates or create pull requests.

Build/test/review may run first, but a repository work cycle is not complete until `ship-it` stages every change, commits, pushes, and performs its `deploy-it` handoff. Production safety remains the hard gate before shipping.

Shipping fails before any repository mutation unless every ordinary GitHub Actions job explicitly includes `self-hosted` in `runs-on`. This rule blocks public, custom, and dynamically selected runners unless the workflow proves that they use self-hosted infrastructure. Reusable-workflow jobs that use `uses` don't select a runner and remain valid.

Install `deploy-it` first. Run `ship-it start` before editing and `ship-it` afterward. An optional positional argument supplies the commit message; otherwise one is generated from the changed paths. Every successful shipping resolution invokes the installed `deploy-it` binary with the exact commit, remote, branch, and tag, including when the revision was already shipped. Repositories without `.deploy-it.json` in that commit skip deployment, while deployable repositories require explicit local trust. Run `ship-it install` to place the binary in `~/.local/bin`, install its embedded skill for Codex, Claude, and Cursor, and register Cursor `sessionStart` / `stop` wrap hooks so Agent prompts finalize with `ship-it`.

Migration steering activates when local or tracked references point to `git.nixc.us` or `woodpecker.nixc.us`: it moves source to private GitHub repositories, images to private GHCR packages, and CI to self-hosted local-runner labels. If GitHub Actions stalls or is insufficient, diagnose and fix `~/dev/gh-runner` first rather than switching to GitHub-hosted runners. Before any external change, confirm the exact targets and require visible acceptance results.
