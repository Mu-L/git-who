/*
* Wraps access to data needed from Git.
*
* We invoke Git directly as a subprocess and parse the output rather than using
* git2go/libgit2.
 */
package git

import (
	"context"
	"fmt"
	"io"
	"iter"
	"strings"
	"time"
)

type Commit struct {
	Hash        string
	ShortHash   string
	IsMerge     bool
	AuthorName  string
	AuthorEmail string
	Date        time.Time
	FileDiffs   []FileDiff
}

func (c Commit) Name() string {
	if c.ShortHash != "" {
		return c.ShortHash
	} else if c.Hash != "" {
		return c.Hash
	} else {
		return "unknown"
	}
}

func (c Commit) String() string {
	return fmt.Sprintf(
		"{ hash:%s author:%s <%s> date:%s merge:%v }",
		c.Name(),
		c.AuthorName,
		c.AuthorEmail,
		c.Date.Format("Jan 2, 2006"),
		c.IsMerge,
	)
}

// A file that was changed in a Commit.
type FileDiff struct {
	Path         string
	LinesAdded   int
	LinesRemoved int
}

func (d FileDiff) String() string {
	return fmt.Sprintf(
		"{ path:\"%s\" added:%d removed:%d }",
		d.Path,
		d.LinesAdded,
		d.LinesRemoved,
	)
}

// Returns an iterator over commits identified by the given revisions and paths.
//
// Also returns a closer() function for cleanup and an error when encountered.
func CommitsWithOpts(
	ctx context.Context,
	revs []string,
	paths []string,
	filters LogFilters,
	populateDiffs bool,
) (
	iter.Seq2[Commit, error],
	func() error,
	error,
) {
	subprocess, err := RunLog(ctx, revs, paths, filters, populateDiffs)
	if err != nil {
		return nil, nil, err
	}

	lines := subprocess.StdoutLines()
	commits := ParseCommits(lines)

	closer := func() error {
		return subprocess.Wait()
	}
	return commits, closer, nil
}

func RevList(
	ctx context.Context,
	revranges []string,
	paths []string,
	filters LogFilters,
) (_ []string, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("error getting full rev list: %w", err)
		}
	}()

	revs := []string{}

	subprocess, err := RunRevList(ctx, revranges, paths, filters)
	if err != nil {
		return revs, err
	}

	lines := subprocess.StdoutLines()
	for line, err := range lines {
		if err != nil {
			return revs, err
		}

		revs = append(revs, line)
	}

	err = subprocess.Wait()
	if err != nil {
		return revs, err
	}

	return revs, nil
}

func GetRoot() (_ string, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf(
				"failed to run git rev-parse --show-toplevel: %w",
				err,
			)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	args := []string{"rev-parse", "--show-toplevel"}
	subprocess, err := run(ctx, args, false)
	if err != nil {
		return "", err
	}

	b, err := io.ReadAll(subprocess.stdout)
	if err != nil {
		return "", err
	}

	err = subprocess.Wait()
	if err != nil {
		return "", err
	}

	root := strings.TrimSpace(string(b))
	return root, nil
}

// Returns all paths in the working tree under the given paths.
func WorkingTreeFiles(paths []string) (_ map[string]bool, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("error getting tree files: %w", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wtreeset := map[string]bool{}

	subprocess, err := RunLsFiles(ctx, paths)
	if err != nil {
		return wtreeset, err
	}

	lines := subprocess.StdoutLines()
	for line, err := range lines {
		if err != nil {
			return wtreeset, err
		}
		wtreeset[strings.TrimSpace(line)] = true
	}

	err = subprocess.Wait()
	if err != nil {
		return wtreeset, err
	}

	return wtreeset, nil
}

func LimitDiffsByPath(
	commits iter.Seq2[Commit, error],
	paths []string,
) iter.Seq2[Commit, error] {
	if len(paths) == 0 {
		return commits
	}

	return func(yield func(Commit, error) bool) {
		for commit, err := range commits {
			if err != nil {
				yield(commit, err)
				return
			}

			filtered := []FileDiff{}
			for _, diff := range commit.FileDiffs {
				for _, p := range paths {
					if strings.HasPrefix(diff.Path, p) {
						filtered = append(filtered, diff)
						break
					}
				}
			}

			commit.FileDiffs = filtered
			yield(commit, nil)
		}
	}
}
