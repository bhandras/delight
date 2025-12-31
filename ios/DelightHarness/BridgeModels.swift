import Foundation
import SwiftUI

/// AppearanceMode controls the preferred light/dark/system appearance for the harness.
enum AppearanceMode: String, CaseIterable, Identifiable {
    case system
    case light
    case dark

    var id: String { rawValue }

    var title: String {
        switch self {
        case .system: return "System"
        case .light: return "Light"
        case .dark: return "Dark"
        }
    }

    var preferredColorScheme: ColorScheme? {
        switch self {
        case .system: return nil
        case .light: return .light
        case .dark: return .dark
        }
    }
}

/// PendingPermissionRequest represents a remote-mode tool permission request that
/// should be surfaced as a modal in the harness UI.
struct PendingPermissionRequest: Identifiable, Equatable {
    let sessionID: String
    let requestID: String
    let toolName: String
    let input: String
    let receivedAt: Int64

    var id: String { requestID }
}

/// SessionMetadata contains best-effort machine/session information extracted from
/// session metadata JSON (which may be plaintext JSON or base64-encoded JSON).
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

    /// fromJSON parses a metadata payload that is either JSON or base64(JSON).
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

    /// intValue normalizes JSON number/string values to an Int.
    static func intValue(from value: Any?) -> Int? {
        if let number = value as? NSNumber {
            return number.intValue
        }
        if let string = value as? String, let number = Int(string) {
            return number
        }
        return nil
    }
}

/// SessionAgentState represents the durable agentState persisted by the CLI.
struct SessionAgentState {
    let controlledByUser: Bool
    let requests: [String: SessionAgentPendingRequest]

    var hasPendingRequests: Bool { !requests.isEmpty }

    struct SessionAgentPendingRequest {
        let toolName: String
        let input: String
        let createdAt: Int64?
    }

    /// fromJSON parses a plaintext JSON agentState string.
    static func fromJSON(_ json: String?) -> SessionAgentState? {
        guard let json, let data = json.data(using: .utf8) else {
            return nil
        }
        guard let decoded = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        let controlledByUser = decoded["controlledByUser"] as? Bool ?? true
        let rawRequests = decoded["requests"] as? [String: Any] ?? [:]
        var parsed: [String: SessionAgentPendingRequest] = [:]
        parsed.reserveCapacity(rawRequests.count)
        for (requestID, value) in rawRequests {
            guard let dict = value as? [String: Any] else { continue }
            let toolName = dict["toolName"] as? String ?? dict["tool_name"] as? String ?? "unknown"
            let input = dict["input"] as? String ?? "{}"
            let createdAt =
                dict["createdAt"] as? Int64
                ?? (dict["createdAt"] as? NSNumber)?.int64Value
                ?? (dict["created_at"] as? NSNumber)?.int64Value
            parsed[requestID] = SessionAgentPendingRequest(toolName: toolName, input: input, createdAt: createdAt)
        }
        return SessionAgentState(controlledByUser: controlledByUser, requests: parsed)
    }
}

/// SessionUIState is the SDK-derived, view-friendly UI state injected into session summaries.
struct SessionUIState: Decodable, Equatable {
    let state: String // disconnected|offline|local|remote
    let connected: Bool
    let active: Bool
    let controlledByUser: Bool
    let switching: Bool
    let transition: String
    let canTakeControl: Bool
    let canSend: Bool

    enum CodingKeys: String, CodingKey {
        case state
        case connected
        case active
        case controlledByUser
        case switching
        case transition
        case canTakeControl
        case canSend
    }

    init(
        state: String,
        connected: Bool,
        active: Bool,
        controlledByUser: Bool,
        switching: Bool,
        transition: String,
        canTakeControl: Bool,
        canSend: Bool
    ) {
        self.state = state
        self.connected = connected
        self.active = active
        self.controlledByUser = controlledByUser
        self.switching = switching
        self.transition = transition
        self.canTakeControl = canTakeControl
        self.canSend = canSend
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        let state = try container.decodeIfPresent(String.self, forKey: .state) ?? "disconnected"
        let connected = try container.decodeIfPresent(Bool.self, forKey: .connected) ?? false
        let active = try container.decodeIfPresent(Bool.self, forKey: .active) ?? false
        let controlledByUser = try container.decodeIfPresent(Bool.self, forKey: .controlledByUser) ?? true
        let switching = try container.decodeIfPresent(Bool.self, forKey: .switching) ?? false
        let transition = try container.decodeIfPresent(String.self, forKey: .transition) ?? ""
        let canTakeControl = try container.decodeIfPresent(Bool.self, forKey: .canTakeControl) ?? false
        let canSend = try container.decodeIfPresent(Bool.self, forKey: .canSend) ?? false
        self.init(
            state: state,
            connected: connected,
            active: active,
            controlledByUser: controlledByUser,
            switching: switching,
            transition: transition,
            canTakeControl: canTakeControl,
            canSend: canSend
        )
    }
}

/// SessionSummary is a lightweight session row model used by the harness UI.
struct SessionSummary: Identifiable {
    let id: String
    let updatedAt: Int64
    let active: Bool
    let activeAt: Int64?
    let title: String?
    let subtitle: String?
    let metadata: SessionMetadata?
    let agentState: SessionAgentState?
    let uiState: SessionUIState?
    let thinking: Bool

    /// updatingActivity returns a copy updated with activity and thinking flags.
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
            uiState: uiState,
            thinking: thinking ?? self.thinking
        )
    }
}

/// MachineMetadata is a best-effort decoded metadata payload for a machine.
struct MachineMetadata {
    let host: String?
    let platform: String?
    let cliVersion: String?
    let homeDir: String?
    let delightHomeDir: String?

    /// fromJSON parses a plaintext JSON metadata payload.
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

/// DaemonState is a best-effort decoded daemon status payload.
struct DaemonState {
    let status: String?
    let pid: Int?
    let startedAt: Int64?

    /// fromJSON parses a plaintext JSON daemon state payload.
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

/// MachineInfo is a lightweight row model for the machine list UI.
struct MachineInfo: Identifiable {
    let id: String
    let metadata: MachineMetadata?
    let daemonState: DaemonState?
    let daemonStateVersion: Int64
    let active: Bool
    let activeAt: Int64?
}

/// MessageRole represents the logical sender type for a rendered message row.
enum MessageRole: String {
    case user
    case assistant
    case system
    case tool
    case event
    case unknown
}

/// ToolCallSummary is a compact representation of a tool call used for rendering.
struct ToolCallSummary: Hashable {
    let title: String
    let icon: String
    let subtitle: String?
}

/// MessageBlock is a view-friendly parsed block for a message (text, code, or tool call).
enum MessageBlock: Hashable {
    case text(String)
    case code(language: String?, content: String)
    case toolCall(ToolCallSummary)
}

/// MessageItem is a rendered message row model used by the terminal detail view.
struct MessageItem: Identifiable, Hashable {
    let id: String
    let seq: Int64?
    let localID: String?
    let uuid: String?
    let role: MessageRole
    let blocks: [MessageBlock]
    let createdAt: Int64?
}

/// ScrollRequest requests a scroll in the terminal message list.
struct ScrollRequest: Identifiable, Hashable {
    enum Target: Hashable {
        case message(id: String, anchor: UnitPoint)
        case bottom
    }

    let id = UUID()
    let target: Target
}

/// AccountCreatedReceipt contains credential material to show after onboarding.
struct AccountCreatedReceipt: Identifiable {
    let id = UUID()
    let serverURL: String
    let masterKey: String
    let publicKey: String
    let privateKey: String
    let token: String
}

/// TerminalPairingReceipt contains terminal pairing info shown after approving a terminal.
struct TerminalPairingReceipt: Identifiable {
    let id = UUID()
    let serverURL: String
    let host: String?
    let machineID: String?
    let terminalKey: String
}
