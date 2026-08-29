# UBI-216: candidate-provider survey

Real resource/data-source counts for 17 named candidates, run alongside
the DigitalOcean proving case. A spec existing is not the test --
Datadog v1 had 150 paths and yielded 26 resources, the precedent this
survey follows.

## Method

Same method as `gcp-unconfigured-api-survey.md`: fetch each candidate's
real, published spec, then run the real Go functions this pipeline
already uses at generation time -- `internal/resourcemap.Discover` and
`internal/resourcemap.DiscoverDataSources` (`ubx-provider-dynamic`) --
directly against the loaded document, via a scratch tool wrapping
`internal/openapi.Load` + both `Discover` calls (not committed --
one-shot, deleted after this survey; the GCP survey used a Python
reimplementation instead because `discoverydoc.Discover` needed inputs
this session's tooling didn't have on hand at the time, not because
calling the real Go function directly is wrong -- it is what
DigitalOcean's own step-one work this session already did).

Counts here are raw `Discover()` output -- before the full `ubx sdk gen`
pipeline's own collision resolution and unusable-field filtering, which
trims the number slightly (confirmed against DigitalOcean itself: 61
resources + 140 data sources raw here, 59 + 137 after the full pipeline
in the step-one PR -- a ~2% trim, consistent across both counts). Real
enough to scope artifact work; not the exact number a final onboarded
config will produce.

## Results

| Provider | Format | Paths | Resources | Data sources | Total |
|---|---|---|---|---|---|
| cloudflare | OpenAPI 3.0.3 | 2123 | 307 | 714 | 1021 |
| gitlab | OpenAPI 3.0.0 | 1287 | 93 | 237 | 330 |
| mongodbatlas | OpenAPI 3.0.1 | 333 | 66 | 145 | 211 |
| digitalocean (in flight, UBI-216 step one) | OpenAPI 3.0 (needs bundling) | 445 | 61 | 140 | 201 |
| auth0 | OpenAPI 3.1.0 | 257 | 44 | 140 | 184 |
| elasticstack | OpenAPI 3.0.3 | 581 | 24 | 154 | 178 |
| opensearch | OpenAPI 3.1.0 (bundled release artifact) | 472 | 27 | 134 | 161 |
| confluent | OpenAPI 3.0.0 | 299 | 59 | 91 | 150 |
| coralogix | OpenAPI 3.0.0 | 219 | 24 | 92 | 116 |
| linode | OpenAPI 3.0.1 | 308 | 44 | 55 | 99 |
| grafana | Swagger 2.0 | 207 | 20 | 71 | 91 |
| vercel | OpenAPI 3.0.3 | 287 | 27 | 63 | 90 |
| logzio | Swagger 2.0, 6 real per-product fragments | 19 | 5 | 8 | 13 |
| 1password | OpenAPI 3.0.2 | 11 | 1 | 5 | 6 |

**No usable count -- real, named reasons, not a gap in this survey:**

- **databricks**: no real, public OpenAPI/spec repo found. Its own SDKs
  (`databricks-sdk-go`, `databricks-sdk-py`) are generated from an
  internal spec -- confirmed via `.codegen.json` and README ("generated
  directly from the Databricks... service specifications"), never
  published for external fetch.
- **scaleway**: no single, real, fetchable spec. `scaleway-sdk-go`
  generates each of its ~40+ per-service packages from its own internal
  spec, none committed alongside the generated Go code, and no public
  per-product swagger/openapi endpoint found at the expected
  `api.scaleway.com`/`www.scaleway.com` paths.
- **splunk**: same shape as Databricks -- `splunk-cloud-sdk-python`'s own
  README confirms generation "directly from the Splunk Cloud Services
  service specifications," never published publicly. No public REST v2
  swagger found either (Splunk Enterprise's own REST API reference is
  hand-authored docs, not a machine-readable spec).

**Unresolved, not "no spec" -- a real spec exists, this session
couldn't reach it programmatically:**

- **ionoscloud**: `api.ionos.com/docs/cloud/v6` is a real, live interactive
  API reference with its own "Download OpenAPI specification" button
  confirming CLOUD API v6.0 -- but the page is a client-rendered SPA
  whose real backing spec URL could not be located by direct fetch in
  reasonable effort (every guessed static path 404'd; the one path that
  didn't, `/cloudapi/v6/openapi.json`, returned 401, meaning it's a real
  endpoint but auth-gated, not the public spec download). Worth a
  browser-based follow-up (inspect the real XHR the download button
  fires) rather than counted as "no spec."

## Real parser gaps found, same shape as DigitalOcean's own step-one finding

- **linode**: real, valid JSON -- `📘` (a UTF-16 surrogate-pair
  escape for an emoji embedded in one field's description text) is
  legal JSON but not accepted by `oasdiff/yaml`, the library
  `internal/openapi.Load` uses to parse both JSON and YAML uniformly.
  Worked around for this survey by round-tripping the file through
  Python's own `json` module with `ensure_ascii=False` (decodes the
  surrogate pair to a literal UTF-8 character, sidestepping the escape
  entirely) before loading -- not yet a real fix in `ubx-provider-dynamic`
  itself. A real gap: any spec with a non-ASCII character (emoji,
  accented text) anywhere in a JSON-sourced description will hit this.
- **digitalocean**: see the step-one PR's own doc comment -- two
  Redocly-only, non-standard `$ref` conventions, requiring a real
  bundling step before this pipeline's loader accepts the document at
  all.

## Confirmed-out candidates, re-checked, none wrong

- **new relic**: confirmed GraphQL-primary (NerdGraph). No public REST
  v2 OpenAPI/swagger spec found at either of the two real, standard
  paths checked (`api.newrelic.com/docs/api.newrelic.com.json`,
  `api.newrelic.com/swagger.json` -- both 404).
- **heroku**: confirmed real JSON Hyper-Schema, not OpenAPI --
  `api.heroku.com/schema`'s own `$schema` field is
  `http://interagent.github.io/interagent-hyper-schema`, a real,
  different (pre-OpenAPI-3) schema family this pipeline's loader has no
  path for.
- **azure ad**: not independently re-fetched (well-established, no
  contradicting evidence found) -- real Azure AD management goes
  through Microsoft Graph (`graph.microsoft.com`), a fully separate
  protocol from the ARM-based OpenAPI this project's existing `azure`
  entry already consumes.

## What this tells us

By real yield, worth onboarding in roughly this order: cloudflare
(largest single candidate found this survey, 1021), gitlab (330),
mongodbatlas (211), digitalocean (201, already in flight), auth0 (184),
elasticstack (178), opensearch (161), confluent (150), coralogix (116),
linode (99), vercel (90), grafana (91). logzio and 1password are real
but small -- worth onboarding cheaply once the runbook exists, not
worth a dedicated push. databricks, scaleway, and splunk are not
onboardable via this pipeline's real, current mechanism (openapi/
smithy/discovery_docs/cloudformation) without a private spec source
none of them publish.
