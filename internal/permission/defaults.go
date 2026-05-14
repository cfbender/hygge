package permission

import (
	"github.com/cfbender/hygge/internal/config"
)

// modeToAction converts a config.PermissionMode scalar into the engine Action.
// An empty or unrecognised mode is treated as "ask" — the safest default.
func modeToAction(m config.PermissionMode) Action {
	switch m {
	case config.PermAllow:
		return ActionAllow
	case config.PermDeny:
		return ActionDeny
	case config.PermAsk:
		return ActionAsk
	default:
		return ActionAsk
	}
}

// defaultRules synthesises the lowest-priority "catch-all" layer from the
// scalar settings in cfg.Permission.  These rules are appended after the
// secrets denylist, persisted state allowances, and any user-declared config
// rules; they exist solely so that Match never returns ActionAsk when the
// config has expressed a concrete default for the category.
//
// The synthesised set is, in evaluation order:
//
//  1. file.read inside PWD -> allow  (the "always allow reads under $PWD" default)
//  2. file.read anywhere else -> permission.file_read_outside_pwd
//  3. file.write anywhere -> permission.file_write
//  4. shell anywhere -> permission.shell
//  5. network anywhere -> permission.network
//  6. mcp anywhere -> permission.mcp
//  7. agent anywhere -> ask  (sub-agent dispatch: always confirm with user;
//     individual tools the sub-agent runs still go through their own gate)
//
// All synthesised rules carry Source = "default" so Decision.Reason can name
// the origin.
func defaultRules(cfg *config.Config) []Rule {
	if cfg == nil {
		// No config — fall back to the safest defaults: ask for everything
		// except deny network (matches config's hard-coded defaults).
		return []Rule{
			{
				Category:             CategoryFileRead,
				Pattern:              "**",
				Action:               ActionAllow,
				Source:               "default",
				AppliesInsidePwdOnly: true,
			},
			{Category: CategoryFileRead, Pattern: "**", Action: ActionAsk, Source: "default"},
			{Category: CategoryFileWrite, Pattern: "**", Action: ActionAsk, Source: "default"},
			{Category: CategoryShell, Pattern: "**", Action: ActionAsk, Source: "default"},
			{Category: CategoryNetwork, Pattern: "**", Action: ActionDeny, Source: "default"},
			{Category: CategoryMCP, Pattern: "**", Action: ActionAsk, Source: "default"},
			{Category: CategoryAgent, Pattern: "**", Action: ActionAsk, Source: "default"},
		}
	}
	p := cfg.Permission
	return []Rule{
		{
			Category:             CategoryFileRead,
			Pattern:              "**",
			Action:               ActionAllow,
			Source:               "default",
			AppliesInsidePwdOnly: true,
		},
		{
			Category: CategoryFileRead,
			Pattern:  "**",
			Action:   modeToAction(p.FileReadOutsidePwd),
			Source:   "default",
		},
		{
			Category: CategoryFileWrite,
			Pattern:  "**",
			Action:   modeToAction(p.FileWrite),
			Source:   "default",
		},
		{
			Category: CategoryShell,
			Pattern:  "**",
			Action:   modeToAction(p.Shell),
			Source:   "default",
		},
		{
			Category: CategoryNetwork,
			Pattern:  "**",
			Action:   modeToAction(p.Network),
			Source:   "default",
		},
		{
			Category: CategoryMCP,
			Pattern:  "**",
			Action:   modeToAction(p.MCP),
			Source:   "default",
		},
		// Sub-agent dispatch is always "ask" by default; we intentionally
		// do not let cfg.Permission introduce a blanket "allow" because
		// the user expectation is to confirm at least the first time a
		// sub-agent is launched on their behalf.  Per-pattern allows can
		// still come from persisted state.
		{
			Category: CategoryAgent,
			Pattern:  "**",
			Action:   ActionAsk,
			Source:   "default",
		},
		// Plugin tools default to "ask" so the user knows a plugin
		// tool is running.  CategoryPlugin is not yet surfaced in
		// config.PermissionConfig; when it is, wire it here similarly
		// to CategoryMCP.
		{
			Category: CategoryPlugin,
			Pattern:  "**",
			Action:   ActionAsk,
			Source:   "default",
		},
	}
}
