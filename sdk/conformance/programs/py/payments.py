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
from generated.hashicorp_aws import AwsDbInstance, AwsDbInstanceConfig


def describe():
    sdk.intent(
        "Provision a small Postgres RDS instance in the payments stack, "
        "modeled on the staging database but downsized for low initial traffic."
    )

    sdk.resource(
        AwsDbInstance,
        "payments",
        AwsDbInstanceConfig(
            engine="postgres",
            instance_class="db.t3.small",
            allocated_storage=20,
            db_name="payments",
            username="payments_admin",
        ),
    )


if __name__ == "__main__":
    sdk.run("payments", describe)
