# UBI-175 Phase 4: AWS redirect-diff report

Numbers only. No regeneration, no renaming. Real, current data: 1,684 live AWS resource-reference pages (`hashicorp/aws`-sourced) against 1,437 real resource types in `ubiquex/sdk/providers/descriptions/aws.json` (CFN registry-sourced). Every bucket below verified with zero classification collisions on either side (checked explicitly, not assumed).

## The four buckets

| Bucket | Count | What it means |
|---|---:|---|
| **CLEAN** | 352 | Exact name match. Same real resource, same real name on both sides. A regeneration replaces these pages in place, no redirect needed. |
| **PROBABLE** (mechanical rename) | 334 | Same real resource, different naming convention only -- CFN splits multi-word service names on every capital (`AccessAnalyzer` -> `access_analyzer`), Terraform's own provider does not (`accessanalyzer`). Unambiguous: stripping underscores from both sides produces exactly one match, no collisions. A regeneration replaces these pages too, at a NEW path, and needs a real redirect rule from the old path to the new one -- the identical mechanism this session already used for GCP's own singularize-bug corrections and Azure's own 3 renames this same phase. |
| **NET-NEW** (CFN-only) | 751 | CFN's registry has no real correspondence to any live AWS page at all, even after normalizing away the word-splitting difference. A regeneration adds these as genuinely new pages -- real corpus growth, not a rename. |
| **ORPHANED** (HashiCorp-only) | 998 | The live corpus has 998 real resource-reference pages with no real CFN counterpart at all, even after normalizing. This is Phase 3's own "not yet answered" question -- answered here. A regeneration built purely from CFN would leave these with nowhere to go: either kept as-is (a permanently mixed corpus, CFN for what CFN covers, `hashicorp/aws` for what it doesn't) or dropped (a real, one-way loss of 998 real pages of already-migrated content, including all 82 hand-authored ones from Phase 2 -- s3_bucket, iam_role, vpc, and the rest, none of which appear in CLEAN, PROBABLE, or NET-NEW at all since they were checked separately in Phase 2 and are Terraform-only concepts CFN has no equivalent for).

Sanity-checked both directions: CLEAN + PROBABLE + NET-NEW = 1,437 (exactly the real CFN total). CLEAN + PROBABLE + ORPHANED = 1,684 (exactly the real live-page total). Nothing double-counted, nothing missing.

## One honest caveat on PROBABLE/NET-NEW's own boundary

The 334-count rename detection only catches pure word-SPLITTING differences (`access_analyzer` vs `accessanalyzer`). It does not catch word-CHOICE differences -- a real, observed example: CFN's `aws_amazon_mq_broker` vs Terraform's real `aws_mq_broker` are almost certainly the same underlying resource, but "amazon" is an extra word CFN's own naming includes that Terraform's own naming drops entirely, so simple underscore-stripping doesn't unify them. This means the real 751/998 split includes some further renames a smarter (but no longer purely mechanical) matcher would catch -- the 352+334=686 confirmed-rename count is a real, verified lower bound, not a claimed ceiling. Not resolved here, since resolving it needs real, resource-by-resource judgment, not a bigger regex -- exactly the kind of decision this report exists to surface, not make.

## What a real AWS regeneration would actually cost, given these numbers

- A real redirect diff at 1,684 + 334 = roughly 2x the size of any single redirect-diff this arc has run so far (GCP Compute's own 171 pages was the largest prior one).
- 751 real, net-new pages -- a bigger single addition than any provider onboarded from scratch this session (GitHub was 68, Kubernetes 71, Datadog 25, GCP Compute 95).
- A real, upfront decision on the 998 orphaned pages that this report does not make: keep them as a permanently mixed corpus (CFN where CFN covers it, HashiCorp where it doesn't -- the exact same shape Azure's own regeneration just accepted for its own ~1,084 untouched pages this phase), or something else. No option here is free of a real tradeoff.

Never self-merged, nothing regenerated -- this file is the only output of this half of the phase.
