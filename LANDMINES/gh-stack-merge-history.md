# Stack rebases can replay integrated merge history

**Applies when:** `gh stack` v0.1.1 rebases an imported stack whose branches
contain merge commits and dependencies previously squash-merged into the trunk.

**Reproduced:** stack #193 on 21 October 2026. The bottom branch already contained
`main` at `581cf065`, but the default `gh stack rebase` replayed older reservation
and creation commits, producing conflicts in already-integrated Credit contracts.
After aborting and setting local `rebase.rebaseMerges=true`, the CLI instead
stopped with `could not determine the previous base of yihao/feat/order-cancellation`
and restored all branches. The imported metadata named the latest parent tip
`8da41163` as the cancellation base, but its actual shared ancestor was `59f8b4ea`.

**Workaround:** abort the CLI operation before retrying. Record the original tips
and actual merge bases. Keep a bottom branch that already contains the trunk;
rebase descendants in order with
`git rebase --rebase-merges --onto <updated-parent> <original-shared-base> <child>`.
Use the original shared base even after the parent has moved. Recreated merge
commits can lose their earlier manual resolutions, including in files Git merges
without conflicts. Compare the resulting tree against the original branch tip;
only intended changes inherited from the updated parent should appear. Restore
verified earlier resolutions where appropriate and run the relevant checks.
When a lower PR is squash-merged, GitHub can retarget the next PR to `main`.
Rebase that child using the original lower-branch tip as its old base, then
propagate the updated child through the remaining stack. In stack #193, merging
#192 and #134 moved `main` to `bab1b8b9` and conflicted #171 again. At a recreated
merge, `git merge-tree --write-tree --merge-base=<verified-old-parent>` against
the updated parent and original child supplied a clean combined tree that kept
the earlier resolutions. Check the exit status and resulting tree before using
it; do not accept a tree containing unresolved conflicts. Comparing every final
branch tree with that independently computed combination verified that only the
new parent changes were inherited.

Then `gh stack push` can publish the active branches with its per-branch leases.
This workaround needs no repository implementation change.

**Remove when:** the installed stack CLI preserves integrated merge history and
correctly finds actual parent bases for this imported-stack case without manual
rebases; verify both behavior and the resulting branch trees before removal.
