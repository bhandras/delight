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
    if (process.env.DELIGHT_CLAUDE_CONFIG_DIR || process.env.HAPPY_CLAUDE_CONFIG_DIR) {
        process.env.CLAUDE_CONFIG_DIR = process.env.DELIGHT_CLAUDE_CONFIG_DIR || process.env.HAPPY_CLAUDE_CONFIG_DIR;
    } else if (process.env.DELIGHT_CLAUDE_PROFILE || process.env.HAPPY_CLAUDE_PROFILE) {
        const profile = process.env.DELIGHT_CLAUDE_PROFILE || process.env.HAPPY_CLAUDE_PROFILE;
        process.env.CLAUDE_CONFIG_DIR = path.join(os.homedir(), `.claude-${profile}`);
    }
}

// Allow overriding which Claude SDK/CLI module to load
// Set DELIGHT_CLAUDE_CLI to a package name or absolute path
const customCliModule = process.env.DELIGHT_CLAUDE_CLI || process.env.HAPPY_CLAUDE_CLI;
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
    let queryRunning = false;

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
                    // Abort current query
                    if (currentAbortController) {
                        debugLog('Aborting current query');
                        currentAbortController.abort();
                    }
                    if (currentChild && !currentChild.killed) {
                        try {
                            currentChild.kill('SIGTERM');
                        } catch { }
                    }
                    break;

                case 'shutdown':
                    // Graceful shutdown
                    debugLog('Shutdown requested');
                    messageQueue.close();
                    if (currentAbortController) {
                        currentAbortController.abort();
                    }
                    if (currentChild && !currentChild.killed) {
                        try {
                            currentChild.kill('SIGTERM');
                        } catch { }
                    }
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
        if (currentAbortController) {
            currentAbortController.abort();
        }
        if (currentChild && !currentChild.killed) {
            try {
                currentChild.kill('SIGTERM');
            } catch { }
        }
    });

    // Signal ready
    sendMessage({ type: 'ready' });

    // Track current per-session overrides
    let currentPermissionMode = 'default';
    let currentModel = undefined;
    let currentFallbackModel = undefined;
    let currentCustomSystemPrompt = undefined;
    let currentAppendSystemPrompt = undefined;
    let currentAllowedTools = undefined;
    let currentDisallowedTools = undefined;

    let currentChild = null;

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

    async function runQuery(messageContent) {
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

        const exitPromise = new Promise((resolve) => {
            child.on('close', (code) => resolve(code));
        });

    const stdout = readline.createInterface({ input: child.stdout });
    stdout.on('line', (line) => {
        if (!line.trim()) return;
        let msg;
        try {
            msg = JSON.parse(line);
        } catch (err) {
            debugLog('Invalid SDK message:', err.message);
            return;
        }

        if (msg.type === 'message' && (msg.role === 'assistant' || msg.role === 'user')) {
            const messageId = msg.id || crypto.randomUUID();
            const content = Array.isArray(msg.content) ? msg.content : [];
            const rawRecord = {
                role: 'agent',
                content: {
                    type: 'output',
                    data: {
                        type: msg.role === 'assistant' ? 'assistant' : 'user',
                        isSidechain: false,
                        isCompactSummary: false,
                        isMeta: false,
                        uuid: messageId,
                        parentUuid: null,
                        message: msg.role === 'assistant'
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

            debugLog('SDK message:', msg.type);
            sendMessage(msg);

            if (msg.type === 'system' && msg.subtype === 'init' && msg.session_id) {
                resumeSessionId = msg.session_id;
                debugLog('Session ID:', resumeSessionId);
            }

            if (msg.type === 'result') {
                try {
                    child.stdin.end();
                } catch { }
            }
        });

        if (debug) {
            const errReader = readline.createInterface({ input: child.stderr });
            errReader.on('line', (line) => {
                console.error('[claude stderr]', line);
            });
        }

        const userMessage = {
            type: 'user',
            message: {
                role: 'user',
                content: messageContent
            }
        };
        child.stdin.write(JSON.stringify(userMessage) + '\n');

        const exitCode = await exitPromise;
        if (exitCode !== 0) {
            sendMessage({ type: 'error', error: `Claude Code exited with code ${exitCode}` });
        }
    }

    // Main query loop - runs until shutdown
    while (!messageQueue.closed) {
        const firstMessage = await messageQueue.pull();
        if (!firstMessage) {
            debugLog('No more messages, exiting');
            break;
        }

        debugLog('Starting query with message');

        const meta = firstMessage.meta || {};
        if (Object.prototype.hasOwnProperty.call(meta, 'permissionMode') && typeof meta.permissionMode === 'string') {
            currentPermissionMode = meta.permissionMode;
        }
        if (Object.prototype.hasOwnProperty.call(meta, 'model')) {
            currentModel = meta.model || undefined;
        }
        if (Object.prototype.hasOwnProperty.call(meta, 'fallbackModel')) {
            currentFallbackModel = meta.fallbackModel || undefined;
        }
        if (Object.prototype.hasOwnProperty.call(meta, 'customSystemPrompt')) {
            currentCustomSystemPrompt = meta.customSystemPrompt || undefined;
        }
        if (Object.prototype.hasOwnProperty.call(meta, 'appendSystemPrompt')) {
            currentAppendSystemPrompt = meta.appendSystemPrompt || undefined;
        }
        if (Object.prototype.hasOwnProperty.call(meta, 'allowedTools')) {
            currentAllowedTools = meta.allowedTools || undefined;
        }
        if (Object.prototype.hasOwnProperty.call(meta, 'disallowedTools')) {
            currentDisallowedTools = meta.disallowedTools || undefined;
        }

        currentAbortController = new AbortController();
        queryRunning = true;

        try {
            await runQuery(firstMessage.content);
        } catch (err) {
            debugLog('Query error:', err.message);
            sendMessage({ type: 'error', error: err.message });
        } finally {
            queryRunning = false;
            currentAbortController = null;
            currentChild = null;
        }
    }

    debugLog('Bridge shutting down');
    process.exit(0);
}

main().catch(err => {
    sendMessage({ type: 'error', error: err.message });
    process.exit(1);
});
