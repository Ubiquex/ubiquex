// The SDK conformance suite's own Go case (docs/sdk.md's "The Go
// evaluator: decided empirically," UBI-35): the same logical stack
// sdk/conformance/programs/ts/payments.ts authors in TypeScript,
// independently authored here in Go, not a mechanical transliteration --
// same concrete values (copied verbatim from the same real, live-
// verified `ubx propose --from-doc payments.md` transcript
// sdk/conformance/programs/ts/payments.ts's own doc comment cites), same
// resolved infrastructure, proving a THIRD independent producer
// converges on it.
//
// Every concrete value below matches sdk/conformance/programs/ts/payments.ts
// exactly -- chosen to match that real run, not invented independently.
package main

import (
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"

	db "github.com/ubiquex/ubx-sdk-aws/db"
)

func main() {
	sdk.Main(sdk.Stack("payments", func() {
		sdk.Intent(sdk.IntentInfo{
			Summary: "Provision a small Postgres RDS instance in the payments stack, modeled on the staging database but downsized for low initial traffic.",
		})

		sdk.Resource(db.Instance, "payments", db.InstanceConfig{
			Engine:           "postgres",
			InstanceClass:    "db.t3.small",
			AllocatedStorage: 20,
			DbName:           "payments",
			Username:         "payments_admin",
		})
	}))
}
