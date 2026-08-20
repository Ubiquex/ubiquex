package provider

import (
	"fmt"
	"strings"
)

// defaultSchemaHost is where a schema snapshot's own distribution repo
// lives when a SchemaSource address doesn't say otherwise — GitHub, not
// registry.terraform.io: schema snapshots are distributed as GitHub
// Releases (one repo per provider, reusing the pattern already proven for
// SDK bindings — see reference_real_sdk_repos), never through the
// Terraform/OpenTofu provider registry protocol Source/ParseSource address.
const defaultSchemaHost = "github.com"

// schemaRepoPrefix is the naming convention every schema-snapshot
// distribution repo follows, mirroring the SDK bindings repos' own
// "ubx-sdk-<provider>-<lang>" convention: "ubx-schema-<type>" under
// Namespace (the GitHub org/user). A config entry
//
//	[providers.aws]
//	source = "ubiquex/aws"
//
// resolves to github.com/ubiquex/ubx-schema-aws.
const schemaRepoPrefix = "ubx-schema-"

// SchemaSource identifies a schema snapshot's own distribution repo by
// Hostname/Namespace/Type, in the same "<hostname>/<namespace>/<type>"
// shape Source uses for provider binaries — deliberately a separate type,
// not a reuse of Source itself: Source's own default hostname
// (registry.terraform.io) and its String() form name a Terraform provider
// registry address, a real, different protocol/identity than a plain
// GitHub repo. Conflating the two would make a schema source string look
// like a registry address it isn't.
type SchemaSource struct {
	Hostname  string
	Namespace string
	Type      string
}

// String renders a SchemaSource in its canonical "<hostname>/<namespace>/<type>" form.
func (s SchemaSource) String() string {
	return s.Hostname + "/" + s.Namespace + "/" + s.Type
}

// repo is the real GitHub repository name a SchemaSource's Type resolves
// to, per schemaRepoPrefix's own convention.
func (s SchemaSource) repo() string {
	return schemaRepoPrefix + s.Type
}

// ParseSchemaSource parses a config's own [providers.<name>] source
// string. Both "ubiquex/aws" (shorthand, hostname defaults to
// defaultSchemaHost) and "github.com/ubiquex/aws" (fully qualified) are
// accepted, mirroring ParseSource's own real two-part/three-part shape
// exactly ("copy the discipline verbatim").
func ParseSchemaSource(s string) (SchemaSource, error) {
	parts := strings.Split(s, "/")
	switch len(parts) {
	case 2:
		return SchemaSource{Hostname: defaultSchemaHost, Namespace: parts[0], Type: parts[1]}, nil
	case 3:
		return SchemaSource{Hostname: parts[0], Namespace: parts[1], Type: parts[2]}, nil
	default:
		return SchemaSource{}, fmt.Errorf("%w: %q (want \"namespace/type\" or \"hostname/namespace/type\")", ErrInvalidSource, s)
	}
}
