# R0. Decisions reserved for the owners

These are not decided anywhere in this repository. Each one changes something
downstream, noted so the choice can be made once and early.

| # | decision | what it affects | recommendation, if any |
|---|---|---|---|
| 1 | Repo identity: keep `plsgiveup/fibre` with Huginn attribution in the README, or transfer to a Huginn org | how the FDP application cites the work; who is listed as owner on GitHub; whether commit history shows the team | none; note that `s0urledd/fibre-v` (this repository) is MIT-licensed while `plsgiveup/fibre` is Apache-2.0 with a NOTICE file, so code cannot be copied between them without re-licensing or a notice. Documents in this repo carry no licence conflict because they are new work |
| 2 | Product name and public domain | dashboard title, API base URL, forum post title, README | pick before the collector starts writing data so the vantage name recorded on every measurement (`-vantage`) matches the public name |
| 3 | Attribution split between Huginn Tech and Utku in the application text | application wording, README credits | none |
| 4 | Whether Huginn's own mocha-5 validator (moniker Huginn) is excluded from headline network stats | network overview numbers, appearance of self-grading | include it, label it "operated by the observer's authors" on the validator page and in the report methodology, and make the exclusion a one-line query filter so a reader can recompute without it |
| 5 | Where the research documents live long-term | link targets in the application and forum posts | move `docs/` from this repository into `plsgiveup/fibre/docs/` as-is once decision 1 is made; the files use only relative links inside `docs/` and absolute links elsewhere so they move cleanly |
| 6 | Whether to bump the celestia-app pin before or at activation | fibre-assign differential test, sentinel build, what "verified against the pinned build" means in the report | see the recommendation at the end of `R1-fibre-protocol-surface.md` |
| 7 | Whether to publish the probe policy on the forum before activation | operator goodwill, first-mover claim, risk of being told to change it | publish; see `R4-probe-etiquette.md` |
