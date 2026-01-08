/**
 * Claude Remote Bridge Script
 *
 * This script bridges Go CLI with the Claude Code SDK for remote mode.
 * It receives messages from Go via stdin and sends SDK output via stdout.
 *
 * Protocol:
 * - stdin:  Line-delimited JSON messages (user prompts, control responses)
 * - stdout: Line-delimited JSON messages (SDK output, control requests)
 * - stderr: Debug logs (optional)
 *
 * Message Types from Go:
 * - { type: 'user', content: string, meta?: object } - User message/prompt
 * - { type: 'control_response', ... }           - Permission response
 * - { type: 'abort' }                           - Abort current query
 *
 * Message Types to Go:
 * - SDK messages (assistant, system, result, etc.)
 * - { type: 'control_request', ... }            - Permission request
 * - { type: 'ready' }                           - Bridge is ready
 * - { type: 'error', message: string }          - Error occurred
 */

const crypto = require('crypto');
const fs = require('fs');
const path = require('path');
const os = require('os');
const readline = require('readline');
const { spawn, execSync } = require('child_process');

// Resolve module path relative to this script's location
const scriptDir = __dirname;
const nodeModulesPath = path.join(scriptDir, '..', 'node_modules');
module.paths.unshift(nodeModulesPath);

// Optional profile/config selection for multiple Claude installs
// - DELIGHT_CLAUDE_CONFIG_DIR: absolute path to a config dir
// - DELIGHT_CLAUDE_PROFILE: shortcut to ~/.claude-<profile>
if (!process.env.CLAUDE_CONFIG_DIR) {
    if (process.env.DELIGHT_CLAUDE_CONFIG_DIR) {
        process.env.CLAUDE_CONFIG_DIR = process.env.DELIGHT_CLAUDE_CONFIG_DIR;
    } else if (process.env.DELIGHT_CLAUDE_PROFILE) {
        const profile = process.env.DELIGHT_CLAUDE_PROFILE;
        process.env.CLAUDE_CONFIG_DIR = path.join(os.homedir(), `.claude-${profile}`);
    }
}

// Allow overriding which Claude SDK/CLI module to load
// Set DELIGHT_CLAUDE_CLI to a package name or absolute path
const customCliModule = process.env.DELIGHT_CLAUDE_CLI;
const defaultTarget = '@anthropic-ai/claude-code/cli.js';

function resolveDefaultCliModule() {
    try {
        const npmRoot = execSync('npm root -g', { encoding: 'utf8' }).trim();
        if (npmRoot) {
            return path.join(npmRoot, '@anthropic-ai', 'claude-code', 'cli.js');
        }
    } catch { }
    return null;
}

function resolveCli(target) {
    const candidate = target || resolveDefaultCliModule() || defaultTarget;
    if (candidate && path.isAbsolute(candidate)) {
        if (fs.existsSync(candidate)) {
            return candidate;
        }
        const cliCandidate = path.join(candidate, 'cli.js');
        if (fs.existsSync(cliCandidate)) {
            return cliCandidate;
        }
    }
    const searchPaths = [nodeModulesPath, path.join(scriptDir, '..'), process.cwd(), path.join(process.cwd(), 'node_modules')];
    try {
        return require.resolve(candidate, { paths: searchPaths });
    } catch (err) {
        const hint = [
            `Failed to resolve '${candidate}'.`,
            `Searched: ${searchPaths.join(', ')}`,
            `Set DELIGHT_CLAUDE_CLI to an absolute path (e.g. /full/path/to/cli.js) or install the package globally.`
        ].join(' ');
        throw new Error(hint);
    }
}

// Parse command line arguments
const args = process.argv.slice(2);
let cwd = process.cwd();
let resumeSessionId = null;
let debug = false;

for (let i = 0; i < args.length; i++) {
    if (args[i] === '--cwd' && args[i + 1]) {
        cwd = args[++i];
    } else if (args[i] === '--resume' && args[i + 1]) {
        resumeSessionId = args[++i];
    } else if (args[i] === '--debug') {
        debug = true;
    }
}

// Debug logging to stderr (doesn't interfere with JSON protocol)
function debugLog(...args) {
    if (debug) {
        console.error('[bridge]', ...args);
    }
}

// Send JSON message to Go via stdout
function sendMessage(message) {
    console.log(JSON.stringify(message));
}

// Track pending permission requests
const pendingPermissions = new Map();

// Message queue for incoming user messages
class MessageQueue {
    constructor() {
        this.messages = [];
        this.waiters = [];
        this.closed = false;
    }

    push(message) {
        if (this.waiters.length > 0) {
            const waiter = this.waiters.shift();
            waiter.resolve(message);
        } else {
            this.messages.push(message);
        }
    }

    async pull() {
        if (this.messages.length > 0) {
            return this.messages.shift();
        }
        if (this.closed) {
            return null;
        }
        return new Promise((resolve, reject) => {
            this.waiters.push({ resolve, reject });
        });
    }

    close() {
        this.closed = true;
        for (const waiter of this.waiters) {
            waiter.resolve(null);
        }
        this.waiters = [];
    }
}

// Create async iterable from message queue
function createAsyncIterable(queue, abortSignal) {
    return {
        [Symbol.asyncIterator]() {
            return {
                async next() {
                    if (abortSignal?.aborted) {
                        return { done: true, value: undefined };
                    }
                    const message = await queue.pull();
                    if (message === null) {
                        return { done: true, value: undefined };
                    }
                    return { done: false, value: message };
                }
            };
        }
    };
}

// Main entry point
async function main() {
    debugLog('Starting Claude Remote Bridge');
    debugLog('CWD:', cwd);
    debugLog('Resume Session:', resumeSessionId || 'none');

    // Resolve Claude Code CLI entrypoint
    let claudeCodePath;
    try {
        claudeCodePath = resolveCli(customCliModule);
        debugLog('Found claude-code at:', claudeCodePath);
    } catch (err) {
        sendMessage({ type: 'error', error: `Failed to resolve claude-code CLI: ${err.message}` });
        process.exit(1);
    }

    // Set up message queue and abort controller
    const messageQueue = new MessageQueue();
    let currentAbortController = null;

    // Set up stdin reader
    const rl = readline.createInterface({
        input: process.stdin,
        output: null,
        terminal: false
    });

    rl.on('line', (line) => {
        if (!line.trim()) return;

        try {
            const msg = JSON.parse(line);
            debugLog('Received from Go:', msg.type);

            switch (msg.type) {
                case 'user':
                    // User message - push to queue
                    messageQueue.push({
                        content: msg.content,
                        meta: msg.meta || {}
                    });
                    break;

                case 'control_response':
                    // Permission response from mobile app
                    const handler = pendingPermissions.get(msg.request_id);
                    if (handler) {
                        pendingPermissions.delete(msg.request_id);
                        handler(msg.response);
                    } else {
                        debugLog('Unknown permission request:', msg.request_id);
                    }
                    break;

                case 'abort':
                    // Abort current run. We keep the bridge alive, but restart the Claude
                    // process so the next message can continue cleanly.
                    debugLog('Abort requested');
                    try { currentChild?.kill('SIGTERM'); } catch { }
                    cleanupChild();
                    maybePump();
                    break;

                case 'shutdown':
                    // Graceful shutdown
                    debugLog('Shutdown requested');
                    messageQueue.close();
                    try { currentChild?.kill('SIGTERM'); } catch { }
                    cleanupChild();
                    break;

                default:
                    debugLog('Unknown message type:', msg.type);
            }
        } catch (err) {
            debugLog('Failed to parse message:', err.message);
        }
    });

    rl.on('close', () => {
        debugLog('stdin closed');
        messageQueue.close();
        try { currentChild?.kill('SIGTERM'); } catch { }
        cleanupChild();
    });

    // Track current per-session overrides
    let currentPermissionMode = 'default';
    let currentModel = undefined;
    let currentFallbackModel = undefined;
    let currentCustomSystemPrompt = undefined;
    let currentAppendSystemPrompt = undefined;
    let currentAllowedTools = undefined;
    let currentDisallowedTools = undefined;

    // Track which runtime config has actually been applied inside the Claude
    // process. This matters for resumed sessions: passing `--model` on startup
    // does not necessarily override the model for an existing conversation.
    let appliedPermissionMode = undefined;
    let appliedModel = undefined;
    const pendingControlAcks = new Map(); // request_id -> request

    let currentChild = null;
    let currentStdout = null;
    let currentStderr = null;
    let currentStderrLines = [];
    const resumeNotFoundRegex = /no conversation found with session id/i;
    let readyForNext = true;
    let configDirty = false;

    // Per-turn state machine.
    // Claude sometimes returns the assistant reply only in `result.result` without
    // emitting a streaming `assistant` message. Track whether we saw an assistant
    // text block for the current turn and synthesize one from `result.result` if not.
    let sawAssistantTextThisTurn = false;

    function normalizeContentBlocks(value) {
        if (Array.isArray(value)) return value;
        if (typeof value === 'string') {
            const text = value;
            if (text.trim().length === 0) return [];
            return [{ type: 'text', text }];
        }
        if (value && typeof value === 'object') {
            // Sometimes upstream uses a single content block object.
            if (typeof value.type === 'string') return [value];
            if (typeof value.text === 'string') return [{ type: 'text', text: value.text }];
            if (typeof value.content === 'string') return [{ type: 'text', text: value.content }];
        }
        return [];
    }

    function extractResultText(value) {
        if (typeof value === 'string') return value;
        if (Array.isArray(value)) {
            const parts = value
                .map((block) => (block && block.type === 'text' && typeof block.text === 'string' ? block.text : ''))
                .filter((t) => t && t.trim().length > 0);
            return parts.join('\n\n');
        }
        if (value && typeof value === 'object') {
            if (typeof value.text === 'string') return value.text;
            if (typeof value.content === 'string') return value.content;
            if (typeof value.result === 'string') return value.result;
        }
        return '';
    }

    function buildArgs() {
        const cliArgs = ['--output-format', 'stream-json', '--verbose'];
        if (currentCustomSystemPrompt) cliArgs.push('--system-prompt', currentCustomSystemPrompt);
        if (currentAppendSystemPrompt) cliArgs.push('--append-system-prompt', currentAppendSystemPrompt);
        if (currentModel) cliArgs.push('--model', currentModel);
        if (currentFallbackModel) cliArgs.push('--fallback-model', currentFallbackModel);
        if (resumeSessionId) cliArgs.push('--resume', resumeSessionId);
        if (currentPermissionMode) cliArgs.push('--permission-mode', currentPermissionMode);
        if (Array.isArray(currentAllowedTools) && currentAllowedTools.length > 0) {
            cliArgs.push('--allowedTools', currentAllowedTools.join(','));
        }
        if (Array.isArray(currentDisallowedTools) && currentDisallowedTools.length > 0) {
            cliArgs.push('--disallowedTools', currentDisallowedTools.join(','));
        }

        cliArgs.push('--permission-prompt-tool', 'stdio');
        cliArgs.push('--input-format', 'stream-json');

        return cliArgs;
    }

    function applyMeta(meta) {
        meta = meta || {};
        let restartChanged = false;

        if (Object.prototype.hasOwnProperty.call(meta, 'permissionMode') && typeof meta.permissionMode === 'string') {
            // Permission mode can be applied at runtime via control_request.
            currentPermissionMode = meta.permissionMode;
        }
        if (Object.prototype.hasOwnProperty.call(meta, 'model')) {
            // Model can be applied at runtime via control_request.
            currentModel = meta.model || undefined;
        }
        if (Object.prototype.hasOwnProperty.call(meta, 'fallbackModel')) {
            if (currentFallbackModel !== (meta.fallbackModel || undefined)) restartChanged = true;
            currentFallbackModel = meta.fallbackModel || undefined;
        }
        if (Object.prototype.hasOwnProperty.call(meta, 'customSystemPrompt')) {
            if (currentCustomSystemPrompt !== (meta.customSystemPrompt || undefined)) restartChanged = true;
            currentCustomSystemPrompt = meta.customSystemPrompt || undefined;
        }
        if (Object.prototype.hasOwnProperty.call(meta, 'appendSystemPrompt')) {
            if (currentAppendSystemPrompt !== (meta.appendSystemPrompt || undefined)) restartChanged = true;
            currentAppendSystemPrompt = meta.appendSystemPrompt || undefined;
        }
        if (Object.prototype.hasOwnProperty.call(meta, 'allowedTools')) {
            const next = meta.allowedTools || undefined;
            if (JSON.stringify(currentAllowedTools) !== JSON.stringify(next)) restartChanged = true;
            currentAllowedTools = next;
        }
        if (Object.prototype.hasOwnProperty.call(meta, 'disallowedTools')) {
            const next = meta.disallowedTools || undefined;
            if (JSON.stringify(currentDisallowedTools) !== JSON.stringify(next)) restartChanged = true;
            currentDisallowedTools = next;
        }

        // If Claude is already running, changing CLI args requires a respawn.
        if (restartChanged && currentChild) {
            configDirty = true;
        }
    }

    function cleanupChild() {
        try { currentStdout?.close(); } catch { }
        try { currentStderr?.close(); } catch { }
        currentStdout = null;
        currentStderr = null;
        currentStderrLines = [];
        currentChild = null;
        pendingControlAcks.clear();
        appliedPermissionMode = undefined;
        appliedModel = undefined;
        readyForNext = true;
    }

    function sendControlRequestToClaude(request) {
        if (!currentChild || !currentChild.stdin || currentChild.stdin.destroyed) {
            throw new Error('Claude Code process is not running');
        }
        const requestId = crypto.randomUUID();
        const envelope = {
            type: 'control_request',
            request_id: requestId,
            request
        };
        pendingControlAcks.set(requestId, request);
        currentChild.stdin.write(JSON.stringify(envelope) + '\n');
        return requestId;
    }

    function maybeApplyRuntimeConfigToClaude() {
        // Claude Code supports mutating per-session state via `control_request`.
        // This is required when resuming an existing conversation: CLI flags like
        // `--model` may be ignored for a resumed session.
        if (!currentChild) return;

        if (typeof currentPermissionMode === 'string' && currentPermissionMode !== appliedPermissionMode) {
            debugLog('Applying permission mode via control_request:', currentPermissionMode);
            sendControlRequestToClaude({ subtype: 'set_permission_mode', mode: currentPermissionMode });
        }

        if (typeof currentModel === 'string' && currentModel.length > 0 && currentModel !== appliedModel) {
            debugLog('Applying model via control_request:', currentModel);
            sendControlRequestToClaude({ subtype: 'set_model', model: currentModel });
        }
    }

    async function spawnClaude(allowResumeRetry = true) {
        // Ensure SDK entrypoint is set for Claude Code CLI.
        if (!process.env.CLAUDE_CODE_ENTRYPOINT) {
            process.env.CLAUDE_CODE_ENTRYPOINT = 'sdk-ts';
        }

        const cliArgs = buildArgs();
        debugLog('Spawning Claude Code:', process.execPath, claudeCodePath, cliArgs.join(' '));

        const child = spawn(process.execPath, [claudeCodePath, ...cliArgs], {
            cwd,
            stdio: ['pipe', 'pipe', 'pipe']
        });
        currentChild = child;
        configDirty = false;

        // Capture stderr so we can detect recoverable startup failures (e.g. invalid --resume).
        currentStderrLines = [];
        currentStderr = readline.createInterface({ input: child.stderr });
        currentStderr.on('line', (line) => {
            if (!line) return;
            currentStderrLines.push(line);
            if (currentStderrLines.length > 200) currentStderrLines.shift();
            if (debug) {
                console.error('[claude stderr]', line);
            }
        });

        currentStdout = readline.createInterface({ input: child.stdout });
        currentStdout.on('line', (line) => {
            if (!line.trim()) return;
            let msg;
            try {
                msg = JSON.parse(line);
            } catch (err) {
                debugLog('Invalid SDK message:', err.message);
                return;
            }

            if (msg.type === 'control_response') {
                const requestId = msg?.response?.request_id;
                if (typeof requestId === 'string' && pendingControlAcks.has(requestId)) {
                    const request = pendingControlAcks.get(requestId);
                    pendingControlAcks.delete(requestId);
                    if (msg?.response?.subtype === 'success' && request?.subtype === 'set_permission_mode') {
                        appliedPermissionMode = request.mode;
                    }
                    if (msg?.response?.subtype === 'success' && request?.subtype === 'set_model') {
                        appliedModel = request.model;
                    }
                    // These are internal acknowledgements; do not forward to Go.
                    return;
                }
            }

            // Normalize Claude stream-json "message"/"assistant"/"user" events into the raw record
            // shape expected by Delight (so desktop + mobile render tool_use/tool_result consistently).
            const isTranscriptMessage =
                (msg.type === 'message' && (msg.role === 'assistant' || msg.role === 'user'))
                || (msg.type === 'assistant' || msg.type === 'user');

            if (isTranscriptMessage) {
                const role = msg.role || msg.type;
                if (role !== 'assistant' && role !== 'user') {
                    // Not a transcript message.
                    // Fall through to forwarding as-is.
                } else {
                const content = normalizeContentBlocks(msg.content);
                if (role === 'assistant') {
                    if (content.some((block) => block && block.type === 'text' && typeof block.text === 'string' && block.text.trim().length > 0)) {
                        sawAssistantTextThisTurn = true;
                    }
                }
                const messageId = msg.id || crypto.randomUUID();
                const rawRecord = {
                    role: 'agent',
                    content: {
                        type: 'output',
                        data: {
                            type: role === 'assistant' ? 'assistant' : 'user',
                            isSidechain: false,
                            isCompactSummary: false,
                            isMeta: false,
                            uuid: messageId,
                            parentUuid: null,
                            message: role === 'assistant'
                                ? {
                                    role: 'assistant',
                                    model: msg.model || 'unknown',
                                    content,
                                    ...(msg.usage ? { usage: msg.usage } : {})
                                }
                                : {
                                    role: 'user',
                                    content
                                }
                        }
                    }
                };
                sendMessage({ type: 'raw', message: rawRecord });
                return;
                }
            }

            // Some runs only populate the final assistant text in `result.result`.
            // If we didn't see an assistant text message event this turn, synthesize
            // a transcript entry so the desktop + phone both show the reply.
            if (msg.type === 'result') {
                const synthesized = extractResultText(msg.result);
                if (!sawAssistantTextThisTurn && typeof synthesized === 'string' && synthesized.trim().length > 0) {
                    const messageId = crypto.randomUUID();
                    const content = [{ type: 'text', text: synthesized }];
                    const rawRecord = {
                        role: 'agent',
                        content: {
                            type: 'output',
                            data: {
                                type: 'assistant',
                                isSidechain: false,
                                isCompactSummary: false,
                                isMeta: false,
                                uuid: messageId,
                                parentUuid: null,
                                message: {
                                    role: 'assistant',
                                    model: msg.model || 'unknown',
                                    content,
                                    ...(msg.usage ? { usage: msg.usage } : {})
                                }
                            }
                        }
                    };
                    sendMessage({ type: 'raw', message: rawRecord });
                }
                // Reset per-turn state for the next turn.
                sawAssistantTextThisTurn = false;
            }

            if (msg.type === 'control_request') {
                const requestId = msg.request_id || crypto.randomUUID();
                pendingPermissions.set(requestId, (response) => {
                    const controlResponse = {
                        type: 'control_response',
                        response: {
                            subtype: 'success',
                            request_id: requestId,
                            response
                        }
                    };
                    try {
                        if (!child.stdin.destroyed) {
                            child.stdin.write(JSON.stringify(controlResponse) + '\n');
                        }
                    } catch (err) {
                        debugLog('Failed to write permission response:', err.message);
                    }
                });

                // Forward request to Go (preserve original payload)
                if (!msg.request_id) msg.request_id = requestId;
                sendMessage(msg);
                return;
            }

            sendMessage(msg);

            if (msg.type === 'system' && msg.subtype === 'init' && msg.session_id) {
                resumeSessionId = msg.session_id;
                debugLog('Session ID:', resumeSessionId);
            }

            if (msg.type === 'result') {
                readyForNext = true;
                // If config changed mid-run, respawn before processing the next message.
                if (configDirty) {
                    debugLog('Config changed; restarting Claude Code to apply new args');
                    try { child.kill('SIGTERM'); } catch { }
                }
                maybePump();
            }
        });

        child.on('close', async (code) => {
            const stderrText = currentStderrLines.join('\n');
            cleanupChild();

            // `code` can be null when the process exits due to a signal (SIGTERM
            // etc.). Treat this as a non-error so intentional restarts/aborts
            // don't surface spurious errors to the Go side.
            if (code != null && code !== 0) {
                if (allowResumeRetry && resumeSessionId && resumeNotFoundRegex.test(stderrText)) {
                    debugLog('Resume session not found; retrying without --resume:', resumeSessionId);
                    resumeSessionId = null;
                    await spawnClaude(false);
                    maybePump();
                    return;
                }
                sendMessage({ type: 'error', error: `Claude Code exited with code ${code}` });
            }

            // If the CLI exits cleanly (or was killed), we can respawn lazily when more messages arrive.
        });
    }

    function sendUserToClaude(content) {
        if (!currentChild || !currentChild.stdin || currentChild.stdin.destroyed) {
            throw new Error('Claude Code process is not running');
        }
        // New user turn begins.
        sawAssistantTextThisTurn = false;
        const contentBlocks = normalizeContentBlocks(content);
        if (!contentBlocks || contentBlocks.length === 0) {
            throw new Error('Empty user content');
        }
        const userMessage = {
            type: 'user',
            message: {
                role: 'user',
                content: contentBlocks
            }
        };
        currentChild.stdin.write(JSON.stringify(userMessage) + '\n');
        readyForNext = false;
    }

    let pumping = false;
    async function maybePump() {
        if (pumping) return;
        pumping = true;
        try {
            if (!readyForNext) return;

            const next = messageQueue.messages.length > 0 ? messageQueue.messages[0] : null;
            if (!next) return;

            // Dequeue now that we're going to process it.
            const msg = await messageQueue.pull();
            if (!msg) return;

            applyMeta(msg.meta);

            // If configuration changed in a way that requires new CLI args,
            // restart the Claude process before sending the next message so the
            // new args take effect immediately.
            if (configDirty && currentChild) {
                debugLog('Config changed; restarting Claude Code to apply new args');
                try { currentChild.kill('SIGTERM'); } catch { }
                cleanupChild();
            }

            if (!currentChild) {
                await spawnClaude(true);
            }

            // Apply model + permission selection via stream-json control_request.
            // This is required when resuming a session: `--model` does not always
            // override the model for the existing conversation.
            maybeApplyRuntimeConfigToClaude();

            sendUserToClaude(msg.content);
        } catch (err) {
            debugLog('Pump error:', err.message);
            sendMessage({ type: 'error', error: err.message });
            try { currentChild?.kill('SIGTERM'); } catch { }
            cleanupChild();
        } finally {
            pumping = false;
        }
    }

    // When new messages arrive, pump if possible.
    const origPush = messageQueue.push.bind(messageQueue);
    messageQueue.push = (message) => {
        origPush(message);
        maybePump();
    };

    // Delight parity: start Claude immediately in remote mode so we don't pay the
    // spawn cost on the first message (and can warm caches / load context).
    //
    // Claude Code CLI will remain idle until it receives the first stream-json
    // user message on stdin.
    try {
        await spawnClaude(true);
    } catch (err) {
        debugLog('Failed to spawn Claude Code eagerly (will retry on first message):', err?.message || String(err));
    }

    // Signal ready once the bridge is fully wired (and we've attempted eager spawn).
    // This avoids dropping the first message if it arrives immediately after "ready".
    sendMessage({ type: 'ready' });

    // For completeness, handle shutdown by stopping the child.
    currentAbortController = new AbortController();

    // Block forever until the queue is closed (stdin close/shutdown).
    while (!messageQueue.closed) {
        // Sleep lightly; real work happens in maybePump() + stdout callbacks.
        // eslint-disable-next-line no-await-in-loop
        await new Promise((r) => setTimeout(r, 250));
    }

    debugLog('Bridge shutting down');
    try { currentChild?.kill('SIGTERM'); } catch { }
    process.exit(0);
}

main().catch(err => {
    sendMessage({ type: 'error', error: err.message });
    process.exit(1);
});
