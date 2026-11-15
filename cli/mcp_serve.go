package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/provider"
)

// servedStack is one stack `ubx mcp --serve` was started with (UBI-295).
//
// The agent-facing change tools (plan, apply) and the read tools that
// feed them (stacks, state, schema) see only these. An agent addresses a
// stack by name and never by path, so it cannot point the server at a
// stack nobody configured it to serve. Widening that later is painless;
// narrowing it would break whatever had come to rely on it. The nine
// older tools keep taking a ledger_dir per call, unchanged.
type servedStack struct {
	Name string
	Root string
	Cfg  *Config

	schemaMu sync.Mutex
	schemas  []stackSchema
}

// servedStacks is the server's fixed set, by name.
type servedStacks struct {
	byName map[string]*servedStack
}

// loadServedStacks resolves each --serve root: it must be a ubx root (a
// directory holding .ubx/), and its .ubx/config must name its stack,
// since that name is the only handle an agent gets. Two roots naming the
// same stack are refused rather than one silently shadowing the other.
func loadServedStacks(roots []string, warnOut io.Writer) (*servedStacks, error) {
	out := &servedStacks{byName: map[string]*servedStack{}}
	for _, r := range roots {
		root, err := resolveLedgerDir(r)
		if err != nil {
			return nil, fmt.Errorf("--serve %s: %w", r, err)
		}
		cfg, err := loadConfigFromDir(root, warnOut)
		if err != nil {
			return nil, fmt.Errorf("--serve %s: %w", r, err)
		}
		if cfg.Stack == "" {
			return nil, fmt.Errorf("--serve %s: its .ubx/config names no stack. Set `stack = \"<name>\"` there: an agent addresses a served stack by that name and never by path", root)
		}
		if prev, ok := out.byName[cfg.Stack]; ok {
			return nil, fmt.Errorf("--serve: stack %q is named by both %s and %s", cfg.Stack, prev.Root, root)
		}
		out.byName[cfg.Stack] = &servedStack{Name: cfg.Stack, Root: root, Cfg: cfg}
	}
	return out, nil
}

// names returns the served stack names, sorted.
func (s *servedStacks) names() []string {
	names := make([]string, 0, len(s.byName))
	for n := range s.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// get returns the served stack called name, or an error written for the
// model that has to recover from it.
func (s *servedStacks) get(name string) (*servedStack, error) {
	if len(s.byName) == 0 {
		return nil, errors.New("this server serves no stacks. It has to be started with `ubx mcp --serve <stack-root>`, once per stack; ask the person who configured it to add one")
	}
	if name == "" {
		return nil, fmt.Errorf("stack is required. Served stacks: %s", strings.Join(s.names(), ", "))
	}
	st, ok := s.byName[name]
	if !ok {
		return nil, fmt.Errorf("stack %q is not served here. Served stacks: %s. Call the stacks tool to see them", name, strings.Join(s.names(), ", "))
	}
	return st, nil
}

// openLedger opens st's ledger the same way every CLI verb does.
func (st *servedStack) openLedger(ctx context.Context) (*core.Ledger, func() error, error) {
	return openLedgerForStack(ctx, st.Root, st.Name, st.Cfg)
}

// providerSelection is the single-provider selection st's config makes,
// exactly what applyProviderDefaults would fill in for a CLI verb given
// no flags.
func (st *servedStack) providerSelection() (path, source, version string) {
	switch {
	case st.Cfg.Provider.Path != "":
		return st.Cfg.Provider.Path, "", ""
	case st.Cfg.Provider.Source != "":
		return "", st.Cfg.Provider.Source, st.Cfg.Provider.Version
	}
	return "", "", ""
}

// providerSchemas fetches every declared provider's schema once per
// server process and keeps it: a provider's schema does not change while
// the server runs, and launching a provider per call is the slowest
// thing these tools do.
func (st *servedStack) providerSchemas(ctx context.Context) ([]stackSchema, error) {
	st.schemaMu.Lock()
	defer st.schemaMu.Unlock()
	if st.schemas != nil {
		return st.schemas, nil
	}
	path, source, version := st.providerSelection()
	loaded, err := loadStackSchemas(ctx, st.Cfg, path, source, version)
	if err != nil {
		return nil, fmt.Errorf("stack %s: load provider schema: %w", st.Name, err)
	}
	st.schemas = loaded
	return loaded, nil
}

// --- stacks ---

type stacksToolInput struct{}

type stacksJSON struct {
	Format int              `json:"format"`
	Stacks []stackEntryJSON `json:"stacks"`
}

type stackEntryJSON struct {
	Name      string   `json:"name"`
	Root      string   `json:"root"`
	Head      string   `json:"head"`
	Resources []string `json:"resources"`
}

func registerStacksTool(server *mcp.Server, served *servedStacks) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "stacks",
		Description: "List the stacks this server serves, with each one's current ledger head and the address of every live resource " +
			"(<stack>.<type>.<name>). These are the only stacks state, schema, plan and apply accept; refer to a stack by the name given here. " +
			"Call this first: a modify or destroy in plan has to name an address that exists.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in stacksToolInput) (*mcp.CallToolResult, any, error) {
		out := stacksJSON{Format: jsonFormatVersion, Stacks: []stackEntryJSON{}}
		for _, name := range served.names() {
			st := served.byName[name]
			ledger, closeLedger, err := st.openLedger(ctx)
			if err != nil {
				return nil, nil, fmt.Errorf("stack %s: %w", name, err)
			}
			head, herr := ledger.Head()
			fleet, ferr := ledger.Fleet(name)
			closeLedger()
			if herr != nil {
				return nil, nil, fmt.Errorf("stack %s: %w", name, herr)
			}
			if ferr != nil {
				return nil, nil, fmt.Errorf("stack %s: %w", name, ferr)
			}
			entry := stackEntryJSON{Name: name, Root: st.Root, Head: head, Resources: []string{}}
			for _, f := range fleet {
				entry.Resources = append(entry.Resources, f.Address.String())
			}
			sort.Strings(entry.Resources)
			out.Stacks = append(out.Stacks, entry)
		}
		return nil, out, nil
	})
}

// --- state ---

type stateToolInput struct {
	Stack   string `json:"stack" jsonschema:"a stack name, as the stacks tool lists it"`
	Address string `json:"address,omitempty" jsonschema:"optional: one resource address (<stack>.<type>.<name>) to return the full recorded state of, every attribute and its current value. Omit to list the stack's resources without their values"`
}

// stateJSON is ubx_status's own format-1 payload (cli/status.go), with
// one addition: the folded state of one resource when an address is
// asked for. The payload `ubx status --json` and ubx_status return is
// not changed or forked.
type stateJSON struct {
	statusJSON
	Address string          `json:"address,omitempty"`
	State   json.RawMessage `json:"state,omitempty"`
}

func registerStateTool(server *mcp.Server, served *servedStacks) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "state",
		Description: "Report what a served stack holds now, from its ledger: every live resource, the proposal that last changed it, and when. " +
			"Pass address to also get that one resource's full recorded state (every attribute's current value), which is what a modify " +
			"in plan has to be written against. Same JSON shape as ubx_status, plus address and state. Reads the ledger only; it never " +
			"contacts a provider.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in stateToolInput) (*mcp.CallToolResult, any, error) {
		st, err := served.get(in.Stack)
		if err != nil {
			return nil, nil, err
		}
		status, err := computeStatusJSON(ctx, statusJSONOptions{Config: st.Cfg, LedgerDir: st.Root, Stack: st.Name})
		if err != nil {
			return nil, nil, err
		}
		out := stateJSON{statusJSON: *status}
		if in.Address == "" {
			return nil, out, nil
		}
		addr, ok := core.ParseAddress(in.Address)
		if !ok {
			return nil, nil, fmt.Errorf("address %q is not of the form <stack>.<type>.<name>", in.Address)
		}
		if addr.Stack != st.Name {
			return nil, nil, fmt.Errorf("address %s belongs to stack %q, not %q", in.Address, addr.Stack, st.Name)
		}
		ledger, closeLedger, err := st.openLedger(ctx)
		if err != nil {
			return nil, nil, err
		}
		defer closeLedger()
		folded, found, err := ledger.FoldState(addr)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			return nil, nil, fmt.Errorf("stack %s holds no live resource at %s. Call stacks to see the addresses it does hold", st.Name, in.Address)
		}
		out.Address = addr.String()
		out.State = folded
		return nil, out, nil
	})
}

// --- schema ---

type schemaToolInput struct {
	Stack    string `json:"stack" jsonschema:"a stack name, as the stacks tool lists it"`
	Type     string `json:"type,omitempty" jsonschema:"optional: one resource type (e.g. fake_widget). Omit to list every resource type the stack's providers offer"`
	Provider string `json:"provider,omitempty" jsonschema:"optional: which declared provider (its source, as listed) to look the type up in, needed only when two of the stack's providers both offer the type"`
}

type schemaListJSON struct {
	Format    int                  `json:"format"`
	Stack     string               `json:"stack"`
	Providers []schemaProviderJSON `json:"providers"`
}

type schemaProviderJSON struct {
	Source          string   `json:"source"`
	Version         string   `json:"version,omitempty"`
	ResourceTypes   []string `json:"resource_types"`
	DataSourceTypes []string `json:"data_source_types"`
}

type schemaTypeJSON struct {
	Format   int             `json:"format"`
	Stack    string          `json:"stack"`
	Provider string          `json:"provider"`
	Type     string          `json:"type"`
	Block    schemaBlockJSON `json:"block"`
}

type schemaBlockJSON struct {
	Attributes []schemaAttrJSON   `json:"attributes"`
	Blocks     []schemaNestedJSON `json:"blocks,omitempty"`
}

type schemaAttrJSON struct {
	Name        string           `json:"name"`
	Type        json.RawMessage  `json:"type,omitempty"`
	Required    bool             `json:"required,omitempty"`
	Optional    bool             `json:"optional,omitempty"`
	Computed    bool             `json:"computed,omitempty"`
	Sensitive   bool             `json:"sensitive,omitempty"`
	Description string           `json:"description,omitempty"`
	Nested      []schemaAttrJSON `json:"nested,omitempty"`
}

type schemaNestedJSON struct {
	Name     string          `json:"name"`
	Nesting  string          `json:"nesting"`
	MaxItems int64           `json:"max_items,omitempty"`
	Block    schemaBlockJSON `json:"block"`
}

func registerSchemaTool(server *mcp.Server, served *servedStacks) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "schema",
		Description: "Describe what a served stack's providers accept. With no type: every resource and data source type, per provider. " +
			"With type: that resource type's attributes, each marked required, optional, computed (the provider may set it) and sensitive " +
			"(write it as a {\"$secret\": {...}} reference, never as a value), with nested blocks. Read this before writing a plan, so " +
			"the config you propose matches what the provider takes.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in schemaToolInput) (*mcp.CallToolResult, any, error) {
		st, err := served.get(in.Stack)
		if err != nil {
			return nil, nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		loaded, err := st.providerSchemas(ctx)
		if err != nil {
			return nil, nil, err
		}

		if in.Type == "" {
			out := schemaListJSON{Format: jsonFormatVersion, Stack: st.Name, Providers: []schemaProviderJSON{}}
			for _, l := range loaded {
				out.Providers = append(out.Providers, schemaProviderJSON{
					Source:          l.Source,
					Version:         l.Version,
					ResourceTypes:   sortedSchemaKeys(l.Schemas.Resources),
					DataSourceTypes: sortedSchemaKeys(l.Schemas.DataSources),
				})
			}
			return nil, out, nil
		}

		var matches []stackSchema
		for _, l := range loaded {
			if in.Provider != "" && l.Source != in.Provider {
				continue
			}
			if _, ok := l.Schemas.Resources[in.Type]; ok {
				matches = append(matches, l)
			}
		}
		switch len(matches) {
		case 0:
			if in.Provider != "" {
				return nil, nil, fmt.Errorf("provider %q of stack %s offers no resource type %q. Call schema without type to list what it offers", in.Provider, st.Name, in.Type)
			}
			return nil, nil, fmt.Errorf("no provider of stack %s offers a resource type %q. Call schema without type to list what they offer", st.Name, in.Type)
		case 1:
		default:
			sources := make([]string, len(matches))
			for i, m := range matches {
				sources[i] = m.Source
			}
			return nil, nil, fmt.Errorf("resource type %q is offered by more than one provider of stack %s (%s). Pass provider to choose", in.Type, st.Name, strings.Join(sources, ", "))
		}
		m := matches[0]
		return nil, schemaTypeJSON{
			Format:   jsonFormatVersion,
			Stack:    st.Name,
			Provider: m.Source,
			Type:     in.Type,
			Block:    schemaBlockToJSON(m.Schemas.Resources[in.Type].Block),
		}, nil
	})
}

func sortedSchemaKeys(m map[string]*provider.Schema) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func schemaBlockToJSON(b provider.Block) schemaBlockJSON {
	out := schemaBlockJSON{Attributes: schemaAttrsToJSON(b.Attributes)}
	for _, nb := range b.NestedBlocks {
		out.Blocks = append(out.Blocks, schemaNestedJSON{
			Name:     nb.TypeName,
			Nesting:  nestingName(nb.Nesting),
			MaxItems: nb.MaxItems,
			Block:    schemaBlockToJSON(nb.Block),
		})
	}
	return out
}

func schemaAttrsToJSON(attrs []provider.Attribute) []schemaAttrJSON {
	out := make([]schemaAttrJSON, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, schemaAttrJSON{
			Name:        a.Name,
			Type:        a.Type,
			Required:    a.Required,
			Optional:    a.Optional,
			Computed:    a.Computed,
			Sensitive:   a.Sensitive,
			Description: a.Description,
			Nested:      schemaAttrsToJSON(a.NestedAttributes),
		})
	}
	return out
}

// nestingName is a nested block's nesting mode in the words Terraform's
// own schema documentation uses for it.
func nestingName(m provider.NestingMode) string {
	switch m {
	case provider.NestingSingle:
		return "single"
	case provider.NestingList:
		return "list"
	case provider.NestingSet:
		return "set"
	case provider.NestingMap:
		return "map"
	case provider.NestingGroup:
		return "group"
	}
	return "invalid"
}
