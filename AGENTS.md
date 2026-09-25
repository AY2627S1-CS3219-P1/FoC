Read `CONVENTIONS.md` before changing or reviewing implementation. If the user's request conflicts with a convention, explain the conflict and ask which to follow. If the decision changes the convention rather than making a one-time exception, update `CONVENTIONS.md`.

Read `LANDMINES/README.md` before starting a task. Follow each linked entry whose **Applies when** condition matches the task or environment. Remove landmines that no longer apply.

## Maintaining landmines

Add or update a detail file in `LANDMINES/` after reproducing a non-obvious failure that can affect future work, and add it to the index in `LANDMINES/README.md`. Do not add one-off command mistakes or unverified explanations. Do not add a landmine if you fixed the cause and the same failure or triggering agent behavior cannot recur.

When a fix is required and not immediately implemented, create a GitHub issue and link it in the landmine. When the fix lands, verify the removal condition, update or remove the landmine, and close the issue.
