/*
* Wraps access to data needed from Git.
*
* We invoke Git directly as a subprocess and parse the output rather than using
* git2go/libgit2.
 */
package git

import (
	"fmt"
	"iter"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var fileRenameRegexp *regexp.Regexp

// A file that was changed in a Commit.
type FileDiff struct {
	Path         string
	LinesAdded   int
	LinesRemoved int
	MoveDest     string // Empty unless the file was renamed
}

type Commit struct {
	Hash        string
	ShortHash   string
	AuthorName  string
	AuthorEmail string
	Date        time.Time
	Subject     string
	FileDiffs   []FileDiff
}

func (c Commit) String() string {
	return fmt.Sprintf(
		"{ hash:%s email:%s date:%s files:%d }",
		c.ShortHash,
		c.AuthorEmail,
		c.Date,
		len(c.FileDiffs),
	)
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

func init() {
	fileRenameRegexp = regexp.MustCompile(`{"?(.+)"? => "?(.+)"?}`)
}

// Parse the path given by git log --numstat for a file diff.
//
// Sometimes this looks like /foo/{bar => bim}/baz.txt when a file is moved.
func parseDiffPath(path string) (outPath string, dst string, err error) {
	var pathBuilder strings.Builder
	var dstBuilder strings.Builder

	parts := strings.Split(path, string(os.PathSeparator))
	for i, part := range parts {
		if strings.Contains(part, "=>") {
			matches := fileRenameRegexp.FindStringSubmatch(part)
			if matches == nil || len(matches) != 3 {
				return "", "", fmt.Errorf(
					"error parsing rename from \"%s\" in path \"%s\"",
					part,
					path,
				)
			}

			fmt.Fprintf(&pathBuilder, matches[1])
			fmt.Fprintf(&dstBuilder, matches[2])
		} else {
			fmt.Fprintf(&pathBuilder, part)
			fmt.Fprintf(&dstBuilder, part)
		}

		if i < len(parts)-1 {
			fmt.Fprintf(&pathBuilder, string(os.PathSeparator))
			fmt.Fprintf(&dstBuilder, string(os.PathSeparator))
		}
	}

	outPath = pathBuilder.String()
	dst = dstBuilder.String()
	if dst == outPath {
		dst = ""
	}

	return outPath, dst, nil
}

func parseFileDiff(line string) (diff FileDiff, err error) {
	parts := strings.Split(line, "\t")
	if len(parts) != 3 {
		return diff, fmt.Errorf("could not parse file diff: %s", line)
	}

	if parts[0] != "-" {
		added, err := strconv.Atoi(parts[0])
		if err != nil {
			return diff,
				fmt.Errorf("could not parse %s as int on line \"%s\": %w",
					parts[0],
					line,
					err,
				)
		}

		diff.LinesAdded = added
	}

	if parts[1] != "-" {
		removed, err := strconv.Atoi(parts[1])
		if err != nil {
			return diff,
				fmt.Errorf("could not parse %s as int on line \"%s\": %w",
					parts[1],
					line,
					err,
				)
		}
		diff.LinesRemoved = removed
	}

	diff.Path, diff.MoveDest, err = parseDiffPath(parts[2])
	if err != nil {
		return diff, fmt.Errorf(
			"could not parse path part of file diff on line \"%s\": %w",
			line,
			err,
		)
	}

	return diff, nil
}

// Turns an iterator over lines from git log into an iterator of commits
func parseCommits(lines iter.Seq2[string, error]) iter.Seq2[Commit, error] {
	return func(yield func(Commit, error) bool) {
		commit := Commit{FileDiffs: make([]FileDiff, 0)}
		linesThisCommit := 0

		for line, err := range lines {
			if err != nil {
				yield(
					commit,
					fmt.Errorf(
						"error reading commit %s: %w",
						commit.Name(),
						err,
					),
				)
				return
			}

			if len(line) == 0 {
				logger().Debug("yielding parsed commit", "hash", commit.Name())
				if !yield(commit, nil) {
					return
				}

				commit = Commit{FileDiffs: make([]FileDiff, 0)}
				linesThisCommit = 0
				continue
			}

			switch {
			case linesThisCommit == 0:
				commit.Hash = line
			case linesThisCommit == 1:
				commit.ShortHash = line
			case linesThisCommit == 2:
				commit.AuthorName = line
			case linesThisCommit == 3:
				commit.AuthorEmail = line
			case linesThisCommit == 4:
				i, err := strconv.Atoi(line)
				if err != nil {
					yield(
						commit,
						fmt.Errorf(
							"error parsing date from commit %s: %w",
							commit.Name(),
							err,
						),
					)
					return
				}

				commit.Date = time.Unix(int64(i), 0)
			case linesThisCommit == 5:
				commit.Subject = line
			case linesThisCommit >= 6:
				diff, err := parseFileDiff(line)
				if err != nil {
					yield(
						commit,
						fmt.Errorf(
							"error parsing file diffs from commit %s: %w",
							commit.Name(),
							err,
						),
					)
					return
				}
				commit.FileDiffs = append(commit.FileDiffs, diff)
			}

			linesThisCommit += 1
		}

		if linesThisCommit > 0 {
			logger().Debug("yielding parsed commit", "hash", commit.Name())
			yield(commit, nil)
		}
	}
}

// Returns an iterator over commits identified by the given revisions and paths.
//
// Also returns a closer() function for cleanup and an error when encountered.
func Commits(revs []string, paths []string) (
	iter.Seq2[Commit, error],
	func() error,
	error,
) {
	subprocess, err := RunLog(revs, paths)
	if err != nil {
		return nil, nil, err
	}

	lines := subprocess.StdoutLines()
	commits := parseCommits(lines)

	closer := func() error {
		return subprocess.Wait()
	}
	return commits, closer, nil
}
