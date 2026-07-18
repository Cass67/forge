// Package plugins collects all native plugin packages for their init() side effects.
// Import each plugin package here. The import causes the package's init() to run,
// which registers the plugin with internal/plugin.Register().
package plugins

import (
	// Register the Docker sandbox plugin.
	_ "forge/plugin/sandbox" // side-effect: registers tool, slash command, and skill at init()
)
