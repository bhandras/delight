import Foundation
import SwiftUI
import DelightSDK

/// PermissionDecisionParams is the payload sent to `sessionID:permission` RPC calls.
private struct PermissionDecisionParams: Encodable {
    let requestId: String
    let allow: Bool
    let message: String
}

/// SwitchControlParams is the payload sent to `sessionID:switch` RPC calls.
private struct SwitchControlParams: Encodable {
    let mode: String
}

/// SwitchControlResponse is the best-effort response schema for switch RPC calls.
private struct SwitchControlResponse: Decodable {
    struct Result: Decodable {
        let mode: String?
    }

    let result: Result?
}

/// RawUserMessageRecord is the schema used by the CLI to represent a user chat message.
///
/// This is forwarded to the CLI via the Go SDK as `rawRecordJSON`.
private struct RawUserMessageRecord: Encodable {
    struct Content: Encodable {
        let type: String
        let text: String
    }

    let role: String
    let content: Content
}

/// UpdateEnvelope is the outer JSON envelope delivered to `onUpdate`.
///
/// The server/SDK emits multiple shapes:
/// - root-level fields (`type`, `t`, `sid`, `message`, ...)
/// - nested `body` with `t`/`type` + payload fields
///
/// We decode a superset here and then interpret it in the higher-level helpers.
private struct UpdateEnvelope: Decodable {
    let body: UpdateBody?

    // Some sources include these at the root level.
    let sid: String?
    let id: String?
    let t: String?
    let type: String?
    let message: JSONValue?

    // Root-level activity payload (best-effort).
    let active: Bool?
    let activeAt: Int64?
    let thinking: Bool?
    let time: Int64?

    // Root-level permission request payload (best-effort).
    let requestId: String?
    let toolName: String?
    let input: String?
}

/// UpdateBody is the nested payload for many update messages.
private struct UpdateBody: Decodable {
    // Discriminants: older updates use `t`, newer may use `type`.
    let t: String?
    let type: String?

    // Session identifiers appear as either `sid` or `id`.
    let sid: String?
    let id: String?

    // Common payload fields (present depending on `t`/`type`).
    let ui: SessionUIState?
    let message: JSONValue?

    // Permission request payload.
    let requestId: String?
    let toolName: String?
    let input: String?

    // Activity payload.
    let active: Bool?
    let activeAt: Int64?
    let thinking: Bool?
    let time: Int64?
}

/// UpdateKind enumerates the update event discriminants sent by the server/SDK.
private enum UpdateKind: String {
    case activity = "activity"
    case newMessage = "new-message"
    case permissionRequest = "permission-request"
    case sessionAlive = "session-alive"
    case sessionUI = "session-ui"
}

/// UpdateFields collects JSON keys used in update payloads.
private enum UpdateFields {
    static let content = "content"
    static let createdAt = "createdAt"
    static let data = "data"
    static let id = "id"
    static let localID = "localId"
    static let message = "message"
    static let payload = "c"
    static let role = "role"
    static let seq = "seq"
    static let sessionID = "sessionId"
    static let text = "text"
    static let type = "type"
    static let typeShort = "t"
    static let uuid = "uuid"
}

/// MessageValue enumerates the string values we expect in message payloads.
///
/// These values originate from the server and Claude SDK streams.
private enum MessageValue {
    enum BlockType {
        static let ciphertext = "ciphertext"
        static let encrypted = "encrypted"
        static let fileHistorySnapshot = "file-history-snapshot"
        static let thinking = "thinking"
        static let text = "text"
        static let toolCallDash = "tool-call"
        static let toolResultDash = "tool-result"
        static let toolResultSnake = "tool_result"
        static let toolUseDash = "tool-use"
        static let toolUseSnake = "tool_use"

        static let toolCallTypes: Set<String> = [
            toolCallDash,
            toolUseDash,
            toolUseSnake,
        ]

        static let toolResultTypes: Set<String> = [
            toolResultDash,
            toolResultSnake,
        ]

        static let encryptedTypes: Set<String> = [
            ciphertext,
            encrypted,
        ]
    }

    enum Role {
        static let agent = "agent"
        static let assistant = "assistant"
        static let event = "event"
        static let system = "system"
        static let tool = "tool"
        static let user = "user"
    }
}

/// MessageFields collects JSON keys used in Claude message blocks and server responses.
private enum MessageFields {
    static let command = "command"
    static let filePath = "file_path"
    static let hasMore = "hasMore"
    static let id = "id"
    static let input = "input"
    static let items = "items"
    static let message = "message"
    static let messages = "messages"
    static let name = "name"
    static let nextBeforeSeq = "nextBeforeSeq"
    static let page = "page"
    static let path = "path"
    static let pattern = "pattern"
}

/// UpdateTiming collects debounce and clock-related constants.
private enum UpdateTiming {
    static let millisecondsPerSecond: Double = 1000
    static let sessionRefreshDelaySeconds: TimeInterval = 0.35
    static let sessionRefreshMinIntervalSeconds: TimeInterval = 1.0
}

/// LogLimits defines the maximum log buffer size retained in memory.
private enum LogLimits {
    static let maxLines = 100
}

final class HarnessViewModel: NSObject, ObservableObject, SdkListenerProtocol {
    @Published var serverURL: String = "http://localhost:3005" {
        didSet { persistSettings() }
    }
    @Published var token: String = "" {
        didSet {
            persistSettings()
            if !oldValue.isEmpty && token.isEmpty {
                clearSessionState(reason: "token cleared")
            }
        }
    }
    @Published var masterKey: String = "" {
        didSet { persistKeys() }
    }
    @Published var terminalURL: String = ""
    @Published var sessionID: String = ""
    @Published var messageText: String = ""
    @Published var status: String = "disconnected"
    @Published var logs: String = ""
    @Published var lastLogLine: String = ""
    @Published var publicKey: String = "" {
        didSet { persistKeys() }
    }
    @Published var privateKey: String = "" {
        didSet { persistKeys() }
    }
    @Published var sessions: [SessionSummary] = []
    @Published var messages: [MessageItem] = []
    @Published var hasMoreHistory: Bool = false
    @Published var isLoadingHistory: Bool = false
    @Published var scrollRequest: ScrollRequest?
    @Published var machines: [MachineInfo] = []
    @Published var logServerURL: String = ""
    @Published var logServerRunning: Bool = false
    @Published var showCrashReport: Bool = false
    @Published var crashReportText: String = ""
    @Published var appearanceMode: AppearanceMode = .system {
        didSet { persistSettings() }
    }
    @Published var permissionQueue: [PendingPermissionRequest] = []
    @Published var activePermissionRequest: PendingPermissionRequest?
    @Published var showPermissionPrompt: Bool = false
    @Published var isRespondingToPermission: Bool = false
    @Published var isCreatingAccount: Bool = false
    @Published var isApprovingTerminal: Bool = false
    @Published var isLoggingOut: Bool = false
    @Published var showAccountCreatedReceipt: Bool = false
    @Published var showTerminalPairingReceipt: Bool = false
    @Published var showLogoutConfirm: Bool = false

    @Published var lastAccountCreatedReceipt: AccountCreatedReceipt?
    @Published var lastTerminalPairingReceipt: TerminalPairingReceipt?

    private let client: SdkClient
    private static let settingsKeyPrefix = "delight.harness."
    private var selectedMetadata: SessionMetadata?
    private var logLines: [String] = []
    private var needsSessionRefresh: Bool = false
    private var oldestLoadedSeq: Int64?
    private var scheduledSessionRefresh: DispatchWorkItem?
    private var lastSessionRefreshAt: Date = .distantPast

    // Calls into the gomobile-generated SDK must be serialized and must never be made
    // synchronously from inside a Go→Swift callback (e.g. `onUpdate`).
    //
    // We saw a reproducible Go runtime crash ("bulkBarrierPreWrite: unaligned arguments")
    // when Swift called back into Go from inside a listener callback stack.
    private let sdkCallQueue = DispatchQueue(label: "com.bhandras.delight.harness.sdkCallQueue")
    private let sdkCallQueueKey = DispatchSpecificKey<Void>()

    override init() {
        let defaults = UserDefaults.standard
        let loadedServerURL = defaults.string(forKey: Self.settingsKeyPrefix + "serverURL") ?? "http://localhost:3005"
        let loadedToken = defaults.string(forKey: Self.settingsKeyPrefix + "token") ?? ""
        let loadedAppearanceMode =
            AppearanceMode(rawValue: defaults.string(forKey: Self.settingsKeyPrefix + "appearanceMode") ?? "")
            ?? .system
        let loadedMasterKey = KeychainStore.string(for: "masterKey") ?? ""
        let loadedPublicKey = KeychainStore.string(for: "publicKey") ?? ""
        let loadedPrivateKey = KeychainStore.string(for: "privateKey") ?? ""

        guard let client = SdkNewClient(loadedServerURL) else {
            fatalError("Failed to create SDK client")
        }
        self.client = client
        super.init()
        serverURL = loadedServerURL
        token = loadedToken
        appearanceMode = loadedAppearanceMode
        masterKey = loadedMasterKey
        publicKey = loadedPublicKey
        privateKey = loadedPrivateKey
        configureLogDirectory()
        ensureKeys()
        sdkCallQueue.setSpecific(key: sdkCallQueueKey, value: ())
        // Register the Go→Swift listener for live updates.
        //
        // IMPORTANT: Never call into Go synchronously from within the listener
        // callback stack. All callbacks should schedule work asynchronously.
        sdkCallAsync {
            self.client.setListener(self)
        }
    }

    /// decodeUpdateEnvelope decodes the best-effort update envelope used by `onUpdate`.
    private func decodeUpdateEnvelope(_ json: String) -> UpdateEnvelope? {
        try? JSONCoding.decode(UpdateEnvelope.self, from: json)
    }

    /// decodeJSONValue decodes a JSON string into a JSONValue tree.
    private func decodeJSONValue(_ json: String) -> JSONValue? {
        try? JSONCoding.decode(JSONValue.self, from: json)
    }

    private func firstNonNull(_ candidates: JSONValue?...) -> JSONValue? {
        for candidate in candidates {
            if case .null = candidate {
                continue
            }
            if let candidate {
                return candidate
            }
        }
        return nil
    }

    private func jsonString(_ obj: [String: JSONValue], _ key: String) -> String? {
        obj[key]?.string
    }

    private func jsonBool(_ obj: [String: JSONValue], _ key: String) -> Bool? {
        obj[key]?.bool
    }

    private func jsonInt64(_ obj: [String: JSONValue], _ key: String) -> Int64? {
        guard let value = obj[key] else { return nil }
        if let int64 = value.int64 { return int64 }
        if let double = value.number { return Int64(double) }
        return nil
    }

    private func jsonObject(_ obj: [String: JSONValue], _ key: String) -> [String: JSONValue]? {
        obj[key]?.object
    }

    private func jsonArray(_ obj: [String: JSONValue], _ key: String) -> [JSONValue]? {
        obj[key]?.array
    }

    private func stringFromBuffer(_ buffer: SdkBuffer?) -> String? {
        guard let buffer else { return nil }
        let length = Int(buffer.len())
        if length == 0 {
            return ""
        }
        var data = Data(count: length)
        var written: Int = 0
        var copyError: Error?
        let ok: Bool = data.withUnsafeMutableBytes { raw in
            guard let base = raw.baseAddress else { return false }
            let ptr = Int64(UInt(bitPattern: base))
            do {
                try buffer.copy(to: ptr, dstLen: length, ret0_: &written)
                return true
            } catch {
                copyError = error
                return false
            }
        }
        if let copyError {
            log("Buffer copy error: \(copyError)")
            return nil
        }
        guard ok else {
            log("Buffer copy failed")
            return nil
        }
        if written < 0 {
            return nil
        }
        if written < data.count {
            data = data.prefix(written)
        }
        return String(data: data, encoding: .utf8)
    }

    private func sdkCallSync<T>(_ work: () throws -> T) rethrows -> T {
        if DispatchQueue.getSpecific(key: sdkCallQueueKey) != nil {
            return try work()
        }
        return try sdkCallQueue.sync {
            try work()
        }
    }

    private func sdkCallAsync(_ work: @escaping () -> Void) {
        sdkCallQueue.async(execute: work)
    }

    func startup() {
        if CrashLogger.consumeCrashFlag() {
            crashReportText = crashLogTail()
            showCrashReport = true
            resetAfterCrash()
        }
        if !masterKey.isEmpty && (!token.isEmpty || (!publicKey.isEmpty && !privateKey.isEmpty)) {
            connect()
            listSessions()
        }
    }

    func resetAfterCrash() {
        sdkCallAsync {
            self.client.disconnect()
        }
        clearSessionState(reason: "crash detected")
    }

    private func clearSessionState(reason: String) {
        sessionID = ""
        selectedMetadata = nil
        messages = []
        sessions = []
        machines = []
        permissionQueue = []
        activePermissionRequest = nil
        showPermissionPrompt = false
        isRespondingToPermission = false
        logLines = []
        needsSessionRefresh = true
        oldestLoadedSeq = nil
        scrollRequest = nil
        log("Cleared cached session state (\(reason))")
    }

    func generateKeys() {
        var error: NSError?
        let masterBuf = SdkGenerateMasterKeyBase64Buffer(&error)
        if let error {
            log("Generate master key error: \(error)")
            return
        }
        guard let master = stringFromBuffer(masterBuf) else {
            log("Generate master key error: unable to decode master key")
            return
        }
        guard let keypair = SdkGenerateEd25519KeyPairBuffers(&error) else {
            log("Generate keypair error: \(error?.localizedDescription ?? "unknown error")")
            return
        }
        if let error {
            log("Generate keypair error: \(error)")
            return
        }
        guard let pub = stringFromBuffer(keypair.publicKey()),
              let priv = stringFromBuffer(keypair.privateKey()) else {
            log("Generate keypair error: unable to decode keypair")
            return
        }
        masterKey = master
        publicKey = pub
        privateKey = priv
        log("Generated master + ed25519 keypair")
    }

    func resetKeys() {
        KeychainStore.delete("masterKey")
        KeychainStore.delete("publicKey")
        KeychainStore.delete("privateKey")
        masterKey = ""
        publicKey = ""
        privateKey = ""
        token = ""
        sdkCallAsync {
            self.client.disconnect()
        }
        DispatchQueue.main.async {
            self.status = "disconnected"
            self.log("Keys reset")
        }
    }

    func createAccount() {
        guard !isCreatingAccount else { return }

        if publicKey.isEmpty || privateKey.isEmpty || masterKey.isEmpty {
            generateKeys()
        }

        isCreatingAccount = true
        sdkCallAsync {
            do {
                let tokenBuf = try self.sdkCallSync {
                    try self.client.auth(withKeyPairBuffer: self.publicKey, privateKeyB64: self.privateKey)
                }
                guard let tokenValue = self.stringFromBuffer(tokenBuf) else {
                    DispatchQueue.main.async {
                        self.isCreatingAccount = false
                        self.log("Auth error: unable to decode token")
                    }
                    return
                }
                DispatchQueue.main.async {
                    self.token = tokenValue
                    self.lastAccountCreatedReceipt = AccountCreatedReceipt(
                        serverURL: self.serverURL,
                        masterKey: self.masterKey,
                        publicKey: self.publicKey,
                        privateKey: self.privateKey,
                        token: tokenValue
                    )
                    self.showAccountCreatedReceipt = true
                    self.isCreatingAccount = false
                    self.connect()
                }
            } catch {
                DispatchQueue.main.async {
                    self.isCreatingAccount = false
                    self.log("Auth error: \(error)")
                }
            }
        }
    }

    func logout() {
        guard !isLoggingOut else { return }

        isLoggingOut = true
        sdkCallAsync {
            self.client.disconnect()
            DispatchQueue.main.async {
                self.token = ""
                self.status = "disconnected"
                self.showLogoutConfirm = false
                self.isLoggingOut = false
                self.log("Logged out")
            }
        }
    }

    func dismissPermissionPrompt() {
        showPermissionPrompt = false
    }

    func submitPermissionDecision(allow: Bool, message: String) {
        guard let request = activePermissionRequest else {
            showPermissionPrompt = false
            return
        }
        guard !isRespondingToPermission else { return }

        isRespondingToPermission = true
        sdkCallAsync {
            do {
                let paramsJSON = try JSONCoding.encode(
                    PermissionDecisionParams(requestId: request.requestID, allow: allow, message: message)
                )
                let method = request.sessionID + ":permission"

                _ = try self.sdkCallSync {
                    try self.client.callRPCBuffer(method, paramsJSON: paramsJSON)
                }

                DispatchQueue.main.async {
                    self.completePermissionRequest(requestID: request.requestID)
                }
            } catch {
                DispatchQueue.main.async {
                    self.isRespondingToPermission = false
                    self.logSwiftOnly("Permission response error: \(error)")
                }
            }
        }
    }

    func requestSessionControl(mode: String, sessionID: String? = nil) {
        let targetID = sessionID ?? self.sessionID
        guard !targetID.isEmpty else { return }

        sdkCallAsync {
            do {
                let paramsJSON = try JSONCoding.encode(SwitchControlParams(mode: mode))
                let responseBuf = try self.sdkCallSync {
                    try self.client.callRPCBuffer(targetID + ":switch", paramsJSON: paramsJSON)
                }
                let responseJSON = self.stringFromBuffer(responseBuf) ?? ""
                let returnedMode: String? = (try? JSONCoding.decode(SwitchControlResponse.self, from: responseJSON))
                    .flatMap { $0.result?.mode }

                // Do not optimistically rewrite session state in Swift.
                // The Go SDK is the source of truth for control FSM; we refresh sessions below.
                _ = returnedMode // parsed for debugging / future UI messaging.

                // Refresh sessions to pick up the authoritative agent state (plus requests).
                self.listSessions()
            } catch {
                self.logSwiftOnly("Switch control error: \(error)")
            }
        }
    }

    private func completePermissionRequest(requestID: String) {
        permissionQueue.removeAll(where: { $0.requestID == requestID })
        if let next = permissionQueue.first {
            activePermissionRequest = next
            let owningSession = sessions.first(where: { $0.id == next.sessionID })
            let controlledByDesktop = owningSession?.uiState?.controlledByUser
                ?? owningSession?.agentState?.controlledByUser
                ?? false
            showPermissionPrompt = !controlledByDesktop
        } else {
            activePermissionRequest = nil
            showPermissionPrompt = false
        }
        isRespondingToPermission = false
    }

    func sessionTitle(for id: String) -> String? {
        if let session = sessions.first(where: { $0.id == id }) {
            return session.title ?? session.metadata?.host ?? session.id
        }
        return nil
    }

    func prettyPrintedJSON(fromJSONString json: String) -> String? {
        JSONCoding.prettyPrint(json: json)
    }

    func authWithKeypair() {
        do {
            let tokenBuf = try sdkCallSync {
                try client.auth(withKeyPairBuffer: publicKey, privateKeyB64: privateKey)
            }
            guard let tokenValue = stringFromBuffer(tokenBuf) else {
                log("Auth error: unable to decode token")
                return
            }
            token = tokenValue
            log("Auth ok")
        } catch {
            log("Auth error: \(error)")
        }
    }

    private func parseTerminalURLMetadata(_ raw: String) -> (host: String?, machineID: String?) {
        guard let components = URLComponents(string: raw) else { return (nil, nil) }
        let host = components.queryItems?.first(where: { $0.name == "host" })?.value
        let machineID =
            components.queryItems?.first(where: { $0.name == "machineId" })?.value
            ?? components.queryItems?.first(where: { $0.name == "machine_id" })?.value
        return (host, machineID)
    }

    func approveTerminal() {
        guard !isApprovingTerminal else { return }
        guard !token.isEmpty else {
            log("Approve error: must be logged in to approve a terminal")
            return
        }

        isApprovingTerminal = true
        let rawURL = terminalURL
        let metadata = parseTerminalURLMetadata(rawURL)

        sdkCallAsync {
            var error: NSError?
            let terminalKeyBuf = SdkParseTerminalURLBuffer(rawURL, &error)
            if let error {
                DispatchQueue.main.async {
                    self.isApprovingTerminal = false
                    self.log("Approve error: \(error)")
                }
                return
            }
            guard let terminalKey = self.stringFromBuffer(terminalKeyBuf) else {
                DispatchQueue.main.async {
                    self.isApprovingTerminal = false
                    self.log("Approve error: unable to decode terminal key")
                }
                return
            }
            do {
                try self.sdkCallSync {
                    self.client.setServerURL(self.serverURL)
                    self.client.setToken(self.token)
                    try self.client.setMasterKeyBase64(self.masterKey)
                    try self.client.approveTerminalAuth(terminalKey, masterKeyB64: self.masterKey)
                }
                DispatchQueue.main.async {
                    self.lastTerminalPairingReceipt = TerminalPairingReceipt(
                        serverURL: self.serverURL,
                        host: metadata.host,
                        machineID: metadata.machineID,
                        terminalKey: terminalKey
                    )
                    self.showTerminalPairingReceipt = true
                    self.isApprovingTerminal = false
                    self.terminalURL = ""
                    self.listSessions()
                    self.log("Approved terminal auth")
                }
            } catch {
                DispatchQueue.main.async {
                    self.isApprovingTerminal = false
                    self.log("Approve error: \(error)")
                }
            }
        }
    }

    func connect() {
        do {
            if masterKey.isEmpty {
                log("Master key missing; generating a new one.")
                generateKeys()
            }
            guard !masterKey.isEmpty else {
                log("Connect error: master key is empty")
                return
            }
            if token.isEmpty {
                if publicKey.isEmpty || privateKey.isEmpty {
                    log("Connect error: token missing and keypair not generated")
                    return
                }
                log("Token missing; attempting auth with keypair.")
                do {
                    let tokenBuf = try sdkCallSync {
                        try client.auth(withKeyPairBuffer: publicKey, privateKeyB64: privateKey)
                    }
                    guard let tokenValue = stringFromBuffer(tokenBuf) else {
                        log("Auth error: unable to decode token")
                        return
                    }
                    token = tokenValue
                } catch {
                    log("Auth error: \(error)")
                    return
                }
                log("Auth ok")
            }
            try sdkCallSync {
                client.setServerURL(serverURL)
                client.setToken(token)
                try client.setMasterKeyBase64(masterKey)
                client.setListener(self)
                try client.connect()
            }
            status = "connected"
            if sessions.isEmpty || needsSessionRefresh {
                listSessions()
                needsSessionRefresh = false
            }
        } catch {
            log("Connect error: \(error)")
        }
    }

    func disconnect() {
        sdkCallAsync {
            self.client.disconnect()
        }
        DispatchQueue.main.async {
            self.status = "disconnected"
        }
    }

    var permissionQueueCount: Int { permissionQueue.count }

    func listSessions() {
        do {
            let responseBuf = try sdkCallSync {
                try client.listSessionsBuffer()
            }
            guard let json = stringFromBuffer(responseBuf) else {
                log("List sessions error: unable to decode response")
                return
            }
            parseSessions(json)
            listMachines()
            log("Sessions loaded")
        } catch {
            log("List sessions error: \(error)")
        }
    }

    func listMachines() {
        do {
            let responseBuf = try sdkCallSync {
                try client.listMachinesBuffer()
            }
            if let json = stringFromBuffer(responseBuf), !json.isEmpty {
                parseMachines(json)
                log("Machines loaded")
            }
        } catch {
            log("List machines error: \(error)")
        }
    }

    func selectSession(_ id: String) {
        sessionID = id
        selectedMetadata = sessions.first(where: { $0.id == id })?.metadata
        messages = []
        hasMoreHistory = false
        oldestLoadedSeq = nil
        fetchLatestMessages(reset: true)
    }

    func fetchMessages() {
        fetchLatestMessages(reset: true)
    }

    func fetchLatestMessages(reset: Bool) {
        guard !sessionID.isEmpty else {
            log("Session ID required")
            return
        }
        do {
            let responseBuf: SdkBuffer? = try sdkCallSync {
                // Prefer cursor-based pagination if available.
                try client.getSessionMessagesPageBuffer(sessionID, limit: 50, beforeSeq: 0)
            }
            guard let json = stringFromBuffer(responseBuf) else {
                log("Get messages error: unable to decode response")
                return
            }
            log("getSessionMessages raw: \(json)")
            applyMessagesResponse(json, reset: reset, scrollToBottom: true)
        } catch {
            log("Get messages error: \(error)")
        }
    }

    func fetchOlderMessages() {
        guard !sessionID.isEmpty else { return }
        guard hasMoreHistory else { return }
        guard !isLoadingHistory else { return }
        guard let cursor = oldestLoadedSeq, cursor > 0 else { return }

        isLoadingHistory = true

        sdkCallAsync {
            do {
                let responseBuf = try self.sdkCallSync {
                    try self.client.getSessionMessagesPageBuffer(self.sessionID, limit: 50, beforeSeq: cursor)
                }
                guard let json = self.stringFromBuffer(responseBuf) else {
                    self.log("Get older messages error: unable to decode response")
                    DispatchQueue.main.async {
                        self.isLoadingHistory = false
                    }
                    return
                }
                DispatchQueue.main.async {
                    self.applyMessagesResponse(json, reset: false, scrollToBottom: false)
                    self.isLoadingHistory = false
                }
            } catch {
                DispatchQueue.main.async {
                    self.isLoadingHistory = false
                }
                self.log("Get older messages error: \(error)")
            }
        }
    }

    func sendMessage() {
        guard !sessionID.isEmpty else {
            log("Session ID required")
            return
        }
        guard !messageText.isEmpty else {
            log("Message required")
            return
        }

        // Require explicit "Take Control" before sending from phone.
        if let session = sessions.first(where: { $0.id == sessionID }) {
            let ui = session.uiState
            let controlledByDesktop = ui?.controlledByUser ?? (session.agentState?.controlledByUser ?? true)
            if controlledByDesktop {
                log("Desktop controls this session. Tap “Take Control” first.")
                return
            }
        }

        let outgoingText = messageText
        messageText = ""
        let localID = UUID().uuidString

        updateSessionThinking(true)

        // Optimistic UI: show the user's message immediately.
        let optimistic = MessageItem(
            id: "local-\(localID)",
            seq: nil,
            localID: localID,
            uuid: nil,
            role: .user,
            blocks: [.text(outgoingText)],
            createdAt: Int64(Date().timeIntervalSince1970 * 1000)
        )
        DispatchQueue.main.async {
            self.messages.append(optimistic)
            self.scrollRequest = ScrollRequest(target: .bottom)
        }

        do {
            let json = try JSONCoding.encode(
                RawUserMessageRecord(
                    role: MessageValue.Role.user,
                    content: .init(type: MessageValue.BlockType.text, text: outgoingText)
                )
            )

            // Do network work on the SDK queue to keep the UI responsive.
            sdkCallAsync {
                do {
                    try self.sdkCallSync {
                        try self.client.sendMessage(withLocalID: self.sessionID, localID: localID, rawRecordJSON: json)
                    }

                    self.log("Sent message")
                    // Pull latest state after send to incorporate server ordering + assistant reply.
                    //
                    // This merges (rather than replacing) so we don't blow away older pages.
                    self.fetchLatestMessages(reset: false)
                } catch {
                    DispatchQueue.main.async {
                        self.updateSessionThinking(false)
                    }
                    self.log("Send error: \(error)")
                }
            }
        } catch {
            updateSessionThinking(false)
            log("Send error: \(error)")
        }
    }

    // MARK: - SdkListener

    @objc func onConnected() {
        updateStatus("connected")
        clearThinkingState()
        if needsSessionRefresh {
            needsSessionRefresh = false
            // Avoid calling back into Go synchronously from a Go→Swift callback stack.
            DispatchQueue.main.async {
                self.listSessions()
            }
        }
    }

    @objc func onDisconnected(_ reason: String?) {
        updateStatus("disconnected: \(reason ?? "unknown")")
        clearThinkingState()
    }

    @objc func onUpdate(_ sessionID: String?, updateJSON: String?) {
        // IMPORTANT: `onUpdate` is invoked from Go into Swift. Do NOT call back into Go
        // synchronously from this callback, or we can trigger re-entrant cgo calls and
        // crash the Go runtime.
        if let updateJSON {
            logSwiftOnly("Update: \(updateJSON)")
            if handleSessionUIUpdate(updateJSON) {
                return
            }
            handleActivityUpdate(updateJSON)
            handlePermissionRequestUpdate(updateJSON)
            if let updateSessionID = extractUpdateSessionID(from: updateJSON) {
                guard updateSessionID == self.sessionID else { return }
                if tryAppendMessageFromUpdate(updateJSON, sessionID: updateSessionID) {
                    // If we successfully rendered the message from the update payload,
                    // don't immediately refetch from the server. This avoids any risk of
                    // "stale" pagination/ordering issues (limit=50) and also reduces load.
                    return
                }
                updateThinkingFromUpdate(updateJSON, targetSessionID: updateSessionID)
                if shouldFetchMessages(fromUpdateJSON: updateJSON) {
                    sdkCallAsync {
                        guard self.sessionID == updateSessionID else { return }
                        self.fetchMessages()
                    }
                }
                return
            }
        }
        guard let sessionID else { return }
        if sessionID == self.sessionID {
            if let updateJSON, tryAppendMessageFromUpdate(updateJSON, sessionID: sessionID) {
                return
            }
            sdkCallAsync {
                guard self.sessionID == sessionID else { return }
                self.fetchMessages()
            }
        }
    }

    /// handleSessionUIUpdate applies session UI updates to the cached summary list.
    private func handleSessionUIUpdate(_ json: String) -> Bool {
        guard let update = decodeUpdateEnvelope(json),
              let body = update.body,
              body.t == UpdateKind.sessionUI.rawValue,
              let sessionID = body.sid,
              let ui = body.ui else {
            return false
        }

        DispatchQueue.main.async {
            if let index = self.sessions.firstIndex(where: { $0.id == sessionID }) {
                let prev = self.sessions[index]
                self.sessions[index] = SessionSummary(
                    id: prev.id,
                    updatedAt: prev.updatedAt,
                    active: prev.active,
                    activeAt: prev.activeAt,
                    title: prev.title,
                    subtitle: prev.subtitle,
                    metadata: prev.metadata,
                    agentState: prev.agentState,
                    uiState: ui,
                    thinking: prev.thinking
                )
            }
        }
        return true
    }

    /// tryAppendMessageFromUpdate attempts to render a new message without a fetch.
    private func tryAppendMessageFromUpdate(_ updateJSON: String, sessionID: String) -> Bool {
        guard let update = decodeUpdateEnvelope(updateJSON),
              let body = update.body,
              body.t == UpdateKind.newMessage.rawValue,
              let messageValue = body.message,
              let message = messageValue.object else {
            return false
        }

        // Ignore messages we intentionally don't render as transcript entries.
        let content = normalizeContent(firstNonNull(message[UpdateFields.content], message[UpdateFields.data]))
        if isNullMessage(content) || isFileHistorySnapshot(content) || isToolResultMessage(content) {
            return true
        }

        let id = message[UpdateFields.id]?.string ?? UUID().uuidString
        let createdAt = jsonInt64(message, UpdateFields.createdAt)
        let seq = jsonInt64(message, UpdateFields.seq)

        var blocks = extractBlocks(from: content, sessionID: sessionID)
        if blocks.isEmpty, let text = extractText(from: content) {
            blocks = [.text(text)]
        }
        if blocks.isEmpty {
            if containsThinkingBlock(content) {
                // Thinking-only events are handled via `updateThinkingFromUpdate`.
                return true
            }
            logUnsupportedMessage(id: id, content: content)
            return false
        }

        let role = extractRole(from: message, content: content)
        let localID = message[UpdateFields.localID]?.string
        let uuid = self.extractMessageUUID(from: content)
        let serverItem = MessageItem(id: id, seq: seq, localID: localID, uuid: uuid, role: role, blocks: blocks, createdAt: createdAt)

        DispatchQueue.main.async {
            // Deduplicate if we already have this message.
            if self.messages.contains(where: { $0.id == serverItem.id }) {
                return
            }

            // Reconcile optimistic messages: replace in-place once the server echo arrives.
            if serverItem.role == .user {
                if let localID = serverItem.localID, !localID.isEmpty {
                    let optimisticID = "local-\(localID)"
                    if let idx = self.messages.firstIndex(where: { $0.id == optimisticID }) {
                        // Replace in-place to avoid a brief "duplicate bubble" flicker.
                        self.messages[idx] = serverItem
                        if self.shouldAutoScrollToBottom(afterAppending: serverItem) {
                            self.scrollRequest = ScrollRequest(target: .bottom)
                        }
                        return
                    }
                }

                // Deduplicate "echoes" of user messages coming from multiple sources.
                //
                // In practice we can see the same user message twice:
                //  - A raw record sent from the mobile UI (no `uuid` in decrypted content).
                //  - A later CLI-forwarded Claude session message (has `uuid`).
                // Prefer the uuid-bearing message, since it represents the canonical
                // transcript entry from the CLI session stream.
                if self.squashUserEchoes(prefer: serverItem) {
                    if self.shouldAutoScrollToBottom(afterAppending: serverItem) {
                        self.scrollRequest = ScrollRequest(target: .bottom)
                    }
                    return
                }

                // Fallback heuristic when localId isn't present: match by normalized blocks.
                let serverSig = self.blocksSignature(serverItem.blocks)
                if let idx = self.messages.lastIndex(where: { existing in
                    guard existing.id.hasPrefix("local-"), existing.role == .user else { return false }
                    return self.blocksSignature(existing.blocks) == serverSig
                }) {
                    self.messages.remove(at: idx)
                }
            }

            self.messages.append(serverItem)
            if self.shouldAutoScrollToBottom(afterAppending: serverItem) {
                self.scrollRequest = ScrollRequest(target: .bottom)
            }
        }

        // If we rendered a real message, ensure we clear thinking for this session.
        updateSessionThinking(false, sessionID: sessionID)
        return true
    }

    /// shouldFetchMessages decides if an update requires a server-side refresh.
    private func shouldFetchMessages(fromUpdateJSON json: String) -> Bool {
        guard let update = decodeUpdateEnvelope(json),
              let body = update.body,
              body.t == UpdateKind.newMessage.rawValue,
              let messageValue = body.message,
              let message = messageValue.object else {
            return true
        }
        let content = normalizeContent(firstNonNull(message[UpdateFields.content], message[UpdateFields.data]))
        if isNullMessage(content) || isFileHistorySnapshot(content) || isToolResultMessage(content) {
            return false
        }
        if containsThinkingBlock(content) && extractBlocks(from: content, sessionID: sessionID).isEmpty {
            return false
        }
        if extractBlocks(from: content, sessionID: sessionID).isEmpty, extractText(from: content) == nil {
            return false
        }
        return true
    }

    private func clearThinkingState() {
        let targetID = sessionID
        if targetID.isEmpty {
            DispatchQueue.main.async {
                self.sessions = self.sessions.map { $0.updatingActivity(active: nil, activeAt: nil, thinking: false) }
            }
            return
        }
        updateSessionThinking(false)
    }

    @objc func onError(_ message: String?) {
        log("SDK error: \(message ?? "unknown")")
    }

    private func updateStatus(_ value: String) {
        DispatchQueue.main.async {
            self.status = value
        }
    }

    func handleActivityUpdate(_ json: String) {
        guard let update = decodeUpdateEnvelope(json) else {
            return
        }
        if let payload = extractActivityPayload(from: update) {
            DispatchQueue.main.async {
                if let index = self.sessions.firstIndex(where: { $0.id == payload.id }) {
                    let updated = self.sessions[index].updatingActivity(
                        active: payload.active,
                        activeAt: payload.activeAt,
                        thinking: payload.thinking
                    )
                    self.sessions[index] = updated
                }
            }
            // Activity / keep-alive updates are the earliest reliable signal that a CLI
            // came online after the phone app started. Refresh sessions so the UI state
            // (remote/local/offline) stays SDK-owned and up-to-date.
            scheduleSessionsRefreshDebounced()
        }
    }

    /// scheduleSessionsRefreshDebounced refreshes sessions after activity updates.
    private func scheduleSessionsRefreshDebounced(
        minIntervalSeconds: TimeInterval = UpdateTiming.sessionRefreshMinIntervalSeconds,
        delaySeconds: TimeInterval = UpdateTiming.sessionRefreshDelaySeconds
    ) {
        DispatchQueue.main.async {
            self.scheduledSessionRefresh?.cancel()
            let work = DispatchWorkItem { [weak self] in
                guard let self else { return }
                let now = Date()
                if now.timeIntervalSince(self.lastSessionRefreshAt) < minIntervalSeconds {
                    return
                }
                self.lastSessionRefreshAt = now
                self.listSessions()
            }
            self.scheduledSessionRefresh = work
            DispatchQueue.main.asyncAfter(deadline: .now() + delaySeconds, execute: work)
        }
    }

    private func handlePermissionRequestUpdate(_ json: String) {
        guard let update = decodeUpdateEnvelope(json) else {
            return
        }
        guard let payload = extractPermissionRequestPayload(from: update) else {
            return
        }

        let request = PendingPermissionRequest(
            sessionID: payload.sessionID,
            requestID: payload.requestID,
            toolName: payload.toolName,
            input: payload.input,
            receivedAt: Int64(Date().timeIntervalSince1970 * UpdateTiming.millisecondsPerSecond)
        )

        DispatchQueue.main.async {
            if self.permissionQueue.contains(where: { $0.requestID == request.requestID }) {
                return
            }
            self.permissionQueue.append(request)

            // Use SDK-derived UI state when available. agentState can lag behind
            // during transitions, and we don't want to drop permission prompts.
            let controlledByDesktop: Bool = {
                guard let session = self.sessions.first(where: { $0.id == request.sessionID }) else {
                    return false
                }
                if let ui = session.uiState {
                    return ui.controlledByUser
                }
                return session.agentState?.controlledByUser ?? false
            }()

            if self.activePermissionRequest == nil {
                self.activePermissionRequest = request
                // Only auto-present the modal when the phone controls the session.
                self.showPermissionPrompt = !controlledByDesktop
            }
        }

        // Permission prompts are actionable; refresh session UI state promptly so
        // control transitions resolve quickly (e.g., after "Take Control").
        scheduleSessionsRefreshDebounced()
    }

    /// extractPermissionRequestPayload normalizes permission prompts from update envelopes.
    private func extractPermissionRequestPayload(from update: UpdateEnvelope) -> (sessionID: String, requestID: String, toolName: String, input: String)? {
        if update.type == UpdateKind.permissionRequest.rawValue || update.t == UpdateKind.permissionRequest.rawValue {
            let sessionID = update.id ?? update.sid
            let requestID = update.requestId
            let toolName = update.toolName
            let input = update.input
            if let sessionID, !sessionID.isEmpty,
               let requestID, !requestID.isEmpty,
               let toolName, !toolName.isEmpty,
               let input, !input.isEmpty {
                return (sessionID: sessionID, requestID: requestID, toolName: toolName, input: input)
            }
        }

        if let body = update.body,
           body.type == UpdateKind.permissionRequest.rawValue || body.t == UpdateKind.permissionRequest.rawValue {
            let sessionID = body.id ?? body.sid
            guard let sessionID, !sessionID.isEmpty,
                  let requestID = body.requestId, !requestID.isEmpty,
                  let toolName = body.toolName, !toolName.isEmpty,
                  let input = body.input, !input.isEmpty else {
                return nil
            }
            return (sessionID: sessionID, requestID: requestID, toolName: toolName, input: input)
        }
        return nil
    }

    /// extractActivityPayload normalizes activity updates from update envelopes.
    private func extractActivityPayload(from update: UpdateEnvelope) -> (id: String, active: Bool?, activeAt: Int64?, thinking: Bool?)? {
        // Root-level activity messages.
        if update.type == UpdateKind.activity.rawValue || update.t == UpdateKind.activity.rawValue {
            let id = update.id ?? update.sid
            guard let id, !id.isEmpty else { return nil }
            return (id: id, active: update.active, activeAt: update.activeAt, thinking: update.thinking)
        }
        if update.type == UpdateKind.sessionAlive.rawValue || update.t == UpdateKind.sessionAlive.rawValue {
            let id = update.id ?? update.sid
            guard let id, !id.isEmpty else { return nil }
            let activeAt = update.activeAt ?? update.time
            return (id: id, active: true, activeAt: activeAt, thinking: nil)
        }

        guard let body = update.body else { return nil }
        let bodyType = body.t ?? body.type
        guard let bodyType else { return nil }

        if bodyType == UpdateKind.activity.rawValue {
            let id = body.id ?? body.sid
            guard let id, !id.isEmpty else { return nil }
            return (id: id, active: body.active, activeAt: body.activeAt, thinking: body.thinking)
        }

        if bodyType == UpdateKind.sessionAlive.rawValue {
            let id = body.id ?? body.sid
            guard let id, !id.isEmpty else { return nil }
            // Session alive implies the session is active.
            let activeAt = body.activeAt ?? body.time
            return (id: id, active: true, activeAt: activeAt, thinking: nil)
        }

        return nil
    }

    private func log(_ message: String) {
        // Bridge logs to Go asynchronously to avoid re-entrant calls from callbacks.
        sdkCallAsync {
            self.client.logLine(message)
        }
        logSwiftOnly(message)
    }

    private func logSwiftOnly(_ message: String) {
        DispatchQueue.main.async {
            self.lastLogLine = message
            self.logLines.append(message)
            if self.logLines.count > LogLimits.maxLines {
                self.logLines = Array(self.logLines.suffix(LogLimits.maxLines))
            }
            self.logs = self.logLines.joined(separator: "\n")
        }
    }

    func clearLogs() {
        logs = ""
        lastLogLine = ""
        logLines = []
    }

    func startLogServer() {
        do {
            let urlBuf = try sdkCallSync {
                try client.startLogServerBuffer()
            }
            logServerURL = stringFromBuffer(urlBuf) ?? ""
            logServerRunning = !logServerURL.isEmpty
            if logServerRunning {
                log("Log server running at \(logServerURL)")
            }
        } catch {
            log("Start log server error: \(error)")
        }
    }

    func stopLogServer() {
        do {
            _ = try sdkCallSync {
                try client.stopLogServer()
            }
        } catch {
            log("Stop log server error: \(error)")
        }
        logServerURL = ""
        logServerRunning = false
        log("Log server stopped")
    }

    var crashLogPath: String {
        CrashLogger.logURL.path
    }

    private func configureLogDirectory() {
        guard let dir = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first else {
            log("Log directory unavailable")
            return
        }
        let logDir = dir.appendingPathComponent("delight-logs", isDirectory: true).path
        do {
            _ = try sdkCallSync {
                try client.setLogDirectory(logDir)
            }
        } catch {
            log("Set log directory error: \(error)")
        }
    }

    func crashLogTail(maxBytes: Int = 4000) -> String {
        guard let handle = try? FileHandle(forReadingFrom: CrashLogger.logURL) else {
            return "Crash log unavailable."
        }
        defer { try? handle.close() }
        if let data = try? handle.readToEnd(), !data.isEmpty {
            let slice = data.count > maxBytes ? data.suffix(maxBytes) : data
            return String(data: slice, encoding: .utf8) ?? "Crash log unreadable."
        }
        return "Crash log empty."
    }

    private func persistSettings() {
        let defaults = UserDefaults.standard
        defaults.set(serverURL, forKey: Self.settingsKeyPrefix + "serverURL")
        defaults.set(token, forKey: Self.settingsKeyPrefix + "token")
        defaults.set(appearanceMode.rawValue, forKey: Self.settingsKeyPrefix + "appearanceMode")
    }

    private func persistKeys() {
        if !masterKey.isEmpty {
            KeychainStore.set(masterKey, for: "masterKey")
        }
        if !publicKey.isEmpty {
            KeychainStore.set(publicKey, for: "publicKey")
        }
        if !privateKey.isEmpty {
            KeychainStore.set(privateKey, for: "privateKey")
        }
    }

    private func ensureKeys() {
        if masterKey.isEmpty || publicKey.isEmpty || privateKey.isEmpty {
            generateKeys()
        }
    }

    func parseSessions(_ json: String) {
        struct SessionsResponse: Decodable {
            struct Session: Decodable {
                let id: String
                let updatedAt: Int64
                let active: Bool
                let activeAt: Int64?
                let metadata: String?
                let agentState: String?
                let machineId: String?
            }
            let sessions: [Session]
        }
        struct SessionsUIResponse: Decodable {
            struct Session: Decodable {
                let id: String
                let ui: SessionUIState?
            }
            let sessions: [Session]
        }

        guard let decoded = try? JSONCoding.decode(SessionsResponse.self, from: json) else {
            return
        }
        // Decode UI state from the SDK-enriched JSON response.
        let uiByID: [String: SessionUIState] = {
            guard let uiDecoded = try? JSONCoding.decode(SessionsUIResponse.self, from: json) else { return [:] }
            var out: [String: SessionUIState] = [:]
            out.reserveCapacity(uiDecoded.sessions.count)
            for raw in uiDecoded.sessions {
                guard let uiState = raw.ui else { continue }
                out[raw.id] = uiState
            }
            return out
        }()

        DispatchQueue.main.async { [self] in
            let parsedSessions: [SessionSummary] = decoded.sessions.map { session in
                let metadata = SessionMetadata.fromJSON(session.metadata)
                let agentState = SessionAgentState.fromJSON(session.agentState)
                let uiState = uiByID[session.id]
                let title = metadata?.agent
                    ?? metadata?.summaryText
                    ?? session.machineId
                return SessionSummary(
                    id: session.id,
                    updatedAt: session.updatedAt,
                    active: session.active,
                    activeAt: session.activeAt,
                    title: title,
                    subtitle: metadata?.host
                        ?? session.machineId,
                    metadata: metadata,
                    agentState: agentState,
                    uiState: uiState,
                    thinking: false
                )
            }
            self.sessions = parsedSessions

            // Hydrate pending permission prompts from durable agent state.
            let now = Int64(Date().timeIntervalSince1970 * 1000)
            for session in parsedSessions {
                // Keep the permission queue in sync with durable agent state, but
                // only auto-present prompts while the phone controls the session.
                //
                // This avoids losing prompts during UI-state lag or transitions.
                let durable = session.agentState?.requests ?? [:]
                if durable.isEmpty {
                    self.permissionQueue.removeAll(where: { $0.sessionID == session.id })
                    continue
                }
                for (requestID, req) in session.agentState?.requests ?? [:] {
                    if self.permissionQueue.contains(where: { $0.requestID == requestID }) {
                        continue
                    }
                    self.permissionQueue.append(
                        PendingPermissionRequest(
                            sessionID: session.id,
                            requestID: requestID,
                            toolName: req.toolName,
                            input: req.input,
                            receivedAt: req.createdAt ?? now
                        )
                    )
                }
            }

            if self.activePermissionRequest == nil, let next = self.permissionQueue.first {
                self.activePermissionRequest = next
            }
            if let active = self.activePermissionRequest {
                let owningSession = self.sessions.first(where: { $0.id == active.sessionID })
                let controlledByDesktop = owningSession?.uiState?.controlledByUser
                    ?? owningSession?.agentState?.controlledByUser
                    ?? false
                self.showPermissionPrompt = !controlledByDesktop
            } else {
                self.showPermissionPrompt = false
            }
            if let current = self.sessions.first(where: { $0.id == self.sessionID }) {
                self.selectedMetadata = current.metadata
            }
        }
    }

    func parseMachines(_ json: String) {
        struct MachinePayload: Decodable {
            let id: String?
            let metadata: String?
            let daemonState: String?
            let daemonStateVersion: Int64?
            let active: Bool?
            let activeAt: Int64?
        }
        struct MachinesResponse: Decodable {
            let machines: [MachinePayload]
        }

        let payloads: [MachinePayload]
        if let decoded = try? JSONCoding.decode([MachinePayload].self, from: json) {
            payloads = decoded
        } else if let decoded = try? JSONCoding.decode(MachinesResponse.self, from: json) {
            payloads = decoded.machines
        } else {
            log("Parse machines error: invalid JSON payload")
            return
        }

        let machines: [MachineInfo] = payloads.map { item in
            let id = item.id ?? UUID().uuidString
            let metadata = MachineMetadata.fromJSON(item.metadata)
            let daemonState = DaemonState.fromJSON(item.daemonState)
            let daemonStateVersion = item.daemonStateVersion ?? 0
            let active = item.active ?? false
            let activeAt = item.activeAt
            return MachineInfo(
                id: id,
                metadata: metadata,
                daemonState: daemonState,
                daemonStateVersion: daemonStateVersion,
                active: active,
                activeAt: activeAt
            )
        }
        DispatchQueue.main.async {
            self.machines = machines
        }
    }

    private func sessionDisplayTitle(agent: String?, path: String?, homeDir: String?, fallback: String?) -> String? {
        let trimmedAgent = agent?.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedPath = path?.trimmingCharacters(in: .whitespacesAndNewlines)
        let displayPath = trimmedPath.map { formatPath($0, homeDir: homeDir) }
        if let trimmedAgent, !trimmedAgent.isEmpty, let trimmedPath, !trimmedPath.isEmpty {
            return "\(trimmedAgent) • \(displayPath ?? trimmedPath)"
        }
        if let trimmedAgent, !trimmedAgent.isEmpty {
            return trimmedAgent
        }
        if let trimmedPath, !trimmedPath.isEmpty {
            return displayPath ?? trimmedPath
        }
        return fallback
    }

    private func formatPath(_ path: String, homeDir: String?) -> String {
        guard let homeDir, !homeDir.isEmpty else {
            return path
        }
        let normalizedHome = homeDir.hasSuffix("/") ? String(homeDir.dropLast()) : homeDir
        if path == normalizedHome {
            return "~"
        }
        if path.hasPrefix(normalizedHome + "/") {
            let suffix = path.dropFirst(normalizedHome.count)
            return "~" + suffix
        }
        return path
    }

    func parseMessages(_ json: String) {
        applyMessagesResponse(json, reset: true, scrollToBottom: false)
    }

    private struct MessagesPage {
        let messages: [MessageItem]
        let hasMore: Bool
        let nextBeforeSeq: Int64?
    }

    private func applyMessagesResponse(
        _ json: String,
        reset: Bool,
        scrollToBottom: Bool,
        anchorID: String? = nil
    ) {
        let page = decodeMessagesPage(json)
        DispatchQueue.main.async {
            if reset {
                self.messages = page.messages
            } else {
                self.messages = self.mergeMessages(existing: self.messages, incoming: page.messages)
            }

            self.oldestLoadedSeq = self.messages.compactMap(\.seq).min()
            self.hasMoreHistory = page.hasMore

            if scrollToBottom, !self.messages.isEmpty {
                self.scrollRequest = ScrollRequest(target: .bottom)
            } else if let anchorID {
                self.scrollRequest = ScrollRequest(target: .message(id: anchorID, anchor: .top))
            }
        }
    }

    private func mergeMessages(existing: [MessageItem], incoming: [MessageItem]) -> [MessageItem] {
        if existing.isEmpty { return incoming }
        if incoming.isEmpty { return existing }

        var resultByID: [String: MessageItem] = [:]
        for message in existing {
            resultByID[message.id] = message
        }
        for message in incoming {
            resultByID[message.id] = message
        }

        // Reconcile optimistic messages (local user bubbles) once we see a server echo.
        let serverLocalIDs = Set(
            incoming
                .compactMap(\.localID)
                .filter { !$0.isEmpty }
        )
        // Signature-only dedupe is too aggressive when users send the same text twice
        // (e.g. "ls -l" again). Use (signature, time window) instead so we don't
        // hide the new optimistic bubble until the server echo arrives.
        var serverUserCreatedAtsBySignature: [String: [Int64]] = [:]
        for item in incoming {
            guard item.role == .user, !item.id.hasPrefix("local-") else { continue }
            guard let createdAt = item.createdAt else { continue }
            let sig = blocksSignature(item.blocks)
            serverUserCreatedAtsBySignature[sig, default: []].append(createdAt)
        }
        let merged = resultByID.values.filter { item in
            if item.id.hasPrefix("local-"), item.role == .user {
                if let localID = item.localID, !localID.isEmpty, serverLocalIDs.contains(localID) {
                    return false
                }
                guard let optimisticAt = item.createdAt else { return true }
                let sig = blocksSignature(item.blocks)
                if let ats = serverUserCreatedAtsBySignature[sig] {
                    // If the server already has a matching user message within a short
                    // window, the optimistic bubble is redundant and can be removed.
                    for at in ats where isNearInTime(optimisticAt, at) {
                        return false
                    }
                }
                return true
            }
            return true
        }

        return squashDuplicateUserMessages(merged.sorted(by: messageSortKey))
    }

    private func squashDuplicateUserMessages(_ sorted: [MessageItem]) -> [MessageItem] {
        // Prefer uuid-bearing user messages over non-uuid duplicates when they have the
        // same content and occur within a short window (typically mobile-send + CLI echo).
        if sorted.count < 2 { return sorted }

        var result: [MessageItem] = []
        result.reserveCapacity(sorted.count)

        for item in sorted {
            if item.role == .user,
               let last = result.last,
               last.role == .user,
               blocksSignature(last.blocks) == blocksSignature(item.blocks),
               isNearInTime(last.createdAt, item.createdAt) {
                let keep = preferUUIDMessage(lhs: last, rhs: item)
                result[result.count - 1] = keep
                continue
            }
            result.append(item)
        }
        return result
    }

    private func isNearInTime(_ a: Int64?, _ b: Int64?, windowMs: Int64 = 5_000) -> Bool {
        guard let a, let b else { return false }
        let delta = a > b ? (a - b) : (b - a)
        return delta <= windowMs
    }

    private func preferUUIDMessage(lhs: MessageItem, rhs: MessageItem) -> MessageItem {
        // If exactly one has a uuid, keep that one.
        let lhsHasUUID = (lhs.uuid ?? "").isEmpty == false
        let rhsHasUUID = (rhs.uuid ?? "").isEmpty == false
        if lhsHasUUID != rhsHasUUID {
            return rhsHasUUID ? rhs : lhs
        }
        // Otherwise keep the later one (higher seq if present, else later createdAt).
        if let lhsSeq = lhs.seq, let rhsSeq = rhs.seq, lhsSeq != rhsSeq {
            return rhsSeq > lhsSeq ? rhs : lhs
        }
        if let lhsAt = lhs.createdAt, let rhsAt = rhs.createdAt, lhsAt != rhsAt {
            return rhsAt > lhsAt ? rhs : lhs
        }
        return rhs
    }

    private func squashUserEchoes(prefer incoming: MessageItem) -> Bool {
        guard incoming.role == .user else { return false }
        let incomingSig = blocksSignature(incoming.blocks)

        // If the incoming message has a uuid, replace any near-duplicate without a uuid.
        if let uuid = incoming.uuid, !uuid.isEmpty {
            if let idx = messages.lastIndex(where: { existing in
                guard existing.role == .user else { return false }
                if blocksSignature(existing.blocks) != incomingSig { return false }
                if !isNearInTime(existing.createdAt, incoming.createdAt) { return false }
                return (existing.uuid ?? "").isEmpty
            }) {
                messages[idx] = incoming
                return true
            }
            return false
        }

        // If the incoming message has no uuid, and we already have the uuid-bearing
        // near-duplicate, drop the incoming one.
        if messages.contains(where: { existing in
            guard existing.role == .user else { return false }
            if blocksSignature(existing.blocks) != incomingSig { return false }
            if !isNearInTime(existing.createdAt, incoming.createdAt) { return false }
            return (existing.uuid ?? "").isEmpty == false
        }) {
            return true
        }

        return false
    }

    private func blocksSignature(_ blocks: [MessageBlock]) -> String {
        // Stable, normalized representation used for deduping optimistic user messages
        // against server echoes (both for realtime updates and page fetches).
        let pieces: [String] = blocks.map { block in
            switch block {
            case .text(let text):
                return "text:\(normalizeText(text))"
            case .code(let language, let content):
                let lang = (language ?? "").lowercased()
                return "code(\(lang)):\(normalizeText(content))"
            case .toolCall(let summary):
                return "tool:\(normalizeText(summary.title))"
            }
        }
        return pieces.joined(separator: "\n")
    }

    private func normalizeText(_ value: String) -> String {
        value
            .replacingOccurrences(of: "\r\n", with: "\n")
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func messageSortKey(_ lhs: MessageItem, _ rhs: MessageItem) -> Bool {
        let lhsSeq = lhs.seq ?? Int64.max
        let rhsSeq = rhs.seq ?? Int64.max
        if lhsSeq != rhsSeq { return lhsSeq < rhsSeq }

        let lhsTime = lhs.createdAt ?? 0
        let rhsTime = rhs.createdAt ?? 0
        if lhsTime != rhsTime { return lhsTime < rhsTime }

        return lhs.id < rhs.id
    }

    private func shouldAutoScrollToBottom(afterAppending newItem: MessageItem) -> Bool {
        // Keep the transcript pinned to the bottom whenever a conversational message
        // arrives (user or assistant). History pagination is handled separately and
        // explicitly opts out of scrolling to bottom.
        return newItem.role == .user || newItem.role == .assistant
    }

    private func decodeMessagesPage(_ json: String) -> MessagesPage {
        guard let parsed = decodeJSONValue(json) else {
            log("Parse messages error: invalid JSON payload")
            return MessagesPage(messages: [], hasMore: false, nextBeforeSeq: nil)
        }

        let itemsArray: [JSONValue]
        var hasMore: Bool = false
        var nextBeforeSeq: Int64?

        switch parsed {
        case .array(let array):
            itemsArray = array
        case .object(let dict):
            if let messages = dict[MessageFields.messages]?.array {
                itemsArray = messages
                if let page = dict[MessageFields.page]?.object {
                    hasMore = page[MessageFields.hasMore]?.bool ?? false
                    nextBeforeSeq = page[MessageFields.nextBeforeSeq]?.int64
                        ?? (page[MessageFields.nextBeforeSeq]?.number).map { Int64($0) }
                }
            } else if let dataDict = dict[UpdateFields.data]?.object,
                      let messages = dataDict[MessageFields.messages]?.array {
                itemsArray = messages
            } else if let items = dict[MessageFields.items]?.array {
                itemsArray = items
            } else if let dataItems = dict[UpdateFields.data]?.array {
                itemsArray = dataItems
            } else {
                itemsArray = []
            }
        default:
            itemsArray = []
        }

        var sawThinkingOnly = false
        var messages: [MessageItem] = []
        var seenKeys = Set<String>()
        var richFallbackKeys = Set<String>()

        for item in itemsArray {
            guard let dict = item.object else { continue }
            let content = normalizeContent(firstNonNull(dict[UpdateFields.content], dict[UpdateFields.message], dict[UpdateFields.data]))
            if isNullMessage(content) || isFileHistorySnapshot(content) || isToolResultMessage(content) {
                continue
            }
            let localID = extractLocalID(from: dict)
            let uuid = extractMessageUUID(from: content)
            if localID != nil || uuid != nil {
                let role = extractRole(from: dict, content: content)
                let text = extractText(from: content)
                if let key = fallbackDedupeKey(role: role, createdAt: jsonInt64(dict, UpdateFields.createdAt), text: text) {
                    richFallbackKeys.insert(key)
                }
            }
        }

        for item in itemsArray {
            guard let dict = item.object else { continue }
            let id = jsonString(dict, MessageFields.id) ?? UUID().uuidString
            let createdAt = jsonInt64(dict, UpdateFields.createdAt)
            let seq = jsonInt64(dict, UpdateFields.seq)
            let content = normalizeContent(firstNonNull(dict[UpdateFields.content], dict[UpdateFields.message], dict[UpdateFields.data]))
            if isNullMessage(content) || isFileHistorySnapshot(content) {
                continue
            }
            if isToolResultMessage(content) {
                continue
            }
            let role = extractRole(from: dict, content: content)
            let hasThinking = containsThinkingBlock(content)
            let localID = extractLocalID(from: dict)
            let uuid = extractMessageUUID(from: content)
            if localID == nil && uuid == nil {
                let text = extractText(from: content)
                if let key = fallbackDedupeKey(role: role, createdAt: createdAt, text: text),
                   richFallbackKeys.contains(key) {
                    continue
                }
            }
            var blocks = extractBlocks(from: content, sessionID: self.sessionID)
            if blocks.isEmpty, let fallback = extractText(from: content) {
                blocks = [.text(fallback)]
            }
            if blocks.isEmpty {
                if hasThinking {
                    sawThinkingOnly = true
                    continue
                }
                self.logUnsupportedMessage(id: id, content: content)
                continue
            }
            let primaryKey = messagePrimaryKey(
                id: id,
                localID: localID,
                uuid: uuid,
                role: role,
                createdAt: createdAt,
                blocks: blocks
            )
            if let primaryKey, seenKeys.contains(primaryKey) {
                continue
            }
            if let primaryKey {
                seenKeys.insert(primaryKey)
            }
            messages.append(MessageItem(id: id, seq: seq, localID: localID, uuid: uuid, role: role, blocks: blocks, createdAt: createdAt))
        }

        if sawThinkingOnly && messages.isEmpty {
            log("Only thinking blocks found (no renderable messages).")
        }

        // Best-effort inference for servers/clients that don't include `page` metadata.
        if nextBeforeSeq == nil {
            nextBeforeSeq = messages.compactMap(\.seq).min()
        }

        return MessagesPage(messages: messages, hasMore: hasMore, nextBeforeSeq: nextBeforeSeq)
    }

    private func normalizeContent(_ content: JSONValue?) -> JSONValue? {
        guard let content else { return nil }
        if let text = content.string, let nested = parseJSONString(text) {
            return nested
        }
        return content
    }

    private func extractUpdateSessionID(from json: String) -> String? {
        guard let update = decodeUpdateEnvelope(json) else { return nil }
        if let sid = update.body?.sid, !sid.isEmpty { return sid }
        if let sid = update.sid, !sid.isEmpty { return sid }
        if let messageValue = update.body?.message, let message = messageValue.object,
           let sessionID = message[UpdateFields.sessionID]?.string, !sessionID.isEmpty {
            return sessionID
        }
        if let messageValue = update.message, let message = messageValue.object,
           let sessionID = message[UpdateFields.sessionID]?.string, !sessionID.isEmpty {
            return sessionID
        }
        return nil
    }

    private func updateThinkingFromUpdate(_ json: String) {
        updateThinkingFromUpdate(json, targetSessionID: sessionID)
    }

    private func updateThinkingFromUpdate(_ json: String, targetSessionID: String?) {
        guard let targetSessionID, !targetSessionID.isEmpty else {
            return
        }
        guard let update = decodeUpdateEnvelope(json),
              let messageValue = update.body?.message,
              let message = messageValue.object else {
            return
        }
        let content = normalizeContent(firstNonNull(message[UpdateFields.content], message[UpdateFields.data]))
        let hasThinking = containsThinkingBlock(content)
        let blocks = extractBlocks(from: content, sessionID: targetSessionID)
        if hasThinking && blocks.isEmpty {
            updateSessionThinking(true, sessionID: targetSessionID)
        } else if !blocks.isEmpty {
            updateSessionThinking(false, sessionID: targetSessionID)
        }
    }

    private func extractBlocks(from content: JSONValue?, sessionID: String?) -> [MessageBlock] {
        guard let content else { return [] }
        if let dict = content.object {
            if let type = dict[UpdateFields.typeShort]?.string, let payload = dict[UpdateFields.payload] {
                if type == MessageValue.BlockType.text, let text = payload.string {
                    return [.text(text)]
                }
                if MessageValue.BlockType.encryptedTypes.contains(type) {
                    return [.text("Encrypted message")]
                }
                return extractBlocks(from: payload, sessionID: sessionID)
            }
            if let text = dict[UpdateFields.text]?.string {
                return splitMarkdownBlocks(text)
            }
            if let contentText = dict[UpdateFields.content]?.string {
                return splitMarkdownBlocks(contentText)
            }
            if let inner = dict[UpdateFields.content] {
                return extractBlocks(from: inner, sessionID: sessionID)
            }
            if let message = dict[UpdateFields.message] {
                return extractBlocks(from: message, sessionID: sessionID)
            }
            if let data = dict[UpdateFields.data] {
                return extractBlocks(from: data, sessionID: sessionID)
            }
        } else if let array = content.array {
            var blocks: [MessageBlock] = []
            for part in array {
                guard let dict = part.object else { continue }
                if let type = dict[UpdateFields.type]?.string {
                    if type == MessageValue.BlockType.text, let text = dict[UpdateFields.text]?.string {
                        blocks.append(contentsOf: splitMarkdownBlocks(text))
                        continue
                    }
                    if type == MessageValue.BlockType.thinking {
                        continue
                    }
                    if MessageValue.BlockType.toolCallTypes.contains(type) {
                        let name = dict[MessageFields.name]?.string ?? "tool"
                        if let summary = toolSummary(name: name, input: dict[MessageFields.input]) {
                            blocks.append(.toolCall(summary))
                        }
                        continue
                    }
                    if MessageValue.BlockType.toolResultTypes.contains(type) {
                        continue
                    }
                }
                if let name = dict[MessageFields.name]?.string, let input = dict[MessageFields.input] {
                    if let summary = toolSummary(name: name, input: input) {
                        blocks.append(.toolCall(summary))
                    }
                    continue
                }
                if let text = dict[UpdateFields.text]?.string {
                    blocks.append(contentsOf: splitMarkdownBlocks(text))
                    continue
                }
                if let contentText = dict[UpdateFields.content]?.string {
                    blocks.append(contentsOf: splitMarkdownBlocks(contentText))
                    continue
                }
            }
            return blocks
        } else if let text = content.string {
            if let nested = parseJSONString(text) {
                return extractBlocks(from: nested, sessionID: sessionID)
            }
            if isProbablyBase64(text) {
                return [.text("Encrypted message")]
            }
            return splitMarkdownBlocks(text)
        }
        return []
    }

    private func isNullMessage(_ content: JSONValue?) -> Bool {
        guard let content else { return true }
        if case .null = content {
            return true
        }
        if let dict = content.object, case .null = dict[UpdateFields.message] {
            return true
        }
        return false
    }

    private func isFileHistorySnapshot(_ content: JSONValue?) -> Bool {
        guard let content else { return false }
        if let dict = content.object {
            if dict[UpdateFields.type]?.string == MessageValue.BlockType.fileHistorySnapshot {
                return true
            }
            if let message = dict[UpdateFields.message]?.object,
               message[UpdateFields.type]?.string == MessageValue.BlockType.fileHistorySnapshot {
                return true
            }
            if let inner = dict[UpdateFields.content], isFileHistorySnapshot(inner) {
                return true
            }
        }
        return false
    }

    private func isToolResultMessage(_ content: JSONValue?) -> Bool {
        guard let content else { return false }
        if let dict = content.object {
            if let type = dict[UpdateFields.type]?.string,
               MessageValue.BlockType.toolResultTypes.contains(type) {
                return true
            }
            if let message = dict[UpdateFields.message] {
                return isToolResultMessage(message)
            }
            if let inner = dict[UpdateFields.content] {
                return isToolResultMessage(inner)
            }
            if let data = dict[UpdateFields.data] {
                return isToolResultMessage(data)
            }
        }
        if let array = content.array {
            for part in array {
                if let dict = part.object,
                   let type = dict[UpdateFields.type]?.string,
                   MessageValue.BlockType.toolResultTypes.contains(type) {
                    return true
                }
            }
        }
        return false
    }

    private func extractLocalID(from dict: [String: JSONValue]) -> String? {
        guard let localID = dict[UpdateFields.localID]?.string, !localID.isEmpty else {
            return nil
        }
        return localID
    }

    private func extractMessageUUID(from content: JSONValue?) -> String? {
        guard let content else { return nil }
        if let dict = content.object {
            if let uuid = dict[UpdateFields.uuid]?.string, !uuid.isEmpty {
                return uuid
            }
            if let message = dict[UpdateFields.message]?.object,
               let uuid = message[UpdateFields.uuid]?.string, !uuid.isEmpty {
                return uuid
            }
            if let inner = dict[UpdateFields.content], let uuid = extractMessageUUID(from: inner) {
                return uuid
            }
            if let message = dict[UpdateFields.message], let uuid = extractMessageUUID(from: message) {
                return uuid
            }
            if let data = dict[UpdateFields.data], let uuid = extractMessageUUID(from: data) {
                return uuid
            }
        }
        return nil
    }

    private func messagePrimaryKey(
        id: String,
        localID: String?,
        uuid: String?,
        role: MessageRole,
        createdAt: Int64?,
        blocks: [MessageBlock]
    ) -> String? {
        if let localID {
            return "local:\(localID)"
        }
        if let uuid {
            return "uuid:\(uuid)"
        }
        let text = blocks.compactMap { block -> String? in
            if case let .text(value) = block {
                return value
            }
            return nil
        }.joined(separator: "\n")
        return fallbackDedupeKey(role: role, createdAt: createdAt, text: text.isEmpty ? nil : text)
    }

    private func fallbackDedupeKey(role: MessageRole, createdAt: Int64?, text: String?) -> String? {
        guard let createdAt, let text, !text.isEmpty else { return nil }
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty { return nil }
        let sample = String(trimmed.prefix(200))
        return "fallback:\(role.rawValue):\(createdAt):\(sample)"
    }

    private func containsThinkingBlock(_ content: JSONValue?) -> Bool {
        guard let content else { return false }
        if let dict = content.object {
            if dict[UpdateFields.type]?.string == MessageValue.BlockType.thinking {
                return true
            }
            if let inner = dict[UpdateFields.content], containsThinkingBlock(inner) {
                return true
            }
            if let message = dict[UpdateFields.message], containsThinkingBlock(message) {
                return true
            }
            if let data = dict[UpdateFields.data], containsThinkingBlock(data) {
                return true
            }
        }
        if let array = content.array {
            for part in array where containsThinkingBlock(part) {
                return true
            }
        }
        return false
    }

    private func updateSessionThinking(_ thinking: Bool) {
        updateSessionThinking(thinking, sessionID: sessionID)
    }

    private func updateSessionThinking(_ thinking: Bool, sessionID: String?) {
        guard let targetID = sessionID, !targetID.isEmpty else { return }
        DispatchQueue.main.async {
            if let index = self.sessions.firstIndex(where: { $0.id == targetID }) {
                let updated = self.sessions[index].updatingActivity(
                    active: nil,
                    activeAt: nil,
                    thinking: thinking
                )
                self.sessions[index] = updated
            }
        }
    }

    private func extractText(from content: JSONValue?) -> String? {
        guard let content else { return nil }
        if let dict = content.object {
            if let type = dict[UpdateFields.typeShort]?.string, let payload = dict[UpdateFields.payload] {
                if type == MessageValue.BlockType.text, let text = payload.string {
                    return text
                }
                if MessageValue.BlockType.encryptedTypes.contains(type) {
                    return "Encrypted message"
                }
                return extractText(from: payload)
            }
            if let text = dict[UpdateFields.text]?.string {
                return text
            }
            if let inner = dict[UpdateFields.content] {
                return extractText(from: inner)
            }
            if let message = dict[UpdateFields.message] {
                return extractText(from: message)
            }
            if let data = dict[UpdateFields.data] {
                return extractText(from: data)
            }
        } else if let array = content.array {
            let texts = array.compactMap { part -> String? in
                guard let dict = part.object else { return nil }
                if dict[UpdateFields.type]?.string == MessageValue.BlockType.text {
                    return dict[UpdateFields.text]?.string
                }
                return dict[UpdateFields.text]?.string
            }
            if !texts.isEmpty {
                return texts.joined(separator: "\n")
            }
        } else if let text = content.string {
            if let nested = parseJSONString(text) {
                return extractText(from: nested)
            }
            if isProbablyBase64(text) {
                return "Encrypted message"
            }
            return text
        }
        return nil
    }

    private func parseJSONString(_ text: String) -> JSONValue? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.first == "{" || trimmed.first == "[" else {
            return nil
        }
        return try? JSONCoding.decode(JSONValue.self, from: trimmed)
    }

    private func isProbablyBase64(_ value: String) -> Bool {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.count < 24 {
            return false
        }
        if trimmed.contains(" ") || trimmed.contains("\n") {
            return false
        }
        let base64Charset = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=-_")
        let validCount = trimmed.unicodeScalars.filter { base64Charset.contains($0) }.count
        let ratio = Double(validCount) / Double(trimmed.unicodeScalars.count)
        if trimmed.count > 800 {
            return ratio > 0.95
        }
        return ratio > 0.98
    }

    private func logUnsupportedMessage(id: String, content: JSONValue?) {
        let snippet = describeContent(content)
        log("Unsupported message format id=\(id) content=\(snippet)")
    }

    private func describeContent(_ content: JSONValue?) -> String {
        if let content {
            if let json = try? JSONCoding.encode(content) {
                return truncate(json)
            }
        }
        return "nil"
    }

    private func truncate(_ text: String, limit: Int = 280) -> String {
        if text.count <= limit {
            return text
        }
        let index = text.index(text.startIndex, offsetBy: limit)
        return String(text[..<index]) + "…"
    }

    private func splitMarkdownBlocks(_ text: String) -> [MessageBlock] {
        var blocks: [MessageBlock] = []
        var remaining = text[...]
        while let fenceRange = remaining.range(of: "```") {
            let before = String(remaining[..<fenceRange.lowerBound])
            if !before.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                blocks.append(.text(before))
            }
            remaining = remaining[fenceRange.upperBound...]
            let lineEnd = remaining.firstIndex(of: "\n") ?? remaining.endIndex
            let language = String(remaining[..<lineEnd]).trimmingCharacters(in: .whitespacesAndNewlines)
            remaining = remaining[lineEnd...]
            if remaining.first == "\n" {
                remaining = remaining.dropFirst()
            }
            guard let endFence = remaining.range(of: "```") else {
                let leftover = "```" + remaining
                blocks.append(.text(String(leftover)))
                return blocks
            }
            let code = String(remaining[..<endFence.lowerBound]).trimmingCharacters(in: .newlines)
            blocks.append(.code(language: language.isEmpty ? nil : language, content: code))
            remaining = remaining[endFence.upperBound...]
        }
        let tail = String(remaining)
        if !tail.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            blocks.append(.text(tail))
        }
        return blocks
    }

    private func toolSummary(name: String, input: JSONValue?) -> ToolCallSummary? {
        let normalized = name.lowercased()
        let icon: String
        switch normalized {
        case "grep", "read":
            icon = "eye"
        case "bash", "codexbash":
            icon = "terminal"
        case "glob", "ls":
            icon = "magnifyingglass"
        case "edit", "multiedit", "write":
            icon = "doc.text"
        default:
            icon = "wrench.and.screwdriver"
        }

        if normalized == "grep",
           let dict = input?.object,
           let pattern = dict[MessageFields.pattern]?.string {
            return ToolCallSummary(title: "grep(pattern: \(pattern))", icon: icon, subtitle: nil)
        }
        if normalized == "read",
           let dict = input?.object,
           let filePath = dict[MessageFields.filePath]?.string {
            return ToolCallSummary(title: displayPath(filePath), icon: icon, subtitle: nil)
        }
        if normalized == "glob",
           let dict = input?.object,
           let pattern = dict[MessageFields.pattern]?.string {
            return ToolCallSummary(title: pattern, icon: icon, subtitle: nil)
        }
        if normalized == "ls",
           let dict = input?.object,
           let path = dict[MessageFields.path]?.string {
            return ToolCallSummary(title: displayPath(path), icon: icon, subtitle: nil)
        }
        if normalized == "bash",
           let dict = input?.object,
           let command = dict[MessageFields.command]?.string {
            return ToolCallSummary(title: command, icon: icon, subtitle: nil)
        }
        return ToolCallSummary(title: name, icon: icon, subtitle: nil)
    }

    private func displayPath(_ path: String) -> String {
        if let homeDir = selectedMetadata?.homeDir, path.hasPrefix(homeDir) {
            let trimmed = path.dropFirst(homeDir.count)
            if trimmed.hasPrefix("/") {
                return "~\(trimmed)"
            }
            return "~/" + trimmed
        }
        if let range = path.range(of: "/src/") {
            return "src/" + path[range.upperBound...]
        }
        return path
    }

    private func extractRole(from message: [String: JSONValue], content: JSONValue?) -> MessageRole {
        if let role = message[UpdateFields.role]?.string {
            return normalizeRole(role)
        }
        if let dict = content?.object {
            if let role = dict[UpdateFields.role]?.string {
                return normalizeRole(role)
            }
            if let inner = dict[UpdateFields.content]?.object,
               let role = inner[UpdateFields.role]?.string {
                return normalizeRole(role)
            }
            if let message = dict[UpdateFields.message]?.object,
               let role = message[UpdateFields.role]?.string {
                return normalizeRole(role)
            }
            if let message = dict[UpdateFields.message]?.object,
               let inner = message[UpdateFields.content]?.object,
               let role = inner[UpdateFields.role]?.string {
                return normalizeRole(role)
            }
            if let data = dict[UpdateFields.data]?.object,
               let message = data[UpdateFields.message]?.object,
               let role = message[UpdateFields.role]?.string {
                return normalizeRole(role)
            }
        }
        return .unknown
    }

    private func normalizeRole(_ role: String) -> MessageRole {
        switch role.lowercased() {
        case MessageValue.Role.user:
            return .user
        case MessageValue.Role.assistant, MessageValue.Role.agent:
            return .assistant
        case MessageValue.Role.system:
            return .system
        case MessageValue.Role.tool:
            return .tool
        case MessageValue.Role.event:
            return .event
        default:
            return .unknown
        }
    }
}
