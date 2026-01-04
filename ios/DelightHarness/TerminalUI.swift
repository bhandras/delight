import SwiftUI

/// TerminalsView lists available terminal sessions and links into their detail view.
struct TerminalsView: View {
    @ObservedObject var model: HarnessViewModel
    @Binding var showScanner: Bool
    @State private var showPairTerminalSheet: Bool = false

    var body: some View {
        let isLoggedIn = !model.token.isEmpty
        NavigationStack {
            ZStack {
                Theme.background.ignoresSafeArea()
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        if !isLoggedIn {
                            LoggedOutTerminalEmptyState(model: model)
                        } else if model.sessions.isEmpty {
                            SettingSection(title: "Pair Terminal") {
                                PairTerminalForm(model: model, showScanner: $showScanner)
                            }
                        } else {
                            HStack {
                                Text("TERMINALS")
                                    .font(.system(size: 13, weight: .semibold))
                                    .foregroundColor(Theme.mutedText)
                                Spacer()
                                Button {
                                    model.listSessions()
                                } label: {
                                    Label("Refresh", systemImage: "arrow.clockwise")
                                        .font(.system(size: 12, weight: .semibold))
                                        .padding(.horizontal, 10)
                                        .padding(.vertical, 6)
                                        .background(Theme.cardBackground)
                                        .clipShape(Capsule())
                                        .overlay(
                                            Capsule()
                                                .stroke(Color(uiColor: .separator).opacity(0.6), lineWidth: 1)
                                        )
                                }
                                .buttonStyle(.plain)
                            }

                            ForEach(terminalGroups(from: model.sessions), id: \.id) { group in
                                Text(group.name)
                                    .font(.system(size: 13, weight: .semibold))
                                    .foregroundColor(Theme.mutedText)
                                    .padding(.horizontal, 4)
                                FeatureListCard {
                                    ForEach(Array(group.sessions.enumerated()), id: \.element.id) { index, session in
                                        NavigationLink {
                                            TerminalDetailView(model: model, session: session)
                                        } label: {
                                            TerminalRow(session: session)
                                        }
                                        .buttonStyle(.plain)
                                        if index < group.sessions.count - 1 {
                                            Divider()
                                        }
                                    }
                                }
                            }
                        }
                    }
                    .padding()
                }
            }
            .navigationTitle("Terminals")
            .toolbar {
                if isLoggedIn {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button {
                            showPairTerminalSheet = true
                        } label: {
                            Image(systemName: "plus")
                        }
                        .accessibilityLabel("Pair Terminal")
                    }
                }
            }
        }
        .sheet(isPresented: $showPairTerminalSheet) {
            PairTerminalSheet(model: model, showScanner: $showScanner)
        }
        .onAppear {
            if !model.token.isEmpty {
                model.listSessions()
            }
        }
    }
}

private struct PairTerminalSheet: View {
    @ObservedObject var model: HarnessViewModel
    @Binding var showScanner: Bool
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ZStack {
                Theme.background.ignoresSafeArea()
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        SettingSection(title: "Pair Terminal") {
                            Text("Scan a QR code from the CLI or paste the pairing URL.")
                                .font(Theme.caption)
                                .foregroundColor(Theme.mutedText)
                            PairTerminalForm(model: model, showScanner: $showScanner)
                        }
                    }
                    .padding()
                }
            }
            .navigationTitle("Pair Terminal")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Close") {
                        dismiss()
                    }
                }
            }
        }
    }
}

private struct TerminalRow: View {
    let session: SessionSummary

    var body: some View {
        let status = statusInfo(for: session)
        let agentLabel = terminalAgentLabel(for: session)
        HStack(spacing: 12) {
            Circle()
                .fill(status.dotColor)
                .frame(width: 12, height: 12)
            VStack(alignment: .leading, spacing: 4) {
                Text(agentLabel)
                    .font(.system(size: 16, weight: .semibold))
                    .lineLimit(1)
                Text(sessionDisplayPath(for: session) ?? session.subtitle ?? status.text)
                    .font(.system(size: 13))
                    .foregroundColor(Theme.mutedText)
            }
            Spacer()
            Image(systemName: "chevron.right")
                .foregroundColor(Theme.mutedText)
        }
        .padding(12)
        .background(Theme.cardBackground)
        .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
    }
}

/// TerminalDetailView shows messages, control state, and a composer for a single session.
struct TerminalDetailView: View {
    @ObservedObject var model: HarnessViewModel
    let session: SessionSummary
    @State private var initialScrollDone: Bool = false
    @State private var showTerminalPropertiesSheet: Bool = false
    @State private var showTextSizeSheet: Bool = false
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        let currentSession = model.sessions.first(where: { $0.id == session.id }) ?? session
        let agentLabel = terminalAgentLabel(for: currentSession)
        let ui = currentSession.uiState
        let uiState = ui?.state ?? "disconnected"
        // The phone should only send input when it controls the session.
        // Even if the backend accidentally marks `canSend=true` while in local mode,
        // keep the UX consistent: user must tap "Take Control" first.
        let controlledByDesktop = ui?.controlledByUser ?? (currentSession.agentState?.controlledByUser ?? true)
        let isComposerEnabled = (ui?.canSend ?? false) && !controlledByDesktop
        let isPhoneControlled = (uiState == "remote") && !controlledByDesktop
        let placeholder: String = {
            switch ui?.state {
            case "disconnected":
                return "Disconnected…"
            case "offline":
                return "Terminal offline…"
            case "local":
                return "Tap “Take Control” to type from phone…"
            case "remote":
                return "Type a message..."
            default:
                return "Type a message..."
            }
        }()
        ZStack {
            Theme.background.ignoresSafeArea()
            VStack(spacing: 0) {
                // Only show the control banner when the desktop controls the session.
                // In remote mode, it's redundant noise (the composer is enabled and the
                // user is actively interacting already).
                if uiState == "local" {
                    ControlStatusBanner(model: model, session: currentSession)
                }
                TerminalMessagesView(
                    messages: model.messages,
                    hasMoreHistory: model.hasMoreHistory,
                    isLoadingHistory: model.isLoadingHistory,
                    onLoadOlder: { model.fetchOlderMessages() },
                    scrollRequest: model.scrollRequest,
                    onConsumeScrollRequest: { model.scrollRequest = nil },
                    fontSize: CGFloat(model.terminalFontSize)
                )
                // Force a transcript re-host when font size changes so the
                // underlying UITableView + hosted SwiftUI views are rebuilt.
                // This avoids stale layout when toggling appearance settings.
                .id("transcript-\(currentSession.id)-\(Int(model.terminalFontSize))")
                .contentShape(Rectangle())
                .highPriorityGesture(
                    TapGesture(count: 2).onEnded {
                        model.fetchMessages()
                        model.scrollRequest = ScrollRequest(target: .bottom)
                    }
                )
                .onTapGesture {
                    model.scrollRequest = ScrollRequest(target: .bottom)
                }

                ConnectionStatusRow(
                    status: statusInfo(for: currentSession, thinkingOverride: model.isThinking(sessionID: currentSession.id)),
                    activityText: model.isThinking(sessionID: currentSession.id) ? "thinking" : nil,
                    activityChipFontSize: CGFloat(TerminalAppearance.chipFontSize(for: model.terminalFontSize)),
                    labelFontSize: CGFloat(max(model.terminalFontSize * 0.75, 12))
                )
                .background(Theme.cardBackground)
                TerminalAgentConfigControls(model: model, session: currentSession, isEnabled: isPhoneControlled)
                    .background(Theme.cardBackground)
                MessageComposer(model: model, isEnabled: isComposerEnabled, placeholder: placeholder)
                    .background(Theme.cardBackground)
            }
        }
        .navigationTitle(session.title ?? "Terminal")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar(.hidden, for: .tabBar)
        .alert("Error", isPresented: $model.showErrorAlert) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(model.errorAlertMessage)
        }
        .toolbar {
            ToolbarItem(placement: .principal) {
                VStack(spacing: 2) {
                    Text(agentLabel)
                        .font(.system(size: 17, weight: .semibold))
                        .foregroundColor(Theme.messageText)
                    if let path = sessionDisplayPath(for: session) {
                        Text(path)
                            .font(.system(size: 12))
                            .foregroundColor(Theme.mutedText)
                    }
                }
            }
            ToolbarItemGroup(placement: .topBarTrailing) {
                ToolbarIconButton(systemImage: "textformat.size", accessibilityLabel: "Text Size") {
                    showTextSizeSheet = true
                }
                ToolbarIconButton(systemImage: "gearshape", accessibilityLabel: "Terminal Details") {
                    showTerminalPropertiesSheet = true
                }
            }
        }
        .sheet(isPresented: $showTerminalPropertiesSheet) {
            TerminalPropertiesSheet(model: model, session: currentSession) {
                showTerminalPropertiesSheet = false
                dismiss()
            }
        }
        .sheet(isPresented: $showTextSizeSheet) {
            NavigationStack {
                TerminalTextSizeView(model: model)
            }
        }
        .onAppear {
            initialScrollDone = false
            model.selectSession(session.id)
        }
    }
}

private struct ToolbarIconButton: View {
    let systemImage: String
    let accessibilityLabel: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Image(systemName: systemImage)
                .font(.system(size: 14, weight: .semibold))
                .foregroundColor(Theme.mutedText)
                .padding(10)
                .background(Theme.cardBackground)
                .clipShape(Circle())
                .overlay(
                    Circle()
                        .stroke(Color(uiColor: .separator).opacity(0.6), lineWidth: 1)
                )
        }
        .accessibilityLabel(accessibilityLabel)
        .buttonStyle(.plain)
    }
}

/// terminalAgentLabel returns the best-effort agent identifier for display in
/// the terminal header.
///
/// `SessionSummary.title` is sourced from session metadata, which can lag or be
/// static even if the user changes agent engines. Prefer the durable `agentState`
/// when available.
private func terminalAgentLabel(for session: SessionSummary) -> String {
    let agent = session.agentState?.agentType
        ?? session.metadata?.agent
        ?? "terminal"
    return agent.isEmpty ? "terminal" : agent
}

private struct TerminalAgentConfigControls: View {
    @ObservedObject var model: HarnessViewModel
    let session: SessionSummary
    let isEnabled: Bool
    @State private var showModelSheet = false
    @State private var showPermissionsSheet = false
    @State private var isFetchingSettings = false

    private enum PendingSheet {
        case model
        case permissions
    }

    @State private var pendingSheet: PendingSheet?

    var body: some View {
        let settings = model.agentEngineSettings[session.id]
        let isOnline = (session.uiState?.connected ?? false) && ((session.uiState?.state ?? "") != "offline")

        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 14) {
                Button {
                    pendingSheet = .model
                    isFetchingSettings = true
                    model.fetchAgentCapabilities(sessionID: session.id, suppressErrors: false) {
                        isFetchingSettings = false
                        showModelSheet = true
                    }
                } label: {
                    Image(systemName: "lightbulb")
                        .font(.system(size: 15, weight: .semibold))
                }
                .disabled(!isEnabled || !isOnline || isFetchingSettings)

                Button {
                    pendingSheet = .permissions
                    isFetchingSettings = true
                    model.fetchAgentCapabilities(sessionID: session.id, suppressErrors: false) {
                        isFetchingSettings = false
                        showPermissionsSheet = true
                    }
                } label: {
                    Image(systemName: "exclamationmark.circle")
                        .font(.system(size: 15, weight: .semibold))
                }
                .disabled(!isEnabled || !isOnline || isFetchingSettings)

                Spacer()
            }
            .foregroundColor(Theme.mutedText)

            if !isEnabled {
                Text("Take Control to change agent settings.")
                    .font(Theme.caption)
                    .foregroundColor(Theme.mutedText)
            } else if !isOnline {
                Text("Agent settings are available once the CLI is online.")
                    .font(Theme.caption)
                    .foregroundColor(Theme.mutedText)
            } else if isFetchingSettings {
                Text("Fetching agent settings…")
                    .font(Theme.caption)
                    .foregroundColor(Theme.mutedText)
            } else if pendingSheet != nil && settings == nil {
                Text("Fetching agent settings…")
                    .font(Theme.caption)
                    .foregroundColor(Theme.mutedText)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .sheet(isPresented: $showModelSheet) {
            let fresh = model.agentEngineSettings[session.id]
            let caps = fresh?.capabilities
            TerminalModelEffortSheet(
                currentModel: fresh?.desiredConfig.model?.trimmingCharacters(in: .whitespacesAndNewlines),
                currentEffort: fresh?.desiredConfig.reasoningEffort?.trimmingCharacters(in: .whitespacesAndNewlines),
                onApply: { modelSelection, effortSelection in
                    model.setAgentConfig(
                        model: modelSelection,
                        permissionMode: nil,
                        reasoningEffort: effortSelection,
                        sessionID: session.id
                    )
                },
                models: caps?.models ?? [],
                reasoningEfforts: caps?.reasoningEfforts ?? []
            )
        }
        .sheet(isPresented: $showPermissionsSheet) {
            let fresh = model.agentEngineSettings[session.id]
            let caps = fresh?.capabilities
            TerminalPermissionsSheet(
                currentPermissionMode: fresh?.desiredConfig.permissionMode?.trimmingCharacters(in: .whitespacesAndNewlines),
                onApply: { selected in
                    model.setAgentConfig(
                        model: nil,
                        permissionMode: selected,
                        reasoningEffort: nil,
                        sessionID: session.id
                    )
                },
                permissionModes: caps?.permissionModes ?? []
            )
        }
        .onChange(of: showModelSheet) { newValue in
            if !newValue {
                pendingSheet = nil
            }
        }
        .onChange(of: showPermissionsSheet) { newValue in
            if !newValue {
                pendingSheet = nil
            }
        }
    }
}

private struct TerminalPropertiesSheet: View {
    @ObservedObject var model: HarnessViewModel
    let session: SessionSummary
    let onDeletedTerminal: () -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var showDeleteConfirm: Bool = false

    var body: some View {
        let terminalID = session.terminalID ?? session.metadata?.terminalId ?? "unknown"
        let terminal = model.terminals.first(where: { $0.id == terminalID })
        let host = terminal?.metadata?.host
            ?? session.metadata?.host
            ?? terminalID
        let agent = terminalAgentLabel(for: session)
        let platformDisplay: String = {
            let trimmed = terminal?.metadata?.platform?.trimmingCharacters(in: .whitespacesAndNewlines)
            if let trimmed, !trimmed.isEmpty {
                return trimmed
            }
            return "unknown"
        }()
        let flavor = session.metadata?.flavor ?? "unknown"
        let flavorDisplay: String = {
            // Treat "Flavor" as the agent identifier instead of mixing static
            // session metadata with the current engine selection.
            if agent != "terminal" {
                return agent
            }
            return flavor == "unknown" ? platformDisplay : flavor
        }()
        let online: Bool = {
            if let ui = session.uiState {
                if ui.state == "offline" || ui.state == "disconnected" {
                    return false
                }
                return ui.connected
            }
            return terminal?.active ?? session.active
        }()
        let daemonStatus: String = {
            if !online {
                return "offline"
            }
            let status = terminal?.daemonState?.status?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            return status.isEmpty ? "likely alive" : status
        }()
        let daemonPid = terminal?.daemonState?.pid ?? session.metadata?.daemonPid
        let daemonVersion: Int64? = {
            if let terminal {
                return terminal.daemonStateVersion
            }
            if let version = session.metadata?.daemonStateVersion {
                return Int64(version)
            }
            return nil
        }()

        NavigationStack {
            ZStack {
                Theme.background.ignoresSafeArea()
                List {
                    Section("Daemon") {
                        HStack {
                            Text("Status")
                            Spacer()
                            Text(daemonStatus)
                                .foregroundColor(online ? Theme.success : Theme.mutedText)
                        }
                        HStack {
                            Text("Last Known PID")
                            Spacer()
                            Text(daemonPid.map { String($0) } ?? "—")
                                .foregroundColor(Theme.mutedText)
                        }
                        HStack {
                            Text("Daemon State Version")
                            Spacer()
                            Text(daemonVersion.map { String($0) } ?? "—")
                                .foregroundColor(Theme.mutedText)
                        }
                    }

                    Section("Terminal") {
                        HStack {
                            Text("Host")
                            Spacer()
                            Text(host)
                                .foregroundColor(Theme.mutedText)
                        }
                        HStack {
                            Text("OS")
                            Spacer()
                            Text(platformDisplay)
                                .foregroundColor(Theme.mutedText)
                        }
                        HStack {
                            Text("Flavor")
                            Spacer()
                            Text(flavorDisplay)
                                .foregroundColor(Theme.mutedText)
                        }
                        HStack {
                            Text("Terminal ID")
                            Spacer()
                            Text(terminalID)
                                .foregroundColor(Theme.mutedText)
                                .textSelection(.enabled)
                        }
                    }

                    if terminalID != "unknown" {
                        Section {
                            Button(role: .destructive) {
                                showDeleteConfirm = true
                            } label: {
                                ZStack {
                                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                                        .fill(Theme.warning.opacity(0.16))
                                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                                        .stroke(Theme.warning.opacity(0.5), lineWidth: 1)

                                    if model.isDeletingTerminal {
                                        ProgressView()
                                            .tint(Theme.warning)
                                    } else {
                                        HStack(spacing: 10) {
                                            Image(systemName: "trash")
                                                .font(.system(size: 16, weight: .semibold))
                                            Text("Delete Terminal")
                                                .font(Theme.body)
                                        }
                                        .foregroundColor(Theme.warning)
                                    }
                                }
                                .frame(maxWidth: .infinity)
                                .padding(.vertical, 10)
                            }
                            .buttonStyle(.plain)
                            .listRowBackground(Color.clear)
                            .listRowInsets(EdgeInsets(top: 6, leading: 16, bottom: 6, trailing: 16))
                            .disabled(model.isDeletingTerminal)
                        } footer: {
                            Text("This deletes the terminal and all associated sessions from the server. If the terminal is still running, it may re-register.")
                                .font(Theme.caption)
                                .foregroundColor(Theme.mutedText)
                        }
                    }
                }
                .scrollContentBackground(.hidden)
                .listStyle(.insetGrouped)
            }
            .navigationTitle("Terminal")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Close") { dismiss() }
                }
            }
            .alert("Delete Terminal?", isPresented: $showDeleteConfirm) {
                Button("Cancel", role: .cancel) {}
                Button("Delete", role: .destructive) {
                    model.deleteTerminal(terminalID) {
                        dismiss()
                        DispatchQueue.main.async {
                            onDeletedTerminal()
                        }
                    }
                }
            } message: {
                Text("This will remove the terminal and its sessions from the server.")
            }
        }
    }
}

private struct TerminalModelEffortSheet: View {
    let currentModel: String?
    let currentEffort: String?
    let onApply: (String?, String?) -> Void
    let models: [String]
    let reasoningEfforts: [String]

    @Environment(\.dismiss) private var dismiss
    @State private var selectedModel: String = ""
    @State private var selectedEffort: String = ""

    var body: some View {
        NavigationStack {
            Form {
                Section("Model") {
                    if !models.isEmpty {
                        ForEach(models, id: \.self) { item in
                            Button {
                                selectedModel = item
                            } label: {
                                HStack {
                                    Text(item)
                                    Spacer()
                                    if selectedModel == item {
                                        Image(systemName: "checkmark")
                                    }
                                }
                            }
                            .buttonStyle(.plain)
                        }
                    } else {
                        Text("Model selection is not available for this agent.")
                            .foregroundColor(Theme.mutedText)
                    }
                }

                Section("Reasoning effort") {
                    if !reasoningEfforts.isEmpty {
                        ForEach(reasoningEfforts, id: \.self) { effort in
                            Button {
                                selectedEffort = effort
                            } label: {
                                HStack {
                                    Text(effort)
                                    Spacer()
                                    if selectedEffort == effort {
                                        Image(systemName: "checkmark")
                                    }
                                }
                            }
                            .buttonStyle(.plain)
                        }
                    } else {
                        Text("Reasoning effort is not available for this agent.")
                            .foregroundColor(Theme.mutedText)
                    }
                }
            }
            .navigationTitle("Model & Effort")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Apply") {
                        let nextModel: String? = selectedModel.isEmpty ? nil : selectedModel
                        let nextEffort: String? =
                            reasoningEfforts.isEmpty ? nil : (selectedEffort.isEmpty ? nil : selectedEffort)
                        onApply(nextModel, nextEffort)
                        dismiss()
                    }
                    .disabled(
                        (models.isEmpty && reasoningEfforts.isEmpty)
                            || (!models.isEmpty && selectedModel.isEmpty)
                    )
                }
            }
            .onAppear {
                if selectedModel.isEmpty {
                    selectedModel = currentModel ?? models.first ?? ""
                }
                if selectedEffort.isEmpty {
                    selectedEffort = currentEffort ?? reasoningEfforts.first ?? ""
                }
            }
        }
    }
}

private struct TerminalPermissionsSheet: View {
    let currentPermissionMode: String?
    let onApply: (String) -> Void
    let permissionModes: [String]

    @Environment(\.dismiss) private var dismiss
    @State private var selected: String = "default"

    var body: some View {
        NavigationStack {
            Form {
                Section("Permission level") {
                    if permissionModes.isEmpty {
                        Text("Permission selection is not available for this agent.")
                            .foregroundColor(Theme.mutedText)
                    } else {
                        ForEach(permissionModes, id: \.self) { mode in
                            Button {
                                selected = mode
                            } label: {
                                HStack {
                                    Text(mode)
                                    Spacer()
                                    if selected == mode {
                                        Image(systemName: "checkmark")
                                    }
                                }
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
            }
            .navigationTitle("Permissions")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Apply") {
                        onApply(selected)
                        dismiss()
                    }
                    .disabled(permissionModes.isEmpty)
                }
            }
            .onAppear {
                if let currentPermissionMode, !currentPermissionMode.isEmpty {
                    selected = currentPermissionMode
                } else if selected.isEmpty, let first = permissionModes.first {
                    selected = first
                }
            }
        }
    }
}

private struct ControlStatusBanner: View {
    @ObservedObject var model: HarnessViewModel
    let session: SessionSummary

    var body: some View {
        let ui = session.uiState
        let state = ui?.state ?? "disconnected"
        let switching = ui?.switching ?? false
        let transition = ui?.transition ?? ""
        let isConnectedAndActive = (state == "local" || state == "remote")
        let controlledByDesktop = isConnectedAndActive
            ? (ui?.controlledByUser ?? (session.agentState?.controlledByUser ?? true))
            : true
        let controllerText = isConnectedAndActive ? (controlledByDesktop ? "Desktop" : "Phone") : "—"
        let subtitle: String = {
            switch ui?.state {
            case "disconnected":
                return "Disconnected from server."
            case "offline":
                return "Terminal is offline. Start the CLI to take control."
            case "local":
                return "Desktop controls this session. Tap “Take Control” to send from phone."
            case "remote":
                return "Phone controls this session. To return control, press space twice on desktop."
            default:
                return controlledByDesktop
                    ? "Desktop controls this session. Tap “Take Control” to send from phone."
                    : "Phone controls this session. To return control, press space twice on desktop."
            }
        }()
        let canTakeControl = ui?.canTakeControl ?? false

        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .center, spacing: 10) {
                StatusDot(color: controlledByDesktop ? Theme.success : Theme.accent, isPulsing: false, size: 7)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Controlled by: \(controllerText)")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(Theme.messageText)
                    if model.permissionQueueCount > 0 {
                        Text("permission request pending")
                            .font(Theme.caption)
                            .foregroundColor(Theme.warning)
                    }
                    if switching {
                        Text(transition.isEmpty ? "switching…" : transition.replacingOccurrences(of: "to-", with: "switching to ") + "…")
                            .font(Theme.caption)
                            .foregroundColor(Theme.mutedText)
                    }
                }
                Spacer()
                // Phone UI only supports "Take Control" (switch to remote). Returning
                // control is a desktop-only action (space twice).
                if controlledByDesktop && state == "local" {
                    Button("Take Control") {
                        model.requestSessionControl(mode: "remote", sessionID: session.id)
                    }
                    .buttonStyle(PillButtonStyle(fill: Theme.accent))
                    .disabled(switching || !canTakeControl)
                }
            }

            Text(subtitle)
                .font(Theme.caption)
                .foregroundColor(Theme.mutedText)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .background(Theme.cardBackground)
    }
}

private struct ConnectionStatusRow: View {
    let status: SessionStatusInfo
    let activityText: String?
    let activityChipFontSize: CGFloat
    let labelFontSize: CGFloat

    var body: some View {
        HStack(spacing: 8) {
            StatusDot(color: status.dotColor, isPulsing: status.isPulsing, size: 7)
            Text(status.text)
                .font(.custom("AvenirNext-Medium", size: labelFontSize))
                .foregroundColor(status.textColor)
            Spacer()
            if let activityText {
                ActivityChip(text: activityText, fontSize: activityChipFontSize)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 6)
    }
}

private struct StatusDot: View {
    let color: Color
    let isPulsing: Bool
    let size: CGFloat
    @State private var pulse = false

    var body: some View {
        Circle()
            .fill(color)
            .frame(width: size, height: size)
            .scaleEffect(isPulsing && pulse ? 1.2 : 1.0)
            .opacity(isPulsing && pulse ? 0.6 : 1.0)
            .onAppear {
                guard isPulsing else { return }
                withAnimation(.easeInOut(duration: 1.2).repeatForever(autoreverses: true)) {
                    pulse = true
                }
            }
    }
}

private struct MessageComposer: View {
    @ObservedObject var model: HarnessViewModel
    let isEnabled: Bool
    let placeholder: String

    var body: some View {
        HStack(spacing: 12) {
            TextField(text: $model.messageText, axis: .vertical) {
                Text(placeholder)
                    .foregroundColor(Color(uiColor: .secondaryLabel))
            }
            .font(Theme.body)
            .foregroundColor(Theme.messageText)
            .tint(Theme.accent)
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(Color(uiColor: .tertiarySystemBackground))
            .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 16, style: .continuous)
                    .stroke(Color(uiColor: .separator).opacity(0.7), lineWidth: 1)
            )
            .disabled(!isEnabled)
            Button {
                model.sendMessage()
                model.messageText = ""
            } label: {
                Image(systemName: "paperplane.fill")
                    .font(.system(size: 16, weight: .bold))
                    .padding(10)
                    .background(Theme.accent)
                    .foregroundColor(.white)
                    .clipShape(Circle())
            }
            .disabled(!isEnabled || model.sessionID.isEmpty || model.messageText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            .opacity((!isEnabled || model.messageText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty) ? 0.5 : 1.0)
        }
        .padding()
    }
}

private struct SessionStatusInfo {
    let text: String
    let dotColor: Color
    let textColor: Color
    let isPulsing: Bool
}

private func statusInfo(for session: SessionSummary, thinkingOverride: Bool? = nil) -> SessionStatusInfo {
    let thinking = thinkingOverride ?? session.thinking
    if !session.active {
        return SessionStatusInfo(
            text: "offline",
            dotColor: Theme.muted,
            textColor: Theme.mutedText,
            isPulsing: false
        )
    }
    if session.agentState?.hasPendingRequests == true {
        return SessionStatusInfo(
            text: "permission required",
            dotColor: Theme.warning,
            textColor: Theme.warning,
            isPulsing: true
        )
    }
    if thinking {
        return SessionStatusInfo(
            text: "thinking",
            dotColor: Theme.accent,
            textColor: Theme.success,
            isPulsing: true
        )
    }
    return SessionStatusInfo(
        text: "online",
        dotColor: Theme.success,
        textColor: Theme.success,
        isPulsing: false
    )
}

private func vibingMessage(for sessionID: String) -> String {
    let messages = vibingMessages
    let index = Int(sessionID.hashValue.magnitude % UInt(messages.count))
    return messages[index].lowercased()
}

private func sessionDisplayPath(for session: SessionSummary) -> String? {
    guard let path = session.metadata?.path else {
        return nil
    }
    if let homeDir = session.metadata?.homeDir, path.hasPrefix(homeDir) {
        let trimmed = path.dropFirst(homeDir.count)
        if trimmed.hasPrefix("/") {
            return "~\(trimmed)"
        }
        return "~/" + trimmed
    }
    return path
}

private struct TerminalGroup: Identifiable {
    let id: String
    let name: String
    let sessions: [SessionSummary]
}

private func terminalGroups(from sessions: [SessionSummary]) -> [TerminalGroup] {
    let grouped = Dictionary(grouping: sessions) { session in
        session.terminalID
            ?? session.metadata?.terminalId
            ?? session.subtitle
            ?? "unknown"
    }
    return grouped
        .map { key, value in
            let representative = value[0]
            let displayName = sessionDisplayPath(for: representative)
                ?? representative.metadata?.host
                ?? key
            return TerminalGroup(
                id: key,
                name: displayName,
                sessions: value.sorted(by: { ($0.title ?? $0.id) < ($1.title ?? $1.id) })
            )
        }
        .sorted(by: { $0.name < $1.name })
}

private let vibingMessages = [
    "Accomplishing", "Actioning", "Actualizing", "Baking", "Booping", "Brewing",
    "Calculating", "Cerebrating", "Channelling", "Churning", "Clauding", "Coalescing",
    "Cogitating", "Computing", "Combobulating", "Concocting", "Conjuring", "Considering",
    "Contemplating", "Cooking", "Crafting", "Creating", "Crunching", "Deciphering",
    "Deliberating", "Determining", "Discombobulating", "Divining", "Doing", "Effecting",
    "Elucidating", "Enchanting", "Envisioning", "Finagling", "Flibbertigibbeting",
    "Forging", "Forming", "Frolicking", "Generating", "Germinating", "Hatching",
    "Herding", "Honking", "Ideating", "Imagining", "Incubating", "Inferring",
    "Manifesting", "Marinating", "Meandering", "Moseying", "Mulling", "Mustering",
    "Musing", "Noodling", "Percolating", "Perusing", "Philosophising", "Pontificating",
    "Pondering", "Processing", "Puttering", "Puzzling", "Reticulating", "Ruminating",
    "Scheming", "Schlepping", "Shimmying", "Simmering", "Smooshing", "Spelunking",
    "Spinning", "Stewing", "Sussing", "Synthesizing", "Thinking", "Tinkering",
    "Transmuting", "Unfurling", "Unravelling", "Vibing", "Wandering", "Whirring",
    "Wibbling", "Wizarding", "Working", "Wrangling"
]
