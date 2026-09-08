package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// identityFilename is the third file in a snapshot's split format,
// alongside manifest.json and members/. Written by ubx-provider-dynamic's
// own SaveSplit; the spelling is a constant on both sides so the writer
// and this reader cannot drift.
const identityFilename = "identity.json"

// ReadSnapshotIdentity returns a snapshot's own resource type ->
// identifying attribute names map, or nil if it does not publish one.
//
// Identity has no channel in the tfplugin6 schema. A SchemaAttribute can
// say Required, Optional, Computed or Sensitive, and none of those means
// "this is how you find the resource again", so ubx derived a lookup key
// the only way the schema allowed: an attribute literally named "id", or
// failing that the Required ones. That is the Terraform shape and it does
// not fit every provider. A CloudFormation resource's identifier is in
// readOnlyProperties, hence Computed, which the derivation deliberately
// excludes as unstable, so 10% of AWS resource types recorded no lookup
// key at all and another 53% recorded one that could not re-find the
// resource. Those resources could not be destroyed through ubx and, more
// seriously, were never drift-monitored: status --drift counts them
// unreadable rather than checking them.
//
// The snapshot carries it instead of the protocol because the schema is
// HashiCorp's wire format and not ours to extend, while the snapshot is
// already ours and already travels with the provider. It is computed once
// at snapshot generation time from whichever source format that provider
// used, and published in one normalized shape, so this side never learns
// to parse CloudFormation, Smithy, OpenAPI or discovery documents. ubx
// continues to treat member files as opaque payload.
//
// Absent is legal and returns nil: every snapshot published before this
// file existed simply cannot say, and the caller falls back to the
// existing derivation. A present but unreadable file is an error, because
// reading it as absent would be indistinguishable from "this provider has
// nothing to report" and would restore the silence this exists to end.
func ReadSnapshotIdentity(schemaDir string) (map[string][]string, error) {
	data, err := os.ReadFile(filepath.Join(schemaDir, identityFilename))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s in %s: %w", identityFilename, schemaDir, err)
	}
	var out map[string][]string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s in %s: %w", identityFilename, schemaDir, err)
	}
	return out, nil
}
