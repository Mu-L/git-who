package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/sinclairtarget/git-who/internal/ansi"
	"github.com/sinclairtarget/git-who/internal/concurrent"
	"github.com/sinclairtarget/git-who/internal/format"
	"github.com/sinclairtarget/git-who/internal/git"
	"github.com/sinclairtarget/git-who/internal/tally"
)

const defaultMaxDepth = 100

type printTreeOpts struct {
	mode       tally.TallyMode
	maxDepth   int
	showHidden bool
}

type treeOutputLine struct {
	indent    string
	path      string
	metric    string
	tally     tally.FinalTally
	showLine  bool
	showTally bool
	dimTally  bool
	dimPath   bool
}

func tree(
	revs []string,
	paths []string,
	mode tally.TallyMode,
	depth int,
	showEmail bool,
	showHidden bool,
	countMerges bool,
	since string,
	authors []string,
	nauthors []string,
) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("error running \"tree\": %w", err)
		}
	}()

	logger().Debug(
		"called tree()",
		"revs",
		revs,
		"paths",
		paths,
		"mode",
		mode,
		"depth",
		depth,
		"showEmail",
		showEmail,
		"showHidden",
		showHidden,
		"countMerges",
		countMerges,
		"since",
		since,
		"authors",
		authors,
		"nauthors",
		nauthors,
	)

	wtreeset, err := git.WorkingTreeFiles(paths)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gitRootPath, err := git.GetRoot()
	if err != nil {
		return err
	}

	filters := git.LogFilters{
		Since:    since,
		Authors:  authors,
		Nauthors: nauthors,
	}

	tallyOpts := tally.TallyOpts{Mode: mode, CountMerges: countMerges}
	if showEmail {
		tallyOpts.Key = func(c git.Commit) string { return c.AuthorEmail }
	} else {
		tallyOpts.Key = func(c git.Commit) string { return c.AuthorName }
	}

	var root *tally.TreeNode
	if runtime.GOMAXPROCS(0) > 1 {
		root, err = concurrent.TallyCommitsTree(
			ctx,
			revs,
			paths,
			filters,
			tallyOpts,
			wtreeset,
			gitRootPath,
		)

		if err == tally.EmptyTreeErr {
			logger().Debug("Tree was empty.")
			return nil
		}

		if err != nil {
			return err
		}
	} else {
		commits, closer, innererr := git.CommitsWithOpts(
			ctx,
			revs,
			paths,
			filters,
			true,
		)
		if innererr != nil {
			return innererr
		}

		root, innererr = tally.TallyCommitsTree(
			commits,
			tallyOpts,
			wtreeset,
			gitRootPath,
		)
		if innererr == tally.EmptyTreeErr {
			logger().Debug("Tree was empty.")
			return nil
		}

		if innererr != nil {
			return fmt.Errorf("failed to tally commits: %w", innererr)
		}

		innererr = closer()
		if innererr != nil {
			return innererr
		}
	}

	root = root.Rank(mode)

	maxDepth := depth
	if depth == 0 {
		maxDepth = defaultMaxDepth
	}

	opts := printTreeOpts{
		maxDepth:   maxDepth,
		mode:       mode,
		showHidden: showHidden,
	}
	lines := toLines(root, ".", 0, "", []bool{}, opts, []treeOutputLine{})
	printTree(lines, showEmail)
	return nil
}

// Recursively descend tree, turning tree nodes into output lines.
func toLines(
	node *tally.TreeNode,
	path string,
	depth int,
	lastAuthor string,
	isFinalChild []bool,
	opts printTreeOpts,
	lines []treeOutputLine,
) []treeOutputLine {
	if path == tally.NoDiffPathname {
		return lines
	}

	if depth > opts.maxDepth {
		return lines
	}

	if depth < opts.maxDepth && len(node.Children) == 1 {
		// Path ellision
		for k, v := range node.Children {
			lines = toLines(
				v,
				filepath.Join(path, k),
				depth+1,
				lastAuthor,
				isFinalChild,
				opts,
				lines,
			)
		}
		return lines
	}

	var line treeOutputLine

	var indentBuilder strings.Builder
	for i, isFinal := range isFinalChild {
		if i < len(isFinalChild)-1 {
			if isFinal {
				fmt.Fprintf(&indentBuilder, "    ")
			} else {
				fmt.Fprintf(&indentBuilder, "│   ")
			}
		} else {
			if isFinal {
				fmt.Fprintf(&indentBuilder, "└── ")
			} else {
				fmt.Fprintf(&indentBuilder, "├── ")
			}
		}
	}
	line.indent = indentBuilder.String()

	line.path = path
	if len(node.Children) > 0 {
		// Have a directory
		line.path = path + string(os.PathSeparator)
	}

	line.tally = node.Tally
	line.metric = fmtTallyMetric(node.Tally, opts)
	line.showLine = node.InWorkTree || opts.showHidden
	line.dimTally = len(node.Children) > 0
	line.dimPath = !node.InWorkTree

	newAuthor := node.Tally.AuthorEmail != lastAuthor
	line.showTally = opts.showHidden || newAuthor || len(node.Children) > 0

	lines = append(lines, line)

	childPaths := slices.SortedFunc(
		maps.Keys(node.Children),
		func(a, b string) int {
			// Show directories first
			aHasChildren := len(node.Children[a].Children) > 0
			bHasChildren := len(node.Children[b].Children) > 0

			if aHasChildren == bHasChildren {
				return strings.Compare(a, b) // Sort alphabetically
			} else if aHasChildren {
				return -1
			} else {
				return 1
			}
		},
	)

	// Find last non-hidden child
	finalChildIndex := 0
	for i, p := range childPaths {
		child := node.Children[p]
		if child.InWorkTree || opts.showHidden {
			finalChildIndex = i
		}
	}

	for i, p := range childPaths {
		child := node.Children[p]
		lines = toLines(
			child,
			p,
			depth+1,
			node.Tally.AuthorEmail,
			append(isFinalChild, i == finalChildIndex),
			opts,
			lines,
		)
	}

	return lines
}

func fmtTallyMetric(t tally.FinalTally, opts printTreeOpts) string {
	switch opts.mode {
	case tally.CommitMode:
		return fmt.Sprintf("(%s)", format.Number(t.Commits))
	case tally.FilesMode:
		return fmt.Sprintf("(%s)", format.Number(t.FileCount))
	case tally.LinesMode:
		return fmt.Sprintf(
			"(%s%s%s / %s%s%s)",
			ansi.Green,
			format.Number(t.LinesAdded),
			ansi.DefaultColor,
			ansi.Red,
			format.Number(t.LinesRemoved),
			ansi.DefaultColor,
		)
	case tally.LastModifiedMode:
		return fmt.Sprintf(
			"(%s)",
			format.RelativeTime(progStart, t.LastCommitTime),
		)
	default:
		panic("unrecognized mode in switch")
	}
}

func printTree(lines []treeOutputLine, showEmail bool) {
	longest := 0
	for _, line := range lines {
		indentLen := utf8.RuneCountInString(line.indent)
		pathLen := utf8.RuneCountInString(line.path)
		if indentLen+pathLen > longest {
			longest = indentLen + pathLen
		}
	}

	tallyStart := longest + 4 // Use at least 4 "." to separate path from tally

	for _, line := range lines {
		if !line.showLine {
			continue
		}

		var path string
		if line.dimPath {
			path = fmt.Sprintf("%s%s%s", ansi.Dim, line.path, ansi.Reset)
		} else {
			path = line.path
		}

		if !line.showTally {
			fmt.Printf("%s%s\n", line.indent, path)
			continue
		}

		var author string
		if showEmail {
			author = format.Abbrev(format.GitEmail(line.tally.AuthorEmail), 25)
		} else {
			author = format.Abbrev(line.tally.AuthorName, 25)
		}

		indentLen := utf8.RuneCountInString(line.indent)
		pathLen := utf8.RuneCountInString(line.path)
		separator := strings.Repeat(".", tallyStart-indentLen-pathLen)

		if line.dimTally {
			fmt.Printf(
				"%s%s%s%s%s%s %s\n",
				line.indent,
				path,
				ansi.Dim,
				separator,
				ansi.Reset,
				author,
				line.metric,
			)
		} else {
			fmt.Printf(
				"%s%s%s%s%s %s%s\n",
				line.indent,
				path,
				ansi.Dim,
				separator,
				author,
				line.metric,
				ansi.Reset,
			)
		}
	}
}
