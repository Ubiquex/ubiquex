// The SDK conformance suite's own first golden case (docs/sdk.md's own
// "Implementation slices" slice 6/7): the same logical stack
// intentprovider/conformance/fixtures/payments.md's own md-medium fixture
// describes, authored directly in TypeScript instead. A typed program has
// no interpretation step -- there is nothing here to guess at, so
// (unlike the md medium's own real, live-verified draft) intent.
// assumptions/defaults/questions are absent entirely, by construction,
// not merely empty.
//
// Every concrete value below is copied verbatim from a real,
// live-verified `ubx propose --from-doc payments.md` transcript
// (UBI-33/34 session 4 -- see docs/sdk.md's own "Live finale" section
// for the full real transcript and the real byte-identical comparison
// this fixture's own golden/payments.json backs) -- chosen to match that
// real run, not invented independently, so this case is a genuine
// convergence proof, not a coincidence.
import { intent, resource, stack } from "@ubx/sdk";
import { Instance } from "./generated/hashicorp-aws/aws/db/instance.ts";

export default stack("payments", () => {
  intent({
    summary:
      "Provision a small Postgres RDS instance in the payments stack, modeled on the staging database but downsized for low initial traffic.",
  });

  resource(Instance, "payments", {
    engine: "postgres",
    instanceClass: "db.t3.small",
    allocatedStorage: 20,
    dbName: "payments",
    username: "payments_admin",
  });
});
