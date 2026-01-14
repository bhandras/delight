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
                        TerminalsHeader(isLoggedIn: isLoggedIn) {
                            showPairTerminalSheet = true
                        }
                        if !isLoggedIn {
                            LoggedOutTerminalEmptyState(model: model)
                        } else if model.sessions.isEmpty {
                            SettingSection(title: "Pair Terminal") {
                                PairTerminalForm(model: model, showScanner: $showScanner)
                            }
                        } else {
                            ForEach(terminalGroups(from: model.sessions, terminals: model.terminals), id: \.id) { group in
                                Text(group.name)
                                    .font(.system(size: 13, weight: .semibold))
                                    .foregroundColor(Theme.mutedText)
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
                .refreshable {
                    model.listSessions()
                }
                .dismissKeyboardOnTap()
            }
            .navigationTitle("")
            .navigationBarTitleDisplayMode(.inline)
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

private struct TerminalsHeader: View {
    let isLoggedIn: Bool
    let onTapAddTerminal: () -> Void

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text("Terminals")
                .font(.largeTitle)
                .fontWeight(.bold)
                .foregroundColor(Theme.messageText)
            Spacer()
            if isLoggedIn {
                Button(action: onTapAddTerminal) {
                    Image(systemName: "plus")
                        .font(.system(size: 22, weight: .semibold))
                        .foregroundColor(Theme.accent)
                        .frame(width: 36, height: 36)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Pair Terminal")
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
                .dismissKeyboardOnTap()
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
                    .truncationMode(.tail)
                Text(sessionDisplayPath(for: session) ?? session.subtitle ?? status.text)
                    .font(.system(size: 13))
                    .foregroundColor(Theme.mutedText)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            .layoutPriority(1)
            Spacer()
            Image(systemName: "chevron.right")
                .foregroundColor(Theme.mutedText)
        }
        // Make the whole row tappable, not just the visible pixels.
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
        .padding(.vertical, 12)
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

    /// TerminalComposerState captures which parts of the composer should be
    /// interactive for the current session.
    ///
    /// Note: `SessionUIState.online` represents session activity/online-ness
    /// (keep-alive), not whether a model turn is currently in progress. Busy UI
    /// should be driven by `SessionUIState.working`.
    struct TerminalComposerState: Equatable {
        let isInputEnabled: Bool
        let isHistoryEnabled: Bool
        let isShowingStop: Bool

        static func make(ui: SessionUIState?, controlledByDesktop: Bool) -> TerminalComposerState {
            let canSendFromPhone = (ui?.canSend ?? false) && !controlledByDesktop
            let isTurnInFlight = (ui?.working ?? false)

            // Keep prompt history usable even while a turn is running.
            let isHistoryEnabled = canSendFromPhone
            let isInputEnabled = canSendFromPhone && !isTurnInFlight
            let isShowingStop = canSendFromPhone && isTurnInFlight

            return TerminalComposerState(
                isInputEnabled: isInputEnabled,
                isHistoryEnabled: isHistoryEnabled,
                isShowingStop: isShowingStop
            )
        }
    }

	    var body: some View {
	        let currentSession = model.sessions.first(where: { $0.id == session.id }) ?? session
	        let agentLabel = terminalAgentLabel(for: currentSession)
	        let ui = currentSession.uiState
	        let uiState = ui?.state ?? "disconnected"
	        let transcriptFontSize = model.effectiveTerminalFontSize(for: currentSession)
	        // The phone should only send input when it controls the session.
	        // Even if the backend accidentally marks `canSend=true` while in local mode,
	        // keep the UX consistent: user must tap "Take Control" first.
	        let controlledByDesktop = ui?.mode != "remote"
        let isPhoneControlled = (ui?.mode == "remote") && (ui?.connected ?? false) && (ui?.online ?? false)
        let composerState = TerminalComposerState.make(
            ui: ui,
            controlledByDesktop: controlledByDesktop
        )
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
	                TerminalTranscriptCollectionView(
	                    messages: model.messages,
	                    hasMoreHistory: model.hasMoreHistory,
	                    isLoadingHistory: model.isLoadingHistory,
	                    isLoadingLatest: model.isLoadingLatest,
	                    onLoadOlder: { model.fetchOlderMessages() },
	                    onDoubleTap: {
	                        // Double-tap: jump to the newest message.
	                        model.scrollRequest = ScrollRequest(target: .bottom)
	                    },
	                    scrollRequest: model.scrollRequest,
	                    onConsumeScrollRequest: { model.scrollRequest = nil },
	                    fontSize: CGFloat(transcriptFontSize)
	                )
	                // Re-host on font size changes to keep the transcript layout stable.
	                .id("collection-transcript-\(currentSession.id)-\(Int(transcriptFontSize))")
	                // Keep "tap to dismiss keyboard" behavior scoped to the transcript
	                // so composer interactions (including paste) don't immediately
	                // resign first responder.
	                .dismissKeyboardOnTap()
                TerminalAgentConfigControls(model: model, session: currentSession, isEnabled: isPhoneControlled)
                    .background(Theme.cardBackground)
                MessageComposer(
                    model: model,
                    isInputEnabled: composerState.isInputEnabled,
                    isHistoryEnabled: composerState.isHistoryEnabled,
                    isShowingStop: composerState.isShowingStop,
                    placeholder: placeholder
                )
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
                let online = isSessionOnline(currentSession)
                VStack(spacing: 2) {
                    HStack(spacing: 6) {
                        StatusDot(color: online ? Theme.success : Theme.muted, isPulsing: false, size: 7)
                            .accessibilityLabel(online ? "online" : "offline")
                        Text(agentLabel)
                            .font(.system(size: 17, weight: .semibold))
                            .foregroundColor(Theme.messageText)
                    }
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
                TerminalTranscriptPreferencesView(model: model, session: currentSession)
            }
        }
        .onAppear {
            initialScrollDone = false
            model.selectSession(session.id)
            model.requestSessionsRefresh(reason: "terminal detail opened")
        }
    }
}

private struct ToolbarIconButton: View {
    let systemImage: String
    let accessibilityLabel: String
    let action: () -> Void

    private enum Layout {
        static let buttonSize: CGFloat = 32
        static let iconSize: CGFloat = 13
        static let strokeOpacity: Double = 0.6
        static let strokeWidth: CGFloat = 1
    }

    var body: some View {
        Button(action: action) {
            Image(systemName: systemImage)
                .font(.system(size: Layout.iconSize, weight: .semibold))
                .foregroundColor(Theme.mutedText)
                .frame(width: Layout.buttonSize, height: Layout.buttonSize)
                .background(Theme.cardBackground)
                .clipShape(Circle())
                .overlay(Circle().stroke(Color(uiColor: .separator).opacity(Layout.strokeOpacity), lineWidth: Layout.strokeWidth))
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
        let ui = session.uiState
        let isOnline = (ui?.connected ?? false) && (ui?.online ?? false)
        // Disable model/permission changes while the agent is actively working.
        // SessionUIState.online reflects keep-alive/online-ness, not turn state.
        let isLocked = (ui?.working ?? false)
        let vibe: String? = {
            // If the CLI goes offline, hide the activity chip entirely. Otherwise,
            // stale "thinking" state can linger visually after disconnects.
            if !isOnline { return nil }
            if session.agentState?.hasPendingRequests == true {
                return "permission required"
            }
            if ui?.working == true { return vibingMessage(for: session.id) }
            if isFetchingSettings {
                return "loading…"
            }
            if pendingSheet != nil && settings == nil {
                return "loading…"
            }
            return nil
        }()

        VStack(alignment: .leading, spacing: 0) {
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
                .disabled(!isEnabled || !isOnline || isFetchingSettings || isLocked)

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
                .disabled(!isEnabled || !isOnline || isFetchingSettings || isLocked)

                Spacer()

                if let vibe {
                    ActivityChip(text: vibe, fontSize: 12)
                }
            }
            .foregroundColor(Theme.mutedText)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 6)
        .sheet(isPresented: $showModelSheet) {
            let fresh = model.agentEngineSettings[session.id]
            TerminalModelEffortSheet(
                model: model,
                sessionID: session.id,
                currentModel: fresh?.desiredConfig.model?.trimmingCharacters(in: .whitespacesAndNewlines),
                currentEffort: fresh?.desiredConfig.reasoningEffort?.trimmingCharacters(in: .whitespacesAndNewlines),
                isLocked: isLocked,
                onApply: { modelSelection, effortSelection in
                    model.setAgentConfig(
                        model: modelSelection,
                        permissionMode: nil,
                        reasoningEffort: effortSelection,
                        sessionID: session.id
                    )
                }
            )
        }
        .sheet(isPresented: $showPermissionsSheet) {
            let fresh = model.agentEngineSettings[session.id]
            let caps = fresh?.capabilities
            TerminalPermissionsSheet(
                currentPermissionMode: fresh?.desiredConfig.permissionMode?.trimmingCharacters(in: .whitespacesAndNewlines),
                isLocked: isLocked,
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
    @State private var activeAlert: ActiveAlert?

    private enum ActiveAlert: String, Identifiable {
        case deleteTerminal
        case stopCLI
        case restartCLI

        var id: String { rawValue }
    }

    private enum UsageFormat {
        static let costDecimals: Int = 4
    }

	    var body: some View {
	        let terminalID = session.terminalID ?? session.metadata?.terminalId
	        let terminal = terminalID.flatMap { id in model.terminals.first(where: { $0.id == id }) }
	        let terminalIDDisplay = terminalID ?? "unknown"
	        let host = terminal?.metadata?.host
	            ?? session.metadata?.host
	            ?? terminalID
	            ?? "unknown"
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
                return ui.connected && ui.online
            }
            return terminal?.active ?? session.active
        }()
        let usage = model.usageBySessionID[session.id]
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

	                    if let terminalID, !terminalID.isEmpty {
	                        Section {
	                            SheetActionButton(
	                                title: "Restart CLI",
	                                systemImage: "arrow.clockwise",
                                tint: Theme.accent
                            ) {
                                activeAlert = .restartCLI
                            }
                            .listRowBackground(Color.clear)
                            .listRowInsets(EdgeInsets(top: 6, leading: 16, bottom: 6, trailing: 16))
                            .disabled(!online)

                            SheetActionButton(
                                title: "Stop CLI",
                                systemImage: "power",
                                tint: Theme.warning
                            ) {
                                activeAlert = .stopCLI
                            }
                            .listRowBackground(Color.clear)
                            .listRowInsets(EdgeInsets(top: 6, leading: 16, bottom: 6, trailing: 16))
                            .disabled(!online)
                        } footer: {
	                            Text("Restart exits the CLI with a special restart code. If you run the CLI under a wrapper script, it can automatically re-launch in the same directory.")
	                                .font(Theme.caption)
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
	                            Text(terminalIDDisplay)
	                                .foregroundColor(Theme.mutedText)
	                                .textSelection(.enabled)
	                        }
	                    }

                    if let usage, usage.tokensTotal != nil || usage.costTotal != nil {
                        Section("Usage") {
                            if !usage.key.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                                HStack {
                                    Text("Source")
                                    Spacer()
                                    Text(usage.key)
                                        .foregroundColor(Theme.mutedText)
                                }
                            }

                            if let total = usage.tokensTotal {
                                let detail: String = {
                                    let parts: [String] = [
                                        usage.tokensInput.map { "in \($0)" },
                                        usage.tokensOutput.map { "out \($0)" },
                                        usage.tokensCacheRead.map { "cache read \($0)" },
                                        usage.tokensCacheCreation.map { "cache write \($0)" },
                                    ].compactMap { $0 }
                                    if parts.isEmpty { return "\(total)" }
                                    return "\(total) (\(parts.joined(separator: ", ")))"
                                }()
                                HStack {
                                    Text("Tokens")
                                    Spacer()
                                    Text(detail)
                                        .foregroundColor(Theme.mutedText)
                                }
                            }

                            if let total = usage.costTotal {
                                let formatted = String(format: "$%.*f", UsageFormat.costDecimals, total)
                                HStack {
                                    Text("Cost")
                                    Spacer()
                                    Text(formatted)
                                        .foregroundColor(Theme.mutedText)
                                }
                            }
                        }
                    }

	                    if let terminalID, !terminalID.isEmpty {
	                        Section {
	                            SheetActionButton(
	                                title: model.isDeletingTerminal ? "Deleting…" : "Delete Terminal",
	                                systemImage: model.isDeletingTerminal ? "hourglass" : "trash",
                                tint: Theme.warning
                            ) {
                                activeAlert = .deleteTerminal
                            }
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
	            .alert(item: $activeAlert) { alert in
	                switch alert {
	                case .deleteTerminal:
	                    guard let terminalID, !terminalID.isEmpty else {
	                        return Alert(
	                            title: Text("Terminal unavailable"),
	                            message: Text("Unable to determine terminal ID."),
	                            dismissButton: .cancel()
	                        )
	                    }
	                    return Alert(
	                        title: Text("Delete Terminal?"),
	                        message: Text("This will remove the terminal and its sessions from the server."),
	                        primaryButton: .destructive(Text("Delete")) {
                            model.deleteTerminal(terminalID) {
                                dismiss()
                                DispatchQueue.main.async {
                                    onDeletedTerminal()
                                }
                            }
                        },
	                        secondaryButton: .cancel()
	                    )
	                case .restartCLI:
	                    guard let terminalID, !terminalID.isEmpty else {
	                        return Alert(
	                            title: Text("Terminal unavailable"),
	                            message: Text("Unable to determine terminal ID."),
	                            dismissButton: .cancel()
	                        )
	                    }
	                    return Alert(
	                        title: Text("Restart CLI?"),
	                        message: Text("This requests the CLI shut down and (optionally) restart if it is running under a wrapper."),
	                        primaryButton: .destructive(Text("Restart")) {
	                            model.restartDaemon(terminalID: terminalID)
	                            dismiss()
	                        },
	                        secondaryButton: .cancel()
	                    )
	                case .stopCLI:
	                    guard let terminalID, !terminalID.isEmpty else {
	                        return Alert(
	                            title: Text("Terminal unavailable"),
	                            message: Text("Unable to determine terminal ID."),
	                            dismissButton: .cancel()
	                        )
	                    }
	                    return Alert(
	                        title: Text("Stop CLI?"),
	                        message: Text("This requests the CLI shut down. You can start it again from your terminal."),
	                        primaryButton: .destructive(Text("Stop")) {
                            model.stopDaemon(terminalID: terminalID)
                            dismiss()
                        },
                        secondaryButton: .cancel()
                    )
                }
            }
        }
    }
}

private struct TerminalTranscriptPreferencesView: View {
    @ObservedObject var model: HarnessViewModel
    let session: SessionSummary

    private var terminalID: String? {
        session.terminalID ?? session.metadata?.terminalId
    }

    private enum Preview {
        static let sampleText = "The quick brown fox jumps over the lazy dog."
        static let sampleCommand = "echo \"hello\""
    }

	    var body: some View {
	        let terminalIDValue = terminalID?.trimmingCharacters(in: .whitespacesAndNewlines)
	        let hasTerminalID = terminalIDValue != nil && !(terminalIDValue?.isEmpty ?? true)
	        let effectiveTerminalFontSize = model.effectiveTerminalFontSize(for: session)

        ZStack {
            Theme.background.ignoresSafeArea()
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
	                    if let terminalID = terminalIDValue, hasTerminalID {
	                        FeatureListCard {
	                            VStack(alignment: .leading, spacing: 12) {
	                                HStack {
	                                    Text("This Terminal")
	                                        .font(Theme.body)
                                        .foregroundColor(Theme.messageText)
                                    Spacer()
                                    if model.hasTerminalTranscriptOverrides(terminalID: terminalID) {
	                                        Text("override")
	                                            .font(Theme.caption)
	                                            .foregroundColor(Theme.mutedText)
	                                    } else {
	                                        Text("default")
	                                            .font(Theme.caption)
	                                            .foregroundColor(Theme.mutedText)
	                                    }
	                                }

                                Toggle(
                                    isOn: Binding(
                                        get: { model.effectiveShowToolUse(forTerminalID: terminalID) },
                                        set: { model.setTerminalTranscriptShowToolUse(terminalID: terminalID, value: $0) }
                                    )
                                ) {
                                    Text("Show tool use")
                                }

                                Toggle(
                                    isOn: Binding(
                                        get: { model.effectiveShowToolOutput(forTerminalID: terminalID) },
                                        set: { model.setTerminalTranscriptShowToolOutput(terminalID: terminalID, value: $0) }
                                    )
                                ) {
                                    Text("Show tool output")
                                }
                                .disabled(!model.effectiveShowToolUse(forTerminalID: terminalID))

                                Toggle(
                                    isOn: Binding(
                                        get: { model.effectiveShowReasoning(forTerminalID: terminalID) },
                                        set: { model.setTerminalTranscriptShowReasoning(terminalID: terminalID, value: $0) }
                                    )
                                ) {
                                    Text("Show reasoning summaries")
                                }

                                Divider()

                                HStack {
                                    Text("Text Size")
                                        .font(Theme.body)
                                        .foregroundColor(Theme.messageText)
                                    Spacer()
                                    Text("\(Int(effectiveTerminalFontSize))")
                                        .font(Theme.caption)
                                        .foregroundColor(Theme.mutedText)
                                }
	                                Slider(
	                                    value: Binding(
	                                        get: { model.effectiveTerminalFontSize(for: session) },
	                                        set: { model.setTerminalTranscriptFontSize(terminalID: terminalID, value: $0) }
	                                    ),
	                                    in: TerminalAppearance.minFontSize...TerminalAppearance.maxFontSize,
	                                    step: TerminalAppearance.fontSizeStep
	                                )

	                                VStack(alignment: .leading, spacing: 10) {
	                                    Text("Preview")
	                                        .font(Theme.caption)
	                                        .foregroundColor(Theme.mutedText)
	                                    Text(Preview.sampleText)
	                                        .font(TerminalAppearance.swiftUIFont(size: effectiveTerminalFontSize))
	                                        .foregroundColor(Theme.messageText)
	                                    Text(Preview.sampleCommand)
	                                        .font(
	                                            .custom(
	                                                TerminalAppearance.transcriptFontFamilyName,
	                                                size: CGFloat(TerminalAppearance.codeFontSize(for: effectiveTerminalFontSize))
	                                            )
	                                        )
	                                        .foregroundColor(Theme.codeText)
	                                        .padding(10)
	                                        .background(Theme.codeBackground)
	                                        .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
	                                }

	                                Button("Use Global Defaults") {
	                                    model.clearTerminalTranscriptOverrides(terminalID: terminalID)
	                                }
	                                .buttonStyle(.borderless)
                                .foregroundColor(Theme.mutedText)
                                .disabled(!model.hasTerminalTranscriptOverrides(terminalID: terminalID))
                            }
                            .padding(.vertical, 8)
	                        }
	                        .padding(.horizontal, 16)
	                    } else {
	                        FeatureListCard {
	                            Text("Terminal transcript settings are unavailable for this session.")
	                                .font(Theme.body)
	                                .foregroundColor(Theme.mutedText)
	                                .frame(maxWidth: .infinity, alignment: .leading)
	                                .padding(.vertical, 8)
	                        }
	                        .padding(.horizontal, 16)
	                    }

	                    Spacer(minLength: 0)
	                }
                .padding(.top, 12)
            }
        }
        .navigationTitle("Transcript")
        .navigationBarTitleDisplayMode(.inline)
    }
}

private struct SheetActionButton: View {
    let title: String
    let systemImage: String
    let tint: Color
    let action: () -> Void

    @Environment(\.isEnabled) private var isEnabled

    private enum Layout {
        static let fontSize: CGFloat = 15
        static let paddingVertical: CGFloat = 14
        static let borderOpacity: Double = 0.6
        static let borderWidth: CGFloat = 1
    }

    var body: some View {
        Button(action: action) {
            Label(title, systemImage: systemImage)
                .font(.system(size: Layout.fontSize, weight: .semibold))
                .foregroundColor(isEnabled ? tint : Theme.mutedText)
                .frame(maxWidth: .infinity)
                .padding(.vertical, Layout.paddingVertical)
                .background(Color(uiColor: .secondarySystemBackground))
                .clipShape(Capsule())
                .overlay(
                    Capsule()
                        .stroke(Color(uiColor: .separator).opacity(Layout.borderOpacity), lineWidth: Layout.borderWidth)
                )
        }
        .buttonStyle(.plain)
    }
}

private struct TerminalModelEffortSheet: View {
    @ObservedObject var model: HarnessViewModel
    let sessionID: String
    let currentModel: String?
    let currentEffort: String?
    let isLocked: Bool
    let onApply: (String?, String?) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var selectedModel: String = ""
    @State private var selectedEffort: String = ""
    @State private var isRefreshing = false

    private var availableModels: [String] {
        model.agentEngineSettings[sessionID]?.capabilities.models ?? []
    }

    private var availableReasoningEfforts: [String] {
        model.agentEngineSettings[sessionID]?.capabilities.reasoningEfforts ?? []
    }

    var body: some View {
        NavigationStack {
            Form {
                if isLocked {
                    Section {
                        Text("Agent is currently running. Model and permission settings are locked until the turn completes.")
                            .foregroundColor(Theme.mutedText)
                    }
                }
                Section("Model") {
                    if !availableModels.isEmpty {
                        ForEach(availableModels, id: \.self) { item in
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
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                            .disabled(isLocked)
                        }
                    } else {
                        Text("Model selection is not available for this agent.")
                            .foregroundColor(Theme.mutedText)
                    }
                }

                Section("Reasoning effort") {
                    if !availableReasoningEfforts.isEmpty {
                        ForEach(availableReasoningEfforts, id: \.self) { effort in
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
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                            .disabled(isLocked)
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
                            availableReasoningEfforts.isEmpty ? nil : (selectedEffort.isEmpty ? nil : selectedEffort)
                        onApply(nextModel, nextEffort)
                        dismiss()
                    }
                    .disabled(
                        isLocked
                            || (availableModels.isEmpty && availableReasoningEfforts.isEmpty)
                            || (!availableModels.isEmpty && selectedModel.isEmpty)
                    )
                }
            }
            .onAppear {
                if selectedModel.isEmpty {
                    selectedModel = currentModel ?? availableModels.first ?? ""
                }
                if selectedEffort.isEmpty {
                    selectedEffort = currentEffort ?? availableReasoningEfforts.first ?? ""
                }
            }
            .onChange(of: selectedModel) { newValue in
                guard !isLocked else { return }
                let trimmed = newValue.trimmingCharacters(in: .whitespacesAndNewlines)
                guard !trimmed.isEmpty else { return }
                isRefreshing = true
                model.fetchAgentCapabilities(sessionID: sessionID, desiredModel: trimmed, suppressErrors: true) {
                    isRefreshing = false
                }
            }
            .onChange(of: availableReasoningEfforts) { _ in
                if selectedEffort.isEmpty {
                    selectedEffort = availableReasoningEfforts.first ?? ""
                    return
                }
                if !availableReasoningEfforts.contains(selectedEffort) {
                    selectedEffort = availableReasoningEfforts.first ?? ""
                }
            }
        }
    }
}

private struct TerminalPermissionsSheet: View {
    let currentPermissionMode: String?
    let isLocked: Bool
    let onApply: (String) -> Void
    let permissionModes: [String]

    @Environment(\.dismiss) private var dismiss
    @State private var selected: String = "default"

    var body: some View {
        NavigationStack {
            Form {
                if isLocked {
                    Section {
                        Text("Agent is currently running. Permission settings are locked until the turn completes.")
                            .foregroundColor(Theme.mutedText)
                    }
                }
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
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                            .disabled(isLocked)
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
                    .disabled(isLocked || permissionModes.isEmpty)
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
        let controlledByDesktop = ui?.mode != "remote"
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
    let isInputEnabled: Bool
    let isHistoryEnabled: Bool
    let isShowingStop: Bool
    let placeholder: String

    var body: some View {
        let isWorking = isShowingStop
        let hasHistory = model.hasPromptHistory()

        HStack(spacing: 12) {
            VStack(spacing: 6) {
                Button {
                    model.stepPromptHistory(direction: -1)
                } label: {
                    Image(systemName: "chevron.up")
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundColor(Theme.mutedText)
                        .frame(width: 30, height: 30)
                        .background(Color(uiColor: .tertiarySystemBackground))
                        .clipShape(Circle())
                }
                .disabled(!isHistoryEnabled || !hasHistory)

                Button {
                    model.stepPromptHistory(direction: 1)
                } label: {
                    Image(systemName: "chevron.down")
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundColor(Theme.mutedText)
                        .frame(width: 30, height: 30)
                        .background(Color(uiColor: .tertiarySystemBackground))
                        .clipShape(Circle())
                }
                .disabled(!isHistoryEnabled || !hasHistory)
            }
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
            .disabled(!isInputEnabled)

            if isWorking {
                Button {
                    model.abortCurrentTurn()
                } label: {
                    Image(systemName: "stop.fill")
                        .font(.system(size: 16, weight: .bold))
                        .padding(10)
                        .background(Theme.warning)
                        .foregroundColor(.white)
                        .clipShape(Circle())
                }
                .disabled(!isHistoryEnabled || model.sessionID.isEmpty)
            } else {
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
                .disabled(
                    !isInputEnabled
                        || model.sessionID.isEmpty
                        || model.messageText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                )
                .opacity(
                    (!isInputEnabled || model.messageText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                        ? 0.5
                        : 1.0
                )
            }
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

private func isSessionOnline(_ session: SessionSummary) -> Bool {
    if let ui = session.uiState {
        return ui.connected && ui.online
    }
    return session.active
}

private func statusInfo(for session: SessionSummary, workingOverride: Bool? = nil) -> SessionStatusInfo {
    let working = workingOverride ?? (session.uiState?.working ?? false)
    if let ui = session.uiState {
        if !ui.online {
            return SessionStatusInfo(
                text: "offline",
                dotColor: Theme.muted,
                textColor: Theme.mutedText,
                isPulsing: false
            )
        }
        if !ui.connected {
            return SessionStatusInfo(
                text: "connecting",
                dotColor: Theme.muted,
                textColor: Theme.mutedText,
                isPulsing: true
            )
        }
    }
    if session.uiState == nil && !session.active {
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
    if working {
        return SessionStatusInfo(
            text: "working",
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

private func terminalsListSortKey(for session: SessionSummary) -> (String, String, String) {
    let path = sessionDisplayPath(for: session)?.lowercased() ?? ""
    let agent = terminalAgentLabel(for: session).lowercased()
    return (path, agent, session.id)
}

private func terminalGroups(from sessions: [SessionSummary], terminals: [TerminalInfo]) -> [TerminalGroup] {
    let terminalsByID = Dictionary(uniqueKeysWithValues: terminals.map { ($0.id, $0) })
    let grouped = Dictionary(grouping: sessions) { session in
        let terminalID = session.terminalID ?? session.metadata?.terminalId ?? ""
        let host = terminalsByID[terminalID]?.metadata?.host?.trimmingCharacters(in: .whitespacesAndNewlines)
        let sessionHost = session.metadata?.host?.trimmingCharacters(in: .whitespacesAndNewlines)
        return (host?.isEmpty == false ? host! : (sessionHost?.isEmpty == false ? sessionHost! : "unknown"))
    }
    return grouped
        .map { host, value in
            TerminalGroup(
                id: host,
                name: host,
                sessions: value.sorted(by: { terminalsListSortKey(for: $0) < terminalsListSortKey(for: $1) })
            )
        }
        .sorted(by: { lhs, rhs in
            let left = lhs.name.lowercased()
            let right = rhs.name.lowercased()
            if left == right {
                return lhs.name < rhs.name
            }
            return left < right
        })
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
