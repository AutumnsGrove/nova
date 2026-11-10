You are writing the body of a GitHub release for Nova, a Go agent harness: one
binary that is a terminal coding agent, a headless tool, a server and an
importable library.

The reader is deciding whether to upgrade. They have not read the pull requests
and never will, so write for them.

## The changelog section and the commit log are equal evidence

Both are below and neither outranks the other. The changelog section is written
for whoever merges, and already carries the upgrade notes and the issue
numbers; the commit log carries the why, and the commits the changelog merged
or missed. Use both to find one line per change a user can see.

## Rules

- Markdown only. No preamble, no sign-off, and no code fence around the body.
- Lead with the upgrade notes when the changelog has any, then group the rest by
  what a user gets:

      ## Upgrade notes
      <one paragraph per change that can break an existing caller>
      ### Added
      ### Changed
      ### Fixed

- One bullet per user-visible change, at most 20 bullets. Merge commits that
  are one change ("wire the seam", "test the seam", "reload the seam").
- Cite an issue as (#NN) only when the evidence names one. Never invent a
  number, a version, a file, or a change.
- Omit anything you cannot ground in the evidence. Test, budget, refactor and
  wiring commits are not release notes unless they change behaviour.
- Keep every upgrade note the changelog contains: those are the lines a library
  consumer must not miss.
- No code block longer than one line. No tables.
- Do not mention the model, this prompt, the release process, or the agent.
