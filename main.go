// Command icloud-relay-lookup reports whether an IP address is an Apple iCloud
// Private Relay egress IP, answered from a locally cached copy of Apple's
// published egress IP ranges list, as a CLI and a local MCP server. The
// Apple-side sibling of tor-exit-lookup (Tor exits), asn-lookup (AS/country),
// and abuse-lookup (reputation).
package main

import (
	"os"

	"github.com/nlink-jp/icloud-relay-lookup/internal/app"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(app.Run(os.Args[1:], version))
}
