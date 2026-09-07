---
name: ship-it
description: Use the no-argument ship-it binary for automatic daily pull and end-of-cycle Git delivery through native lifecycle hooks.
---

Repository delivery uses only `ship-it`, with no arguments. The binary selects the required work from the hook event and repository state. SessionStart and Stop are hook events, not command-line subcommands. Scripts must not select a lifecycle phase.

Native SessionStart and Stop hooks run the same no-argument `ship-it` binary. The first invocation for a repository each local day pulls the latest upstream code. A bare invocation completes delivery after any required daily pull, including the first call of the day. A SessionStart only ensures that daily pull. A completed Stop stages the whole repository with `git add .`, creates an automatic commit when changes are staged, and pushes the current branch with `git push`.

Agents implement and review the requested work, then finish. Do not invoke ship-it for routine delivery, add shipping prompts, or poll it. If a delivery failure is reported, diagnose and repair its cause within the authorized task. Routine Git commands belong to ship-it. An agent may use Git only to diagnose or repair a ship-it defect and must add regression coverage for the repair.

After a successful push, ship-it invokes `deploy-it` when the pushed revision contains `.deploy-it.json`. The repository command owns the destination and live acceptance check. Repositories without this file still ship normally. If production delivery is requested, finish the repository deployment command before ending the turn. Use existing project configuration and the authorized destination to prepare that command.

Keep the Git flow small. Do not add verification gates, tags, branch switching, wrapper scripts, pull requests, or workflow policies to ship-it. The commit history is the delivery record. A push proves Git delivery; a successful destination check proves deployment.

A failed attempt is a diagnosis step. Fix recoverable code, configuration, build, and command failures within the authorized scope. After diagnosis, rerun the affected deployment when its effects are understood. Do not invent a blocker from missing evidence, stale instructions, or a previous failed attempt. If progress requires external input, identify the attempted command, observed failure, and exact missing access or information. Preserve real permission boundaries and do not claim deployment before observing its result.
