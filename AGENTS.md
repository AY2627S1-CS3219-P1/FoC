Read `CONVENTIONS.md` before changing or reviewing implementation. Explicitly raise to the user if user intent != conventions listed, and ask for user's decision until you get it. If user intent is the final decision and not a one-off exception, update conventions to match.

Read `LANDMINES.md` before starting a task. Follow each entry whose **Applies when** condition matches the task or environment. Remove landmines that no longer apply.

## Maintaining landmines

Add or update a landmine after reproducing a non-obvious failure that can affect future work. Do not add one-off command mistakes or unverified explanations. No need to add landmine if you have implemented a fix and confident the same class of problem or triggering agent behaviours won't occur again.

When a fix is required and not immediately implemented, create a GitHub issue and link it in the landmine. When the fix lands, verify the removal condition, update or remove the landmine, and close the issue.
