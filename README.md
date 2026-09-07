# ship-it

`ship-it` is one no-argument command for routine Git delivery.

Before the first delivery each local day, it runs `git pull --autostash`. Every bare invocation then runs `git add .`, creates an automatic commit when changes are staged, and runs `git push` in the same call. Scripts and agents do not select a lifecycle phase.

Native Codex and Cursor lifecycle hooks invoke the same binary. Session start ensures the daily pull has happened. Stop ships the completed coding cycle. After a successful push, a tracked `.deploy-it.json` triggers `deploy-it` for the repository’s declared destination. Repositories without a deployment contract still ship normally. Git delivery requires no verification contract, tags, branch switches, wrapper scripts, or GitHub Actions policy.

Run `ship-it install` once to install the binary, skill, and lifecycle hooks. Routine use requires no arguments or agent involvement.

The hooks use the repository paths in their event payloads. Hook stdout stays valid JSON. Each run saves command output as it arrives in a private log under `~/.local/share/ship-it/hooks/`, with an explicit completion or failure record. Failed synchronous hooks also report their diagnostics on stderr. The Codex Stop timeout allows ten minutes for Git delivery and the bounded deployment command.

Existing tabs can retain the old `ship-it hook` or `ship-it cursor-hook` Stop handler with a five-second timeout. Those legacy handlers start the same no-argument binary in a separate process session and return the result-log path immediately. The log records the eventual outcome; the queued message does not claim that delivery succeeded. New sessions continue to use the installed no-argument hooks.
