package mcp

// Battery server bridge — strangler fig step 1.
//
// This server's noOut wrapper, text()/jsonResult() helpers, and ToolMeta
// pattern are compatible with battery/server equivalents. Phase 2 will
// replace these with battery/server imports.
//
// Import verifies dependency compiles.

import _ "github.com/dpopsuev/battery/server" // verify dependency wiring
