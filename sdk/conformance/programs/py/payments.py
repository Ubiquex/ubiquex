# The SDK conformance suite's own Python case (docs/sdk.md's "The Python
# evaluator: decided empirically," UBI-36): the same logical stack
# sdk/conformance/programs/ts/payments.ts and .../go/payments.go author,
# independently authored here in Python, not a mechanical
# transliteration -- same concrete values (copied verbatim from the same
# real, live-verified `ubx propose --from-doc payments.md` transcript
# payments.ts's own doc comment cites), same resolved infrastructure,
# proving a FOURTH independent producer (after the hand-written intent
# file, the md-medium/LLM transcript, and TS/Go) converges on it.
#
# Every concrete value below matches payments.ts/payments.go exactly --
# chosen to match that real run, not invented independently.
import ubx_sdk as sdk

# UBI-98: generated/aws/db/, not generated/hashicorp-aws/aws/db/ --
# unlike TS/Go's own conformance fixtures, which nest under the source-
# sanitized directory name a real `ubx sdk gen` run would use verbatim
# ("hashicorp-aws"), Python's own dotted `import` syntax cannot traverse
# a hyphenated path segment at all (a real, load-bearing language
# constraint, not a stylistic choice) -- generated/'s own real service
# packages are placed directly inside it here instead, the same
# deliberately-curated-for-this-fixture placement the pre-restructure
# comment on this file's own generated/ sibling already established
# ("filtered to aws_db_instance only, unlike a real ubx sdk gen run").
# The "aws/" segment itself (UBI-106: every service package nests under
# the provider's own shortName directory) stays -- "aws" has no hyphen,
# so only the "hashicorp-aws" SOURCE directory is the one real Python
# import can never traverse, not this one.
from generated.aws.db.instance import Instance, InstanceConfig


def describe():
    sdk.intent(
        "Provision a small Postgres RDS instance in the payments stack, "
        "modeled on the staging database but downsized for low initial traffic."
    )

    sdk.resource(
        Instance,
        "payments",
        InstanceConfig(
            engine="postgres",
            instance_class="db.t3.small",
            allocated_storage=20,
            db_name="payments",
            username="payments_admin",
        ),
    )


if __name__ == "__main__":
    sdk.run("payments", describe)
