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
        guard let json else {
            return nil
        }
        guard let payload: SessionMetadataPayload = BridgeJSONDecoder.decode(json, allowBase64: true) else {
            return nil
        }
        let summaryAgent = payload.summary?.agent ?? payload.summary?.name
        let host = payload.host
            ?? payload.hostname
            ?? payload.hostName
            ?? payload.machineName
        let path = payload.path
            ?? payload.cwd
            ?? payload.workDir
            ?? payload.dir
        let daemonPid = payload.daemonPid?.value
            ?? payload.daemon?.pid?.value
            ?? payload.pid?.value
        let daemonStateVersion = payload.daemonStateVersion?.value
            ?? payload.daemon?.stateVersion?.value
            ?? payload.daemon?.version?.value
        let flavor = payload.flavor
            ?? payload.os
            ?? payload.platform
        return SessionMetadata(
            path: path,
            host: host,
            homeDir: payload.homeDir,
            summaryText: payload.summary?.text,
            agent: payload.agent ?? summaryAgent ?? flavor,
            flavor: flavor,
            daemonPid: daemonPid,
            daemonStateVersion: daemonStateVersion,
            machineId: payload.machineId
        )
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
        guard let json else {
            return nil
        }
        guard let payload: SessionAgentStatePayload = BridgeJSONDecoder.decode(json) else {
            return nil
        }
        let controlledByUser = payload.controlledByUser ?? true
        let rawRequests = payload.requests ?? [:]
        var parsed: [String: SessionAgentPendingRequest] = [:]
        parsed.reserveCapacity(rawRequests.count)
        for (requestID, request) in rawRequests {
            let toolName = request.toolName ?? request.toolNameLegacy ?? "unknown"
            let input = request.input ?? "{}"
            let createdAt = request.createdAt?.value ?? request.createdAtLegacy?.value
            parsed[requestID] = SessionAgentPendingRequest(
                toolName: toolName,
                input: input,
                createdAt: createdAt
            )
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
        guard let json else {
            return nil
        }
        guard let payload: MachineMetadataPayload = BridgeJSONDecoder.decode(json) else {
            return nil
        }
        return MachineMetadata(
            host: payload.host,
            platform: payload.platform,
            cliVersion: payload.happyCliVersion ?? payload.cliVersion,
            homeDir: payload.homeDir,
            delightHomeDir: payload.happyHomeDir
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
        guard let json else {
            return nil
        }
        guard let payload: DaemonStatePayload = BridgeJSONDecoder.decode(json) else {
            return nil
        }
        return DaemonState(
            status: payload.status,
            pid: payload.pid?.value,
            startedAt: payload.startedAt?.value
        )
    }
}

// MARK: - Codable payload helpers

/// IntOrString decodes JSON numbers or numeric strings into an Int.
private struct IntOrString: Decodable {
    let value: Int?

    /// init(from:) decodes an Int from either a number or a numeric string.
    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let intValue = try? container.decode(Int.self) {
            value = intValue
            return
        }
        if let stringValue = try? container.decode(String.self),
           let intValue = Int(stringValue) {
            value = intValue
            return
        }
        value = nil
    }
}

/// Int64OrString decodes JSON numbers or numeric strings into an Int64.
private struct Int64OrString: Decodable {
    let value: Int64?

    /// init(from:) decodes an Int64 from either a number or a numeric string.
    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let intValue = try? container.decode(Int64.self) {
            value = intValue
            return
        }
        if let stringValue = try? container.decode(String.self),
           let intValue = Int64(stringValue) {
            value = intValue
            return
        }
        if let doubleValue = try? container.decode(Double.self) {
            value = Int64(doubleValue)
            return
        }
        value = nil
    }
}

/// BridgeJSONDecoder centralizes decoding for JSON string payloads, including base64-wrapped JSON.
private enum BridgeJSONDecoder {
    /// decode parses a JSON string into a typed payload.
    ///
    /// When allowBase64 is true, this will attempt to decode base64(JSON) if the first pass fails.
    static func decode<T: Decodable>(_ json: String, allowBase64: Bool = false) -> T? {
        if let decoded = try? JSONCoding.decode(T.self, from: json) {
            return decoded
        }
        if allowBase64, let decoded: T = decodeBase64(json) {
            return decoded
        }
        return nil
    }

    /// decodeBase64 decodes a base64 string into a JSON payload.
    static func decodeBase64<T: Decodable>(_ value: String) -> T? {
        guard let data = Data(base64Encoded: value),
              let json = String(data: data, encoding: .utf8) else {
            return nil
        }
        return try? JSONCoding.decode(T.self, from: json)
    }
}

/// SessionMetadataPayload is the raw JSON payload structure for session metadata.
private struct SessionMetadataPayload: Decodable {
    struct Summary: Decodable {
        let agent: String?
        let name: String?
        let text: String?
    }

    struct Daemon: Decodable {
        let pid: IntOrString?
        let stateVersion: IntOrString?
        let version: IntOrString?
    }

    let summary: Summary?
    let host: String?
    let hostname: String?
    let hostName: String?
    let machineName: String?
    let path: String?
    let cwd: String?
    let workDir: String?
    let dir: String?
    let daemonPid: IntOrString?
    let daemon: Daemon?
    let pid: IntOrString?
    let daemonStateVersion: IntOrString?
    let flavor: String?
    let os: String?
    let platform: String?
    let homeDir: String?
    let agent: String?
    let machineId: String?
}

/// SessionAgentStatePayload decodes the JSON wire payload for agent state.
private struct SessionAgentStatePayload: Decodable {
    let controlledByUser: Bool?
    let requests: [String: SessionAgentRequestPayload]?
}

/// SessionAgentRequestPayload decodes a single pending permission request.
private struct SessionAgentRequestPayload: Decodable {
    let toolName: String?
    let toolNameLegacy: String?
    let input: String?
    let createdAt: Int64OrString?
    let createdAtLegacy: Int64OrString?

    private enum CodingKeys: String, CodingKey {
        case toolName
        case toolNameLegacy = "tool_name"
        case input
        case createdAt
        case createdAtLegacy = "created_at"
    }
}

/// MachineMetadataPayload decodes machine metadata JSON.
private struct MachineMetadataPayload: Decodable {
    let host: String?
    let platform: String?
    let happyCliVersion: String?
    let cliVersion: String?
    let homeDir: String?
    let happyHomeDir: String?
}

/// DaemonStatePayload decodes daemon state JSON.
private struct DaemonStatePayload: Decodable {
    let status: String?
    let pid: IntOrString?
    let startedAt: Int64OrString?
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
