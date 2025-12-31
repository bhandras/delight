package actor

import (
	framework "github.com/bhandras/delight/cli/internal/actor"
)

// PersistAgentState returns a command input that requests persisting the given
// agentState JSON string to the server.
//
// This command is intended for the transitional period where the production
// session manager still owns the canonical `types.AgentState` struct but wants
// to delegate persistence (including version-mismatch handling) to the actor.
func PersistAgentState(agentStateJSON string) framework.Input {
	return cmdPersistAgentState{AgentStateJSON: agentStateJSON}
}

