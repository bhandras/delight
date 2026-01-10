package appserver

// This package contains a small JSON-RPC 2.0 client for `codex app-server`.
//
// The app-server protocol is newline-delimited JSON (JSONL) over stdio. It is
// described in https://github.com/openai/codex/tree/main/codex-rs/app-server.

const (
	// MethodInitialize is the initial request method that must be called once
	// after spawning the app-server process.
	MethodInitialize = "initialize"
	// MethodInitialized is the client notification sent after initialize succeeds.
	MethodInitialized = "initialized"
)

const (
	// MethodThreadStart creates a new thread and subscribes to its events.
	MethodThreadStart = "thread/start"
	// MethodThreadResume resumes an existing thread by id.
	MethodThreadResume = "thread/resume"
	// MethodTurnStart starts a new turn for a thread (user input).
	MethodTurnStart = "turn/start"
	// MethodTurnInterrupt cancels an in-flight turn.
	MethodTurnInterrupt = "turn/interrupt"
)

const (
	// NotifyThreadStarted is emitted after thread/start (and after thread/fork).
	NotifyThreadStarted = "thread/started"
	// NotifyTurnStarted is emitted when a new turn begins streaming.
	NotifyTurnStarted = "turn/started"
	// NotifyTurnCompleted is emitted when a turn finishes.
	NotifyTurnCompleted = "turn/completed"
	// NotifyItemStarted is emitted when an item starts.
	NotifyItemStarted = "item/started"
	// NotifyItemCompleted is emitted when an item finishes.
	NotifyItemCompleted = "item/completed"
	// NotifyItemAgentMessageDelta streams assistant message deltas.
	NotifyItemAgentMessageDelta = "item/agentMessage/delta"
	// NotifyError is emitted when the server hits an error mid-turn.
	NotifyError = "error"
)

const (
	// ItemTypeAgentMessage is the ThreadItem union tag for assistant messages.
	ItemTypeAgentMessage = "agentMessage"
	// ItemTypeCommandExecution is the ThreadItem union tag for command runs.
	ItemTypeCommandExecution = "commandExecution"
	// ItemTypeFileChange is the ThreadItem union tag for file changes.
	ItemTypeFileChange = "fileChange"
	// ItemTypeMCPToolCall is the ThreadItem union tag for MCP tool calls.
	ItemTypeMCPToolCall = "mcpToolCall"
	// ItemTypeReasoning is the ThreadItem union tag for reasoning events.
	ItemTypeReasoning = "reasoning"
)
