package conformance

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/provider"
)

// azureSubscriptionID is the real Azure subscription these live
// conformance tests run against -- owned by the account running this
// suite, `az login`-authenticated. Every resource created here is a
// throwaway, destroyed (and, for Key Vault, purged) in the test's own
// cleanup -- same discipline as the AWS/GCP live tests' create+destroy
// pattern.
const azureSubscriptionID = "315b4ae0-7c86-41f1-b0a0-ac735e8b4683"

// azureLocation is the region every throwaway resource below is created
// in -- East US, confirmed available on this subscription via `az
// account list-locations`.
const azureLocation = "eastus"

// realAzureProviderPath acquires (or reuses the cached) real
// hashicorp/azurerm provider binary via provider.Acquire -- same
// dogfooding of UBI-8's acquisition path realAWSProviderPath/
// realGCPProviderPath already do.
func realAzureProviderPath(t *testing.T) string {
	t.Helper()
	src, err := provider.ParseSource("hashicorp/azurerm")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	result, err := provider.Acquire(ctx, src, azurermProviderVersion)
	if err != nil {
		t.Fatalf("acquire real azurerm provider: %v", err)
	}
	return result.Path
}

// runAz runs `az <args> --output json`, failing the test on a non-zero
// exit, and returns stdout -- the az-CLI counterpart to runGCloud, used
// here (rather than the azurerm provider's own Configure+Create, which
// this project deliberately never uses for conformance setup -- see
// docs' own ship-verification rule) to create/mutate/destroy every
// throwaway resource out of band, exactly the way `gcloud`/`aws` already
// do for the other two cloud platforms.
func runAz(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("az", append(args, "--output", "json")...).CombinedOutput()
	if err != nil {
		t.Fatalf("az %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// azureProviderConfig is the provider-level Configure payload every
// Azure conformance test shares. Empirically confirmed while writing
// this test: azurerm's own "features" provider block is NestingSingle
// (the well-known, Terraform-required `provider "azurerm" { features {}
// }` stanza) -- like every other NestingSingle block this project has
// hit (Kubernetes' own metadata[0]), it must be encoded as a one-element
// list, not a bare object; a bare `{}` fails Configure outright
// ("missing expected [").
func azureProviderConfig() json.RawMessage {
	return MustMarshal(map[string]interface{}{
		"subscription_id": azureSubscriptionID,
		"use_cli":         true,
		"features":        []interface{}{map[string]interface{}{}},
	})
}

func azureResourceID(rg, provider, resourceType, name string) string {
	return "/subscriptions/" + azureSubscriptionID + "/resourceGroups/" + rg +
		"/providers/" + provider + "/" + resourceType + "/" + name
}

// TestConformance_AzureResourceGroup creates a throwaway resource group
// (free), tests it, and deletes it.
func TestConformance_AzureResourceGroup(t *testing.T) {
	RequireLive(t)
	providerPath := realAzureProviderPath(t)
	name := uniqueName("ubx-conformance-rg")

	runAz(t, "group", "create", "--name", name, "--location", azureLocation)
	t.Cleanup(func() { runAz(t, "group", "delete", "--name", name, "--yes") })

	// Empirically confirmed while writing this test: a resource group's
	// own ARM id is JUST "/subscriptions/<sub>/resourceGroups/<name>" --
	// unlike every OTHER azurerm type's own id (a resourceGroups/<rg>/
	// providers/<ns>/<type>/<name> path), a resource group has no
	// "providers/Microsoft.Resources/resourceGroups/<name>" suffix of
	// its own; azureResourceID's own generic helper doesn't apply here.
	id := "/subscriptions/" + azureSubscriptionID + "/resourceGroups/" + name

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Source:         "hashicorp/azurerm",
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "azurerm_resource_group", Name: name},
		Lookup:         MustMarshal(map[string]string{"id": id, "name": name}),
		ProviderConfig: azureProviderConfig(),
		Mutate: func(t *testing.T) {
			runAz(t, "group", "update", "--name", name, "--tags", "ubx-conformance=resourcegroup")
		},
	})
}

// azureConformanceResourceGroup creates a throwaway resource group to
// hold ONE of the other four types' own throwaway resource (storage
// account/container, key vault, user-assigned identity all require a
// resource group to live in) -- registers its own deletion in
// t.Cleanup, run AFTER the resource-under-test's own cleanup (Go runs
// t.Cleanup funcs in LIFO order, so registering this one FIRST, before
// the resource's own, is what makes the resource get deleted before its
// own resource group).
func azureConformanceResourceGroup(t *testing.T) string {
	t.Helper()
	rg := uniqueName("ubx-conformance-rg")
	runAz(t, "group", "create", "--name", rg, "--location", azureLocation)
	t.Cleanup(func() { runAz(t, "group", "delete", "--name", rg, "--yes") })
	return rg
}

// TestConformance_AzureStorageAccount creates a throwaway storage
// account (free at this scale) inside its own throwaway resource group,
// tests it, and deletes it.
//
// Empirically confirmed while writing this test: "id" alone (the full
// ARM resource path) IS sufficient -- a real, empirically-confirmed
// divergence from google_storage_bucket's own "both id and name
// required" trap in the OPPOSITE direction (azurerm's ARM id path
// already fully qualifies the resource; GCP's bucket "id" -- just the
// bucket name -- does not).
func TestConformance_AzureStorageAccount(t *testing.T) {
	RequireLive(t)
	providerPath := realAzureProviderPath(t)
	rg := azureConformanceResourceGroup(t)
	// Storage account names must be 3-24 chars, lowercase alphanumeric
	// only (no hyphens) -- empirically confirmed while writing this test
	// (uniqueName's own hyphenated prefix is rejected outright).
	name := "ubxsa" + strings.ReplaceAll(time.Now().UTC().Format("020150405.000000"), ".", "")
	if len(name) > 24 {
		name = name[:24]
	}

	runAz(t, "storage", "account", "create", "--name", name, "--resource-group", rg,
		"--location", azureLocation, "--sku", "Standard_LRS")
	t.Cleanup(func() { runAz(t, "storage", "account", "delete", "--name", name, "--resource-group", rg, "--yes") })

	id := azureResourceID(rg, "Microsoft.Storage", "storageAccounts", name)

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Source:         "hashicorp/azurerm",
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "azurerm_storage_account", Name: name},
		Lookup:         MustMarshal(map[string]string{"id": id}),
		ProviderConfig: azureProviderConfig(),
		Mutate: func(t *testing.T) {
			runAz(t, "storage", "account", "update", "--name", name, "--resource-group", rg, "--tags", "ubx-conformance=storageaccount")
		},
	})
}

// TestConformance_AzureStorageContainer creates a throwaway storage
// account + container inside its own throwaway resource group, tests
// the container, and deletes everything.
//
// Empirically confirmed while writing this test: "id" alone is
// sufficient, same shape as azurerm_storage_account.
func TestConformance_AzureStorageContainer(t *testing.T) {
	RequireLive(t)
	providerPath := realAzureProviderPath(t)
	rg := azureConformanceResourceGroup(t)
	accountName := "ubxsc" + strings.ReplaceAll(time.Now().UTC().Format("020150405.000000"), ".", "")
	if len(accountName) > 24 {
		accountName = accountName[:24]
	}
	containerName := uniqueName("ubx-conformance-ctr")

	runAz(t, "storage", "account", "create", "--name", accountName, "--resource-group", rg,
		"--location", azureLocation, "--sku", "Standard_LRS")
	t.Cleanup(func() {
		runAz(t, "storage", "account", "delete", "--name", accountName, "--resource-group", rg, "--yes")
	})

	runAz(t, "storage", "container", "create", "--name", containerName, "--account-name", accountName, "--auth-mode", "login")

	accountID := azureResourceID(rg, "Microsoft.Storage", "storageAccounts", accountName)
	id := accountID + "/blobServices/default/containers/" + containerName

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Source:         "hashicorp/azurerm",
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "azurerm_storage_container", Name: containerName},
		Lookup:         MustMarshal(map[string]string{"id": id}),
		ProviderConfig: azureProviderConfig(),
		Mutate: func(t *testing.T) {
			runAz(t, "storage", "container", "metadata", "update", "--name", containerName,
				"--account-name", accountName, "--auth-mode", "login", "--metadata", "ubxconformance=container")
		},
	})
}

// TestConformance_AzureKeyVault creates a throwaway Key Vault inside its
// own throwaway resource group, tests it, deletes it, and PURGES it
// (soft-delete would otherwise reserve the name for up to 90 days).
//
// Empirically confirmed while writing this test: "id" alone is
// sufficient, same shape as azurerm_storage_account.
func TestConformance_AzureKeyVault(t *testing.T) {
	RequireLive(t)
	providerPath := realAzureProviderPath(t)
	rg := azureConformanceResourceGroup(t)
	name := uniqueName("ubx-conf-kv")
	if len(name) > 24 {
		name = name[:24]
	}

	runAz(t, "keyvault", "create", "--name", name, "--resource-group", rg, "--location", azureLocation)
	t.Cleanup(func() {
		runAz(t, "keyvault", "delete", "--name", name)
		runAz(t, "keyvault", "purge", "--name", name, "--location", azureLocation)
	})

	id := azureResourceID(rg, "Microsoft.KeyVault", "vaults", name)

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Source:         "hashicorp/azurerm",
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "azurerm_key_vault", Name: name},
		Lookup:         MustMarshal(map[string]string{"id": id}),
		ProviderConfig: azureProviderConfig(),
		Mutate: func(t *testing.T) {
			// Empirically confirmed while writing this test: unlike every
			// other type here, `az keyvault update` has no --tags flag at
			// all -- a real az-CLI-specific limitation (not an azurerm
			// provider one), so this mutates a real boolean property
			// instead.
			runAz(t, "keyvault", "update", "--name", name, "--enabled-for-disk-encryption", "true")
		},
	})
}

// TestConformance_AzureUserAssignedIdentity creates a throwaway
// user-assigned identity inside its own throwaway resource group, tests
// it, and deletes it.
//
// Empirically confirmed while writing this test: "id" alone is
// sufficient -- no companion field needed, the same shape
// google_service_account/google_project_iam_custom_role turned out to
// have on GCP.
func TestConformance_AzureUserAssignedIdentity(t *testing.T) {
	RequireLive(t)
	providerPath := realAzureProviderPath(t)
	rg := azureConformanceResourceGroup(t)
	name := uniqueName("ubx-conformance-uai")

	runAz(t, "identity", "create", "--name", name, "--resource-group", rg, "--location", azureLocation)
	t.Cleanup(func() { runAz(t, "identity", "delete", "--name", name, "--resource-group", rg) })

	id := azureResourceID(rg, "Microsoft.ManagedIdentity", "userAssignedIdentities", name)

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Source:         "hashicorp/azurerm",
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "azurerm_user_assigned_identity", Name: name},
		Lookup:         MustMarshal(map[string]string{"id": id}),
		ProviderConfig: azureProviderConfig(),
		Mutate: func(t *testing.T) {
			runAz(t, "identity", "update", "--name", name, "--resource-group", rg, "--tags", "ubx-conformance=identity")
		},
	})
}
