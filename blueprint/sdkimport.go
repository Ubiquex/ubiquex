package blueprint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// sdkimport.go is the "import the published SDK instead of emitting our
// own bindings" mode.
//
// Without it, `ubx blueprint build` emits a bindings file per language
// carrying a ResourceBinding and a Config struct for every resource the
// blueprint uses. Those are a narrowed copy of definitions the published
// per-provider SDK already ships, so a provider's resource types are
// generated once into ubx-sdk-<provider> and then again, partially, into
// every blueprint that touches them. This mode removes the second copy.
//
// It is opt-in via the Ubxfile's own sdk: block, and that is forced
// rather than a hedge: a blueprint against a provider with no published
// SDK (fakeprovider's fake_widget, every conformance fixture, any
// provider onboarded before its SDK repo exists) has nothing to import,
// so the emitting path has to remain reachable.
//
// The hard part is not emitting an import, it is knowing WHICH package.
// A published SDK lays resources out per service (aws/sqs, aws/ec2), and
// the service is NOT derivable from the wire type: measured against the
// real ubiquex/aws 4.0.0 snapshot, 966 of 1728 AWS types land in the
// wrong package under a mechanical split of the wire name, because the
// split takes one token where the real namespace is a whole word
// (aws_instance is in ec2, not "instance"; aws_arc_zonal_shift_* is in
// arczonalshift, not "arc"). So this resolves the mapping from the
// provider snapshot's own CloudFormation type names, which is the same
// source `ubx sdk gen` uses (ir.ServiceAndLocalNameForType's own
// RealNamespace), read straight off the already-cached snapshot rather
// than by launching a provider.

// SDKSpec is the Ubxfile's own sdk: block: which published SDK to import
// from, per language, and which provider snapshot to resolve service
// packages against.
type SDKSpec struct {
	// Provider is "<namespace>/<name>@<version>", e.g.
	// "ubiquex/aws@4.0.0" -- names the cached snapshot whose own type
	// names supply the service-package mapping. Required whenever any
	// language target is set, since without it the package a resource
	// lives in cannot be known.
	Provider string
	// Go is "<module path>@<version>", the path INCLUDING its
	// major-version suffix, e.g.
	// "github.com/ubiquex/ubx-sdk-aws/sdk/go/v3@v3.0.1". Stated explicitly
	// rather than derived: a published SDK's own major version tracks
	// upstream schema breaks and does not track the provider snapshot
	// version (aws snapshot 4.0.0 against sdk/go v3.0.1 today), so there
	// is nothing to derive it from.
	Go string
	// TS is the TypeScript package specifier, e.g. "@ubx/sdk-aws".
	TS string
	// Py is the Python distribution's own import root, e.g.
	// "ubx_sdk_aws".
	Py string
}

// enabled reports whether this blueprint imports a published SDK at all.
func (s *SDKSpec) enabled() bool {
	return s != nil && s.Provider != ""
}

// sdkTarget is one resource type's own resolved location in a published
// SDK: the service package it lives in, and the type name within it.
type sdkTarget struct {
	// Service is the package's own last path segment, e.g. "sqs" -- both
	// the import's own trailing element and the Go/Python qualifier.
	Service string
	// LocalWire is the wire type's own local part, e.g. "queue" for
	// aws_sqs_queue. TypeScript's published SDK is laid out per RESOURCE
	// rather than per service (jsr exports "./aws/sqs/queue", not
	// "./aws/sqs"), so its import path needs this where Go and Python
	// need only Service.
	LocalWire string
	// TypeName is the PascalCase resource type within that package, e.g.
	// "Queue" for aws_sqs_queue or "QueuePolicy" for
	// aws_sqs_queue_policy. The Config struct is this + "Config".
	TypeName string
}

// parseSDKProvider splits "<namespace>/<name>@<version>".
func parseSDKProvider(s string) (namespace, name, version string, err error) {
	at := strings.LastIndex(s, "@")
	if at < 0 {
		return "", "", "", fmt.Errorf("sdk.provider %q must be \"<namespace>/<name>@<version>\", e.g. \"ubiquex/aws@4.0.0\"", s)
	}
	version = s[at+1:]
	slash := strings.Index(s[:at], "/")
	if slash < 0 || version == "" {
		return "", "", "", fmt.Errorf("sdk.provider %q must be \"<namespace>/<name>@<version>\", e.g. \"ubiquex/aws@4.0.0\"", s)
	}
	return s[:slash], s[slash+1 : at], version, nil
}

// snapshotDir is a package var so tests can point resolution at a
// fixture instead of the real ~/.ubx cache, matching cli's own
// configSearchStartDir/userHomeDir convention.
var snapshotDir = func(namespace, name, version string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ubx", "schemas", namespace, name, version), nil
}

// resolveSDKTargets maps every wire type the blueprint uses to its own
// service package and type name, from the named snapshot.
//
// Every type must resolve. A wire type the snapshot does not carry is a
// hard error rather than a mechanical-split fallback: the fallback is
// wrong for more than half of AWS, and a guessed import path fails as a
// compile error in the caller's own stack rather than as a message from
// ubx.
func resolveSDKTargets(spec *SDKSpec, wireTypes []string) (map[string]sdkTarget, error) {
	namespace, name, version, err := parseSDKProvider(spec.Provider)
	if err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}
	dir, err := snapshotDir(namespace, name, version)
	if err != nil {
		return nil, fmt.Errorf("blueprint: resolve sdk provider: %w", err)
	}
	members, err := filepath.Glob(filepath.Join(dir, "members", "*.json"))
	if err != nil {
		return nil, fmt.Errorf("blueprint: resolve sdk provider: %w", err)
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("blueprint: sdk.provider %q: no snapshot at %s -- acquire it first (a stack configured for this provider caches it), or drop the sdk: block to emit bindings instead", spec.Provider, dir)
	}

	byWire := map[string]sdkTarget{}
	ambiguous := map[string]bool{}
	for _, m := range members {
		data, err := os.ReadFile(m)
		if err != nil {
			return nil, fmt.Errorf("blueprint: read snapshot member %s: %w", m, err)
		}
		var member struct {
			RawSpec json.RawMessage `json:"raw_spec"`
		}
		if err := json.Unmarshal(data, &member); err != nil {
			continue
		}
		// raw_spec is an object in every snapshot this has been run
		// against, but a JSON-string-encoded object is the other shape
		// the field has carried, so both are accepted rather than
		// silently skipping a member and reporting the type as absent.
		rawSpec := member.RawSpec
		var asString string
		if json.Unmarshal(rawSpec, &asString) == nil {
			rawSpec = json.RawMessage(asString)
		}
		var spec map[string]json.RawMessage
		if json.Unmarshal(rawSpec, &spec) != nil {
			continue
		}
		for typeName := range spec {
			candidates, target, ok := cfnCandidates(name, typeName)
			if !ok {
				continue
			}
			for _, wire := range candidates {
				if prev, dup := byWire[wire]; dup && prev != target {
					// Two CFN types whose candidate names collide. The
					// provider picked one via its own known-names table,
					// which is not here, so this is recorded as ambiguous
					// and refused at lookup rather than resolved by
					// whichever member file happened to be read last.
					ambiguous[wire] = true
					continue
				}
				byWire[wire] = target
			}
		}
	}

	out := map[string]sdkTarget{}
	var missing []string
	for _, wt := range wireTypes {
		if ambiguous[wt] {
			return nil, fmt.Errorf("blueprint: sdk.provider %q: %q matches more than one type in the snapshot, so the package it lives in is ambiguous -- state the import by hand or drop the sdk: block", spec.Provider, wt)
		}
		t, ok := byWire[wt]
		if !ok {
			missing = append(missing, wt)
			continue
		}
		out[wt] = t
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("blueprint: sdk.provider %q carries no type named %s -- the published SDK has no package to import for it", spec.Provider, strings.Join(missing, ", "))
	}
	return out, nil
}

// cfnCandidates turns "AWS::SQS::Queue" into every wire name the
// provider could have given it, each paired with the same target.
//
// The provider does not derive a wire name mechanically. It resolves
// against a table of real hashicorp/aws names, trying the prefixed form
// "aws_<namespace>_<type>" and then the bare "aws_<type>", whichever is
// a known real name (ubx-provider-dynamic internal/cloudformation/
// naming.go's own Resolve). That table is not in the snapshot, so this
// cannot reproduce the choice. It does not need to: the caller already
// HAS the real wire name, from the blueprint's own resources.json, so
// indexing both candidates and looking the real one up inverts the
// resolution exactly.
//
// AWS::EC2::Instance is the case that proves it matters. Its real wire
// name is the bare "aws_instance", not "aws_ec2_instance", and its
// package is ec2, which neither the wire name nor a mechanical split of
// it contains anywhere.
func cfnCandidates(provider, cfnType string) (wireNames []string, t sdkTarget, ok bool) {
	parts := strings.Split(cfnType, "::")
	if len(parts) != 3 {
		return nil, sdkTarget{}, false
	}
	service := strings.ToLower(parts[1])
	local := snakeCase(parts[2])
	typeName, err := pascalCase(local)
	if err != nil {
		return nil, sdkTarget{}, false
	}
	prefixed := provider + "_" + snakeCase(parts[1]) + "_" + local
	bare := provider + "_" + local
	return []string{prefixed, bare}, sdkTarget{Service: service, LocalWire: local, TypeName: typeName}, true
}

// snakeCase lower-snakes a CamelCase CloudFormation segment, matching
// the wire names the dynamic provider itself derives.
func snakeCase(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if r >= 'A' && r <= 'Z' {
			prevLower := i > 0 && (runes[i-1] >= 'a' && runes[i-1] <= 'z' || runes[i-1] >= '0' && runes[i-1] <= '9')
			nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			if i > 0 && (prevLower || nextLower) {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sdkServices returns every distinct service package the blueprint needs
// to import, sorted for determinism.
func sdkServices(targets map[string]sdkTarget) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range targets {
		if !seen[t.Service] {
			seen[t.Service] = true
			out = append(out, t.Service)
		}
	}
	sort.Strings(out)
	return out
}

// providerRootFromSDKSpec returns the package root a published SDK lays
// its service packages out under, which is the provider's own name
// (github.com/ubiquex/ubx-sdk-aws/sdk/go/v3 + "/aws/" + "sqs").
func providerRootFromSDKSpec(spec *SDKSpec) string {
	_, name, _, err := parseSDKProvider(spec.Provider)
	if err != nil {
		return ""
	}
	return name
}

// goModulePathAndVersion splits the sdk.go spec into the module path and
// its required version.
//
// The version is not optional. Emitting a require line with a v0.0.0
// placeholder is exactly the bug UBI-237 found live: every blueprint
// built that way failed to compile against the real published module,
// and only hermetic tests (which append their own local replace over
// whatever the version says) failed to notice.
func goModulePathAndVersion(spec string) (path, version string, err error) {
	at := strings.LastIndex(spec, "@")
	if at < 0 || at == len(spec)-1 {
		return "", "", fmt.Errorf("sdk.go %q must be \"<module path>@<version>\", e.g. \"github.com/ubiquex/ubx-sdk-aws/sdk/go/v3@v3.0.1\" -- a require line needs a real version, not a placeholder", spec)
	}
	return spec[:at], spec[at+1:], nil
}

// tsModulePaths returns one import path per resource type, sorted, since
// TypeScript's published SDK exports a module per resource rather than
// per service.
func tsModulePaths(targets map[string]sdkTarget) []sdkTarget {
	out := make([]sdkTarget, 0, len(targets))
	for _, t := range targets {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].LocalWire < out[j].LocalWire
	})
	return out
}
