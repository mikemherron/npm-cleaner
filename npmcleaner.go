package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
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

// Start with platform '.'
var DefaultStartDir = "."
var onWindows = false

const HOME = "HOME"
const WINHOME = "HOMEPATH"

func platformSetup() {
	if runtime.GOOS == "windows" {
		DefaultStartDir = os.Getenv(WINHOME)
		onWindows = true
	} else if runtime.GOOS == "linux" {
		DefaultStartDir = os.Getenv(HOME)
	} else {
		val, ok := os.LookupEnv(HOME)
		if ok {
			DefaultStartDir = val
		}
	}
}

func (c Config) String() string {

	var sb strings.Builder

	fmt.Fprintf(&sb, "Config\n======\n")
	fmt.Fprintf(&sb, "On Windows         %v\n", c.onWindows)
	fmt.Fprintf(&sb, "Start Path:        %s\n", c.fromDir)
	fmt.Fprintf(&sb, "Delete:            %v\n", c.delete)
	fmt.Fprintf(&sb, "Older than (days): %v\n", c.daysAgo)
	fmt.Fprintf(&sb, "MB Threshold:      %v\n", c.mbGreater)
	fmt.Fprintf(&sb, "Folders limit:     %v\n", c.limit)
	if c.debug {
		fmt.Fprintf(&sb, "Debug              %v\n", c.debug)
	}

	return sb.String()
}

func newConfig() *Config {
	platformSetup()

	deleteFlag := flag.Bool("delete", false, "set to delete found folders")
	fromDirFlag := flag.String("from", DefaultStartDir, "set starting directory")
	mbThresh := flag.Int("mbthresh", DefaultMbGreater, "set mb size threshold")
	older := flag.Int("older", DefaultDaysAgo, "examine folders older than (days)")
	limit := flag.Int("limit", DefaultLimit, "limit to this many folders")
	debugFlag := flag.Bool("debug", false, "set to output debug information")
	flag.Parse()

	c := &Config{
		daysAgo:   *older,
		mbGreater: *mbThresh,
		limit:     *limit,
		fromDir:   *fromDirFlag,
		delete:    *deleteFlag,
		onWindows: onWindows,
		debug:     *debugFlag,
	}

	fmt.Println(c)

	return c
}

func main() {
	c := newConfig()

	results, debug, err := run(c)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %s", err)
		os.Exit(1)
	}

	if c.debug {
		// Need this 'type conversion'
		Debugs(debug).print()
	}

	if len(results.folders) == 0 {
		fmt.Printf("No results found\n")
		return
	}

	results.print()

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

func (r *Results) print() {
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

func (d Debugs) print() {
	fmt.Print("Debug\n")
	for _, dbg := range d {
		fmt.Printf("%s: File: %s, debug: %s\n", dbg.action, dbg.path, dbg.reason)
	}
	fmt.Print("\n")
}

type Folder struct {
	path       string
	sizeMb     int
	modDaysAgo int
}

type Debug struct {
	action string
	path   string
	reason string
}

type Debugs []Debug

type Config struct {
	daysAgo   int
	mbGreater int
	limit     int
	fromDir   string
	delete    bool
	onWindows bool
	debug     bool
}

const (
	DefaultLimit     = 10
	DefaultMbGreater = 50
	DefaultDaysAgo   = 7
)

var reachedMax = errors.New("reached max found")

func run(c *Config) (*Results, []Debug, error) {
	results := newResults()
	debug := []Debug{}

	err := filepath.WalkDir(c.fromDir, func(path string, d fs.DirEntry, err error) error {
		if d == nil {
			if c.debug {
				dbg := Debug{
					action: "ERROR!",
					path:   path,
					reason: fmt.Sprintf("PATH is INVALID"),
				}
				debug = append(debug, dbg)
			}

			return nil
		}

		if !d.IsDir() {
			return nil
		}

		// Some places to ignore on Windows
		if c.onWindows {
			for _, excludePattern := range excludeFolders {
				if excludePattern.MatchString(path) {
					return fs.SkipDir
				}
			}
		}

		if filepath.Base(path) == NodeModules {

			info, err := d.Info()
			if err != nil {
				return err
			}

			modDaysAgo := daysSince(info.ModTime())

			// NOTE: Not sure we need to do this extra work?
			// modDaysAgo, err := latestModifiedFile(filepath.Dir(path))
			// if err != nil {
			// 	return err
			// }

			if modDaysAgo < c.daysAgo {
				if c.debug {
					dbg := Debug{
						action: "SKIP",
						path:   path,
						reason: fmt.Sprintf("Age is less than %d days", c.daysAgo),
					}
					debug = append(debug, dbg)
				}
				return fs.SkipDir
			}

			sizeMb, err := folderSizeMb(path)
			if err != nil {
				return err
			}

			if sizeMb < c.mbGreater {
				if c.debug {
					dbg := Debug{
						action: "SKIP",
						path:   path,
						reason: fmt.Sprintf("Size is less than %dMB", c.mbGreater),
					}
					debug = append(debug, dbg)
				}
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
		return nil, debug, err
	}

	results.sort()
	return results, debug, nil
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
