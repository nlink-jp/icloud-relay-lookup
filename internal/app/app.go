// Package app implements the icloud-relay-lookup command-line interface:
// subcommand dispatch plus the check / update / status / mcp commands. Core
// logic lives in the relaylist, config, engine, apple, and mcp packages; this
// package is the thin I/O shell around them.
package app

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nlink-jp/icloud-relay-lookup/internal/apple"
	"github.com/nlink-jp/icloud-relay-lookup/internal/config"
	"github.com/nlink-jp/icloud-relay-lookup/internal/engine"
	"github.com/nlink-jp/icloud-relay-lookup/internal/relaylist"
)

// Exit codes. `check` uses the grep-style convention so it composes in shell:
//
//	if icloud-relay-lookup check "$ip"; then echo "via Private Relay"; fi
const (
	exitIsRelay  = 0 // the IP is an iCloud Private Relay egress IP
	exitNotRelay = 1 // the IP is not an iCloud Private Relay egress IP
	exitError    = 2 // usage / lookup error
)

// Run dispatches a subcommand and returns a process exit code.
func Run(args []string, version string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return exitError
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "check":
		return cmdCheck(rest)
	case "update":
		return cmdUpdate(rest)
	case "status":
		return cmdStatus(rest)
	case "mcp":
		return cmdMCP(rest, version)
	case "version", "--version", "-v":
		fmt.Println("icloud-relay-lookup " + version)
		fmt.Println("Data: Apple iCloud Private Relay egress IP ranges (https://mask-api.icloud.com/egress-ip-ranges.csv).")
		return 0
	case "help", "-h", "--help":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage(os.Stderr)
		return exitError
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `icloud-relay-lookup — is an IP address an iCloud Private Relay egress IP? (offline, cached list)

Usage:
  icloud-relay-lookup <command> [flags] [args]

Commands:
  check <IP>...   Report whether each IP is a Private Relay egress IP (stdin if no args)
  update          Revalidate/download the egress list and rebuild the local store
  status          Show the cached list's freshness and size
  mcp             Run as a local MCP server (stdio)
  version         Print the version

check flags:
  -j, --json      JSON Lines output (one object per IP)
  --no-update     Do not auto-revalidate even if the list is stale

check exit codes (single IP, text mode):
  0  the IP is an iCloud Private Relay egress IP
  1  the IP is not an iCloud Private Relay egress IP
  2  error (invalid IP, no local list, ...)
  (batch mode — multiple IPs, stdin, or --json — exits 0 unless an error occurs)

Common flags:
  -c, --config <path>   Config file (default ~/.config/icloud-relay-lookup/config.toml)
  --store-dir <path>    Store directory (default ~/.local/share/icloud-relay-lookup)

A hit carries the list's geo hints (country / ISO region / city) for where
that egress range serves. The list auto-revalidates (ETag conditional GET)
when older than the TTL (default 1h, 1h floor); disable with --no-update or
[apple] auto_update = false.

Data: Apple iCloud Private Relay egress IP ranges
(https://mask-api.icloud.com/egress-ip-ranges.csv).
`)
}

// commonFlags are the config-resolution flags shared by every command.
type commonFlags struct {
	config   string
	storeDir string
	url      string
}

// register binds the common flags onto fs. When withUpdate is true it also
// registers --url (only meaningful for commands that download).
func (c *commonFlags) register(fs *flag.FlagSet, withUpdate bool) {
	fs.StringVar(&c.config, "config", "", "config file path")
	fs.StringVar(&c.config, "c", "", "config file path (shorthand)")
	fs.StringVar(&c.storeDir, "store-dir", "", "store directory override")
	if withUpdate {
		fs.StringVar(&c.url, "url", "", "egress list URL override")
	}
}

func (c *commonFlags) buildEngine() (*engine.Engine, error) {
	cfg, err := config.Load(c.config, c.storeDir, c.url)
	if err != nil {
		return nil, err
	}
	return engine.New(cfg, apple.NewHTTPFetcher()), nil
}

// loadListOrHint loads the store, printing an actionable hint on ErrNoList.
func loadListOrHint(e *engine.Engine, errw io.Writer) (*relaylist.List, int) {
	list, err := e.LoadList()
	if err != nil {
		if isNoList(err) {
			fmt.Fprintf(errw, "%v\nrun 'icloud-relay-lookup update' to download Apple's egress IP list.\n", err)
			return nil, exitError
		}
		fmt.Fprintf(errw, "error: %v\n", err)
		return nil, exitError
	}
	return list, 0
}

func isNoList(err error) bool { return errors.Is(err, engine.ErrNoList) }

// parseInterspersed parses fs while tolerating flags that appear after
// positional arguments (Go's flag package otherwise stops at the first
// non-flag). It returns the collected positional arguments. IP inputs never
// begin with '-', so there is no ambiguity.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positionals = append(positionals, args[0])
		args = args[1:]
	}
	return positionals, nil
}

// readInputs returns args verbatim, or whitespace-separated tokens read from
// stdin when args is empty. Blank lines and '#' comment lines are skipped.
func readInputs(args []string, stdin io.Reader) []string {
	if len(args) > 0 {
		return args
	}
	var out []string
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Fields(line)...)
	}
	return out
}
