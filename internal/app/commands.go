package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nlink-jp/icloud-relay-lookup/internal/engine"
	"github.com/nlink-jp/icloud-relay-lookup/internal/mcp"
	"github.com/nlink-jp/icloud-relay-lookup/internal/relaylist"
)

// ---- check ----------------------------------------------------------------

func cmdCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, false)
	var jsonOut, noUpdate bool
	fs.BoolVar(&jsonOut, "json", false, "JSON Lines output")
	fs.BoolVar(&jsonOut, "j", false, "JSON Lines output (shorthand)")
	fs.BoolVar(&noUpdate, "no-update", false, "do not auto-revalidate even if the list is stale")
	positionals, err := parseInterspersed(fs, args)
	if err != nil {
		return exitError
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	return runCheck(context.Background(), os.Stdout, os.Stderr, os.Stdin, e, jsonOut, noUpdate, positionals)
}

// runCheck evaluates each input against the egress list. A single positional
// IP in text mode uses grep-style tri-state exit codes (0/1/2). Any other
// shape — multiple IPs, stdin input, or --json — is batch mode: results go to
// stdout and the exit code signals errors only (0 / 2).
func runCheck(ctx context.Context, out, errw io.Writer, stdin io.Reader, e *engine.Engine, jsonOut, noUpdate bool, args []string) int {
	tristate := len(args) == 1 && !jsonOut
	inputs := readInputs(args, stdin)
	if len(inputs) == 0 {
		fmt.Fprintln(errw, "no IP addresses given (pass as an argument or on stdin)")
		return exitError
	}
	list, code := obtainList(ctx, e, errw, noUpdate)
	if code != 0 {
		return code
	}
	now := time.Now().UTC()
	for _, in := range inputs {
		r, err := engine.Lookup(list, in)
		if err != nil { // invalid IP
			if tristate {
				fmt.Fprintf(errw, "error: %v\n", err)
				return exitError
			}
			emitInvalid(out, in, jsonOut, list, now)
			continue
		}
		emitResult(out, in, r, jsonOut, list, now)
		if tristate {
			if r.IsRelay {
				return exitIsRelay
			}
			return exitNotRelay
		}
	}
	return 0
}

// obtainList loads the egress list, auto-revalidating first when enabled and
// the cached copy is missing or stale. A refetch failure with an existing
// cache is a soft warning; only a total absence of data is a hard error.
func obtainList(ctx context.Context, e *engine.Engine, errw io.Writer, noUpdate bool) (*relaylist.List, int) {
	if !e.Cfg.AutoUpdate || noUpdate {
		list, code := loadListOrHint(e, errw)
		if code != 0 {
			return nil, code
		}
		warnIfStale(e, list, errw)
		return list, 0
	}
	list, _, err := e.EnsureFresh(ctx, e.Cfg.TTL)
	if err != nil {
		if list == nil {
			fmt.Fprintf(errw, "no local egress list and refetch failed: %v\nrun 'icloud-relay-lookup update'.\n", err)
			return nil, exitError
		}
		fmt.Fprintf(errw, "warning: auto-update failed (%v); using cached list (fetched %s)\n",
			err, list.Fetched().Format("2006-01-02 15:04"))
	}
	return list, 0
}

// ---- update ---------------------------------------------------------------

func cmdUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, true)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	return runUpdate(os.Stdout, os.Stderr, e)
}

func runUpdate(out, errw io.Writer, e *engine.Engine) int {
	fmt.Fprintf(errw, "revalidating %s …\n", e.Cfg.URL)
	res, err := e.Update(context.Background())
	if err != nil {
		fmt.Fprintf(errw, "update failed: %v\n", err)
		return exitError
	}
	if res.NotModified {
		fmt.Fprintf(out, "unchanged (server returned 304); freshness bumped\n")
	} else {
		fmt.Fprintf(out, "updated %s\n", e.Cfg.CSVPath())
	}
	fmt.Fprintf(out, "  fetched:  %s\n", res.Fetched.Format(time.RFC3339))
	fmt.Fprintf(out, "  ranges:   %d  (v4: %d, v6: %d)  skipped: %d\n",
		res.Count, res.V4Count, res.V6Count, res.Skipped)
	if res.ETag != "" {
		fmt.Fprintf(out, "  etag:     %s\n", res.ETag)
	}
	return 0
}

// ---- status ---------------------------------------------------------------

func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, false)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	return runStatus(os.Stdout, os.Stderr, e)
}

// runStatus reports the cached list's state as-is; it never triggers a fetch.
func runStatus(out, errw io.Writer, e *engine.Engine) int {
	fmt.Fprintf(out, "store:   %s\n", e.Cfg.StoreDir)
	fmt.Fprintf(out, "source:  %s\n", e.Cfg.URL)
	list, err := e.LoadList()
	if err != nil {
		if isNoList(err) {
			fmt.Fprintln(out, "status:  NO LIST — run 'icloud-relay-lookup update'")
		} else {
			fmt.Fprintf(errw, "status:  ERROR — %v\n", err)
		}
		return exitError
	}
	v4, v6 := list.FamilyCounts()
	fmt.Fprintf(out, "fetched: %s\n", list.Fetched().Format(time.RFC3339))
	fmt.Fprintf(out, "ranges:  %d  (v4: %d, v6: %d)\n", list.Len(), v4, v6)
	if list.ETag() != "" {
		fmt.Fprintf(out, "etag:    %s\n", list.ETag())
	}
	if stale, age := e.IsStale(list.Fetched()); stale {
		fmt.Fprintf(out, "status:  STALE — %s old; run 'icloud-relay-lookup update'\n", roundAge(age))
	} else {
		fmt.Fprintln(out, "status:  OK")
	}
	return 0
}

// ---- mcp ------------------------------------------------------------------

func cmdMCP(args []string, version string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, true)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	if err := mcp.Serve(context.Background(), e, version, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: %v\n", err)
		return exitError
	}
	return 0
}

// ---- helpers --------------------------------------------------------------

// warnIfStale prints a freshness warning to errw; it never updates on its own.
func warnIfStale(e *engine.Engine, list *relaylist.List, errw io.Writer) {
	if stale, age := e.IsStale(list.Fetched()); stale {
		fmt.Fprintf(errw, "warning: egress list is %s old (fetched %s); run 'icloud-relay-lookup update'\n",
			roundAge(age), list.Fetched().Format("2006-01-02 15:04"))
	}
}

// roundAge renders a duration as a coarse human string (hours or days).
func roundAge(d time.Duration) string {
	if d >= 48*time.Hour {
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
	return fmt.Sprintf("%d hours", int(d.Hours()))
}
