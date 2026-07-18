package cli

import (
	"fmt"
)

// userGlobalAllowedKeys is ~/.ubx/config's own allowlist (UBI-32 Arc B
// addendum, docs/architecture.md -- "User-global ~/.ubx/config"): the
// only top-level keys ever accepted from a user-global file. Everything
// else -- a real project-truth key (stack, providers, provider_configs,
// ledger, the reserved future intent) or simply an unrecognized one --
// is a hard error, never a warning: user-global config sits outside the
// cascade proper specifically so a checkout resolves identically on
// every machine, and a project-truth key silently leaking in from $HOME
// would be exactly the correctness problem that exists to prevent.
//
// init_format is the one entry today -- ubx init's own default write
// format, a genuinely personal (not project) preference. Add to this map
// deliberately, one at a time, as new personal-preference surfaces are
// designed; never widen it to "everything not explicitly forbidden."
var userGlobalAllowedKeys = map[string]bool{
	"init_format": true,
}

// loadUserGlobalLayer discovers and parses ~/.ubx/config* (the same
// three-format discovery order every other cascade directory uses, via
// pickConfigFile), enforcing userGlobalAllowedKeys against every
// top-level key found. Returns a nil tree and empty path if userHomeDir
// fails or no user-global config file exists -- both are "nothing to
// consult," never an error; only a real, present file with a
// disallowed key is. Deliberately no warnOut parameter, unlike the
// project cascade's own per-layer parsing: a disallowed key here is
// always a hard error, never a warning, so there's nothing to warn about
// on the way to succeeding.
//
// alreadyConsumed is the cascade walk's own layers (nil if there's no
// cascade context at all, e.g. ubx init's own standalone check): if
// $HOME itself turned out to be the cascade's own ceiling (a project run
// directly from $HOME, with no separate repo structure above it),
// $HOME's config was already read and folded in as an ordinary,
// unrestricted cascade layer -- consulting it a *second* time here,
// under the personal-preference-only allowlist, would wrongly reject a
// project-truth key that was never actually a "user-global" concern at
// all. Matching the file path against what the cascade already consumed
// is what tells these two genuinely different cases apart.
func loadUserGlobalLayer(alreadyConsumed []cascadeLayer) (genericTree, string, error) {
	home, err := userHomeDir()
	if err != nil || home == "" {
		return nil, "", nil
	}
	file := pickConfigFile(home)
	if file == "" {
		return nil, "", nil
	}
	for _, l := range alreadyConsumed {
		if l.file == file {
			return nil, "", nil
		}
	}
	tree, err := parseGenericFile(file)
	if err != nil {
		return nil, "", fmt.Errorf("user-global config: parse %s: %w", file, err)
	}
	for _, k := range sortedKeys(tree) {
		if !userGlobalAllowedKeys[k] {
			return nil, "", fmt.Errorf(
				"user-global config: %s: %q is not allowed here -- user-global config may only supply personal-preference keys (currently: init_format), never project-truth keys (stack, providers, provider_configs, ledger, ...), so a checkout resolves identically on every machine; move %q to the project's own .ubx/config instead",
				file, k, k)
		}
	}
	return tree, file, nil
}

// userGlobalInitFormat returns ~/.ubx/config's own init_format value, if
// set -- ubx init's own fallback default (after --format itself, before
// the hardcoded "hcl") for which format an operator personally prefers
// to hand-edit. "" if no user-global config exists, or it doesn't set
// this key. No cascade context of its own (ubx init never walks a
// project cascade to find this), so nothing has been "already consumed."
func userGlobalInitFormat() (string, error) {
	tree, _, err := loadUserGlobalLayer(nil)
	if err != nil {
		return "", err
	}
	v, _ := tree["init_format"].(string)
	return v, nil
}
