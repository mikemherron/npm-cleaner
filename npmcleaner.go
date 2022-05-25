package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

const NodeModules = "node_modules"

var excludeFolders = []*regexp.Regexp{
	//Folders starting with .
	matchFolders(fmt.Sprintf("%s.+?", regexp.QuoteMeta("."))),
	matchFolders("AppData"),
	matchFolders("Program Files"),
}

var separatorEscaped = regexp.QuoteMeta(string(filepath.Separator))

func matchFolders(folderName string) *regexp.Regexp {
	regEx := fmt.Sprintf(".*?%s%s%s.*?",
		separatorEscaped, folderName, separatorEscaped)

	return regexp.MustCompile(regEx)
}

func main() {
	deleteFlag := flag.Bool("delete", false, "set to delete found folders")
	flag.Parse()

	c := newConfig(*deleteFlag)
	results, err := run(c)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %s", err)
		os.Exit(1)
	}

	if len(results.folders) == 0 {
		fmt.Printf("No results found\n")
		return
	}

	results.print()
	if !c.delete {
		fmt.Printf("Run with -delete to delete these folders")
	} else {
		for _, f := range results.folders {
			fmt.Printf("Deleting %s...", f.path)
			err := os.RemoveAll(f.path)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "error deleting %s: %s, exiting", f.path, err)
				os.Exit(1)
			}
			fmt.Printf("OK\n")
		}
	}
}

func newResults() *Results {
	return &Results{
		folders: make([]*Folder, 0, DefaultLimit),
	}
}

type Results struct {
	folders     []*Folder
	totalSizeMb int
}

func (r *Results) add(f *Folder) {
	r.totalSizeMb += f.sizeMb
	r.folders = append(r.folders, f)
}

func (r *Results) sort() {
	sort.Slice(r.folders, func(i, j int) bool {
		return r.folders[i].sizeMb > r.folders[j].sizeMb
	})
}

func (r *Results) print() {
	longestPath := 0
	for _, f := range r.folders {
		if len(f.path) > longestPath {
			longestPath = len(f.path)
		}
	}

	longestPath++

	fmtStringRows := "%-" + strconv.Itoa(longestPath) + "s|%20d|%15dMB\n"
	fmtStringHead := "%-" + strconv.Itoa(longestPath) + "s|%20s|%17s\n"

	fmt.Printf(fmtStringHead, "Path", "Modified Days Ago", "Size MB")
	for _, f := range r.folders {
		fmt.Printf(fmtStringRows, f.path, f.modDaysAgo, f.sizeMb)
	}
}

type Folder struct {
	path       string
	sizeMb     int
	modDaysAgo int
}

type Config struct {
	daysAgo   int
	mbGreater int
	limit     int
	fromDir   string
	delete    bool
}

const (
	DefaultLimit     = 10
	DefaultMbGreater = 50
	DefaultDaysAgo   = 7
)

var DefaultStartDir = string(filepath.Separator)

func newConfig(delete bool) *Config {
	return &Config{
		daysAgo:   DefaultDaysAgo,
		mbGreater: DefaultMbGreater,
		limit:     DefaultLimit,
		fromDir:   DefaultStartDir,
		delete:    delete,
	}
}

var reachedMax = errors.New("reached max found")

func run(c *Config) (*Results, error) {
	results := newResults()
	err := filepath.WalkDir(c.fromDir, func(path string, d fs.DirEntry, err error) error {
		if !d.IsDir() {
			return nil
		}

		for _, excludePattern := range excludeFolders {
			if excludePattern.MatchString(path) {
				return fs.SkipDir
			}
		}

		if filepath.Base(path) == NodeModules {
			modDaysAgo, err := latestModifiedFile(filepath.Dir(path))
			if err != nil {
				return err
			}

			if modDaysAgo < c.daysAgo {
				return fs.SkipDir
			}

			sizeMb, err := folderSizeMb(path)
			if err != nil {
				return err
			}

			if sizeMb < c.mbGreater {
				return fs.SkipDir
			}

			folder := &Folder{
				path:       path,
				sizeMb:     sizeMb,
				modDaysAgo: modDaysAgo,
			}

			results.add(folder)
			if len(results.folders) == c.limit {
				return reachedMax
			}

			return fs.SkipDir
		}

		return nil
	})

	if err != nil && err != reachedMax {
		return nil, err
	}

	results.sort()
	return results, nil
}

func latestModifiedFile(p string) (int, error) {
	lastModified := time.Time{}
	err := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			if filepath.Base(path) == NodeModules {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		modTime := info.ModTime()
		if modTime.After(lastModified) {
			lastModified = modTime
		}

		return nil
	})

	if err != nil {
		return -1, err
	}

	return daysSince(lastModified), nil
}

func daysSince(t time.Time) int {
	return int(time.Now().Unix()-t.Unix()) / 60 / 60 / 24
}

func folderSizeMb(p string) (int, error) {
	var sizeBytes int64
	err := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		sizeBytes += info.Size()
		return nil
	})

	if err != nil {
		return 0, err
	}

	return bytesToMb(sizeBytes), nil
}

func bytesToMb(b int64) int {
	return int(b / 1024 / 1024)
}
