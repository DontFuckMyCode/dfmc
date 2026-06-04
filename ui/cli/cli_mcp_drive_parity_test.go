package cli

import "testing"

// driveSurfaceParity is the documented cross-surface contract for the
// Drive control plane. CLAUDE.md states the MCP `dfmc_drive_*` tools
// "match the HTTP /api/v1/drive/* payloads one-for-one" — but that is
// sync-by-convention. The test below makes the MCP side mechanical:
// the contract is verified against the live driveMCPHandler.Tools() in
// both directions.
//
// The httpMethod/httpPath fields document the corresponding HTTP route
// (the human-facing half of the contract). They are intentionally NOT
// probed at runtime: Go's ServeMux exposes no route list, and black-box
// probing can't tell a specific route apart from the `/{id}` wildcard
// that shadows same-method single-segment paths — a probe-based check
// gives false passes, which is worse than no check. Route registration
// itself is covered by ui/web's own server tests.
//
// mcpTool is "" when a capability is intentionally HTTP-only. Today the
// only such case is delete: removing a persisted run record is a
// human-workbench action, deliberately NOT exposed to IDE/LLM MCP hosts
// (the MCP Drive surface is start/observe/control, not destructive CRUD).
// If that decision changes, flip the contract entry and add the tool.
type driveParityEntry struct {
	capability string
	mcpTool    string // "" = intentionally absent from the MCP surface
	httpMethod string // documentation only (see note above)
	httpPath   string // documentation only
}

var driveSurfaceParity = []driveParityEntry{
	{"start", "dfmc_drive_start", "POST", "/api/v1/drive"},
	{"list", "dfmc_drive_list", "GET", "/api/v1/drive"},
	{"active", "dfmc_drive_active", "GET", "/api/v1/drive/active"},
	{"status", "dfmc_drive_status", "GET", "/api/v1/drive/{id}"},
	{"resume", "dfmc_drive_resume", "POST", "/api/v1/drive/{id}/resume"},
	{"stop", "dfmc_drive_stop", "POST", "/api/v1/drive/{id}/stop"},
	{"delete", "", "DELETE", "/api/v1/drive/{id}"}, // HTTP-only by design
}

// TestDriveSurfaceParity_MCPMatchesContract verifies the MCP side in
// both directions: every advertised dfmc_drive_* tool is in the
// contract, and every contract tool is advertised. The HTTP-only delete
// entry must NOT have a same-name MCP tool — adding one without updating
// the contract fails here, forcing the destructive-surface decision to
// be conscious.
func TestDriveSurfaceParity_MCPMatchesContract(t *testing.T) {
	h := &driveMCPHandler{eng: newCLITestEngine(t)}
	advertised := map[string]bool{}
	for _, tool := range h.Tools() {
		advertised[tool.Name] = true
	}

	contractMCP := map[string]bool{}
	for _, e := range driveSurfaceParity {
		// Keep the documented HTTP half of the contract filled in.
		if e.httpMethod == "" || e.httpPath == "" {
			t.Fatalf("capability %q is missing its documented HTTP route", e.capability)
		}
		if e.mcpTool == "" {
			name := "dfmc_drive_" + e.capability
			if advertised[name] {
				t.Fatalf("%q is marked HTTP-only but MCP now advertises %q — update driveSurfaceParity or drop the tool", e.capability, name)
			}
			continue
		}
		contractMCP[e.mcpTool] = true
		if !advertised[e.mcpTool] {
			t.Fatalf("parity contract lists MCP tool %q but driveMCPHandler.Tools() does not advertise it", e.mcpTool)
		}
	}
	for name := range advertised {
		if !contractMCP[name] {
			t.Fatalf("driveMCPHandler advertises %q but it is missing from driveSurfaceParity — add it with its HTTP route", name)
		}
	}
}
