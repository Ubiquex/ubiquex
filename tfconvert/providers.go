package tfconvert

import (
	"sort"

	"github.com/zclconf/go-cty/cty"
)

// convertProviders reads every `terraform { required_providers { ... } }`
// block's own declared provider pins -- never consulted during
// translation itself (no schema fetch, matching `ubx blueprint build`'s
// own "no provider schema fetch, deliberately" precedent, docs/
// blueprint.md), only surfaced on Result for UBI-125's own item 7 post-
// conversion conformance check ("run the converted blueprint's own build
// against the SAME provider version the original module targeted").
// Best-effort: a provider entry whose own source/version isn't a plain
// literal (the overwhelming common real-world shape) is silently
// skipped -- this is informational metadata, not something a Question is
// worth raising over.
func (c *converter) convertProviders() {
	for _, tb := range c.mod.providers {
		for _, nb := range tb.Body.Blocks {
			if nb.Type != "required_providers" {
				continue
			}
			names := make([]string, 0, len(nb.Body.Attributes))
			for name := range nb.Body.Attributes {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				v, diags := nb.Body.Attributes[name].Expr.Value(nil)
				if diags.HasErrors() || v.IsNull() || !v.Type().IsObjectType() {
					continue
				}
				rp := RequiredProvider{LocalName: name}
				if v.Type().HasAttribute("source") {
					if s := v.GetAttr("source"); s.Type() == cty.String && !s.IsNull() {
						rp.Source = s.AsString()
					}
				}
				if v.Type().HasAttribute("version") {
					if s := v.GetAttr("version"); s.Type() == cty.String && !s.IsNull() {
						rp.Version = s.AsString()
					}
				}
				c.providers = append(c.providers, rp)
			}
		}
	}
}
