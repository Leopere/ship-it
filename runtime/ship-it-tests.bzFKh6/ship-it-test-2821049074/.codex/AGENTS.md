# Repository delivery

Use `$ship-it` for repository delivery behavior.

- Native lifecycle hooks run the no-argument `ship-it` binary. The binary selects the required work from the hook event and repository state. SessionStart and Stop are hook events, not command-line subcommands.
- Before the first delivery each local day, ship-it pulls the latest upstream code. A bare invocation stages all changes, creates an automatic commit when needed, and pushes in the same call. SessionStart only performs the daily pull. A tracked deployment command runs after a successful push.
- Keep Git delivery mechanical: `git add .`, an automatic commit when needed, and push everything Git stages. Do not assess content, select files, or add test, review, or conflict-assessment gates to this flow. The working agent owns diagnosis and repairs.
- Do not require a clean worktree before shipping or deployment. Native shipping stages all changes, commits when needed, and pushes everything Git stages without content selection or approval prompts. Deployment uses the exact pushed commit in a private snapshot; local worktree and index changes do not block it or enter the snapshot. Pending edits belong to the native shipping flow; they are not a deployment blocker.
- Tests provide advisory evidence, not an automatic deployment veto. Diagnose intentional changes, defects, faulty tests, and environment failures, then repair the relevant cause and continue. Verify the actual deployment independently.
- Agents finish their work and let the hooks run. Do not invoke ship-it, poll it, or add shipping prompts.
- Routine Git commands belong to ship-it. Agents may use Git only to diagnose or repair a ship-it defect, with regression coverage.
- For an authorized destination, finish the change and focused checks. Include deployment and live acceptance checks in the repository command. Fix recoverable failures within that scope. A failed attempt is not proof that the task is blocked.
- Report a blocker only with the attempted command, observed failure, and the specific missing access, information, or external change. Do not invent approval or policy requirements.
