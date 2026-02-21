const AUTO_REFRESH_MS = 4500;
const AUTO_REFRESH_SLOW_EVERY = 3;
const STREAM_DEBOUNCE_MS = 220;
const STREAM_DEBOUNCE_FAST_MS = 80;
const SCROLL_STICKY_THRESHOLD_PX = 90;
const MAX_ACTIVITY_LOG_CHARS = 12000;
const MESSAGE_PAGE_LIMIT = 50;
const DEFAULT_VISIBLE_MESSAGE_WINDOW = 140;
const MAX_VISIBLE_MESSAGE_WINDOW = 200;
const MESSAGE_WINDOW_EXTEND_STEP = 50;
const SCROLL_TOP_TRIGGER_PX = 2;
const PULL_TO_LOAD_TRIGGER_PX = 52;
const LOAD_OLDER_COOLDOWN_MS = 650;

const state = {
  apiToken: localStorage.getItem("delight.apiToken") || "",
  streamSince: Number(localStorage.getItem("delight.streamSince") || "0"),
  streamEpoch: localStorage.getItem("delight.streamEpoch") || "",
  eventSource: null,
  sessions: [],
  terminals: [],
  selectedSessionID: "",
  oldestSeqBySession: {},
  hasMoreBySession: {},
  visibleMessageLimitBySession: {},
  loadingOlderBySession: {},
  lastOlderRequestMsBySession: {},
  messagesBySession: {},
  optimisticBySession: {},
  promptHistoryBySession: {},
  promptCursorBySession: {},
  promptDraftBySession: {},
  permissionQueue: [],
  activePermission: null,
  preferences: {
    appearanceMode: "system",
    globalTranscript: {
      showToolUse: true,
      showToolOutput: true,
      showReasoningSummaries: true,
      fontSize: 14,
    },
    perTerminalTranscript: {},
  },
  capabilitiesBySession: {},
  lastLogServerURL: "",
  isConnected: false,
  autoRefreshTimer: null,
  autoRefreshTick: 0,
  refreshInFlight: false,
  refreshDebounceTimer: null,
  refreshPlan: {
    sessions: false,
    terminals: false,
    messages: false,
    capabilities: false,
  },
};

const ui = {
  serverURL: document.getElementById("serverURL"),
  masterKey: document.getElementById("masterKey"),
  pairURL: document.getElementById("pairURL"),
  pairReceipt: document.getElementById("pairReceipt"),
  sessionsList: document.getElementById("sessionsList"),
  terminalsList: document.getElementById("terminalsList"),
  messages: document.getElementById("messages"),
  sessionIdentity: document.getElementById("sessionIdentity"),
  messageInput: document.getElementById("messageInput"),
  activityLog: document.getElementById("activityLog"),
  connectionBadge: document.getElementById("connectionBadge"),
  streamBadge: document.getElementById("streamBadge"),
  refreshBadge: document.getElementById("refreshBadge"),
  actionBadge: document.getElementById("actionBadge"),
  takeControlBtn: document.getElementById("takeControlBtn"),
  openModelBtn: document.getElementById("openModelBtn"),
  scrollBottomBtn: document.getElementById("scrollBottomBtn"),
  closeModelBtn: document.getElementById("closeModelBtn"),
  openTerminalsBtn: document.getElementById("openTerminalsBtn"),
  closeTerminalsBtn: document.getElementById("closeTerminalsBtn"),
  openSettingsBtn: document.getElementById("openSettingsBtn"),
  closeSettingsBtn: document.getElementById("closeSettingsBtn"),
  settingsScreen: document.getElementById("settingsScreen"),
  terminalsScreen: document.getElementById("terminalsScreen"),
  terminalPickerList: document.getElementById("terminalPickerList"),
  refreshTerminalPickerBtn: document.getElementById("refreshTerminalPickerBtn"),
  appearanceSelect: document.getElementById("appearanceSelect"),
  fontSizeInput: document.getElementById("fontSizeInput"),
  showToolUse: document.getElementById("showToolUse"),
  showToolOutput: document.getElementById("showToolOutput"),
  showReasoning: document.getElementById("showReasoning"),
  modelSelect: document.getElementById("modelSelect"),
  reasoningSelect: document.getElementById("reasoningSelect"),
  permissionModeSelect: document.getElementById("permissionModeSelect"),
  debugLogs: document.getElementById("debugLogs"),
  permissionDialog: document.getElementById("permissionDialog"),
  modelDialog: document.getElementById("modelDialog"),
  permissionBody: document.getElementById("permissionBody"),
  permissionMessage: document.getElementById("permissionMessage"),
  scanVideo: document.getElementById("scanVideo"),
};

let scanStream = null;
let scanTimer = null;
let actionBadgeTimer = null;

let markdownRenderer = null;
let markdownRendererName = "fallback";

function logLine(line) {
  const ts = new Date().toISOString();
  const next = `${ts} ${line}\n${ui.activityLog.textContent}`;
  ui.activityLog.textContent = next.slice(0, MAX_ACTIVITY_LOG_CHARS);
}

function cloneJSON(value) {
  if (typeof structuredClone === "function") {
    return structuredClone(value);
  }
  return JSON.parse(JSON.stringify(value));
}

function initializeMarkdownRenderer() {
  // Optional markdown-it support if it exists in the page for local testing.
  if (typeof window.markdownit === "function") {
    markdownRenderer = window.markdownit({
      html: false,
      linkify: true,
      typographer: false,
      breaks: true,
    });
    markdownRendererName = "markdown-it";
    logLine("markdown renderer: markdown-it");
    return;
  }
  markdownRenderer = null;
  markdownRendererName = "fallback";
  logLine("markdown renderer: built-in");
}

function updateRefreshBadge(active) {
  ui.refreshBadge.textContent = active ? "auto: syncing" : "auto: on";
}

function setSettingsScreenOpen(open) {
  if (!ui.settingsScreen) {
    return;
  }
  ui.settingsScreen.classList.toggle("hidden", !open);
  ui.settingsScreen.setAttribute("aria-hidden", open ? "false" : "true");
}

function setTerminalsScreenOpen(open) {
  if (!ui.terminalsScreen) {
    return;
  }
  ui.terminalsScreen.classList.toggle("hidden", !open);
  ui.terminalsScreen.setAttribute("aria-hidden", open ? "false" : "true");
}

function setModelDialogOpen(open) {
  if (!ui.modelDialog) {
    return;
  }
  if (open) {
    if (!ui.modelDialog.open) {
      ui.modelDialog.showModal();
    }
  } else if (ui.modelDialog.open) {
    ui.modelDialog.close();
  }
}

function setActionBadge(text, level = "idle") {
  if (!ui.actionBadge) {
    return;
  }
  ui.actionBadge.textContent = text;
  ui.actionBadge.dataset.level = level;
}

function clearActionBadgeSoon() {
  if (actionBadgeTimer) {
    clearTimeout(actionBadgeTimer);
  }
  actionBadgeTimer = window.setTimeout(() => {
    setActionBadge("action: idle", "idle");
  }, 1400);
}

function isNearBottom(element) {
  if (!element) {
    return true;
  }
  const distance = element.scrollHeight - element.scrollTop - element.clientHeight;
  return distance <= SCROLL_STICKY_THRESHOLD_PX;
}

function isSessionWorking(session) {
  return Boolean(session?.ui?.working);
}

function setConnectionState(connected) {
  state.isConnected = Boolean(connected);
  if (ui.connectionBadge) {
    ui.connectionBadge.textContent = state.isConnected ? "connected" : "disconnected";
  }
  updateAccountConnectionButton();
  updateMainInteractivity();
}

function updateAccountConnectionButton() {
  const button = document.getElementById("connectToggleBtn");
  if (!button) {
    return;
  }
  if (state.isConnected) {
    button.textContent = "Disconnect";
    button.classList.add("warn");
    button.title = "Disconnect from server";
    return;
  }
  button.textContent = "Connect";
  button.classList.remove("warn");
  button.title = "Connect to server";
}

function updateMainInteractivity() {
  const main = document.querySelector("main");
  if (!main) {
    return;
  }

  main.querySelectorAll("button, input, textarea, select").forEach((node) => {
    node.disabled = !state.isConnected;
  });

  if (!state.isConnected) {
    if (ui.openTerminalsBtn) {
      ui.openTerminalsBtn.disabled = true;
      ui.openTerminalsBtn.title = "Connect first";
    }
    setTerminalsScreenOpen(false);
    if (ui.takeControlBtn) {
      ui.takeControlBtn.hidden = true;
      ui.takeControlBtn.title = "Connect first";
    }
    if (ui.openModelBtn) {
      ui.openModelBtn.title = "Connect first";
    }
    if (ui.scrollBottomBtn) {
      ui.scrollBottomBtn.hidden = true;
    }
    updateComposerActionState(null);
    return;
  }

  if (ui.openTerminalsBtn) {
    ui.openTerminalsBtn.disabled = false;
    ui.openTerminalsBtn.title = "Terminals";
  }
  updateSessionActionState(selectedSession());
  updateScrollBottomVisibility();
}

function updateScrollBottomVisibility() {
  if (!ui.scrollBottomBtn || !ui.messages) {
    return;
  }
  ui.scrollBottomBtn.hidden = !state.isConnected || isNearBottom(ui.messages);
}

function updateComposerActionState(session) {
  const sendBtn = document.getElementById("sendBtn");
  if (!sendBtn) {
    return;
  }

  if (!state.isConnected) {
    sendBtn.textContent = "Send";
    sendBtn.classList.remove("warn");
    sendBtn.disabled = true;
    sendBtn.title = "Connect first";
    return;
  }

  if (!session) {
    sendBtn.textContent = "Send";
    sendBtn.classList.remove("warn");
    sendBtn.disabled = true;
    sendBtn.title = "Select a session first";
    return;
  }

  const working = isSessionWorking(session);
  sendBtn.textContent = working ? "Abort Turn" : "Send";
  sendBtn.classList.toggle("warn", working);
  sendBtn.disabled = false;
  sendBtn.title = working ? "Abort current turn" : "Send message";
}

async function api(path, options = {}) {
  const method = options.method || "GET";
  const headers = {
    "Content-Type": "application/json",
    ...(options.headers || {}),
  };
  const token = String(state.apiToken || "").trim();
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }

  const response = await fetch(path, {
    method,
    headers,
    body: options.body ? JSON.stringify(options.body) : undefined,
  });

  const text = await response.text();
  if (!response.ok) {
    let message = `HTTP ${response.status}`;
    try {
      const parsed = JSON.parse(text);
      message = parsed.message || parsed.error || message;
    } catch {
      if (text.trim()) {
        message = text.trim();
      }
    }
    throw new Error(message);
  }

  if (!text) {
    return {};
  }

  try {
    return JSON.parse(text);
  } catch {
    return { raw: text };
  }
}

function selectedSession() {
  return state.sessions.find((item) => item.id === state.selectedSessionID) || null;
}

function escapeHTML(raw) {
  return String(raw)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function parseJSONString(raw) {
  const trimmed = String(raw || "").trim();
  if (!trimmed) {
    return null;
  }
  const first = trimmed[0];
  if (first !== "{" && first !== "[") {
    return null;
  }
  try {
    return JSON.parse(trimmed);
  } catch {
    return null;
  }
}

function prettifyJSONText(raw) {
  const parsed = parseJSONString(raw);
  if (parsed == null) {
    return String(raw || "");
  }
  return `\`\`\`json\n${JSON.stringify(parsed, null, 2)}\n\`\`\``;
}

function peelMessagePayload(value) {
  let cursor = value;
  for (let i = 0; i < 8; i += 1) {
    if (!cursor || typeof cursor !== "object") {
      break;
    }
    if (cursor.type === "output" && cursor.data && typeof cursor.data === "object") {
      cursor = cursor.data;
      continue;
    }
    if (cursor.message && typeof cursor.message === "object") {
      cursor = cursor.message;
      continue;
    }
    if (
      cursor.content &&
      typeof cursor.content === "object" &&
      !Array.isArray(cursor.content) &&
      typeof cursor.content.text !== "string"
    ) {
      cursor = cursor.content;
      continue;
    }
    break;
  }
  return cursor;
}

function asTrimmedString(value) {
  return typeof value === "string" ? value.trim() : "";
}

function extractUIEventPayloadFromNode(node) {
  if (!node || typeof node !== "object" || Array.isArray(node)) {
    return null;
  }

  const kind = asTrimmedString(node.kind || node.eventKind || node.k);
  const briefMarkdown = asTrimmedString(node.briefMarkdown || node.brief_markdown || "");
  const fullMarkdown = asTrimmedString(node.fullMarkdown || node.full_markdown || "");
  const phase = asTrimmedString(node.phase || "");
  const eventID = asTrimmedString(node.eventId || node.eventID || "");
  const hasUIEventShape = Boolean(kind) && (
    "briefMarkdown" in node ||
    "brief_markdown" in node ||
    "fullMarkdown" in node ||
    "full_markdown" in node ||
    "phase" in node ||
    "eventId" in node ||
    "eventID" in node
  );

  if (!hasUIEventShape) {
    return null;
  }

  return {
    kind,
    phase,
    eventID,
    briefMarkdown,
    fullMarkdown,
  };
}

function extractUIEventPayload(message) {
  const roots = [];
  if (message?.content != null) {
    roots.push(message.content);
  }
  const unwrapped = unwrapAgentMessageEnvelope(message?.content);
  if (unwrapped != null) {
    roots.push(unwrapped);
  }

  const queue = roots.slice(0, 8);
  const seen = new Set();
  let hops = 0;
  while (queue.length > 0 && hops < 120) {
    hops += 1;
    const node = queue.shift();
    if (!node || typeof node !== "object") {
      continue;
    }
    if (seen.has(node)) {
      continue;
    }
    seen.add(node);

    const payload = extractUIEventPayloadFromNode(node);
    if (payload) {
      return payload;
    }

    const nestedKeys = ["content", "data", "message", "body", "payload", "update", "event"];
    for (const key of nestedKeys) {
      const nested = node[key];
      if (nested && typeof nested === "object") {
        queue.push(nested);
      }
    }
  }

  return null;
}

function normalizeBlockType(type) {
  return String(type || "").trim().toLowerCase().replaceAll("_", "-");
}

function extractContentBlocks(message) {
  let content = message?.content;
  if (typeof content === "string") {
    content = parseJSONString(content);
  }
  if (!content || typeof content !== "object") {
    return null;
  }
  const unwrapped = unwrapAgentMessageEnvelope(content);
  if (!unwrapped || typeof unwrapped !== "object") {
    return null;
  }
  const blocks = Array.isArray(unwrapped.content) ? unwrapped.content : null;
  if (!blocks || blocks.length === 0) {
    return null;
  }
  const hasStructured = blocks.some((b) => {
    const bt = normalizeBlockType(b?.type);
    return bt === "tool-use" || bt === "tool-result" || bt === "thinking";
  });
  if (!hasStructured) {
    return null;
  }
  return blocks;
}

function renderContentBlocksHTML(blocks, pref) {
  const chunks = [];
  for (const block of blocks) {
    if (!block || typeof block !== "object") {
      continue;
    }
    const blockType = normalizeBlockType(block.type);
    switch (blockType) {
      case "text": {
        const text = String(block.text || "").trim();
        if (text) {
          chunks.push(renderMarkdown(text));
        }
        break;
      }
      case "tool-use": {
        const name = String(block.name || "tool");
        if (pref.showToolUse === false) {
          chunks.push(renderToolCalloutHTML({
            title: name,
            icon: toolIconLabel(name),
            command: "",
            outputBlocks: [],
          }));
          break;
        }
        const input = block.input;
        let command = "";
        if (["bash", "shell", "sh", "zsh", "cmd", "powershell"].includes(name.toLowerCase()) && input?.command) {
          command = String(input.command);
        } else if (input) {
          command = typeof input === "string" ? input : JSON.stringify(input, null, 2);
        }
        chunks.push(renderToolCalloutHTML({
          title: name,
          icon: toolIconLabel(name),
          command,
          outputBlocks: [],
        }));
        break;
      }
      case "tool-result": {
        if (pref.showToolOutput === false) {
          break;
        }
        const resultContent = block.content;
        const isError = Boolean(block.is_error);
        let outputText = "";
        if (typeof resultContent === "string") {
          outputText = resultContent;
        } else if (Array.isArray(resultContent)) {
          outputText = resultContent
            .map((c) => (typeof c === "string" ? c : String(c?.text || "")))
            .filter(Boolean)
            .join("\n");
        } else if (resultContent && typeof resultContent === "object") {
          outputText = resultContent.text || JSON.stringify(resultContent, null, 2);
        }
        if (outputText.trim()) {
          const label = isError ? "Error" : "Tool output";
          chunks.push(
            `<div class="tool-callout">` +
              `<div class="tool-callout-head">` +
                `<span class="tool-callout-chip">tool</span>` +
                `<strong>${escapeHTML(label)}</strong>` +
              `</div>` +
              `<pre class="tool-callout-code"><code>${escapeHTML(outputText)}</code></pre>` +
            `</div>`
          );
        }
        break;
      }
      case "thinking": {
        if (pref.showReasoningSummaries === false) {
          break;
        }
        const thinking = String(block.thinking || block.text || "").trim();
        if (thinking) {
          chunks.push(renderReasoningCalloutHTML("Reasoning\n\n" + thinking));
        }
        break;
      }
      default:
        break;
    }
  }
  return chunks.join("");
}

function isPatchUIEvent(payload) {
  const brief = asTrimmedString(payload?.briefMarkdown);
  if (brief.startsWith("Patch")) {
    return true;
  }
  return String(payload?.fullMarkdown || "").includes("```diff") || brief.includes("```diff");
}

function stripToolOutputSection(markdown) {
  const trimmed = String(markdown || "").trim();
  if (!trimmed) {
    return "";
  }
  const patterns = ["\n\nOutput:\n", "\n\nOutput:\r\n", "\nOutput:\n", "\nOutput:\r\n"];
  for (const pattern of patterns) {
    const index = trimmed.indexOf(pattern);
    if (index >= 0) {
      return trimmed.slice(0, index).trim();
    }
  }
  return trimmed;
}

function stripReasoningHeading(markdown) {
  let value = String(markdown || "").trim();
  if (!value || value === "Reasoning") {
    return "";
  }
  if (value.startsWith("Reasoning\n\n")) {
    value = value.slice("Reasoning\n\n".length);
  } else if (value.startsWith("Reasoning\n")) {
    value = value.slice("Reasoning\n".length);
  }
  return value.trim();
}

function uiEventMarkdown(payload, pref) {
  const brief = asTrimmedString(payload?.briefMarkdown);
  const full = asTrimmedString(payload?.fullMarkdown);
  const kind = asTrimmedString(payload?.kind).toLowerCase();

  if (kind === "tool") {
    if (isPatchUIEvent(payload)) {
      return full || brief;
    }
    if (pref.showToolOutput) {
      return full || brief;
    }
    const withoutOutput = stripToolOutputSection(full);
    return withoutOutput || brief || full;
  }

  if (kind === "reasoning") {
    const strippedBrief = stripReasoningHeading(brief);
    const strippedFull = stripReasoningHeading(full);
    if (strippedFull) {
      return full;
    }
    if (strippedBrief) {
      return brief;
    }
    return "";
  }

  return brief || full;
}

function extractToolCallFromMarkdown(markdown) {
  let remaining = String(markdown || "").trim();
  if (!remaining || !remaining.toLowerCase().startsWith("tool:")) {
    return null;
  }

  const lineEnd = remaining.indexOf("\n");
  const line = lineEnd >= 0 ? remaining.slice(0, lineEnd) : remaining;
  remaining = lineEnd >= 0 ? remaining.slice(lineEnd + 1) : "";

  let payload = line.trim();
  if (payload.toLowerCase().startsWith("tool:")) {
    payload = payload.slice("tool:".length).trim();
  }

  let title = payload;
  const firstTick = payload.indexOf("`");
  if (firstTick >= 0) {
    const secondTick = payload.indexOf("`", firstTick + 1);
    if (secondTick > firstTick) {
      title = payload.slice(firstTick + 1, secondTick).trim();
    }
  }

  for (let i = 0; i < 2; i += 1) {
    if (remaining.startsWith("\n")) {
      remaining = remaining.slice(1);
    }
  }
  return { title: title || "Tool", body: remaining };
}

function stripToolCallTitle(markdown) {
  let remaining = String(markdown || "").trim();
  if (!remaining.toLowerCase().startsWith("tool:")) {
    return remaining;
  }
  const lineEnd = remaining.indexOf("\n");
  remaining = lineEnd >= 0 ? remaining.slice(lineEnd + 1) : "";
  for (let i = 0; i < 2; i += 1) {
    if (remaining.startsWith("\n")) {
      remaining = remaining.slice(1);
    }
  }
  return remaining.trim();
}

function extractFirstCodeFence(markdown) {
  const raw = String(markdown || "").trim();
  if (!raw) {
    return null;
  }

  const fenceStart = raw.indexOf("```");
  if (fenceStart < 0) {
    return null;
  }

  const prefix = raw.slice(0, fenceStart);
  const afterStart = raw.slice(fenceStart + 3);
  const lineEnd = afterStart.indexOf("\n");
  const language = lineEnd >= 0 ? afterStart.slice(0, lineEnd).trim() : "";
  const afterLang = lineEnd >= 0 ? afterStart.slice(lineEnd + 1) : "";
  const endFence = afterLang.indexOf("```");
  if (endFence < 0) {
    return null;
  }

  const content = afterLang.slice(0, endFence).replace(/^\n+|\n+$/g, "");
  const suffix = afterLang.slice(endFence + 3);
  return {
    language: language || null,
    content,
    remainder: `${prefix}${suffix}`.trim(),
  };
}

function stripLeadingOutputHeading(markdown) {
  let value = String(markdown || "").trim();
  const lower = value.toLowerCase();
  if (!lower.startsWith("output:") && !lower.startsWith("output")) {
    return value;
  }
  const lineEnd = value.indexOf("\n");
  value = lineEnd >= 0 ? value.slice(lineEnd + 1) : "";
  for (let i = 0; i < 2; i += 1) {
    if (value.startsWith("\n")) {
      value = value.slice(1);
    }
  }
  return value.trim();
}

function extractToolOutputSection(markdown) {
  const trimmed = String(markdown || "").trim();
  if (!trimmed) {
    return "";
  }
  const patterns = ["\n\nOutput:\n", "\n\nOutput:\r\n", "\nOutput:\n", "\nOutput:\r\n"];
  for (const pattern of patterns) {
    const index = trimmed.indexOf(pattern);
    if (index >= 0) {
      return trimmed.slice(index + pattern.length).trim();
    }
  }
  const normalized = stripLeadingOutputHeading(trimmed);
  if (normalized !== trimmed) {
    return normalized;
  }
  return "";
}

function parseToolOutputBlocks(markdown) {
  const trimmed = String(markdown || "").trim();
  if (!trimmed) {
    return [];
  }

  const blocks = [];
  const fencePattern = /```([^\n`]*)\n([\s\S]*?)```/g;
  let cursor = 0;
  let match = fencePattern.exec(trimmed);
  while (match) {
    const full = match[0];
    const language = asTrimmedString(match[1]) || null;
    const code = String(match[2] || "").replace(/^\n+|\n+$/g, "");
    const prefix = trimmed.slice(cursor, match.index).trim();
    if (prefix) {
      blocks.push({ type: "text", content: prefix });
    }
    if (code) {
      blocks.push({ type: "code", language, content: code });
    }
    cursor = match.index + full.length;
    match = fencePattern.exec(trimmed);
  }

  const tail = trimmed.slice(cursor).trim();
  if (tail) {
    blocks.push({ type: "text", content: tail });
  }

  if (blocks.length === 0) {
    blocks.push({ type: "text", content: trimmed });
  }
  return blocks;
}

function toolIconLabel(title) {
  const normalized = asTrimmedString(title).toLowerCase();
  if (["shell", "bash", "sh", "zsh", "cmd", "powershell"].includes(normalized)) {
    return "terminal";
  }
  return "tool";
}

function toolCalloutSummary(payload, markdown, pref) {
  const trimmed = String(markdown || "").trim();
  if (!trimmed) {
    return null;
  }

  const tool = extractToolCallFromMarkdown(trimmed) || extractToolCallFromMarkdown(payload?.briefMarkdown || "");
  const title = tool?.title || "Tool";
  const icon = toolIconLabel(title);

  const bodyCandidate = tool ? stripToolCallTitle(tool.body) : trimmed;
  const bodySource = bodyCandidate || trimmed;
  const extraction = extractFirstCodeFence(bodySource) || extractFirstCodeFence(trimmed);
  const command = String(extraction?.content || bodySource).trim();

  let outputBlocks = [];
  if (pref.showToolOutput && extraction?.remainder) {
    const outputMarkdown = extractToolOutputSection(extraction.remainder);
    outputBlocks = parseToolOutputBlocks(outputMarkdown);
  }

  return {
    title,
    icon,
    command,
    outputBlocks,
  };
}

function renderToolCalloutHTML(summary) {
  if (!summary) {
    return "";
  }
  const chunks = [];
  chunks.push('<div class="tool-callout">');
  chunks.push(
    `<div class="tool-callout-head">` +
      `<span class="tool-callout-chip">${escapeHTML(summary.icon)}</span>` +
      `<strong>${escapeHTML(summary.title || "Tool")}</strong>` +
    `</div>`
  );

  if (summary.command && summary.command.trim()) {
    chunks.push(`<pre class="tool-callout-code"><code>${escapeHTML(summary.command)}</code></pre>`);
  }

  if (Array.isArray(summary.outputBlocks) && summary.outputBlocks.length > 0) {
    chunks.push('<div class="tool-callout-output-label">Output</div>');
    for (const block of summary.outputBlocks) {
      if (block.type === "code") {
        const klass = block.language ? ` class="language-${escapeHTML(block.language)}"` : "";
        chunks.push(`<pre class="tool-callout-code"><code${klass}>${escapeHTML(block.content || "")}</code></pre>`);
      } else if (block.type === "text") {
        chunks.push(`<pre class="tool-callout-code"><code>${escapeHTML(block.content || "")}</code></pre>`);
      }
    }
  }

  chunks.push("</div>");
  return chunks.join("");
}

function renderReasoningCalloutHTML(markdown) {
  const content = stripReasoningHeading(markdown);
  if (!content) {
    return "";
  }
  return (
    `<div class="reasoning-callout">` +
      `<div class="reasoning-callout-head">Reasoning</div>` +
      `<div class="reasoning-callout-body">${renderMarkdown(content)}</div>` +
    `</div>`
  );
}

function renderUIEventHTML(payload, pref) {
  const kind = asTrimmedString(payload?.kind).toLowerCase();
  const markdown = uiEventMarkdown(payload, pref);

  if (kind === "tool") {
    if (isPatchUIEvent(payload)) {
      return renderMarkdown(markdown);
    }
    if (!pref.showToolUse) {
      return renderToolCalloutHTML({
        title: "Tool use",
        icon: toolIconLabel("tool"),
        command: "",
        outputBlocks: [],
      });
    }
    const summary = toolCalloutSummary(payload, markdown, pref);
    if (summary) {
      return renderToolCalloutHTML(summary);
    }
    if (!markdown.trim()) {
      return "";
    }
    return renderMarkdown(markdown);
  }

  if (kind === "reasoning") {
    return renderReasoningCalloutHTML(markdown);
  }

  if (!markdown.trim()) {
    return "";
  }
  return renderMarkdown(markdown);
}

function parseMetadata(session) {
  const raw = session?.metadata;
  if (typeof raw !== "string" || !raw.trim()) {
    return {};
  }
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

function parseAgentState(session) {
  const raw = session?.agentState;
  if (typeof raw !== "string" || !raw.trim()) {
    return {};
  }
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

function applyTheme(mode) {
  if (mode === "dark" || mode === "light") {
    document.documentElement.setAttribute("data-theme", mode);
    return;
  }
  const systemDark = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.setAttribute("data-theme", systemDark ? "dark" : "light");
}

function currentTranscriptPreferences() {
  return state.preferences.globalTranscript || {
    showToolUse: true,
    showToolOutput: true,
    showReasoningSummaries: true,
    fontSize: 14,
  };
}

function terminalIDForSession(session) {
  if (!session) {
    return "";
  }
  const direct = String(session.terminalId || session.terminalID || "").trim();
  if (direct) {
    return direct;
  }
  const metadata = parseMetadata(session);
  return String(metadata.terminalId || metadata.terminalID || "").trim();
}

function findSessionByTerminalID(terminalID) {
  const wanted = String(terminalID || "").trim();
  if (!wanted) {
    return null;
  }
  return state.sessions.find((session) => terminalIDForSession(session) === wanted) || null;
}

function pathFromMetadata(metadata) {
  if (!metadata || typeof metadata !== "object") {
    return "";
  }
  return String(metadata.path || metadata.cwd || metadata.dir || metadata.workdir || "").trim();
}

function findTerminalByID(terminalID) {
  const wanted = String(terminalID || "").trim();
  if (!wanted) {
    return null;
  }
  return state.terminals.find((terminal) => String(terminal.id || "").trim() === wanted) || null;
}

function statusForTerminalID(terminalID) {
  const session = findSessionByTerminalID(terminalID);
  const uiState = session?.ui || null;
  const terminal = findTerminalByID(terminalID);

  const working = Boolean(uiState?.working) || Boolean(session?.working);
  if (working) {
    return { status: "working", title: "working" };
  }

  const online = Boolean(uiState?.online) || Boolean(session?.active) || Boolean(terminal?.active);
  if (online) {
    return { status: "online", title: "online" };
  }
  return { status: "offline", title: "offline" };
}

async function focusTerminalSession(terminalID) {
  const wanted = String(terminalID || "").trim();
  if (!wanted) {
    return;
  }

  let session = findSessionByTerminalID(wanted);
  if (!session) {
    await refreshSessionsCore({ loadMessages: false, loadCapabilities: false });
    session = findSessionByTerminalID(wanted);
  }
  if (!session) {
    throw new Error("No active session found for this terminal");
  }

  selectSession(session.id);
  setTerminalsScreenOpen(false);
}

function renderTerminalPicker() {
  if (!ui.terminalPickerList) {
    return;
  }

  const chunks = [];
  for (const terminal of state.terminals) {
    let metadata = {};
    if (typeof terminal.metadata === "string" && terminal.metadata.trim()) {
      try {
        metadata = JSON.parse(terminal.metadata);
      } catch {
        metadata = {};
      }
    }

    const terminalID = terminal.id || "";
    const session = findSessionByTerminalID(terminalID);
    const sessionMeta = session ? parseMetadata(session) : {};
    const host = String(sessionMeta.host || metadata.host || "unknown-host").trim() || "unknown-host";
    const path = pathFromMetadata(sessionMeta) || pathFromMetadata(metadata);
    const status = statusForTerminalID(terminalID);
    const buttonLabel = session ? "Open Session" : "No Session";
    const buttonClass = session ? "" : "subtle";
    const disabledAttr = session ? "" : " disabled";
    const pathLabel = path || (session ? "unknown path" : "no active session");
    chunks.push(
      `<div class="item">` +
        `<div>` +
          `<div class="row" style="margin-top:0">` +
            `<span class="status-dot" data-status="${escapeHTML(status.status)}" title="${escapeHTML(status.title)}"></span>` +
            `<div><strong>${escapeHTML(host)}</strong></div>` +
          `</div>` +
          `<div class="meta">${escapeHTML(pathLabel)}</div>` +
        `</div>` +
        `<button data-terminal-focus="${escapeHTML(terminalID)}" class="${buttonClass}"${disabledAttr}>${buttonLabel}</button>` +
      `</div>`
    );
  }

  ui.terminalPickerList.innerHTML = chunks.join("") || "<div class=\"item\">No terminals</div>";
  ui.terminalPickerList.querySelectorAll("button[data-terminal-focus]").forEach((button) => {
    if (button.disabled) {
      return;
    }
    button.addEventListener("click", (event) => {
      runAction(() => focusTerminalSession(button.dataset.terminalFocus), {
        label: "Open terminal session",
        button: event.currentTarget,
      });
    });
  });
}

function renderSessions() {
  const byHost = new Map();
  for (const session of state.sessions) {
    const metadata = parseMetadata(session);
    const host = metadata.host || "unknown-host";
    if (!byHost.has(host)) {
      byHost.set(host, []);
    }
    byHost.get(host).push(session);
  }

  const chunks = [];
  for (const [host, sessions] of byHost.entries()) {
    chunks.push(`<div class="item"><strong>${escapeHTML(host)}</strong><span>${sessions.length} session(s)</span></div>`);
    for (const session of sessions) {
      const uiState = session.ui || {};
      const mode = uiState.mode || "unknown";
      const working = uiState.working ? "working" : "idle";
      const online = uiState.online ? "online" : "offline";
      const label = session.id === state.selectedSessionID ? "Selected" : "Open";
      chunks.push(
        `<div class="item">` +
          `<div><div><strong>${escapeHTML(session.id || "")}</strong></div>` +
          `<div class="meta">${escapeHTML(mode)} · ${escapeHTML(working)} · ${escapeHTML(online)}</div></div>` +
          `<button data-session="${escapeHTML(session.id || "")}">${label}</button>` +
        `</div>`
      );
    }
  }

  ui.sessionsList.innerHTML = chunks.join("") || "<div class=\"item\">No sessions</div>";
  ui.sessionsList.querySelectorAll("button[data-session]").forEach((button) => {
    button.addEventListener("click", () => selectSession(button.dataset.session));
  });
  renderTerminalPicker();
  updateMainInteractivity();
}

function renderTerminals() {
  const chunks = [];
  for (const terminal of state.terminals) {
    let metadata = {};
    if (typeof terminal.metadata === "string" && terminal.metadata.trim()) {
      try {
        metadata = JSON.parse(terminal.metadata);
      } catch {
        metadata = {};
      }
    }

    const host = metadata.host || "unknown";
    const flavor = metadata.flavor || "";

    chunks.push(
      `<div class="item">` +
        `<div><strong>${escapeHTML(terminal.id || "")}</strong>` +
        `<div class="meta">${escapeHTML(host)} ${escapeHTML(flavor)}</div></div>` +
        `<div class="row">` +
          `<button data-terminal-stop="${escapeHTML(terminal.id || "")}" class="warn">Stop</button>` +
          `<button data-terminal-restart="${escapeHTML(terminal.id || "")}">Restart</button>` +
          `<button data-terminal-delete="${escapeHTML(terminal.id || "")}" class="warn">Delete</button>` +
        `</div>` +
      `</div>`
    );
  }

  ui.terminalsList.innerHTML = chunks.join("") || "<div class=\"item\">No terminals</div>";

  ui.terminalsList.querySelectorAll("button[data-terminal-stop]").forEach((button) => {
    button.addEventListener("click", () => terminalAction(button.dataset.terminalStop, "stop-daemon"));
  });
  ui.terminalsList.querySelectorAll("button[data-terminal-restart]").forEach((button) => {
    button.addEventListener("click", () => terminalAction(button.dataset.terminalRestart, "restart-daemon"));
  });
  ui.terminalsList.querySelectorAll("button[data-terminal-delete]").forEach((button) => {
    button.addEventListener("click", () => deleteTerminal(button.dataset.terminalDelete));
  });

  renderTerminalPicker();
  updateMainInteractivity();
}

function unwrapAgentMessageEnvelope(content) {
  let cursor = content;
  if (!cursor || typeof cursor !== "object") {
    return null;
  }
  if (cursor.type === "output" && cursor.data && typeof cursor.data === "object") {
    cursor = cursor.data;
  }
  if (cursor.message && typeof cursor.message === "object") {
    cursor = cursor.message;
  }
  return cursor;
}

function collectTextFragments(node, pieces, depth = 0) {
  if (depth > 7 || node == null) {
    return;
  }
  if (typeof node === "string") {
    const trimmed = node.trim();
    if (trimmed) {
      pieces.push(node);
    }
    return;
  }
  if (Array.isArray(node)) {
    for (const item of node) {
      collectTextFragments(item, pieces, depth + 1);
    }
    return;
  }
  if (typeof node !== "object") {
    return;
  }

  if (typeof node.text === "string" && node.text.trim()) {
    pieces.push(node.text);
  }
  if (typeof node.content === "string" && node.content.trim()) {
    pieces.push(node.content);
  }
  if (typeof node.input === "string" && node.input.trim()) {
    pieces.push(node.input);
  }
  if (typeof node.stdout === "string" && node.stdout.trim()) {
    pieces.push(node.stdout);
  }
  if (typeof node.stderr === "string" && node.stderr.trim()) {
    pieces.push(node.stderr);
  }

  const nestedKeys = ["message", "data", "content", "output", "blocks", "toolCall", "toolOutput"];
  for (const key of nestedKeys) {
    if (key in node) {
      collectTextFragments(node[key], pieces, depth + 1);
    }
  }
}

function extractRole(message) {
  const content = message?.content;
  const unwrapped = unwrapAgentMessageEnvelope(content);
  if (typeof unwrapped?.role === "string" && unwrapped.role.trim()) {
    return unwrapped.role;
  }
  if (typeof unwrapped?.type === "string" && unwrapped.type.trim()) {
    return unwrapped.type;
  }
  if (typeof message?.role === "string") {
    return message.role;
  }
  if (typeof message?.content?.role === "string") {
    return message.content.role;
  }
  return "event";
}

function extractLocalID(message) {
  return message?.localId || message?.localID || message?.local_id || message?.content?.localId || "";
}

function extractMessageText(message) {
  const content = message?.content;
  if (!content) {
    return JSON.stringify(message, null, 2);
  }

  const unwrapped = peelMessagePayload(unwrapAgentMessageEnvelope(content));
  if (unwrapped && typeof unwrapped === "object") {
    if (typeof unwrapped.text === "string" && unwrapped.text.trim()) {
      return prettifyJSONText(unwrapped.text);
    }
    if (typeof unwrapped.content === "string" && unwrapped.content.trim()) {
      return prettifyJSONText(unwrapped.content);
    }
    if (Array.isArray(unwrapped.content)) {
      const direct = unwrapped.content
        .map((part) => {
          if (typeof part === "string") {
            return prettifyJSONText(part);
          }
          if (typeof part?.text === "string") {
            return prettifyJSONText(part.text);
          }
          if (typeof part?.content === "string") {
            return prettifyJSONText(part.content);
          }
          return "";
        })
        .filter(Boolean);
      if (direct.length > 0) {
        return direct.join("\n\n");
      }
    }
  }

  if (typeof content === "string") {
    const parsed = parseJSONString(content);
    if (parsed != null) {
      const parsedText = extractMessageText({ content: parsed });
      if (parsedText && parsedText.trim()) {
        return parsedText;
      }
    }
    return prettifyJSONText(content);
  }

  if (typeof content.text === "string") {
    return prettifyJSONText(content.text);
  }

  if (typeof content.content?.text === "string") {
    return prettifyJSONText(content.content.text);
  }

  if (Array.isArray(content.blocks) && content.blocks.length > 0) {
    const pieces = [];
    for (const block of content.blocks) {
      if (typeof block?.text === "string") {
        pieces.push(block.text);
      } else if (typeof block?.content === "string") {
        pieces.push(block.content);
      } else if (typeof block === "string") {
        pieces.push(block);
      }
    }
    if (pieces.length > 0) {
      return pieces.join("\n\n");
    }
  }

  if (Array.isArray(content)) {
    const pieces = [];
    for (const part of content) {
      if (typeof part === "string") {
        pieces.push(part);
      } else if (typeof part?.text === "string") {
        pieces.push(part.text);
      } else if (typeof part?.content === "string") {
        pieces.push(part.content);
      }
    }
    if (pieces.length > 0) {
      return pieces.join("\n\n");
    }
  }

  const fragments = [];
  collectTextFragments(content, fragments);
  const unique = [];
  const seen = new Set();
  for (const fragment of fragments) {
    const key = fragment.trim();
    if (!key || seen.has(key)) {
      continue;
    }
    seen.add(key);
    unique.push(fragment);
  }
  if (unique.length > 0) {
    if (unique.length === 1) {
      return prettifyJSONText(unique[0]);
    }
    return unique.map((item) => prettifyJSONText(item)).join("\n\n");
  }

  return `\`\`\`json\n${JSON.stringify(content, null, 2)}\n\`\`\``;
}

// Message filtering ported from iOS SDKBridge.swift
// These functions mirror: isNullMessage, isFileHistorySnapshot, isToolResultMessage, isIgnorableMessage

const IGNORABLE_BLOCK_TYPES = new Set([
  "thinking",
  "reasoning",
  "tool-use",
  "tool_use",
  "tool-result",
  "tool_result",
]);

const TOOL_RESULT_TYPES = new Set([
  "tool-result",
  "tool_result",
]);

// isNullMessage checks if content is null or has a null message field.
function isNullMessage(content) {
  if (content == null) return true;
  if (typeof content === "object" && "message" in content && content.message === null) {
    return true;
  }
  return false;
}

// isEmptyContentMessage checks if a message has an empty content array.
// These are messages like { message: { content: [], role: "user" } } that have no actual content.
function isEmptyContentMessage(content, depth = 0) {
  if (depth > 6 || !content) return false;

  if (typeof content === "string") {
    const trimmed = content.trim();
    if (trimmed.startsWith("{")) {
      try {
        return isEmptyContentMessage(JSON.parse(trimmed), depth + 1);
      } catch {
        return false;
      }
    }
    return false;
  }

  if (typeof content !== "object") return false;

  // Check for message.content being an empty array
  const message = content.message;
  if (message && typeof message === "object") {
    if (Array.isArray(message.content) && message.content.length === 0) {
      return true;
    }
  }

  // Check content array directly
  if (Array.isArray(content.content) && content.content.length === 0) {
    return true;
  }

  // Recurse into nested fields
  if (content.data && isEmptyContentMessage(content.data, depth + 1)) return true;
  if (content.content && !Array.isArray(content.content) && isEmptyContentMessage(content.content, depth + 1)) return true;

  return false;
}

// isFileHistorySnapshot checks if content is a file-history-snapshot message.
function isFileHistorySnapshot(content, depth = 0) {
  if (depth > 6 || !content) return false;

  if (typeof content === "string") {
    const trimmed = content.trim();
    if (trimmed.startsWith("{")) {
      try {
        return isFileHistorySnapshot(JSON.parse(trimmed), depth + 1);
      } catch {
        return false;
      }
    }
    return false;
  }

  if (typeof content !== "object") return false;

  const type = content.type || content.t;
  if (type === "file-history-snapshot") return true;

  if (content.message?.type === "file-history-snapshot") return true;

  if (content.content && isFileHistorySnapshot(content.content, depth + 1)) return true;

  return false;
}

// isToolResultMessage checks if content contains only tool-result blocks.
function isToolResultMessage(content, depth = 0) {
  if (depth > 6 || !content) return false;

  if (typeof content === "string") {
    const trimmed = content.trim();
    if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
      try {
        return isToolResultMessage(JSON.parse(trimmed), depth + 1);
      } catch {
        return false;
      }
    }
    return false;
  }

  if (Array.isArray(content)) {
    for (const part of content) {
      if (part && typeof part === "object") {
        const type = normalizeBlockType(part.type);
        if (TOOL_RESULT_TYPES.has(type)) return true;
      }
    }
    return false;
  }

  if (typeof content !== "object") return false;

  const type = normalizeBlockType(content.type);
  if (TOOL_RESULT_TYPES.has(type)) return true;

  if (content.message && isToolResultMessage(content.message, depth + 1)) return true;
  if (content.content && isToolResultMessage(content.content, depth + 1)) return true;
  if (content.data && isToolResultMessage(content.data, depth + 1)) return true;

  return false;
}

// containsAnyBlockType checks if content contains any block with a type in the given set.
function containsAnyBlockType(content, types, depth = 0) {
  if (depth > 6 || !content) return false;

  if (typeof content === "string") {
    const trimmed = content.trim();
    if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
      try {
        return containsAnyBlockType(JSON.parse(trimmed), types, depth + 1);
      } catch {
        return false;
      }
    }
    return false;
  }

  if (Array.isArray(content)) {
    for (const part of content) {
      if (containsAnyBlockType(part, types, depth + 1)) return true;
    }
    return false;
  }

  if (typeof content !== "object") return false;

  const type = normalizeBlockType(content.type);
  if (types.has(type)) return true;

  if (content.content && containsAnyBlockType(content.content, types, depth + 1)) return true;
  if (content.message && containsAnyBlockType(content.message, types, depth + 1)) return true;
  if (content.data && containsAnyBlockType(content.data, types, depth + 1)) return true;

  return false;
}

// isIgnorableMessage returns true for message payloads that we intentionally do not
// render as transcript entries (e.g. tool-use blocks or thinking-only blocks).
// UI state (thinking/tool rendering) is driven by ui.event ephemerals from the CLI.
function isIgnorableMessage(content) {
  if (isToolResultMessage(content)) return true;
  return containsAnyBlockType(content, IGNORABLE_BLOCK_TYPES);
}

// normalizeMessageContent extracts and normalizes the content from a message envelope.
function normalizeMessageContent(message) {
  let content = message?.content ?? message?.data;
  if (typeof content === "string") {
    const trimmed = content.trim();
    if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
      try {
        content = JSON.parse(trimmed);
      } catch {
        // keep as string
      }
    }
  }
  return content;
}

function shouldDisplayMessage(message, uiEventPayload = null) {
  const content = normalizeMessageContent(message);

  // Filter out null, empty, file-history-snapshot, and tool-result-only messages
  if (isNullMessage(content)) return false;
  if (isEmptyContentMessage(content)) return false;
  if (isFileHistorySnapshot(content)) return false;
  if (isToolResultMessage(content)) return false;

  const pref = currentTranscriptPreferences();
  const payload = uiEventPayload || extractUIEventPayload(message);
  if (payload) {
    const kind = asTrimmedString(payload.kind).toLowerCase();
    if (kind === "tool") {
      return true;
    }
    if (kind === "reasoning") {
      return pref.showReasoningSummaries !== false;
    }
  }

  // Check if we can extract renderable blocks
  const contentBlocks = extractContentBlocks(message);
  if (contentBlocks) {
    return true;
  }

  // Check if we can extract any text
  const text = extractText(content);
  if (text && text.trim()) {
    return true;
  }

  // If no renderable content and message contains only ignorable blocks, skip it
  if (isIgnorableMessage(content)) {
    return false;
  }

  // Fallback: try extractMessageText and check if it looks like real content
  const fallbackText = extractMessageText(message);

  // Skip messages that look like raw JSON dumps
  const trimmedFallback = fallbackText.trim();
  if (trimmedFallback.startsWith("{") && trimmedFallback.endsWith("}")) {
    return false;
  }
  if (trimmedFallback.startsWith("[") && trimmedFallback.endsWith("]")) {
    return false;
  }

  const role = extractRole(message);
  const textLower = fallbackText.toLowerCase();

  if (!pref.showToolUse && (role === "tool" || textLower.includes("tool use") || textLower.includes("tool_call"))) {
    return false;
  }
  if (!pref.showToolOutput && (role === "tool_result" || textLower.includes("tool output") || textLower.includes("stdout"))) {
    return false;
  }
  if (!pref.showReasoningSummaries && (role === "reasoning" || textLower.includes("thinking") || textLower.includes("reasoning"))) {
    return false;
  }

  return true;
}

// extractText tries to extract plain text from content (mirrors iOS extractText).
function extractText(content, depth = 0) {
  if (depth > 6 || !content) return null;

  if (typeof content === "string") {
    const trimmed = content.trim();
    if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
      try {
        return extractText(JSON.parse(trimmed), depth + 1);
      } catch {
        return trimmed || null;
      }
    }
    return trimmed || null;
  }

  if (typeof content !== "object") return null;

  // Check for text field directly
  if (typeof content.text === "string" && content.text.trim()) {
    return content.text;
  }

  // Recurse into nested fields
  if (content.content) {
    const nested = extractText(content.content, depth + 1);
    if (nested) return nested;
  }
  if (content.message) {
    const nested = extractText(content.message, depth + 1);
    if (nested) return nested;
  }
  if (content.data) {
    const nested = extractText(content.data, depth + 1);
    if (nested) return nested;
  }

  return null;
}

function mergeMessages(sessionID, serverMessages) {
  const optimistic = state.optimisticBySession[sessionID] || [];
  const serverLocalIDs = new Set(serverMessages.map((item) => extractLocalID(item)).filter(Boolean));
  const remainingOptimistic = optimistic.filter((item) => !serverLocalIDs.has(item.localId));
  state.optimisticBySession[sessionID] = remainingOptimistic;

  const merged = [...serverMessages, ...remainingOptimistic];
  merged.sort((a, b) => {
    const aSeq = Number(a.seq || a.createdAt || 0);
    const bSeq = Number(b.seq || b.createdAt || 0);
    return aSeq - bSeq;
  });
  return merged;
}

function messageSignature(message) {
  const seq = Number(message?.seq || message?.createdAt || 0);
  const id = String(message?.id || message?.uuid || "");
  const localID = String(extractLocalID(message) || "");
  const role = String(extractRole(message) || "");
  let content = "";
  if (typeof message?.content === "string") {
    content = message.content;
  } else if (message?.content && typeof message.content === "object") {
    try {
      content = JSON.stringify(message.content);
    } catch {
      content = String(message.content);
    }
  }
  return `${seq}|${id}|${localID}|${role}|${content}`;
}

function messagesChanged(previousMessages, nextMessages) {
  if (previousMessages.length !== nextMessages.length) {
    return true;
  }
  for (let index = 0; index < previousMessages.length; index += 1) {
    if (messageSignature(previousMessages[index]) !== messageSignature(nextMessages[index])) {
      return true;
    }
  }
  return false;
}

function getVisibleMessageLimit(sessionID) {
  if (!sessionID) {
    return DEFAULT_VISIBLE_MESSAGE_WINDOW;
  }
  const stored = Number(state.visibleMessageLimitBySession[sessionID] || 0);
  if (stored > 0) {
    return Math.min(MAX_VISIBLE_MESSAGE_WINDOW, Math.max(DEFAULT_VISIBLE_MESSAGE_WINDOW, stored));
  }
  state.visibleMessageLimitBySession[sessionID] = DEFAULT_VISIBLE_MESSAGE_WINDOW;
  return DEFAULT_VISIBLE_MESSAGE_WINDOW;
}

function increaseVisibleMessageLimit(sessionID, increaseBy) {
  if (!sessionID) {
    return;
  }
  const next = Math.min(
    MAX_VISIBLE_MESSAGE_WINDOW,
    getVisibleMessageLimit(sessionID) + Math.max(1, Number(increaseBy || MESSAGE_WINDOW_EXTEND_STEP))
  );
  state.visibleMessageLimitBySession[sessionID] = next;
}

function messageDOMKey(message) {
  const seq = String(Number(message?.seq || message?.createdAt || 0));
  const id = String(message?.id || "");
  const uuid = String(message?.uuid || "");
  const localID = String(extractLocalID(message) || "");
  if (seq !== "0" || id || uuid || localID) {
    return `seq:${seq}|id:${id}|uuid:${uuid}|local:${localID}`;
  }
  return `sig:${messageSignature(message)}`;
}

function formatMarkdownForDisplay(raw) {
  const normalized = String(raw || "").replaceAll("\r\n", "\n").replaceAll("\r", "\n");
  const lines = normalized.split("\n");
  if (lines.length <= 1) {
    return normalized;
  }

  let inFence = false;
  let out = "";
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i];
    out += line;
    if (i === lines.length - 1) {
      break;
    }

    const trimmed = line.trim();
    const nextLine = lines[i + 1];
    const isFence = trimmed.startsWith("```");

    if (isFence) {
      out += "\n";
      inFence = !inFence;
      continue;
    }

    if (!line || !nextLine || inFence) {
      out += "\n";
      continue;
    }

    out += "  \n";
  }
  return out;
}

function renderMarkdown(text) {
  const formatted = formatMarkdownForDisplay(text);
  if (markdownRendererName === "markdown-it" && markdownRenderer) {
    return markdownRenderer.render(formatted);
  }
  return fallbackMarkdownToHTML(formatted);
}

function renderInlineMarkdown(raw) {
  let escaped = escapeHTML(raw);
  escaped = escaped.replace(/`([^`]+)`/g, "<code>$1</code>");
  escaped = escaped.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  escaped = escaped.replace(/__([^_]+)__/g, "<strong>$1</strong>");
  escaped = escaped.replace(/\*([^*\n]+)\*/g, "<em>$1</em>");
  escaped = escaped.replace(/_([^_\n]+)_/g, "<em>$1</em>");
  escaped = escaped.replace(
    /\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g,
    (_match, label, href) => `<a href="${href}" target="_blank" rel="noopener noreferrer">${label}</a>`
  );
  return escaped;
}

function fallbackMarkdownToHTML(rawMarkdown) {
  const lines = String(rawMarkdown || "").replaceAll("\r\n", "\n").split("\n");
  const html = [];
  let paragraph = [];
  let listType = "";
  let inFence = false;
  let fenceLang = "";
  let fenceLines = [];

  const flushParagraph = () => {
    if (paragraph.length === 0) {
      return;
    }
    html.push(`<p>${renderInlineMarkdown(paragraph.join("<br>"))}</p>`);
    paragraph = [];
  };

  const closeList = () => {
    if (!listType) {
      return;
    }
    html.push(listType === "ol" ? "</ol>" : "</ul>");
    listType = "";
  };

  const flushFence = () => {
    const klass = fenceLang ? ` class="language-${escapeHTML(fenceLang)}"` : "";
    html.push(`<pre><code${klass}>${escapeHTML(fenceLines.join("\n"))}</code></pre>`);
    inFence = false;
    fenceLang = "";
    fenceLines = [];
  };

  for (const line of lines) {
    const trimmed = line.trim();

    if (trimmed.startsWith("```")) {
      flushParagraph();
      closeList();
      if (inFence) {
        flushFence();
      } else {
        inFence = true;
        fenceLang = trimmed.slice(3).trim();
      }
      continue;
    }

    if (inFence) {
      fenceLines.push(line);
      continue;
    }

    if (trimmed === "") {
      flushParagraph();
      closeList();
      continue;
    }

    if (/^#{1,6}\s+/.test(trimmed)) {
      flushParagraph();
      closeList();
      const level = Math.min(6, Math.max(1, trimmed.match(/^#+/)[0].length));
      const content = trimmed.replace(/^#{1,6}\s+/, "");
      html.push(`<h${level}>${renderInlineMarkdown(content)}</h${level}>`);
      continue;
    }

    if (/^>\s?/.test(trimmed)) {
      flushParagraph();
      closeList();
      html.push(`<blockquote><p>${renderInlineMarkdown(trimmed.replace(/^>\s?/, ""))}</p></blockquote>`);
      continue;
    }

    if (/^[-*]\s+/.test(trimmed)) {
      flushParagraph();
      if (listType !== "ul") {
        closeList();
        listType = "ul";
        html.push("<ul>");
      }
      html.push(`<li>${renderInlineMarkdown(trimmed.replace(/^[-*]\s+/, ""))}</li>`);
      continue;
    }

    if (/^\d+\.\s+/.test(trimmed)) {
      flushParagraph();
      if (listType !== "ol") {
        closeList();
        listType = "ol";
        html.push("<ol>");
      }
      html.push(`<li>${renderInlineMarkdown(trimmed.replace(/^\d+\.\s+/, ""))}</li>`);
      continue;
    }

    if (/^---+$/.test(trimmed) || /^___+$/.test(trimmed)) {
      flushParagraph();
      closeList();
      html.push("<hr>");
      continue;
    }

    paragraph.push(line);
  }

  if (inFence) {
    flushFence();
  }
  flushParagraph();
  closeList();
  return html.join("");
}

function applyDiffSyntaxDecorations(rootElement) {
  if (!rootElement) {
    return;
  }

  rootElement.querySelectorAll("pre > code").forEach((codeNode) => {
    const classes = String(codeNode.className || "");
    if (!classes.includes("language-diff") && !classes.includes("lang-diff")) {
      return;
    }

    const raw = (codeNode.textContent || "").replaceAll("\r\n", "\n");
    const lines = raw.split("\n");
    const html = lines
      .map((line) => {
        let cls = "";
        if (line.startsWith("@@")) {
          cls = "diff-line-hunk";
        } else if (line.startsWith("+") && !line.startsWith("+++")) {
          cls = "diff-line-add";
        } else if (line.startsWith("-") && !line.startsWith("---")) {
          cls = "diff-line-del";
        }
        if (!cls) {
          return escapeHTML(line);
        }
        return `<span class="${cls}">${escapeHTML(line)}</span>`;
      })
      .join("\n");

    codeNode.innerHTML = html;
  });
}

function buildMessageRenderEntry(message, pref) {
  const uiEventPayload = extractUIEventPayload(message);
  if (!shouldDisplayMessage(message, uiEventPayload)) {
    return null;
  }

  const role = extractRole(message);
  const contentBlocks = !uiEventPayload ? extractContentBlocks(message) : null;
  const roleLabel = uiEventPayload ? `ui.${uiEventPayload.kind || "event"}` : role;
  const roleClass = uiEventPayload
    ? "ui-event"
    : String(role).toLowerCase().replace(/[^a-z0-9_-]+/g, "-");

  let markdownHTML;
  if (uiEventPayload) {
    markdownHTML = renderUIEventHTML(uiEventPayload, pref);
  } else if (contentBlocks) {
    markdownHTML = renderContentBlocksHTML(contentBlocks, pref);
  } else {
    markdownHTML = renderMarkdown(extractMessageText(message));
  }
  if (!markdownHTML.trim()) {
    return null;
  }

  const hideRoleRow = Boolean(uiEventPayload) || Boolean(contentBlocks);
  const roleRow = hideRoleRow
    ? ""
    : `<div class="role">${escapeHTML(roleLabel)}</div>`;

  return {
    key: messageDOMKey(message),
    signature: messageSignature(message),
    className: `msg msg-role-${roleClass}${uiEventPayload ? " msg-ui-event" : ""}`,
    innerHTML: `${roleRow}<div class="md-content">${markdownHTML}</div>`,
  };
}

function getVisibleMessageEntries(sessionID, pref) {
  const allMessages = state.messagesBySession[sessionID] || [];
  const renderable = [];
  for (const message of allMessages) {
    const entry = buildMessageRenderEntry(message, pref);
    if (entry) {
      renderable.push(entry);
    }
  }
  const limit = getVisibleMessageLimit(sessionID);
  if (renderable.length <= limit) {
    return renderable;
  }
  return renderable.slice(renderable.length - limit);
}

function createMessageNode(entry) {
  const node = document.createElement("div");
  node.className = entry.className;
  node.dataset.messageKey = entry.key;
  node.dataset.messageSignature = entry.signature;
  node.innerHTML = entry.innerHTML;
  node.querySelectorAll(".md-content").forEach((child) => applyDiffSyntaxDecorations(child));
  return node;
}

function updateMessageNode(node, entry) {
  node.className = entry.className;
  node.dataset.messageSignature = entry.signature;
  node.innerHTML = entry.innerHTML;
  node.querySelectorAll(".md-content").forEach((child) => applyDiffSyntaxDecorations(child));
}

function patchVisibleMessages(entries, options = {}) {
  const { preserveTopAnchor = false } = options;
  const container = ui.messages;
  if (!container) {
    return false;
  }

  const previousScrollTop = container.scrollTop;
  const previousScrollHeight = container.scrollHeight;

  const currentNodes = Array.from(container.children).filter((node) => node.dataset?.messageKey);
  const currentKeys = currentNodes.map((node) => String(node.dataset.messageKey || ""));
  const nextKeys = entries.map((entry) => entry.key);
  const sameOrder = currentKeys.length === nextKeys.length && currentKeys.every((key, index) => key === nextKeys[index]);
  const allSignaturesMatch = sameOrder && currentNodes.every((node, index) => {
    return String(node.dataset.messageSignature || "") === entries[index].signature;
  });
  const hasPlaceholder = Boolean(container.querySelector("[data-empty-placeholder=\"true\"]"));

  if (entries.length === 0) {
    if (hasPlaceholder && container.children.length === 1) {
      return false;
    }
    container.innerHTML = '<div class="msg" data-empty-placeholder="true">No messages</div>';
    return true;
  }

  if (sameOrder && allSignaturesMatch && !hasPlaceholder) {
    return false;
  }

  if (hasPlaceholder) {
    container.querySelectorAll("[data-empty-placeholder=\"true\"]").forEach((node) => node.remove());
  }

  const existingByKey = new Map();
  currentNodes.forEach((node) => {
    existingByKey.set(String(node.dataset.messageKey || ""), node);
  });

  for (let index = 0; index < entries.length; index += 1) {
    const entry = entries[index];
    let node = existingByKey.get(entry.key);
    if (!node) {
      node = createMessageNode(entry);
      existingByKey.set(entry.key, node);
    } else if (String(node.dataset.messageSignature || "") !== entry.signature) {
      updateMessageNode(node, entry);
    }

    const expectedNode = container.children[index] || null;
    if (expectedNode !== node) {
      container.insertBefore(node, expectedNode);
    }
  }

  const keepKeys = new Set(nextKeys);
  Array.from(container.children).forEach((node) => {
    const key = String(node.dataset?.messageKey || "");
    if (!key || keepKeys.has(key)) {
      return;
    }
    node.remove();
  });

  if (preserveTopAnchor) {
    const delta = container.scrollHeight - previousScrollHeight;
    container.scrollTop = previousScrollTop + delta;
  }

  return true;
}

function renderMessages(forceScrollBottom = false, options = {}) {
  const { allowAutoStick = true, preserveTopAnchor = false } = options;
  const sessionID = state.selectedSessionID;
  const pref = currentTranscriptPreferences();
  const fontSize = Number(pref.fontSize || 14);
  ui.messages.style.fontSize = `${fontSize}px`;

  const stickToBottom = forceScrollBottom || (allowAutoStick && isNearBottom(ui.messages));
  const entries = getVisibleMessageEntries(sessionID, pref);
  patchVisibleMessages(entries, { preserveTopAnchor });

  if (stickToBottom) {
    ui.messages.scrollTop = ui.messages.scrollHeight;
  }
  updateScrollBottomVisibility();
  updateMainInteractivity();
}

function updateSessionActionState(session) {
  if (!state.isConnected) {
    updateComposerActionState(null);
    if (ui.openModelBtn) {
      ui.openModelBtn.disabled = true;
      ui.openModelBtn.title = "Connect first";
    }
    if (ui.takeControlBtn) {
      ui.takeControlBtn.hidden = true;
      ui.takeControlBtn.disabled = true;
      ui.takeControlBtn.title = "Connect first";
    }
    return;
  }
  updateComposerActionState(session);
  if (ui.openModelBtn) {
    ui.openModelBtn.disabled = !session;
    ui.openModelBtn.title = session ? "Model settings" : "Select a session first";
  }
  if (!ui.takeControlBtn) {
    return;
  }
  if (!session) {
    ui.takeControlBtn.hidden = true;
    ui.takeControlBtn.disabled = true;
    ui.takeControlBtn.title = "Select a session first";
    return;
  }
  const uiState = session.ui || {};
  const browserControls = String(uiState.mode || "").toLowerCase() === "remote";
  const online = uiState.online !== false;
  ui.takeControlBtn.hidden = browserControls;
  ui.takeControlBtn.disabled = browserControls || !online;
  if (!online) {
    ui.takeControlBtn.title = "Session is offline";
  } else if (browserControls) {
    ui.takeControlBtn.title = "Browser already controls this session";
  } else {
    ui.takeControlBtn.title = "Take control from desktop";
  }
}

function renderSessionMeta() {
  const session = selectedSession();
  if (!session) {
    if (ui.sessionIdentity) {
      ui.sessionIdentity.textContent = "No session selected";
    }
    updateSessionActionState(null);
    return;
  }

  const metadata = parseMetadata(session);
  const host = metadata.host || "unknown-host";
  const path = metadata.path || metadata.cwd || "";
  if (ui.sessionIdentity) {
    ui.sessionIdentity.textContent = path ? `${host} - ${path}` : host;
  }
  updateSessionActionState(session);
}

function hydratePermissionsFromSessions() {
  for (const session of state.sessions) {
    const agentState = parseAgentState(session);
    const requests = agentState.requests || {};
    for (const [requestID, request] of Object.entries(requests)) {
      enqueuePermission({
        sessionID: session.id,
        requestID,
        toolName: request.toolName || request.tool_name || "unknown",
        input: request.input || "{}",
      });
    }
  }
}

function enqueuePermission(request) {
  if (!request || !request.requestID || !request.sessionID) {
    return;
  }
  if (state.permissionQueue.some((item) => item.requestID === request.requestID)) {
    return;
  }
  state.permissionQueue.push(request);
  if (!state.activePermission) {
    state.activePermission = state.permissionQueue[0];
    showPermissionDialog();
  }
}

function showPermissionDialog() {
  const request = state.activePermission;
  if (!request) {
    if (ui.permissionDialog.open) {
      ui.permissionDialog.close();
    }
    return;
  }
  ui.permissionBody.textContent = JSON.stringify(request, null, 2);
  if (!ui.permissionDialog.open) {
    ui.permissionDialog.showModal();
  }
}

function completePermissionRequest(requestID) {
  state.permissionQueue = state.permissionQueue.filter((item) => item.requestID !== requestID);
  state.activePermission = state.permissionQueue[0] || null;

  if (!state.activePermission && ui.permissionDialog.open) {
    ui.permissionDialog.close();
  }
  if (state.activePermission) {
    showPermissionDialog();
  }
}

function parsePermissionFromUpdate(update) {
  if (!update || typeof update !== "object") {
    return null;
  }

  const parseShape = (shape) => {
    if (!shape || typeof shape !== "object") {
      return null;
    }
    const kind = shape.type || shape.t;
    if (kind !== "permission-request") {
      return null;
    }
    const sessionID = shape.id || shape.sid || shape.sessionID;
    const requestID = shape.requestId || shape.requestID;
    const toolName = shape.toolName || shape.tool_name;
    const input = shape.input;
    if (!sessionID || !requestID || !toolName || !input) {
      return null;
    }
    return { sessionID, requestID, toolName, input };
  };

  return parseShape(update) || parseShape(update.body);
}

async function loadSessionMessages(sessionID, beforeSeq = 0, append = false) {
  if (!sessionID) {
    return;
  }

  const params = new URLSearchParams({ limit: String(MESSAGE_PAGE_LIMIT) });
  if (beforeSeq > 0) {
    params.set("beforeSeq", String(beforeSeq));
  }

  const payload = await api(`/api/sessions/${encodeURIComponent(sessionID)}/messages?${params.toString()}`);
  const serverMessages = Array.isArray(payload.messages) ? payload.messages : [];

  if (serverMessages.length > 0) {
    const oldest = Number(serverMessages[0].seq || 0);
    if (oldest > 0) {
      state.oldestSeqBySession[sessionID] = append
        ? Math.min(Number(state.oldestSeqBySession[sessionID] || oldest), oldest)
        : oldest;
    }
  }

  state.hasMoreBySession[sessionID] = serverMessages.length >= MESSAGE_PAGE_LIMIT;
  if (append && serverMessages.length > 0) {
    increaseVisibleMessageLimit(sessionID, Math.max(MESSAGE_WINDOW_EXTEND_STEP, serverMessages.length));
  }

  const previousMessages = state.messagesBySession[sessionID] || [];
  let nextMessages;
  if (append) {
    const existing = state.messagesBySession[sessionID] || [];
    const map = new Map();
    [...serverMessages, ...existing].forEach((item) => {
      const key = `${item.seq || ""}:${extractLocalID(item)}:${item.id || ""}:${item.uuid || ""}`;
      map.set(key, item);
    });
    nextMessages = mergeMessages(sessionID, Array.from(map.values()));
  } else {
    nextMessages = mergeMessages(sessionID, serverMessages);
  }
  state.messagesBySession[sessionID] = nextMessages;

  const hasActivity = messagesChanged(previousMessages, nextMessages);
  renderMessages(false, {
    allowAutoStick: !append && hasActivity,
    preserveTopAnchor: append,
  });
}

async function refreshSessionsCore(options = {}) {
  const {
    loadMessages = true,
    loadCapabilities = true,
  } = options;

  const payload = await api("/api/sessions");
  state.sessions = Array.isArray(payload.sessions) ? payload.sessions : [];

  if (!state.selectedSessionID && state.sessions.length > 0) {
    state.selectedSessionID = state.sessions[0].id;
  }

  if (state.selectedSessionID && !state.sessions.some((item) => item.id === state.selectedSessionID)) {
    state.selectedSessionID = state.sessions[0]?.id || "";
  }

  renderSessions();
  renderSessionMeta();
  hydratePermissionsFromSessions();

  if (state.selectedSessionID && loadMessages) {
    await loadSessionMessages(state.selectedSessionID);
  }

  if (state.selectedSessionID && loadCapabilities) {
    await refreshCapabilities();
  }
}

async function refreshTerminalsCore() {
  const payload = await api("/api/terminals");
  state.terminals = Array.isArray(payload) ? payload : [];
  renderTerminals();
}

function scheduleRefresh(plan, delayMs = STREAM_DEBOUNCE_MS) {
  state.refreshPlan.sessions = state.refreshPlan.sessions || Boolean(plan.sessions);
  state.refreshPlan.terminals = state.refreshPlan.terminals || Boolean(plan.terminals);
  state.refreshPlan.messages = state.refreshPlan.messages || Boolean(plan.messages);
  state.refreshPlan.capabilities = state.refreshPlan.capabilities || Boolean(plan.capabilities);

  if (state.refreshDebounceTimer) {
    return;
  }

  state.refreshDebounceTimer = window.setTimeout(() => {
    state.refreshDebounceTimer = null;
    runScheduledRefresh().catch((error) => {
      logLine(`refresh failed: ${error.message}`);
    });
  }, delayMs);
}

async function runScheduledRefresh() {
  if (state.refreshInFlight) {
    scheduleRefresh({});
    return;
  }

  if (!state.isConnected) {
    state.refreshPlan = {
      sessions: false,
      terminals: false,
      messages: false,
      capabilities: false,
    };
    return;
  }

  const plan = { ...state.refreshPlan };
  state.refreshPlan = {
    sessions: false,
    terminals: false,
    messages: false,
    capabilities: false,
  };

  if (!plan.sessions && !plan.terminals && !plan.messages && !plan.capabilities) {
    return;
  }

  state.refreshInFlight = true;
  updateRefreshBadge(true);

  try {
    if (plan.sessions) {
      await refreshSessionsCore({
        loadMessages: plan.messages,
        loadCapabilities: plan.capabilities,
      });
    } else {
      if (plan.messages && state.selectedSessionID) {
        await loadSessionMessages(state.selectedSessionID);
      }
      if (plan.capabilities && state.selectedSessionID) {
        await refreshCapabilities();
      }
    }

    if (plan.terminals) {
      await refreshTerminalsCore();
    }
  } finally {
    state.refreshInFlight = false;
    updateRefreshBadge(false);

    const pending = state.refreshPlan.sessions || state.refreshPlan.terminals || state.refreshPlan.messages || state.refreshPlan.capabilities;
    if (pending) {
      scheduleRefresh({}, STREAM_DEBOUNCE_FAST_MS);
    }
  }
}

function startAutoRefreshLoop() {
  if (state.autoRefreshTimer) {
    clearInterval(state.autoRefreshTimer);
  }

  state.autoRefreshTick = 0;
  state.autoRefreshTimer = window.setInterval(() => {
    if (document.hidden) {
      return;
    }

    refreshDebugLogs().catch((error) => {
      logLine(`debug refresh failed: ${error.message}`);
    });

    if (!state.isConnected) {
      return;
    }

    state.autoRefreshTick += 1;
    scheduleRefresh({ sessions: true, messages: true, capabilities: false }, STREAM_DEBOUNCE_MS);

    if (state.autoRefreshTick % AUTO_REFRESH_SLOW_EVERY === 0) {
      scheduleRefresh({ terminals: true }, STREAM_DEBOUNCE_MS);
    }
  }, AUTO_REFRESH_MS);
}

function stopAutoRefreshLoop() {
  if (state.autoRefreshTimer) {
    clearInterval(state.autoRefreshTimer);
    state.autoRefreshTimer = null;
  }
}

function selectSession(sessionID) {
  if (!sessionID) {
    return;
  }
  state.selectedSessionID = sessionID;
  renderSessions();
  renderSessionMeta();
  scheduleRefresh({ messages: true, capabilities: true }, STREAM_DEBOUNCE_FAST_MS);
}

async function sendMessage() {
  const sessionID = state.selectedSessionID;
  const text = ui.messageInput.value.trim();
  if (!sessionID || !text) {
    return;
  }

  const localID = window.crypto?.randomUUID ? window.crypto.randomUUID() : `local-${Date.now()}`;
  state.optimisticBySession[sessionID] = state.optimisticBySession[sessionID] || [];
  state.optimisticBySession[sessionID].push({
    id: `local-${localID}`,
    localId: localID,
    role: "user",
    content: { type: "text", text },
    createdAt: Date.now(),
  });

  const sessionMessages = state.messagesBySession[sessionID] || [];
  state.messagesBySession[sessionID] = mergeMessages(sessionID, sessionMessages);
  renderMessages(true);

  recordPromptHistory(sessionID, text);
  ui.messageInput.value = "";

  await api(`/api/sessions/${encodeURIComponent(sessionID)}/send`, {
    method: "POST",
    body: { text, localID },
  });

  scheduleRefresh({ sessions: true, messages: true, capabilities: false }, STREAM_DEBOUNCE_FAST_MS);
}

function recordPromptHistory(sessionID, text) {
  const trimmed = text.trim();
  if (!trimmed) {
    return;
  }

  state.promptHistoryBySession[sessionID] = state.promptHistoryBySession[sessionID] || [];
  const entries = state.promptHistoryBySession[sessionID];
  if (entries[entries.length - 1] !== trimmed) {
    entries.push(trimmed);
  }

  state.promptCursorBySession[sessionID] = null;
  state.promptDraftBySession[sessionID] = "";
}

function stepPromptHistory(direction) {
  const sessionID = state.selectedSessionID;
  if (!sessionID) {
    return;
  }

  const entries = state.promptHistoryBySession[sessionID] || [];
  if (entries.length === 0) {
    return;
  }

  let cursor = state.promptCursorBySession[sessionID];
  if (cursor == null) {
    state.promptDraftBySession[sessionID] = ui.messageInput.value;
  }

  if (direction < 0) {
    if (cursor == null) {
      cursor = entries.length - 1;
    } else {
      cursor = Math.max(0, cursor - 1);
    }
  } else {
    if (cursor == null) {
      return;
    }
    cursor += 1;
    if (cursor >= entries.length) {
      cursor = null;
    }
  }

  state.promptCursorBySession[sessionID] = cursor;
  ui.messageInput.value = cursor == null ? state.promptDraftBySession[sessionID] || "" : entries[cursor] || "";
}

async function refreshCapabilities() {
  const sessionID = state.selectedSessionID;
  if (!sessionID) {
    return;
  }

  const response = await api(`/api/sessions/${encodeURIComponent(sessionID)}/agent-capabilities`, {
    method: "POST",
    body: { model: ui.modelSelect.value || "" },
  });

  const result = response.result || response;
  state.capabilitiesBySession[sessionID] = result;
  renderCapabilities(result);
}

function renderCapabilities(result) {
  const capabilities = result?.capabilities || {};
  const desired = result?.desiredConfig || {};
  renderSelect(ui.modelSelect, capabilities.models || [], desired.model || "");
  renderSelect(ui.reasoningSelect, capabilities.reasoningEfforts || [], desired.reasoningEffort || "");
  renderSelect(ui.permissionModeSelect, capabilities.permissionModes || [], desired.permissionMode || "");
}

function renderSelect(select, values, selected) {
  const unique = Array.from(new Set(values || []));
  const options = ['<option value="">(none)</option>'];
  unique.forEach((value) => {
    options.push(`<option value="${escapeHTML(value)}">${escapeHTML(value)}</option>`);
  });
  select.innerHTML = options.join("");
  select.value = selected && unique.includes(selected) ? selected : "";
}

async function applyAgentConfig() {
  const sessionID = state.selectedSessionID;
  if (!sessionID) {
    return;
  }

  await api(`/api/sessions/${encodeURIComponent(sessionID)}/agent-config`, {
    method: "POST",
    body: {
      model: ui.modelSelect.value || null,
      permissionMode: ui.permissionModeSelect.value || null,
      reasoningEffort: ui.reasoningSelect.value || null,
    },
  });

  scheduleRefresh({ sessions: true, messages: true, capabilities: true }, STREAM_DEBOUNCE_FAST_MS);
}

async function requestSwitch(mode) {
  const sessionID = state.selectedSessionID;
  if (!sessionID) {
    return;
  }

  await api(`/api/sessions/${encodeURIComponent(sessionID)}/switch`, {
    method: "POST",
    body: { mode },
  });

  scheduleRefresh({ sessions: true, messages: true, capabilities: false }, STREAM_DEBOUNCE_FAST_MS);
}

async function abortTurn() {
  const sessionID = state.selectedSessionID;
  if (!sessionID) {
    return;
  }

  await api(`/api/sessions/${encodeURIComponent(sessionID)}/abort`, { method: "POST", body: {} });
  scheduleRefresh({ sessions: true, messages: true }, STREAM_DEBOUNCE_FAST_MS);
}

async function terminalAction(terminalID, action) {
  if (!terminalID) {
    return;
  }

  await api(`/api/terminals/${encodeURIComponent(terminalID)}/${action}`, { method: "POST", body: {} });
  scheduleRefresh({ sessions: true, terminals: true }, STREAM_DEBOUNCE_FAST_MS);
}

async function deleteTerminal(terminalID) {
  if (!terminalID) {
    return;
  }

  await api(`/api/terminals/${encodeURIComponent(terminalID)}`, { method: "DELETE" });
  scheduleRefresh({ terminals: true, sessions: true }, STREAM_DEBOUNCE_FAST_MS);
}

function loadPreferencesIntoControls() {
  const pref = state.preferences;
  ui.appearanceSelect.value = pref.appearanceMode || "system";

  const transcript = currentTranscriptPreferences();
  ui.showToolUse.checked = transcript.showToolUse !== false;
  ui.showToolOutput.checked = transcript.showToolOutput !== false;
  ui.showReasoning.checked = transcript.showReasoningSummaries !== false;
  ui.fontSizeInput.value = String(transcript.fontSize || 14);

  applyTheme(pref.appearanceMode || "system");
  renderMessages();
}

async function savePreferences() {
  const next = cloneJSON(state.preferences);

  const transcript = {
    showToolUse: ui.showToolUse.checked,
    showToolOutput: ui.showToolOutput.checked,
    showReasoningSummaries: ui.showReasoning.checked,
    fontSize: Number(ui.fontSizeInput.value || 14),
  };

  next.appearanceMode = ui.appearanceSelect.value || "system";
  next.globalTranscript = transcript;
  next.perTerminalTranscript = {};

  const saved = await api("/api/preferences", { method: "POST", body: next });
  state.preferences = saved;
  loadPreferencesIntoControls();
}

async function refreshDebugLogs() {
  const payload = await api("/api/debug/logs");
  const lines = Array.isArray(payload.logs) ? payload.logs : [];
  const latest = lines.slice(Math.max(0, lines.length - 1000));
  ui.debugLogs.textContent = latest
    .map((line) => `${new Date(line.ts).toISOString()} [${line.level}] ${line.message}`)
    .join("\n");
}

async function startLogServer() {
  const payload = await api("/api/debug/log-server/start", { method: "POST", body: {} });
  state.lastLogServerURL = payload.url || "";
  logLine(`log server: ${state.lastLogServerURL || "unknown"}`);
}

async function stopLogServer() {
  await api("/api/debug/log-server/stop", { method: "POST", body: {} });
  logLine("log server stopped");
}

async function decidePermission(allow) {
  const request = state.activePermission;
  if (!request) {
    return;
  }

  await api(`/api/sessions/${encodeURIComponent(request.sessionID)}/permission`, {
    method: "POST",
    body: {
      requestId: request.requestID,
      allow,
      message: ui.permissionMessage.value || "",
    },
  });

  ui.permissionMessage.value = "";
  completePermissionRequest(request.requestID);
  scheduleRefresh({ sessions: true, messages: true }, STREAM_DEBOUNCE_FAST_MS);
}

function connectStream() {
  if (state.eventSource) {
    state.eventSource.close();
  }

  const token = String(state.apiToken || "").trim();
  const url = new URL("/api/stream", window.location.origin);

  if (state.streamSince > 0) {
    url.searchParams.set("since", String(state.streamSince));
  }
  if (token) {
    url.searchParams.set("access_token", token);
  }

  state.eventSource = new EventSource(url.toString());
  ui.streamBadge.textContent = "stream: connecting";

  state.eventSource.onopen = () => {
    ui.streamBadge.textContent = "stream: connected";
  };

  state.eventSource.onerror = () => {
    ui.streamBadge.textContent = "stream: reconnecting";
  };

  const onEvent = (event) => {
    try {
      const envelope = JSON.parse(event.data);
      state.streamSince = Number(envelope.eventID || state.streamSince || 0);
      localStorage.setItem("delight.streamSince", String(state.streamSince));

      if (envelope.kind === "connected") {
        setConnectionState(true);
        scheduleRefresh({ sessions: true, terminals: true, messages: true }, STREAM_DEBOUNCE_FAST_MS);
      }

      if (envelope.kind === "disconnected") {
        setConnectionState(false);
      }

      if (envelope.kind === "error") {
        logLine(`error: ${JSON.stringify(envelope.payload)}`);
      }

      if (envelope.kind === "resync-required") {
        scheduleRefresh({ sessions: true, terminals: true, messages: true, capabilities: true }, STREAM_DEBOUNCE_FAST_MS);
      }

      if (envelope.kind === "update") {
        const payload = envelope.payload || {};
        const update = payload.update || payload;
        const permission = parsePermissionFromUpdate(update);
        if (permission) {
          enqueuePermission(permission);
        }

        if (payload.sessionID && payload.sessionID === state.selectedSessionID) {
          scheduleRefresh({ sessions: true, messages: true }, STREAM_DEBOUNCE_FAST_MS);
        } else {
          scheduleRefresh({ sessions: true }, STREAM_DEBOUNCE_MS);
        }
      }

      if (envelope.kind === "log") {
        const payload = envelope.payload || {};
        if (payload.message) {
          logLine(payload.message);
        }
      }
    } catch (error) {
      logLine(`stream parse error: ${error.message}`);
    }
  };

  ["connected", "disconnected", "update", "error", "log", "resync-required"].forEach((name) => {
    state.eventSource.addEventListener(name, onEvent);
  });
}

function applyStreamCursorReset(config) {
  const epoch = String(config?.streamEpoch || "").trim();
  if (!epoch) {
    return;
  }

  if (state.streamEpoch !== epoch) {
    state.streamEpoch = epoch;
    localStorage.setItem("delight.streamEpoch", epoch);

    state.streamSince = 0;
    localStorage.setItem("delight.streamSince", "0");
  }
}

async function bootstrap() {
  try {
    initializeMarkdownRenderer();
    setConnectionState(false);

    const config = await api("/api/config");
    applyStreamCursorReset(config);

    ui.serverURL.value = config.serverURL || "";
    ui.masterKey.value = config.masterKey || "";

    if (config.preferences) {
      state.preferences = config.preferences;
    }

    loadPreferencesIntoControls();
    await refreshDebugLogs();

    connectStream();
    setConnectionState(Boolean(config.connected));

    if (state.isConnected) {
      scheduleRefresh({ sessions: true, terminals: true, messages: true, capabilities: true }, STREAM_DEBOUNCE_FAST_MS);
    }
    startAutoRefreshLoop();
  } catch (error) {
    logLine(`bootstrap failed: ${error.message}`);
    setConnectionState(false);
  }
}

async function createAccount() {
  const payload = await api("/api/account/create", {
    method: "POST",
    body: {
      serverURL: ui.serverURL.value.trim(),
      masterKey: ui.masterKey.value.trim(),
    },
  });

  if (payload.masterKey) {
    ui.masterKey.value = payload.masterKey;
  }

  scheduleRefresh({ sessions: true, terminals: true, messages: true, capabilities: true }, STREAM_DEBOUNCE_FAST_MS);
}

async function connectAccount() {
  await api("/api/account/connect", {
    method: "POST",
    body: {
      serverURL: ui.serverURL.value.trim(),
      masterKey: ui.masterKey.value.trim(),
    },
  });

  scheduleRefresh({ sessions: true, terminals: true, messages: true, capabilities: true }, STREAM_DEBOUNCE_FAST_MS);
}

async function disconnectAccount() {
  await api("/api/account/disconnect", { method: "POST", body: {} });
  setConnectionState(false);
}

async function toggleAccountConnection() {
  if (state.isConnected) {
    await disconnectAccount();
    return;
  }
  await connectAccount();
}

async function logoutAccount() {
  await api("/api/account/logout", { method: "POST", body: {} });
  setConnectionState(false);
}

async function generateKey() {
  const payload = await api("/api/account/key/generate", { method: "POST", body: {} });
  ui.masterKey.value = payload.masterKey || "";
}

async function resetKey() {
  await api("/api/account/key/reset", { method: "POST", body: {} });
  ui.masterKey.value = "";
  setConnectionState(false);
}

async function pairTerminal() {
  const payload = await api("/api/terminals/pair", {
    method: "POST",
    body: { qrURL: ui.pairURL.value.trim() },
  });

  ui.pairReceipt.textContent = JSON.stringify(payload.receipt || payload, null, 2);
  scheduleRefresh({ terminals: true, sessions: true }, STREAM_DEBOUNCE_FAST_MS);
}

async function loadOlderMessages() {
  const sessionID = state.selectedSessionID;
  if (!sessionID) {
    return;
  }

  if (state.loadingOlderBySession[sessionID]) {
    return;
  }

  const now = Date.now();
  const previousStartedAt = Number(state.lastOlderRequestMsBySession[sessionID] || 0);
  if (now - previousStartedAt < LOAD_OLDER_COOLDOWN_MS) {
    return;
  }

  if (!state.hasMoreBySession[sessionID]) {
    return;
  }

  const beforeSeq = Number(state.oldestSeqBySession[sessionID] || 0);
  if (beforeSeq <= 0) {
    return;
  }

  state.lastOlderRequestMsBySession[sessionID] = now;
  state.loadingOlderBySession[sessionID] = true;
  try {
    await loadSessionMessages(sessionID, beforeSeq, true);
  } finally {
    state.loadingOlderBySession[sessionID] = false;
  }
}

async function startScan() {
  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    logLine("camera API unavailable in this browser");
    return;
  }

  if (!window.BarcodeDetector) {
    logLine("BarcodeDetector unavailable; paste QR URL manually");
    return;
  }

  const detector = new BarcodeDetector({ formats: ["qr_code"] });
  scanStream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" } });
  ui.scanVideo.srcObject = scanStream;
  ui.scanVideo.hidden = false;

  scanTimer = window.setInterval(async () => {
    if (!ui.scanVideo.videoWidth) {
      return;
    }

    try {
      const results = await detector.detect(ui.scanVideo);
      if (results.length > 0) {
        const raw = results[0].rawValue || "";
        if (raw.startsWith("delight://terminal")) {
          ui.pairURL.value = raw;
          stopScan();
          logLine("QR detected");
        }
      }
    } catch {
      // ignore transient detector failures
    }
  }, 300);
}

function stopScan() {
  if (scanTimer) {
    clearInterval(scanTimer);
    scanTimer = null;
  }
  if (scanStream) {
    scanStream.getTracks().forEach((track) => track.stop());
    scanStream = null;
  }
  ui.scanVideo.hidden = true;
  ui.scanVideo.srcObject = null;
}

function bindEvents() {
  if (ui.openTerminalsBtn) {
    ui.openTerminalsBtn.addEventListener("click", () => {
      setSettingsScreenOpen(false);
      setTerminalsScreenOpen(true);
      renderTerminalPicker();
      Promise.all([
        refreshSessionsCore({ loadMessages: false, loadCapabilities: false }),
        refreshTerminalsCore(),
      ]).catch((error) => {
        logLine(`terminal refresh failed: ${error.message}`);
      });
    });
  }
  if (ui.closeTerminalsBtn) {
    ui.closeTerminalsBtn.addEventListener("click", () => {
      setTerminalsScreenOpen(false);
    });
  }
  if (ui.openSettingsBtn) {
    ui.openSettingsBtn.addEventListener("click", () => {
      setTerminalsScreenOpen(false);
      setSettingsScreenOpen(true);
      refreshDebugLogs().catch((error) => {
        logLine(`debug refresh failed: ${error.message}`);
      });
    });
  }
  if (ui.closeSettingsBtn) {
    ui.closeSettingsBtn.addEventListener("click", () => {
      setSettingsScreenOpen(false);
    });
  }

  const bindButtonAction = (id, fn, label) => {
    const button = document.getElementById(id);
    if (!button) {
      return;
    }
    button.addEventListener("click", (event) => {
      runAction(fn, {
        label: label || button.textContent.trim() || id,
        button: event.currentTarget,
      });
    });
  };

  bindButtonAction("generateKeyBtn", generateKey, "Generate key");
  bindButtonAction("createAccountBtn", createAccount, "Create account");
  bindButtonAction("connectToggleBtn", toggleAccountConnection);
  bindButtonAction("logoutBtn", logoutAccount, "Logout");
  bindButtonAction("resetKeyBtn", resetKey, "Reset keys");

  bindButtonAction("pairBtn", pairTerminal, "Pair terminal");
  bindButtonAction("scanBtn", startScan, "Start scan");
  document.getElementById("stopScanBtn").addEventListener("click", stopScan);

  bindButtonAction("refreshSessionsBtn", () => refreshSessionsCore({ loadMessages: true, loadCapabilities: true }), "Refresh sessions");
  bindButtonAction("refreshTerminalsBtn", refreshTerminalsCore, "Refresh terminals");
  bindButtonAction("refreshTerminalPickerBtn", refreshTerminalsCore, "Refresh terminals");
  bindButtonAction("takeControlBtn", () => requestSwitch("remote"), "Take control");
  bindButtonAction("openModelBtn", async () => {
    setModelDialogOpen(true);
    try {
      await refreshCapabilities();
    } catch (error) {
      logLine(`capabilities refresh failed: ${error.message}`);
    }
  }, "Model settings");
  if (ui.closeModelBtn) {
    ui.closeModelBtn.addEventListener("click", () => {
      setModelDialogOpen(false);
    });
  }
  if (ui.scrollBottomBtn) {
    ui.scrollBottomBtn.addEventListener("click", () => {
      ui.messages.scrollTop = ui.messages.scrollHeight;
      updateScrollBottomVisibility();
    });
  }
  if (ui.messages) {
    let touchPullStartY = null;
    let touchPullTriggered = false;

    const triggerLoadOlderIfAtTop = () => {
      if (!state.selectedSessionID) {
        return;
      }
      if (ui.messages.scrollTop > SCROLL_TOP_TRIGGER_PX) {
        return;
      }
      loadOlderMessages().catch((error) => {
        logLine(`older messages load failed: ${error.message}`);
      });
    };

    ui.messages.addEventListener("scroll", () => {
      updateScrollBottomVisibility();
    });

    ui.messages.addEventListener("wheel", (event) => {
      if (event.deltaY >= 0) {
        return;
      }
      triggerLoadOlderIfAtTop();
    }, { passive: true });

    ui.messages.addEventListener("touchstart", (event) => {
      if (ui.messages.scrollTop > SCROLL_TOP_TRIGGER_PX || event.touches.length !== 1) {
        touchPullStartY = null;
        touchPullTriggered = false;
        return;
      }
      touchPullStartY = Number(event.touches[0]?.clientY || 0);
      touchPullTriggered = false;
    }, { passive: true });

    ui.messages.addEventListener("touchmove", (event) => {
      if (touchPullStartY == null || touchPullTriggered || event.touches.length !== 1) {
        return;
      }
      if (ui.messages.scrollTop > SCROLL_TOP_TRIGGER_PX) {
        touchPullStartY = null;
        touchPullTriggered = false;
        return;
      }
      const currentY = Number(event.touches[0]?.clientY || 0);
      const deltaY = currentY - touchPullStartY;
      if (deltaY >= PULL_TO_LOAD_TRIGGER_PX) {
        touchPullTriggered = true;
        triggerLoadOlderIfAtTop();
      }
    }, { passive: true });

    ui.messages.addEventListener("touchend", () => {
      touchPullStartY = null;
      touchPullTriggered = false;
    }, { passive: true });

    ui.messages.addEventListener("touchcancel", () => {
      touchPullStartY = null;
      touchPullTriggered = false;
    }, { passive: true });
  }

  const runPrimaryComposerAction = async () => {
    const session = selectedSession();
    if (!session) {
      return;
    }
    if (isSessionWorking(session)) {
      await abortTurn();
      return;
    }
    await sendMessage();
  };
  const sendButton = document.getElementById("sendBtn");
  if (sendButton) {
    sendButton.addEventListener("click", (event) => {
      const session = selectedSession();
      const label = isSessionWorking(session) ? "Abort turn" : "Send";
      runAction(runPrimaryComposerAction, {
        label,
        button: event.currentTarget,
      });
    });
  }
  ui.messageInput.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      const session = selectedSession();
      const label = isSessionWorking(session) ? "Abort turn" : "Send";
      runAction(runPrimaryComposerAction, { label });
      return;
    }
    if (event.key === "ArrowUp" && !event.shiftKey && ui.messageInput.selectionStart === 0) {
      event.preventDefault();
      stepPromptHistory(-1);
      return;
    }
    if (event.key === "ArrowDown" && !event.shiftKey && ui.messageInput.selectionStart === ui.messageInput.value.length) {
      event.preventDefault();
      stepPromptHistory(1);
    }
  });

  bindButtonAction("refreshCapabilitiesBtn", refreshCapabilities, "Refresh capabilities");
  bindButtonAction("applyAgentConfigBtn", async () => {
    await applyAgentConfig();
    setModelDialogOpen(false);
  }, "Apply agent config");

  bindButtonAction("savePrefsBtn", savePreferences, "Save preferences");
  ui.appearanceSelect.addEventListener("change", () => {
    applyTheme(ui.appearanceSelect.value);
  });

  document.getElementById("allowPermissionBtn").addEventListener("click", (event) => {
    event.preventDefault();
    runAction(() => decidePermission(true), { label: "Allow permission", button: event.currentTarget });
  });
  document.getElementById("denyPermissionBtn").addEventListener("click", (event) => {
    event.preventDefault();
    runAction(() => decidePermission(false), { label: "Deny permission", button: event.currentTarget });
  });

  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) {
      scheduleRefresh({ sessions: true, terminals: true, messages: true, capabilities: true }, STREAM_DEBOUNCE_FAST_MS);
    }
  });

  window.addEventListener("beforeunload", () => {
    stopScan();
    stopAutoRefreshLoop();
    if (state.eventSource) {
      state.eventSource.close();
    }
  });

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") {
      return;
    }
    if (ui.terminalsScreen && !ui.terminalsScreen.classList.contains("hidden")) {
      setTerminalsScreenOpen(false);
      return;
    }
    if (ui.settingsScreen && !ui.settingsScreen.classList.contains("hidden")) {
      setSettingsScreenOpen(false);
    }
  });
}

async function runAction(fn, options = {}) {
  const label = options.label || "Action";
  const button = options.button || null;
  if (button) {
    button.disabled = true;
  }
  setActionBadge(`${label}...`, "working");
  try {
    await fn();
    setActionBadge(`${label} done`, "success");
    clearActionBadgeSoon();
  } catch (error) {
    logLine(error.message || String(error));
    setActionBadge(`${label} failed`, "error");
    clearActionBadgeSoon();
  } finally {
    if (button) {
      button.disabled = false;
    }
    updateMainInteractivity();
  }
}

bindEvents();
bootstrap();
