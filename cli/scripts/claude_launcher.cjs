const crypto = require('crypto');
const fs = require('fs');
const os = require('os');

// Disable autoupdater (never works really)
process.env.DISABLE_AUTOUPDATER = '1';

// Optional profile/config selection for multiple Claude installs
// - DELIGHT_CLAUDE_CONFIG_DIR: absolute path to a config dir
// - DELIGHT_CLAUDE_PROFILE: shortcut to ~/.claude-<profile>
if (!process.env.CLAUDE_CONFIG_DIR) {
    if (process.env.DELIGHT_CLAUDE_CONFIG_DIR || process.env.HAPPY_CLAUDE_CONFIG_DIR) {
        process.env.CLAUDE_CONFIG_DIR = process.env.DELIGHT_CLAUDE_CONFIG_DIR || process.env.HAPPY_CLAUDE_CONFIG_DIR;
    } else if (process.env.DELIGHT_CLAUDE_PROFILE || process.env.HAPPY_CLAUDE_PROFILE) {
        const profile = process.env.DELIGHT_CLAUDE_PROFILE || process.env.HAPPY_CLAUDE_PROFILE;
        process.env.CLAUDE_CONFIG_DIR = require('path').join(os.homedir(), `.claude-${profile}`);
    }
}

// Helper to write JSON messages to fd 3
function writeMessage(message) {
    try {
        fs.writeSync(3, JSON.stringify(message) + '\n');
    } catch (err) {
        // fd 3 not available, ignore
    }
}

// Intercept crypto.randomUUID
const originalRandomUUID = crypto.randomUUID;
Object.defineProperty(global, 'crypto', {
    configurable: true,
    enumerable: true,
    get() {
        return {
            randomUUID: () => {
                const uuid = originalRandomUUID();
                writeMessage({ type: 'uuid', value: uuid });
                return uuid;
            }
        };
    }
});
Object.defineProperty(crypto, 'randomUUID', {
    configurable: true,
    enumerable: true,
    get() {
        return () => {
            const uuid = originalRandomUUID();
            writeMessage({ type: 'uuid', value: uuid });
            return uuid;
        }
    }
});

// Intercept fetch to track activity
const originalFetch = global.fetch;
let fetchCounter = 0;

global.fetch = function(...args) {
    const id = ++fetchCounter;
    const url = typeof args[0] === 'string' ? args[0] : args[0]?.url || '';
    const method = args[1]?.method || 'GET';

    // Parse URL for privacy
    let hostname = '';
    let path = '';
    try {
        const urlObj = new URL(url, 'http://localhost');
        hostname = urlObj.hostname;
        path = urlObj.pathname;
    } catch (e) {
        // If URL parsing fails, use defaults
        hostname = 'unknown';
        path = url;
    }

    // Send fetch start event
    writeMessage({
        type: 'fetch-start',
        id,
        hostname,
        path,
        method,
        timestamp: Date.now()
    });

    // Execute the original fetch immediately
    const fetchPromise = originalFetch(...args);

    // Attach handlers to send fetch end event
    const sendEnd = () => {
        writeMessage({
            type: 'fetch-end',
            id,
            timestamp: Date.now()
        });
    };

    // Send end event on both success and failure
    fetchPromise.then(sendEnd, sendEnd);

    // Return the original promise unchanged
    return fetchPromise;
};

// Preserve fetch properties
Object.defineProperty(global.fetch, 'name', { value: 'fetch' });
Object.defineProperty(global.fetch, 'length', { value: originalFetch.length });

// Resolve the path to claude-code CLI relative to this script's location
// This allows us to run from any directory while still finding the package
const path = require('path');
const scriptDir = __dirname;
const nodeModulesPath = path.join(scriptDir, '..', 'node_modules');

// Add our node_modules to the module search path
module.paths.unshift(nodeModulesPath);

// Allow overriding which Claude CLI module to load (supports multiple installs)
// Set DELIGHT_CLAUDE_CLI to a package name or absolute path, e.g.:
//   DELIGHT_CLAUDE_CLI=claude-work    or    DELIGHT_CLAUDE_CLI=/path/to/cli.js
const customCliModule = process.env.DELIGHT_CLAUDE_CLI || process.env.HAPPY_CLAUDE_CLI;
const defaultTarget = '@anthropic-ai/claude-code/cli.js';
const { execSync } = require('child_process');

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

const claudeCliPath = resolveCli(customCliModule);
import(claudeCliPath)
