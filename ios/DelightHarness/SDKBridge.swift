import Foundation
import DelightSDK

struct SessionSummary: Identifiable {
    let id: String
    let updatedAt: Int64
    let active: Bool

    var statusText: String {
        let state = active ? "active" : "inactive"
        return "\(state) • updated \(updatedAt)"
    }
}

final class HarnessViewModel: NSObject, ObservableObject, SdkListener {
    @Published var serverURL: String = "http://localhost:3005"
    @Published var token: String = ""
    @Published var masterKey: String = ""
    @Published var terminalURL: String = ""
    @Published var sessionID: String = ""
    @Published var messageText: String = ""
    @Published var status: String = "disconnected"
    @Published var logs: String = ""
    @Published var publicKey: String = ""
    @Published var privateKey: String = ""
    @Published var sessions: [SessionSummary] = []

    private let client: SdkClient

    override init() {
        self.client = SdkNewClient("http://localhost:3005")
        super.init()
        self.client.setListener(self)
    }

    func generateKeys() {
        do {
            let master = try SdkGenerateMasterKeyBase64()
            masterKey = master
            let keypair = try SdkGenerateEd25519KeyPair()
            publicKey = keypair.publicKey()
            privateKey = keypair.privateKey()
            log("Generated master + ed25519 keypair")
        } catch {
            log("Generate keys error: \(error)")
        }
    }

    func authWithKeypair() {
        do {
            let newToken = try client.authWithKeyPair(publicKey, privateKeyB64: privateKey)
            token = newToken
            log("Auth ok")
        } catch {
            log("Auth error: \(error)")
        }
    }

    func approveTerminal() {
        do {
            let terminalKey = try SdkParseTerminalURL(terminalURL)
            try client.approveTerminalAuth(terminalKey, masterKeyB64: masterKey)
            log("Approved terminal auth")
        } catch {
            log("Approve error: \(error)")
        }
    }

    func connect() {
        do {
            client.setServerURL(serverURL)
            client.setToken(token)
            try client.setMasterKeyBase64(masterKey)
            try client.connect()
            status = "connected"
        } catch {
            log("Connect error: \(error)")
        }
    }

    func disconnect() {
        client.disconnect()
        status = "disconnected"
    }

    func listSessions() {
        do {
            let response = try client.listSessions()
            parseSessions(response)
            log("Sessions loaded")
        } catch {
            log("List sessions error: \(error)")
        }
    }

    func fetchMessages() {
        guard !sessionID.isEmpty else {
            log("Session ID required")
            return
        }
        do {
            let response = try client.getSessionMessages(sessionID, limit: 50)
            log("Messages: \(response)")
        } catch {
            log("Get messages error: \(error)")
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
        } catch {
            log("Send error: \(error)")
        }
    }

    // MARK: - SdkListener

    func onConnected() {
        updateStatus("connected")
    }

    func onDisconnected(_ reason: String!) {
        updateStatus("disconnected: \(reason ?? "unknown")")
    }

    func onUpdate(_ sessionID: String!, updateJSON: String!) {
        log("Update: \(updateJSON ?? "")")
    }

    func onError(_ message: String!) {
        log("SDK error: \(message ?? "unknown")")
    }

    private func updateStatus(_ value: String) {
        DispatchQueue.main.async {
            self.status = value
        }
    }

    private func log(_ message: String) {
        DispatchQueue.main.async {
            if self.logs.isEmpty {
                self.logs = message
            } else {
                self.logs = self.logs + "\n" + message
            }
        }
    }

    private func parseSessions(_ json: String) {
        guard let data = json.data(using: .utf8) else {
            return
        }
        struct SessionsResponse: Decodable {
            struct Session: Decodable {
                let id: String
                let updatedAt: Int64
                let active: Bool
            }
            let sessions: [Session]
        }
        if let decoded = try? JSONDecoder().decode(SessionsResponse.self, from: data) {
            DispatchQueue.main.async {
                self.sessions = decoded.sessions.map { SessionSummary(id: $0.id, updatedAt: $0.updatedAt, active: $0.active) }
            }
        }
    }
}
