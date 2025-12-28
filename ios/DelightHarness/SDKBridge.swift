import Foundation
import DelightSDK

struct SessionMetadata {
    let path: String?
    let host: String?
    let homeDir: String?
    let summaryText: String?
    let agent: String?
    let flavor: String?
    let daemonPid: Int?
    let daemonStateVersion: Int?
    let machineId: String?

    static func fromJSON(_ json: String?) -> SessionMetadata? {
        guard let json, let data = json.data(using: .utf8) else {
            return nil
        }
        let decoded: [String: Any]?
        if let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
            decoded = object
        } else if let decodedData = Data(base64Encoded: json),
                  let decodedObject = try? JSONSerialization.jsonObject(with: decodedData) as? [String: Any] {
            decoded = decodedObject
        } else {
            decoded = nil
        }
        guard let decoded else { return nil }
        let summary = decoded["summary"] as? [String: Any]
        let summaryAgent = summary?["agent"] as? String ?? summary?["name"] as? String
        let host = decoded["host"] as? String
            ?? decoded["hostname"] as? String
            ?? decoded["hostName"] as? String
            ?? decoded["machineName"] as? String
        let path = decoded["path"] as? String
            ?? decoded["cwd"] as? String
            ?? decoded["workDir"] as? String
            ?? decoded["dir"] as? String
        let daemon = decoded["daemon"] as? [String: Any]
        let daemonPid = intValue(from: decoded["daemonPid"])
            ?? intValue(from: daemon?["pid"])
            ?? intValue(from: decoded["pid"])
        let daemonStateVersion = intValue(from: decoded["daemonStateVersion"])
            ?? intValue(from: daemon?["stateVersion"])
            ?? intValue(from: daemon?["version"])
        let flavor = decoded["flavor"] as? String
            ?? decoded["os"] as? String
            ?? decoded["platform"] as? String
        return SessionMetadata(
            path: path,
            host: host,
            homeDir: decoded["homeDir"] as? String,
            summaryText: summary?["text"] as? String,
            agent: decoded["agent"] as? String ?? summaryAgent ?? flavor,
            flavor: flavor,
            daemonPid: daemonPid,
            daemonStateVersion: daemonStateVersion,
            machineId: decoded["machineId"] as? String
        )
    }

    fileprivate static func intValue(from value: Any?) -> Int? {
        if let number = value as? NSNumber {
            return number.intValue
        }
        if let string = value as? String, let number = Int(string) {
            return number
        }
        return nil
    }
}

struct SessionAgentState {
    let controlledByUser: Bool
    let hasPendingRequests: Bool

    static func fromJSON(_ json: String?) -> SessionAgentState? {
        guard let json, let data = json.data(using: .utf8) else {
            return nil
        }
        guard let decoded = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        let controlledByUser = decoded["controlledByUser"] as? Bool ?? false
        let requests = decoded["requests"] as? [String: Any]
        let hasPendingRequests = !(requests?.isEmpty ?? true)
        return SessionAgentState(controlledByUser: controlledByUser, hasPendingRequests: hasPendingRequests)
    }
}

struct SessionSummary: Identifiable {
    let id: String
    let updatedAt: Int64
    let active: Bool
    let activeAt: Int64?
    let title: String?
    let subtitle: String?
    let metadata: SessionMetadata?
    let agentState: SessionAgentState?
    let thinking: Bool

    func updatingActivity(active: Bool?, activeAt: Int64?, thinking: Bool?) -> SessionSummary {
        SessionSummary(
            id: id,
            updatedAt: updatedAt,
            active: active ?? self.active,
            activeAt: activeAt ?? self.activeAt,
            title: title,
            subtitle: subtitle,
            metadata: metadata,
            agentState: agentState,
            thinking: thinking ?? self.thinking
        )
    }
}

struct MachineMetadata {
    let host: String?
    let platform: String?
    let cliVersion: String?
    let homeDir: String?
    let delightHomeDir: String?

    static func fromJSON(_ json: String?) -> MachineMetadata? {
        guard let json, let data = json.data(using: .utf8) else {
            return nil
        }
        guard let decoded = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        return MachineMetadata(
            host: decoded["host"] as? String,
            platform: decoded["platform"] as? String,
            cliVersion: decoded["happyCliVersion"] as? String ?? decoded["cliVersion"] as? String,
            homeDir: decoded["homeDir"] as? String,
            delightHomeDir: decoded["happyHomeDir"] as? String
        )
    }
}

struct DaemonState {
    let status: String?
    let pid: Int?
    let startedAt: Int64?

    static func fromJSON(_ json: String?) -> DaemonState? {
        guard let json, let data = json.data(using: .utf8) else {
            return nil
        }
        guard let decoded = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        return DaemonState(
            status: decoded["status"] as? String,
            pid: SessionMetadata.intValue(from: decoded["pid"]),
            startedAt: decoded["startedAt"] as? Int64 ?? (decoded["startedAt"] as? NSNumber)?.int64Value
        )
    }
}

struct MachineInfo: Identifiable {
    let id: String
    let metadata: MachineMetadata?
    let daemonState: DaemonState?
    let daemonStateVersion: Int64
    let active: Bool
    let activeAt: Int64?
}

enum MessageRole: String {
    case user
    case assistant
    case system
    case tool
    case event
    case unknown
}

struct ToolCallSummary: Hashable {
    let title: String
    let icon: String
    let subtitle: String?
}

enum MessageBlock: Hashable {
    case text(String)
    case code(language: String?, content: String)
    case toolCall(ToolCallSummary)
}

struct MessageItem: Identifiable, Hashable {
    let id: String
    let role: MessageRole
    let blocks: [MessageBlock]
    let createdAt: Int64?
}

final class HarnessViewModel: NSObject, ObservableObject, SdkListenerProtocol {
    @Published var serverURL: String = "http://localhost:3005" {
        didSet { persistSettings() }
    }
    @Published var token: String = "" {
        didSet { persistSettings() }
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
    @Published var machines: [MachineInfo] = []
    @Published var logServerURL: String = ""
    @Published var logServerRunning: Bool = false

    private let client: SdkClient
    private static let settingsKeyPrefix = "delight.harness."
    private var selectedMetadata: SessionMetadata?
    private var logLines: [String] = []

    override init() {
        let defaults = UserDefaults.standard
        let loadedServerURL = defaults.string(forKey: Self.settingsKeyPrefix + "serverURL") ?? "http://localhost:3005"
        let loadedToken = defaults.string(forKey: Self.settingsKeyPrefix + "token") ?? ""
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
        masterKey = loadedMasterKey
        publicKey = loadedPublicKey
        privateKey = loadedPrivateKey
        configureLogDirectory()
        ensureKeys()
        self.client.setListener(self)
    }

    func startup() {
        if !masterKey.isEmpty && (!token.isEmpty || (!publicKey.isEmpty && !privateKey.isEmpty)) {
            connect()
            listSessions()
        }
    }

    func generateKeys() {
        var error: NSError?
        let master = SdkGenerateMasterKeyBase64(&error)
        if let error {
            log("Generate master key error: \(error)")
            return
        }
        guard let keypair = SdkGenerateEd25519KeyPair(&error) else {
            log("Generate keypair error: \(error?.localizedDescription ?? "unknown error")")
            return
        }
        if let error {
            log("Generate keypair error: \(error)")
            return
        }
        masterKey = master
        publicKey = keypair.publicKey()
        privateKey = keypair.privateKey()
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
        DispatchQueue.global(qos: .userInitiated).async {
            self.client.disconnect()
            DispatchQueue.main.async {
                self.status = "disconnected"
                self.log("Keys reset")
            }
        }
    }

    func createAccount() {
        if publicKey.isEmpty || privateKey.isEmpty {
            generateKeys()
        }
        authWithKeypair()
    }

    func logout() {
        token = ""
        DispatchQueue.global(qos: .userInitiated).async {
            self.client.disconnect()
            DispatchQueue.main.async {
                self.status = "disconnected"
                self.log("Logged out")
            }
        }
    }

    func authWithKeypair() {
        var error: NSError?
        let newToken = client.auth(withKeyPair: publicKey, privateKeyB64: privateKey, error: &error)
        if let error {
            log("Auth error: \(error)")
            return
        }
        token = newToken
        log("Auth ok")
    }

    func approveTerminal() {
        var error: NSError?
        let terminalKey = SdkParseTerminalURL(terminalURL, &error)
        if let error {
            log("Approve error: \(error)")
            return
        }
        do {
            try client.approveTerminalAuth(terminalKey, masterKeyB64: masterKey)
            log("Approved terminal auth")
        } catch {
            log("Approve error: \(error)")
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
                let newToken = client.auth(withKeyPair: publicKey, privateKeyB64: privateKey, error: nil)
                token = newToken
                log("Auth ok")
            }
            client.setServerURL(serverURL)
            client.setToken(token)
            try client.setMasterKeyBase64(masterKey)
            try client.connect()
            status = "connected"
            if sessions.isEmpty {
                listSessions()
            }
        } catch {
            log("Connect error: \(error)")
        }
    }

    func disconnect() {
        DispatchQueue.global(qos: .userInitiated).async {
            self.client.disconnect()
            DispatchQueue.main.async {
                self.status = "disconnected"
            }
        }
    }

    func listSessions() {
        var error: NSError?
        let response = client.listSessions(&error)
        if let error {
            log("List sessions error: \(error)")
            return
        }
        parseSessions(response)
        listMachines()
        log("Sessions loaded")
    }

    func listMachines() {
        var error: NSError?
        let response = client.listMachines(&error)
        if let error {
            log("List machines error: \(error)")
            return
        }
        if !response.isEmpty {
            parseMachines(response)
            log("Machines loaded")
        }
    }

    func selectSession(_ id: String) {
        sessionID = id
        selectedMetadata = sessions.first(where: { $0.id == id })?.metadata
        fetchMessages()
    }

    func fetchMessages() {
        guard !sessionID.isEmpty else {
            log("Session ID required")
            return
        }
        var error: NSError?
        let response = client.getSessionMessages(sessionID, limit: 50, error: &error)
        if let error {
            log("Get messages error: \(error)")
            return
        }
        log("getSessionMessages raw: \(response)")
        parseMessages(response)
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

        let rawRecord: [String: Any] = [
            "role": "user",
            "content": [
                "type": "text",
                "text": messageText
            ]
        ]
        do {
            let data = try JSONSerialization.data(withJSONObject: rawRecord, options: [])
            let json = String(data: data, encoding: .utf8) ?? "{}"
            try client.sendMessage(sessionID, rawRecordJSON: json)
            log("Sent message")
            fetchMessages()
        } catch {
            log("Send error: \(error)")
        }
    }

    // MARK: - SdkListener

    @objc func onConnected() {
        updateStatus("connected")
    }

    @objc func onDisconnected(_ reason: String?) {
        updateStatus("disconnected: \(reason ?? "unknown")")
    }

    @objc func onUpdate(_ sessionID: String?, updateJSON: String?) {
        log("Update: \(updateJSON ?? "")")
        if let updateJSON {
            handleActivityUpdate(updateJSON)
            if let updateSessionID = extractUpdateSessionID(from: updateJSON) {
                if updateSessionID == self.sessionID {
                    updateThinkingFromUpdate(updateJSON)
                    fetchMessages()
                }
                return
            }
        }
        guard let sessionID else { return }
        if sessionID == self.sessionID {
            fetchMessages()
        }
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
        guard let data = json.data(using: .utf8) else {
            return
        }
        guard let decoded = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return
        }
        if let payload = extractActivityPayload(from: decoded) {
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
        }
    }

    private func extractActivityPayload(from decoded: [String: Any]) -> (id: String, active: Bool?, activeAt: Int64?, thinking: Bool?)? {
        if let type = decoded["type"] as? String, type == "activity" {
            return readActivityFields(from: decoded)
        }
        if let body = decoded["body"] as? [String: Any],
           let type = body["t"] as? String, type == "activity" {
            return readActivityFields(from: body)
        }
        if let type = decoded["t"] as? String, type == "activity" {
            return readActivityFields(from: decoded)
        }
        return nil
    }

    private func readActivityFields(from payload: [String: Any]) -> (id: String, active: Bool?, activeAt: Int64?, thinking: Bool?)? {
        let id = payload["id"] as? String ?? payload["sid"] as? String
        guard let id else {
            return nil
        }
        let active = payload["active"] as? Bool
        let activeAt = payload["activeAt"] as? Int64 ?? (payload["activeAt"] as? NSNumber)?.int64Value
        let thinking = payload["thinking"] as? Bool
        return (id: id, active: active, activeAt: activeAt, thinking: thinking)
    }

    private func log(_ message: String) {
        client.logLine(message)
        DispatchQueue.main.async {
            self.lastLogLine = message
            self.logLines.append(message)
            if self.logLines.count > 100 {
                self.logLines = Array(self.logLines.suffix(100))
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
        var error: NSError?
        let url = client.startLogServer(&error)
        if let error {
            log("Start log server error: \(error)")
            return
        }
        logServerURL = url
        logServerRunning = !logServerURL.isEmpty
        if logServerRunning {
            log("Log server running at \(logServerURL)")
        }
    }

    func stopLogServer() {
        do {
            _ = try client.stopLogServer()
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
            _ = try client.setLogDirectory(logDir)
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
        guard let data = json.data(using: .utf8) else {
            return
        }
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
        if let decoded = try? JSONDecoder().decode(SessionsResponse.self, from: data) {
            DispatchQueue.main.async { [self] in
                self.sessions = decoded.sessions.map {
                    let metadata = SessionMetadata.fromJSON($0.metadata)
                    let agentState = SessionAgentState.fromJSON($0.agentState)
                    let title = metadata?.agent
                        ?? metadata?.summaryText
                        ?? $0.machineId
                    return SessionSummary(
                        id: $0.id,
                        updatedAt: $0.updatedAt,
                        active: $0.active,
                        activeAt: $0.activeAt,
                        title: title,
                        subtitle: metadata?.host
                            ?? $0.machineId,
                        metadata: metadata,
                        agentState: agentState,
                        thinking: false
                    )
                }
                if let current = self.sessions.first(where: { $0.id == self.sessionID }) {
                    self.selectedMetadata = current.metadata
                }
            }
        }
    }

    func parseMachines(_ json: String) {
        guard let data = json.data(using: .utf8) else {
            return
        }
        let parsed: Any
        do {
            parsed = try JSONSerialization.jsonObject(with: data, options: [])
        } catch {
            log("Parse machines error: \(error)")
            return
        }
        let items: [Any]
        if let array = parsed as? [Any] {
            items = array
        } else if let dict = parsed as? [String: Any], let dataArray = dict["machines"] as? [Any] {
            items = dataArray
        } else {
            items = []
        }
        let machines: [MachineInfo] = items.compactMap { item in
            guard let dict = item as? [String: Any] else { return nil }
            let id = dict["id"] as? String ?? UUID().uuidString
            let metadata = MachineMetadata.fromJSON(dict["metadata"] as? String)
            let daemonState = DaemonState.fromJSON(dict["daemonState"] as? String)
            let daemonStateVersion = dict["daemonStateVersion"] as? Int64 ?? (dict["daemonStateVersion"] as? NSNumber)?.int64Value ?? 0
            let active = dict["active"] as? Bool ?? false
            let activeAt = dict["activeAt"] as? Int64 ?? (dict["activeAt"] as? NSNumber)?.int64Value
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
        guard let data = json.data(using: .utf8) else {
            return
        }
        let parsed: Any
        do {
            parsed = try JSONSerialization.jsonObject(with: data, options: [])
        } catch {
            log("Parse messages error: \(error)")
            return
        }

        let itemsArray: [Any]
        if let dict = parsed as? [String: Any] {
            if let messages = dict["messages"] as? [Any] {
                itemsArray = messages
            } else if let dataDict = dict["data"] as? [String: Any],
                      let messages = dataDict["messages"] as? [Any] {
                itemsArray = messages
            } else if let items = dict["items"] as? [Any] {
                itemsArray = items
            } else if let dataItems = dict["data"] as? [Any] {
                itemsArray = dataItems
            } else {
                itemsArray = []
            }
        } else if let array = parsed as? [Any] {
            itemsArray = array
        } else {
            itemsArray = []
        }

        var lastThinkingOnly = false
        var messages: [MessageItem] = []
        var seenKeys = Set<String>()
        var richFallbackKeys = Set<String>()

        for item in itemsArray {
            guard let dict = item as? [String: Any] else { continue }
            let content = normalizeContent(firstNonNull(dict["content"], dict["message"], dict["data"]))
            if isNullMessage(content) || isFileHistorySnapshot(content) || isToolResultMessage(content) {
                continue
            }
            let localID = extractLocalID(from: dict)
            let uuid = extractMessageUUID(from: content)
            if localID != nil || uuid != nil {
                let role = extractRole(from: dict, content: content)
                let text = extractText(from: content)
                if let key = fallbackDedupeKey(role: role, createdAt: dict["createdAt"] as? Int64, text: text) {
                    richFallbackKeys.insert(key)
                }
            }
        }

        for item in itemsArray {
            guard let dict = item as? [String: Any] else { continue }
            let id = dict["id"] as? String ?? UUID().uuidString
            let createdAt = dict["createdAt"] as? Int64
            let content = normalizeContent(firstNonNull(dict["content"], dict["message"], dict["data"]))
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
                    lastThinkingOnly = true
                    continue
                }
                self.logUnsupportedMessage(id: id, content: content)
                continue
            }
            if role == .assistant {
                lastThinkingOnly = false
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
            messages.append(MessageItem(id: id, role: role, blocks: blocks, createdAt: createdAt))
        }

        DispatchQueue.main.async {
            self.messages = messages
        }
        updateSessionThinking(lastThinkingOnly)
    }

    private func normalizeContent(_ content: Any?) -> Any? {
        guard let content else { return nil }
        if let text = content as? String, let nested = parseJSONString(text) {
            return nested
        }
        return content
    }

    private func extractUpdateSessionID(from json: String) -> String? {
        guard let data = json.data(using: .utf8),
              let decoded = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        if let body = decoded["body"] as? [String: Any],
           let message = body["message"] as? [String: Any],
           let sessionID = message["sessionId"] as? String {
            return sessionID
        }
        if let message = decoded["message"] as? [String: Any],
           let sessionID = message["sessionId"] as? String {
            return sessionID
        }
        return nil
    }

    private func updateThinkingFromUpdate(_ json: String) {
        guard let data = json.data(using: .utf8),
              let decoded = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return
        }
        guard let body = decoded["body"] as? [String: Any],
              let message = body["message"] as? [String: Any] else {
            return
        }
        let content = normalizeContent(firstNonNull(message["content"], message["data"]))
        let hasThinking = containsThinkingBlock(content)
        let blocks = extractBlocks(from: content, sessionID: sessionID)
        if hasThinking && blocks.isEmpty {
            updateSessionThinking(true)
        } else if !blocks.isEmpty {
            updateSessionThinking(false)
        }
    }

    private func extractBlocks(from content: Any?, sessionID: String?) -> [MessageBlock] {
        if let dict = content as? [String: Any] {
            if let type = dict["t"] as? String, let payload = dict["c"] {
                if type == "text", let text = payload as? String {
                    return [.text(text)]
                }
                if type == "encrypted" || type == "ciphertext" {
                    return [.text("Encrypted message")]
                }
                return extractBlocks(from: payload, sessionID: sessionID)
            }
            if let text = dict["text"] as? String {
                return splitMarkdownBlocks(text)
            }
            if let contentText = dict["content"] as? String {
                return splitMarkdownBlocks(contentText)
            }
            if let inner = dict["content"] {
                return extractBlocks(from: inner, sessionID: sessionID)
            }
            if let message = dict["message"] {
                return extractBlocks(from: message, sessionID: sessionID)
            }
            if let data = dict["data"] {
                return extractBlocks(from: data, sessionID: sessionID)
            }
        } else if let array = content as? [Any] {
            var blocks: [MessageBlock] = []
            for part in array {
                guard let dict = part as? [String: Any] else { continue }
                if let type = dict["type"] as? String {
                    if type == "text", let text = dict["text"] as? String {
                        blocks.append(contentsOf: splitMarkdownBlocks(text))
                        continue
                    }
                    if type == "thinking" {
                        continue
                    }
                    if type == "tool-call" || type == "tool_use" || type == "tool-use" {
                        let name = dict["name"] as? String ?? "tool"
                        if let summary = toolSummary(name: name, input: dict["input"]) {
                            blocks.append(.toolCall(summary))
                        }
                        continue
                    }
                    if type == "tool-result" || type == "tool_result" {
                        continue
                    }
                }
                if let name = dict["name"] as? String, let input = dict["input"] {
                    if let summary = toolSummary(name: name, input: input) {
                        blocks.append(.toolCall(summary))
                    }
                    continue
                }
                if let text = dict["text"] as? String {
                    blocks.append(contentsOf: splitMarkdownBlocks(text))
                    continue
                }
                if let contentText = dict["content"] as? String {
                    blocks.append(contentsOf: splitMarkdownBlocks(contentText))
                    continue
                }
            }
            return blocks
        } else if let text = content as? String {
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

    private func isNullMessage(_ content: Any?) -> Bool {
        if content is NSNull {
            return true
        }
        if let dict = content as? [String: Any] {
            if dict["message"] is NSNull {
                return true
            }
        }
        return false
    }

    private func isFileHistorySnapshot(_ content: Any?) -> Bool {
        if let dict = content as? [String: Any] {
            if let type = dict["type"] as? String, type == "file-history-snapshot" {
                return true
            }
            if let message = dict["message"] as? [String: Any],
               let type = message["type"] as? String, type == "file-history-snapshot" {
                return true
            }
            if let inner = dict["content"], isFileHistorySnapshot(inner) {
                return true
            }
        }
        return false
    }

    private func isToolResultMessage(_ content: Any?) -> Bool {
        if let dict = content as? [String: Any] {
            if let type = dict["type"] as? String,
               type == "tool_result" || type == "tool-result" {
                return true
            }
            if let message = dict["message"] as? [String: Any] {
                return isToolResultMessage(message)
            }
            if let inner = dict["content"] {
                return isToolResultMessage(inner)
            }
            if let data = dict["data"] {
                return isToolResultMessage(data)
            }
        }
        if let array = content as? [Any] {
            for part in array {
                if let dict = part as? [String: Any],
                   let type = dict["type"] as? String,
                   type == "tool_result" || type == "tool-result" {
                    return true
                }
            }
        }
        return false
    }

    private func extractLocalID(from dict: [String: Any]) -> String? {
        if let localID = dict["localId"] as? String, !localID.isEmpty {
            return localID
        }
        return nil
    }

    private func extractMessageUUID(from content: Any?) -> String? {
        if let dict = content as? [String: Any] {
            if let uuid = dict["uuid"] as? String, !uuid.isEmpty {
                return uuid
            }
            if let message = dict["message"] as? [String: Any],
               let uuid = message["uuid"] as? String, !uuid.isEmpty {
                return uuid
            }
            if let inner = dict["content"], let uuid = extractMessageUUID(from: inner) {
                return uuid
            }
            if let message = dict["message"], let uuid = extractMessageUUID(from: message) {
                return uuid
            }
            if let data = dict["data"], let uuid = extractMessageUUID(from: data) {
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

    private func containsThinkingBlock(_ content: Any?) -> Bool {
        if let dict = content as? [String: Any] {
            if let type = dict["type"] as? String, type == "thinking" {
                return true
            }
            if let inner = dict["content"], containsThinkingBlock(inner) {
                return true
            }
            if let message = dict["message"], containsThinkingBlock(message) {
                return true
            }
            if let data = dict["data"], containsThinkingBlock(data) {
                return true
            }
        }
        if let array = content as? [Any] {
            for part in array {
                if containsThinkingBlock(part) {
                    return true
                }
            }
        }
        return false
    }

    private func updateSessionThinking(_ thinking: Bool) {
        let targetID = sessionID
        guard !targetID.isEmpty else { return }
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

    private func extractText(from content: Any?) -> String? {
        if let dict = content as? [String: Any] {
            if let type = dict["t"] as? String, let payload = dict["c"] {
                if type == "text", let text = payload as? String {
                    return text
                }
                if type == "encrypted" || type == "ciphertext" {
                    return "Encrypted message"
                }
                return extractText(from: payload)
            }
            if let text = dict["text"] as? String {
                return text
            }
            if let inner = dict["content"] {
                return extractText(from: inner)
            }
            if let message = dict["message"] {
                return extractText(from: message)
            }
            if let data = dict["data"] {
                return extractText(from: data)
            }
        } else if let array = content as? [Any] {
            let texts = array.compactMap { part -> String? in
                guard let dict = part as? [String: Any] else { return nil }
                if let type = dict["type"] as? String, type == "text" {
                    return dict["text"] as? String
                }
                return dict["text"] as? String
            }
            if !texts.isEmpty {
                return texts.joined(separator: "\n")
            }
        } else if let text = content as? String {
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

    private func firstNonNull(_ candidates: Any?...) -> Any? {
        for candidate in candidates {
            if candidate is NSNull {
                continue
            }
            if let value = candidate {
                return value
            }
        }
        return nil
    }

    private func parseJSONString(_ text: String) -> Any? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.first == "{" || trimmed.first == "[" else {
            return nil
        }
        guard let data = trimmed.data(using: .utf8) else {
            return nil
        }
        return try? JSONSerialization.jsonObject(with: data, options: [])
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

    private func logUnsupportedMessage(id: String, content: Any?) {
        let snippet = describeContent(content)
        log("Unsupported message format id=\(id) content=\(snippet)")
    }

    private func describeContent(_ content: Any?) -> String {
        if let content {
            if let jsonData = try? JSONSerialization.data(withJSONObject: content, options: []),
               let jsonString = String(data: jsonData, encoding: .utf8) {
                return truncate(jsonString)
            }
            if let text = content as? String {
                return truncate(text)
            }
            return truncate(String(describing: content))
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

    private func toolSummary(name: String, input: Any?) -> ToolCallSummary? {
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
           let dict = input as? [String: Any],
           let pattern = dict["pattern"] as? String {
            return ToolCallSummary(title: "grep(pattern: \(pattern))", icon: icon, subtitle: nil)
        }
        if normalized == "read",
           let dict = input as? [String: Any],
           let filePath = dict["file_path"] as? String {
            return ToolCallSummary(title: displayPath(filePath), icon: icon, subtitle: nil)
        }
        if normalized == "glob",
           let dict = input as? [String: Any],
           let pattern = dict["pattern"] as? String {
            return ToolCallSummary(title: pattern, icon: icon, subtitle: nil)
        }
        if normalized == "ls",
           let dict = input as? [String: Any],
           let path = dict["path"] as? String {
            return ToolCallSummary(title: displayPath(path), icon: icon, subtitle: nil)
        }
        if normalized == "bash",
           let dict = input as? [String: Any],
           let command = dict["command"] as? String {
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

    private func extractRole(from message: [String: Any], content: Any?) -> MessageRole {
        if let role = message["role"] as? String {
            return normalizeRole(role)
        }
        if let dict = content as? [String: Any] {
            if let role = dict["role"] as? String {
                return normalizeRole(role)
            }
            if let inner = dict["content"] as? [String: Any],
               let role = inner["role"] as? String {
                return normalizeRole(role)
            }
            if let message = dict["message"] as? [String: Any],
               let role = message["role"] as? String {
                return normalizeRole(role)
            }
            if let message = dict["message"] as? [String: Any],
               let inner = message["content"] as? [String: Any],
               let role = inner["role"] as? String {
                return normalizeRole(role)
            }
            if let data = dict["data"] as? [String: Any],
               let message = data["message"] as? [String: Any],
               let role = message["role"] as? String {
                return normalizeRole(role)
            }
        }
        return .unknown
    }

    private func normalizeRole(_ role: String) -> MessageRole {
        switch role.lowercased() {
        case "user":
            return .user
        case "assistant", "agent":
            return .assistant
        case "system":
            return .system
        case "tool":
            return .tool
        case "event":
            return .event
        default:
            return .unknown
        }
    }
}
