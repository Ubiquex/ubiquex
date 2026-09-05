# UBI-175 Phase 3: naming-mismatch report and proposal

Report only. Nothing renamed, regenerated, or committed. All findings below were verified against real, current files and one real, live binary run today, not assumed from Phase 1/2's own record -- two things changed since Phase 2 wrote its own account, both caught here, not before.

## Per-provider: live-page naming vs. artifact naming

| Provider | Live pages use | Artifacts use | Agree? |
|---|---|---|---|
| AWS | HashiCorp (`hashicorp/aws` wire schema, e.g. `aws_accessanalyzer_analyzer`) | CFN registry (`aws_access_analyzer_analyzer`) | No -- 1,085 / 1,437 mismatch |
| Azure | HashiCorp (`hashicorp/azurerm`, e.g. `azurerm_availability_set`) | OpenAPI/ARM Compute spec (`azure_availability_set`) | No -- 0 overlap, disjoint resource sets |
| GCP (compute/*) | Discovery Docs (`google_compute_instance`) | Discovery Docs (same) | Yes -- already reconciled in Phase 4 |
| GCP (non-compute) | HashiCorp (`google_bigquery_dataset`) | none exist | N/A -- no artifact claims this surface |
| Datadog | OpenAPI (`datadog_monitor`) | OpenAPI (same) | Yes |
| GitHub | OpenAPI (`github_repository`) | OpenAPI (same) | Yes |
| Kubernetes | OpenAPI (`kubernetes_core_pod`) | OpenAPI (same) | Yes |

Three of six providers already agree -- they were the ones actually taken through the docs-regeneration pipeline this session (Phases 1-4) or already matched by construction. The mismatch is confined to AWS and Azure, plus GCP's own already-named non-compute gap, which is a coverage gap, not a naming disagreement (nothing currently claims a name for those ~1,161 pages, so there's nothing to reconcile there, only content to eventually generate).

## Why they diverge

`scripts/resource-reference-gen/gen_provider_docs.py`'s own `SCHEMA_SOURCE_LABEL` table already describes AWS as sourced from "the real AWS CloudFormation resource registry" and Azure from "the real Azure Resource Manager API specification" -- this label was written when the pipeline switched to `ubx-provider-dynamic` for every provider (its own comment: "every resource type across all six providers is now sourced through ubx-provider-dynamic... never a HashiCorp tfplugin provider"). That switch is real and already live in the generator's own code. What never happened is re-running the generator against AWS and Azure's own now-current sources -- their live pages are the OLD output, from before the switch, never regenerated since. GCP-compute, Kubernetes, GitHub, and Datadog all went through a real regeneration this session (or already matched); AWS and Azure did not.

AWS's own CFN-vs-HashiCorp naming gap has one further, distinct real cause beyond "not yet regenerated": CFN's own real service-name convention (`AccessAnalyzer` -> `access_analyzer`, word-split on every capital) differs mechanically from Terraform's own convention (`accessanalyzer`, no split) for THE SAME underlying real service. Checked precisely, not estimated: of the 1,085 CFN resource-type names with no exact live-page match, 334 match a real live page once underscores are stripped from both sides -- a pure, mechanical naming-convention difference, nothing else. The remaining 751 have no live counterpart even ignoring underscores -- CFN's registry covers real AWS resources (or slices resources differently) that HashiCorp's own Terraform provider either doesn't expose at all or bundles into a differently-shaped resource. So AWS's real gap is two things at once, not one: a rename (686 resources, 47.7%) and a real coverage difference (751 resources, 52.3%), and they need different handling.

## Azure: the report corrects Phase 2's own record here, verified live today

Phase 2 (and `sdk/providers/.ubx/config`'s own comment) recorded Azure's OpenAPI discovery as blocked: "resourcemap.Discover finds ZERO resources... because Azure's own real ARM API convention uses PUT (not POST)... only recognizes POST today." **That is stale. The underlying bug was already fixed** -- `ubx-provider-dynamic` commit `a61e489`, "resourcemap: recognize PUT-to-item-path as a real, generic create signal -- fixes Azure ARM's own real convention," dated 2026-08-19 16:41, before today's own session even started. The config file's own comment was simply never updated after the fix landed.

Verified live, today, not assumed from the commit message alone: rebuilt `ubx-provider-dynamic` from the current checkout and ran `--dump-signals` against the real, already-configured `[dynamic_providers.azure]` entry (the real, live Azure Compute Resource Manager spec, fetched fresh). Result: **19 real resource types discovered**, zero errors, a real, working schema. (One resource more than the 18 in the checked-in `azure.json` artifact from yesterday -- Azure's own live spec drifted by one resource type in the day since that artifact was generated; a small, real, separate finding, not a discovery bug.)

**Azure has no remaining technical blocker for what's currently configured.** The real limit is scope, not a bug: `[dynamic_providers.azure]`'s own `schema_url` only ever points at the Compute resource-provider spec -- 19 resources, out of Azure's real ~40 product families and 1,103 live pages. Regenerating today would touch those 19 and nothing else, the exact same shape as GCP's own compute-only precedent.

## What it would take, per provider

**AWS** -- technically ready today, no new code needed. The identical real 3-step pipeline already used for GCP-compute/Kubernetes/GitHub/Datadog (`ubx sdk gen --dump-ir` against the already-configured `[dynamic_providers.aws]` CFN entry, `extract_idents.py`, `gen_new_provider_pages.py`/`gen_complete_pages.py`) would produce CFN-named pages today. What it actually costs is scale and a real content decision, not engineering: a real redirect diff at roughly 10x the size of any single phase run so far (1,684 live pages vs. 1,437 CFN resources), a decision on the 751 CFN-only resources with no HashiCorp counterpart (net-new pages, real, immediate corpus growth), and a decision on whatever HashiCorp-only resources have no CFN counterpart at all (not yet counted precisely -- a real follow-up question, not answered by this report).

**Azure** -- also technically ready today, for the 19 currently-configured Compute resources only. Reconciling those costs the same real, now-proven mechanism GCP-compute already used, at 1/5 the scale of even one phase. It does nothing for the other ~1,084 Azure pages -- those need the same real config-expansion work the ticket itself already named as separate ("Known coverage gap... must be closed before those providers can be fully regenerated, but it is separate from this specification"): declaring real `schema_url` entries for Azure's other real product families, the same shape of work GCP still needs for its own ~40 remaining products.

**GCP non-compute** -- not a naming question, a coverage question, already named in the ticket, unchanged by this report. No artifact currently claims these ~1,161 pages, so there's nothing to reconcile, only real work (Discovery-Doc config entries per product) still ahead, separate from this ticket by the founder's own prior framing.

**Datadog, GitHub, Kubernetes** -- nothing to do. Already agree.

## There is no single move that reconciles everything at once

AWS's problem is real and immediately actionable but large and disruptive (a real corpus-wide rename+redirect, the biggest single regeneration this arc has ever run). Azure's problem is real, immediately actionable, and small (19 resources) but leaves 98% of Azure's live pages exactly where they are. GCP's non-compute gap needs real config work before any regeneration is even possible, the same as Azure's own remaining ~1,084. These are three different shapes of problem -- technical readiness plus scale-of-disruption for AWS, technical readiness plus narrow-scope for Azure's Compute slice, and pure coverage-gap for everything else in Azure and GCP. Forcing one pass across all of it would either stall on the coverage-gap work that isn't done yet, or run the AWS rename at a scale nobody has reviewed a redirect diff for before.

## Proposed sequence (proposal only -- the founder decides)

1. **Azure Compute first, as a real, small, low-risk proof.** 19 resources, the identical mechanism already proven four times this session (Datadog/GitHub/Kubernetes/GCP-compute). Real, cheap validation that regenerating a HashiCorp-sourced provider onto its dynamic-provider replacement works end to end, including redirects, before committing to AWS's much larger version of the same move.
2. **AWS's redirect diff and coverage-gap count, as a real report, before any AWS regeneration.** Not a decision to regenerate yet -- just the numbers a real decision needs: exactly which of the 1,684 live pages redirect cleanly, which orphan, and precisely how many HashiCorp-only resources (if any) have no CFN counterpart at all, mirroring the redirect-diff discipline every prior phase already used, at AWS's own real scale.
3. **The founder decides on AWS's regeneration** with that real number in hand, not before -- this is the one real, disruptive, corpus-wide move in this whole list, and it deserves a real decision, not a default.
4. **Azure's and GCP's remaining coverage gaps (config expansion for both) are real, separate, already-named future work** -- explicitly not blocking anything above, and not scoped into this phase.

Never self-merged, nothing regenerated or renamed -- this file is the only output of this phase.
