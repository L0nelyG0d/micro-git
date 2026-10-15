# Open questions for the user

Decisions that need the user and were not made while they were away. Work continues around them with the simplest reversible default, noted per item. The user will ask about these on return; answer from this file.

Format: `- [ ] (#issue) question - default used - date`

- [ ] (#3/#9) Should `refs(repo_id, oid)` get a foreign key to `objects`? Data-model change. Default: no FK, integrity enforced in the app (#9). - 2026-10-15
- [ ] (#18/#22) How is `/api` served outside the Vite dev proxy (deployment)? Stack decision. Default: local-only, documented in the README; tracked in #22. - 2026-10-15
- [ ] (#12) A commit that only adds or removes a final newline: `modified` with no hunks acceptable? Default: yes. - 2026-10-15
- [ ] (#15/#19/#20) Branch create and repo delete UI placed inside #15; branch delete UI deferred to #20; #19 overlaps #15. Default: as groomed. - 2026-10-15
- [ ] (#21) Stretch features (merge, compare, README render, rename) have an issue but are not part of the 18 tasks. Default: skipped unless time remains. - 2026-10-15
