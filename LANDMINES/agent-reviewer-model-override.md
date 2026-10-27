# Reviewer model arguments can differ from the runtime model

**Applies when:** a review requires an exact provider/model and is launched through
an Agent specialization with a model override.

**Symptom:** the Agent call requests `devin/claude-opus-5.5`, but the independent
session's `model_change` and assistant message metadata record
`openai-codex/gpt-6.1-sol`. A useful review result is not proof that the required
model ran.

**Reproduced in:** OrderOffers' two reviewer sessions on 18 Oct 2026:
`01a14e9a-bfb4-713c-a4b2-e7be8dd5ba77` and
`01a14ea9-f1a5-713c-a4b2-e7c339e35e2c`. Both parent invocations specified the exact
Opus model. Their persisted records selected the reviewer specialization's
`openai-codex/gpt-6.1-sol` instead. The first issued design findings and the second
declined to claim the exact-model gate. The selection mechanism's cause is not
verified. Follow-up: https://github.com/AY2627S1-CS3219-P1/FoC/issues/160.

**Workaround:** do not infer review provenance from tool arguments, a prompt or a
reviewer's self-description. Run an explicit `pi --provider devin --model
claude-opus-5.5` invocation with read-only review instructions and tools, preserve
its session, and inspect the actual provider/model records and successful result.
An authentication/dispatch failure leaves the exact-model review gate blocked.
Do not silently substitute another model. Report and correct earlier inaccurate
review claims.

**Remove this entry when:** an installed tooling fix is verified to select the
exact requested provider/model through this Agent invocation path, or the path is
removed from project review use. Verify runtime metadata before closing #160.
