# Concurrent agents in one worktree destroy each other's uncommitted work

**Applies when:** multiple subagents edit the same worktree concurrently, or an
agent runs git state operations (`git stash`, `git checkout`, `git restore`,
`git reset`) while other agents hold uncommitted changes.

**Symptom:** an agent reports that tracked working-tree edits from another
agent (or its own predecessor) vanished mid-task. Reproduced twice in the
user-deletion work (8 Nov 2026): a failed `git stash pop` + `git stash drop`
destroyed tracked order-service edits, and a mid-task reset wiped in-progress
`user-service/internal/service/auth.go` work. Both were detected only because
builds failed and had to be reapplied by hand.

**How to proceed:**

- Partition work by directory and give each agent an explicit file-ownership
  list; cross-agent needs go through the coordinator, never direct edits.
- Instruct every agent: never run `git stash`, `git checkout <path>`,
  `git restore`, `git reset`, or any command that rewrites the index or
  working tree outside its own untracked files. Agents do not commit; the
  coordinator does.
- The coordinator commits safety checkpoints at meaningful milestones
  (e.g. `wip(...)` commits) so a destroyed tree can be recovered from git
  instead of memory.
- After any report of lost edits, treat the affected files as untrusted: diff
  against the last checkpoint commit and re-run the owning module's build and
  tests before proceeding.
- An agent that notices vanishing edits should stop and report instead of
  "reapplying" silently — silent reapplication is how half-restored states
  survive into review.

**Remove this entry when:** agents work in isolated worktrees/worktrees per
agent, or a harness guarantee makes concurrent same-worktree edits safe.
