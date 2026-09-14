package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/ubiquex/ubiquex/blueprint"
)

// newBlueprintListCmd reports what is in the local blueprint cache.
//
// It exists because the cache became content-addressed. Storage keyed by
// content hash is the only sound layout (a declaration string is a
// mutable pointer, and keying storage on one meant a repointed tag left
// a machine on old content indefinitely), but a directory of bare
// hashes says nothing about what any of it is. Without something to name
// the entries, the layout that fixed the correctness problem would have
// been a straight regression in legibility.
//
// So the origin index and this command arrived together, deliberately.
// An index nothing reads is a file that rots, and a hash-named directory
// nothing can explain is worse than the unsound layout it replaced.
//
// Reporting only. Removing anything is a separate command that does not
// exist yet, and the fields this prints (last used, size) are chosen so
// that it can.
func newBlueprintListCmd() *cobra.Command {
	var fullHashes bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List blueprints in the local content-addressed cache",
		Long: `Lists every blueprint in the local cache (~/.ubx/blueprints), by content hash,
with the sources seen to produce it.

The cache is content-addressed: one directory per content hash, never per name or
tag. A tag is a pointer that can be repointed at any time, so it names storage for
nobody, and a stack resolves a tag to a content hash through its own
.ubx/blueprints.lock.

One content hash can have several sources, which is ordinary: two tags pointing at
the same bytes, or a tag that has since moved on but explains why the content is
here at all.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := blueprint.LoadIndex()
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint list: %w", err)}
			}
			out := cmd.OutOrStdout()
			st := newStylerFull(cmd, fullHashes)

			entries := idx.SortedIndexEntries()
			if len(entries) == 0 {
				fmt.Fprintln(out, "no blueprints cached -- `ubx plan` pulls and caches every blueprint a stack declares")
				return nil
			}

			var total int64
			for _, e := range entries {
				total += e.Entry.SizeBytes

				name := e.Entry.Name
				if name == "" {
					// An entry whose manifest carried no name is
					// printable rather than hidden: it is still taking up
					// disk, and a listing that silently omits things
					// cannot be used to reason about what is there.
					name = "(unnamed)"
				}
				fmt.Fprintf(out, "\n  %s  %s\n", st.Bold(name), st.Hash(displayHash(e.Hash, fullHashes)))
				fmt.Fprintf(out, "    %s\n", st.Dim(fmt.Sprintf("%d file(s) · %s · last used %s",
					e.Entry.FileCount, humanBytes(e.Entry.SizeBytes), lastUsedAge(e.Entry.LastUsed))))
				for _, src := range e.Entry.Sources {
					fmt.Fprintf(out, "    %s %s\n", st.Dim("from"), src)
				}
			}
			fmt.Fprintf(out, "\n%s\n", st.Dim(fmt.Sprintf("%d blueprint(s) · %s total", len(entries), humanBytes(total))))
			return nil
		},
	}
	cmd.Flags().BoolVar(&fullHashes, "full-hashes", false, "render every hash in full instead of the default 12-char short form")
	return cmd
}

// lastUsedAge renders an index entry's RFC3339 timestamp through the
// same humanAge every other elapsed-time line in this package uses,
// rather than a second spelling of "how long ago".
func lastUsedAge(ts string) string {
	if ts == "" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "unknown"
	}
	return humanAge(t)
}
