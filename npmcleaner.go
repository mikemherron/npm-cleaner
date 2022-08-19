package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"time"
)

const NodeModules = "node_modules"

// Start with platform '.'
var DefaultStartDir = "."

const HOME = "HOME"
const WINHOME = "HOMEPATH"

func platformSetup() {
	if runtime.GOOS == "windows" {
		DefaultStartDir = os.Getenv(WINHOME)
	} else if runtime.GOOS == "linux" {
		DefaultStartDir = os.Getenv(HOME)
	} else {
		val, ok := os.LookupEnv(HOME)
		if ok {
			DefaultStartDir = val
		}
	}
}

func getConfig() *Config {
	platformSetup()

	deleteFlag := flag.Bool("delete", false, "set to delete found folders")
	fromDirFlag := flag.String("from", DefaultStartDir, "set starting directory")
	mbThresh := flag.Int("mbthresh", DefaultMbGreater, "set mb size threshold")
	older := flag.Int("older", DefaultDaysAgo, "examine folders older than (days)")
	limit := flag.Int("limit", DefaultLimit, "limit to this many folders")
	flag.Parse()

	c := newConfig(*deleteFlag, *fromDirFlag, *mbThresh, *older, *limit)

	fmt.Printf("Config\n======\n")
	fmt.Printf("Start Path:        %s\n", c.fromDir)
	fmt.Printf("Delete:            %v\n", c.delete)
	fmt.Printf("Older than (days): %v\n", c.daysAgo)
	fmt.Printf("MB Threshold:      %v\n", c.mbGreater)
	fmt.Printf("Folders limit:     %v\n", c.limit)
	fmt.Printf("\n")

	return c
}

func main() {
	c := getConfig()

	results, err := run(c)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %s", err)
		os.Exit(1)
	}

	if len(results.folders) == 0 {
		fmt.Printf("No results found\n")
		return
	}

	results.print(c)
	if !c.delete {
		fmt.Printf("Run with -delete to delete these folders\n")
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

func (r *Results) print(c *Config) {
	longestPath := 0
	for _, f := range r.folders {
		if len(f.path) > longestPath {
			longestPath = len(f.path)
		}
	}

	longestPath++

	fmtStringRows := "%-" + strconv.Itoa(longestPath) + "s|%20d |%15dMB\n"
	fmtStringHead := "%-" + strconv.Itoa(longestPath) + "s|%20s |%17s\n"

	fmt.Printf(fmtStringHead, "Path", "Modified Days Ago", "Size MB")
	for _, f := range r.folders {
		fmt.Printf(fmtStringRows, f.path, f.modDaysAgo, f.sizeMb)
	}

	fmt.Printf("Total Size: %vMB\n", r.totalSizeMb)
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

func newConfig(delete bool, fromDir string, mbThresh int, older int, limit int) *Config {
	return &Config{
		daysAgo:   older,
		mbGreater: mbThresh,
		limit:     limit,
		fromDir:   fromDir,
		delete:    delete,
	}
}

var reachedMax = errors.New("reached max found")

func run(c *Config) (*Results, error) {
	haveSkipped := false

	results := newResults()
	err := filepath.WalkDir(c.fromDir, func(path string, d fs.DirEntry, err error) error {
		if !d.IsDir() {
			return nil
		}

		if filepath.Base(path) == NodeModules {
			modDaysAgo, err := latestModifiedFile(filepath.Dir(path))
			if err != nil {
				return err
			}

			if modDaysAgo < c.daysAgo {
				fmt.Printf("Skipping (age): (%d) %s\n", modDaysAgo, path)
				haveSkipped = true
				return fs.SkipDir
			}

			sizeMb, err := folderSizeMb(path)
			if err != nil {
				return err
			}

			if sizeMb < c.mbGreater {
				haveSkipped = true
				fmt.Printf("Skipping (size): (%d) %s\n", sizeMb, path)
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

	if haveSkipped {
		fmt.Printf("\n")
	}

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
