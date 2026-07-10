package provider

import (
	"fmt"
	"strings"
)

// defaultRegistryHost is Terraform's own default registry hostname, and
// therefore the canonical identity for a provider source address when none
// is given explicitly — e.g. "hashicorp/aws" means
// "registry.terraform.io/hashicorp/aws". This is an identity/addressing
// convention only; see docs/architecture.md for why ubx actually downloads
// from a different host regardless of what's recorded here.
const defaultRegistryHost = "registry.terraform.io"

// Source identifies a provider by its canonical registry address, e.g.
// "registry.terraform.io/hashicorp/aws". Hostname/Namespace/Type follow
// Terraform's own provider addressing convention exactly, since that's
// what shows up in existing Terraform/OpenTofu configs and in
// docs/schema.md's IR provider field — ubx doesn't invent its own.
type Source struct {
	Hostname  string
	Namespace string
	Type      string
}

// String renders a Source in its canonical "<hostname>/<namespace>/<type>"
// form.
func (s Source) String() string {
	return s.Hostname + "/" + s.Namespace + "/" + s.Type
}

// ParseSource parses a canonical provider source address. Both
// "registry.terraform.io/hashicorp/aws" (fully qualified) and the
// shorthand "hashicorp/aws" (hostname defaults to registry.terraform.io,
// matching Terraform's own default) are accepted.
func ParseSource(s string) (Source, error) {
	parts := strings.Split(s, "/")
	switch len(parts) {
	case 2:
		return Source{Hostname: defaultRegistryHost, Namespace: parts[0], Type: parts[1]}, nil
	case 3:
		return Source{Hostname: parts[0], Namespace: parts[1], Type: parts[2]}, nil
	default:
		return Source{}, fmt.Errorf("%w: %q (want \"namespace/type\" or \"hostname/namespace/type\")", ErrInvalidSource, s)
	}
}
