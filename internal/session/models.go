// Package session implements multi-agent session management for DFMC.
//
// A Session runs multiple autonomous agents in a tree structure. Each agent has
// isolated conversation, context, model config, and budget. Agents can delegate
// tasks to their children (parent→child only, no cycles). The session bridges to
// the existing Engine via EngineProvider.
//
// Architecture overview:
//
//	User
//	  |  (talks to root agent only)
//	Agent 1 (root) ←—— user input goes here
//	  |
//	  +—— Agent 2 (child, coordinator=Agent 1)
//	  |     |
//	  |     +—— Agent 3 (child, coordinator=Agent 2)
//	  |
//	  +—— Agent 4 (child, coordinator=Agent 1)
//
// Delegation: Agent N → its children only. Depth cap: 5.
// Coordinator: parent agent. Handles child's waiting_user_input / cap_hit.
package session
