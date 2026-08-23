// Package all registers every built-in provider. Import for side effects from
// binaries that should support all providers:
//
//	import _ "github.com/NathanBhanji/debrid-client/internal/provider/all"
package all

import (
	_ "github.com/NathanBhanji/debrid-client/internal/provider/alldebrid"  // register
	_ "github.com/NathanBhanji/debrid-client/internal/provider/debridlink" // register
	_ "github.com/NathanBhanji/debrid-client/internal/provider/premiumize" // register
	_ "github.com/NathanBhanji/debrid-client/internal/provider/realdebrid" // register
	_ "github.com/NathanBhanji/debrid-client/internal/provider/torbox"     // register
)
