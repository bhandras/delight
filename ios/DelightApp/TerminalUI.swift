import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

/// TerminalsView lists available terminal sessions and links into their detail view.
struct TerminalsView: View {
    @ObservedObject var model: HarnessViewModel
    @Binding var showScanner: Bool
    @State private var showPairTerminalSheet: Bool = false

    var body: some View {
        let isLoggedIn = !model.token.isEmpty
        let terminalsByID = Dictionary(uniqueKeysWithValues: model.terminals.map { ($0.id, $0) })
        let lastMessageAtBySessionID = model.lastMessageAtBySessionID
        let lastTurnCompletedAtBySessionID = model.lastTurnCompletedAtBySessionID
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
                        } else if model.sessions.isEmpty &&
                                    model.terminals.isEmpty &&
                                    model.lastTerminalPairingReceipt == nil {
                            SettingSection(title: "Pair Terminal") {
                                PairTerminalForm(model: model, showScanner: $showScanner)
                            }
                        } else {
                            ForEach(
                                terminalGroups(
                                    from: model.sessions,
                                    terminals: model.terminals,
                                    pairingReceipt: model.lastTerminalPairingReceipt
                                ),
                                id: \.id
                            ) { group in
                                Text(group.name)
                                    .font(.system(size: 13, weight: .semibold))
                                    .foregroundColor(Theme.mutedText)
                                FeatureListCard {
                                    ForEach(Array(group.items.enumerated()), id: \.element.id) { index, item in
                                        switch item {
                                        case .session(let session):
                                            NavigationLink {
                                                TerminalDetailView(model: model, session: session)
                                            } label: {
                                                TerminalRow(
                                                    session: session,
                                                    gitStatus: gitStatus(for: session, terminalsByID: terminalsByID),
                                                    lastMessageAtMs: lastMessageAtBySessionID[session.id],
                                                    lastTurnCompletedAtMs: lastTurnCompletedAtBySessionID[session.id]
                                                )
                                            }
                                            .buttonStyle(.plain)
                                        case .pairedTerminalWithoutSessions(let terminal):
                                            TerminalNoActiveSessionsRow(
                                                terminal: terminal,
                                                gitStatus: gitStatus(for: terminal.metadata)
                                            )
                                        }

                                        if index < group.items.count - 1 {
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
    let gitStatus: TerminalGitStatus?
    let lastMessageAtMs: Int64?
    let lastTurnCompletedAtMs: Int64?

    var body: some View {
        let status = statusInfo(for: session)
        let agentLabel = terminalAgentLabel(for: session)
        let lastActive = sessionLastActivityText(
            for: session,
            lastMessageAtMs: lastMessageAtMs,
            lastTurnCompletedAtMs: lastTurnCompletedAtMs
        )
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
                Text(lastActive)
                    .font(.system(size: 12))
                    .foregroundColor(Theme.mutedText)
                    .lineLimit(1)
                    .truncationMode(.tail)
                if let gitStatus {
                    TerminalGitStatusLine(status: gitStatus)
                }
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

private struct TerminalNoActiveSessionsRow: View {
    let terminal: TerminalInfo
    let gitStatus: TerminalGitStatus?

    var body: some View {
        let host = terminal.metadata?.host?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let title = host.isEmpty ? "Paired terminal" : host
        let lastActive = lastActiveText(for: terminal)

        HStack(spacing: 12) {
            Circle()
                .fill(Theme.muted)
                .frame(width: 12, height: 12)
            VStack(alignment: .leading, spacing: 4) {
                Text(title)
                    .font(.system(size: 16, weight: .semibold))
                    .lineLimit(1)
                    .truncationMode(.tail)
                Text("No active terminals")
                    .font(.system(size: 13))
                    .foregroundColor(Theme.mutedText)
                    .lineLimit(1)
                    .truncationMode(.tail)
                Text(lastActive)
                    .font(.system(size: 12))
                    .foregroundColor(Theme.mutedText)
                    .lineLimit(1)
                    .truncationMode(.tail)
                if let gitStatus {
                    TerminalGitStatusLine(status: gitStatus)
                }
            }
            .layoutPriority(1)
            Spacer()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
        .padding(.vertical, 12)
    }
}

private struct TerminalGitStatusLine: View {
    let status: TerminalGitStatus

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "arrow.triangle.branch")
                .font(.system(size: 11, weight: .semibold))
                .foregroundColor(status.inRepo ? Theme.accent : Theme.mutedText)
            if !status.inRepo {
                Text("Not in git")
                    .foregroundColor(Theme.mutedText)
            } else {
                Text(status.branch)
                    .foregroundColor(Theme.mutedText)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .layoutPriority(1)
                if status.added > 0 || status.removed > 0 {
                    Text("+\(status.added)")
                        .foregroundColor(Theme.success)
                        .font(.system(size: 11, weight: .semibold, design: .monospaced))
                    Text("-\(status.removed)")
                        .foregroundColor(Theme.danger)
                        .font(.system(size: 11, weight: .semibold, design: .monospaced))
                } else if status.dirty {
                    Text("dirty")
                        .foregroundColor(Theme.warning)
                }
            }
        }
        .font(.system(size: 12))
        .lineLimit(1)
        .truncationMode(.tail)
        .padding(.horizontal, 8)
        .padding(.vertical, 3)
        .background(Color.secondary.opacity(0.12), in: Capsule())
        .accessibilityElement(children: .combine)
    }
}

private struct TerminalGitStatusHeaderLine: View {
    let status: TerminalGitStatus

    var body: some View {
        HStack(spacing: 4) {
            Image(systemName: "arrow.triangle.branch")
                .font(.system(size: 10, weight: .semibold))
                .foregroundColor(status.inRepo ? Theme.accent : Theme.mutedText)
            if !status.inRepo {
                Text("Not in git")
                    .foregroundColor(Theme.mutedText)
            } else {
                Text(status.branch)
                    .foregroundColor(Theme.mutedText)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .layoutPriority(1)
                if status.added > 0 || status.removed > 0 {
                    Text("+\(status.added)")
                        .foregroundColor(Theme.success)
                        .font(.system(size: 10, weight: .semibold, design: .monospaced))
                    Text("-\(status.removed)")
                        .foregroundColor(Theme.danger)
                        .font(.system(size: 10, weight: .semibold, design: .monospaced))
                } else if status.dirty {
                    Text("dirty")
                        .foregroundColor(Theme.warning)
                }
            }
        }
        .font(.system(size: 11))
        .lineLimit(1)
        .truncationMode(.tail)
        .accessibilityElement(children: .combine)
    }
}

private struct TerminalNavHeader: View {
    let online: Bool
    let agentLabel: String
    let path: String?
    let gitStatus: TerminalGitStatus?

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(spacing: 8) {
                HStack(spacing: 6) {
                    StatusDot(color: online ? Theme.success : Theme.muted, isPulsing: false, size: 7)
                        .accessibilityLabel(online ? "online" : "offline")
                    Text(agentLabel)
                        .font(.system(size: 15, weight: .semibold))
                        .foregroundColor(Theme.messageText)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
                .layoutPriority(2)
                if let gitStatus {
                    TerminalGitStatusHeaderLine(status: gitStatus)
                        .layoutPriority(1)
                }
            }
            if let path, !path.isEmpty {
                Text(path)
                    .font(.system(size: 11))
                    .foregroundColor(Theme.mutedText)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
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
        let canControlSession: Bool
        let isShowingStop: Bool

        static func make(ui: SessionUIState?, controlledByDesktop: Bool) -> TerminalComposerState {
            let canSendFromPhone = (ui?.canSend ?? false) && !controlledByDesktop
            let isTurnInFlight = (ui?.working ?? false)

            let isInputEnabled = canSendFromPhone && !isTurnInFlight
            let isShowingStop = canSendFromPhone && isTurnInFlight

            return TerminalComposerState(
                isInputEnabled: isInputEnabled,
                canControlSession: canSendFromPhone,
                isShowingStop: isShowingStop
            )
        }
    }

	    var body: some View {
	        let currentSession = model.sessions.first(where: { $0.id == session.id }) ?? session
        let terminalsByID = Dictionary(uniqueKeysWithValues: model.terminals.map { ($0.id, $0) })
        let headerGitStatus = gitStatus(for: currentSession, terminalsByID: terminalsByID)
	        let agentLabel = terminalAgentLabel(for: currentSession)
        let online = isSessionOnline(currentSession)
	        let ui = currentSession.uiState
	        let uiState = ui?.state ?? "disconnected"
	        let transcriptFontSize = model.effectiveTerminalFontSize(for: currentSession)
	        // The phone should only send input when it controls the session.
	        // Even if the backend accidentally marks `canSend=true` while in local mode,
	        // keep the UX consistent: user must tap "Take Control" first.
	        let controlledByDesktop = ui?.mode != "remote"
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
                MessageComposer(
                    model: model,
                    session: currentSession,
                    isInputEnabled: composerState.isInputEnabled,
                    canControlSession: composerState.canControlSession,
                    isShowingStop: composerState.isShowingStop,
                    placeholder: placeholder
                )
                    .background(Theme.cardBackground)
            }
        }
        .navigationTitle("")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar(.hidden, for: .tabBar)
        .alert("Error", isPresented: $model.showErrorAlert) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(model.errorAlertMessage)
        }
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                TerminalNavHeader(
                    online: online,
                    agentLabel: agentLabel,
                    path: sessionDisplayPath(for: currentSession),
                    gitStatus: headerGitStatus
                )
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

/// TerminalAgentSettingsSheet presents model, effort, and permissions in one
/// sheet so the composer can keep a compact messenger-style layout.
private struct TerminalAgentSettingsSheet: View {
    @ObservedObject var model: HarnessViewModel
    let sessionID: String
    let currentModel: String?
    let currentEffort: String?
    let currentPermissionMode: String?
    let isLocked: Bool
    let onApply: (String?, String?, String?) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var selectedModel: String = ""
    @State private var selectedEffort: String = ""
    @State private var selectedPermissionMode: String = ""
    @State private var isRefreshing = false

    private var availableModels: [String] {
        model.agentEngineSettings[sessionID]?.capabilities.models ?? []
    }

    private var availableReasoningEfforts: [String] {
        model.agentEngineSettings[sessionID]?.capabilities.reasoningEfforts ?? []
    }

    private var availablePermissionModes: [String] {
        model.agentEngineSettings[sessionID]?.capabilities.permissionModes ?? []
    }

    private var isApplyDisabled: Bool {
        if isLocked { return true }
        if availableModels.isEmpty && availableReasoningEfforts.isEmpty && availablePermissionModes.isEmpty {
            return true
        }
        if !availableModels.isEmpty && selectedModel.isEmpty {
            return true
        }
        if !availablePermissionModes.isEmpty && selectedPermissionMode.isEmpty {
            return true
        }
        return false
    }

    var body: some View {
        NavigationStack {
            Form {
                if isLocked {
                    Section {
                        Text("Agent is currently running. Settings are locked until the turn completes.")
                            .foregroundColor(Theme.mutedText)
                    }
                }
                if isRefreshing {
                    Section {
                        HStack(spacing: 10) {
                            ProgressView()
                            Text("Refreshing settings…")
                                .foregroundColor(Theme.mutedText)
                        }
                    }
                }
                Section("Model") {
                    if availableModels.isEmpty {
                        Text("Model selection is not available for this agent.")
                            .foregroundColor(Theme.mutedText)
                    } else {
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
                    }
                }
                Section("Reasoning effort") {
                    if availableReasoningEfforts.isEmpty {
                        Text("Reasoning effort is not available for this agent.")
                            .foregroundColor(Theme.mutedText)
                    } else {
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
                    }
                }
                Section("Permission level") {
                    if availablePermissionModes.isEmpty {
                        Text("Permission selection is not available for this agent.")
                            .foregroundColor(Theme.mutedText)
                    } else {
                        ForEach(availablePermissionModes, id: \.self) { mode in
                            Button {
                                selectedPermissionMode = mode
                            } label: {
                                HStack {
                                    Text(mode)
                                    Spacer()
                                    if selectedPermissionMode == mode {
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
            .navigationTitle("Agent Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Apply") {
                        let nextModel = availableModels.isEmpty ? nil : (selectedModel.isEmpty ? nil : selectedModel)
                        let nextEffort =
                            availableReasoningEfforts.isEmpty ? nil : (selectedEffort.isEmpty ? nil : selectedEffort)
                        let nextPermission =
                            availablePermissionModes.isEmpty
                            ? nil
                            : (selectedPermissionMode.isEmpty ? nil : selectedPermissionMode)
                        onApply(nextModel, nextEffort, nextPermission)
                        dismiss()
                    }
                    .disabled(isApplyDisabled)
                }
            }
            .onAppear {
                if selectedModel.isEmpty {
                    selectedModel = currentModel ?? availableModels.first ?? ""
                }
                if selectedEffort.isEmpty {
                    selectedEffort = currentEffort ?? availableReasoningEfforts.first ?? ""
                }
                if selectedPermissionMode.isEmpty {
                    selectedPermissionMode = currentPermissionMode ?? availablePermissionModes.first ?? ""
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
            .onChange(of: availablePermissionModes) { _ in
                if selectedPermissionMode.isEmpty {
                    selectedPermissionMode = availablePermissionModes.first ?? ""
                    return
                }
                if !availablePermissionModes.contains(selectedPermissionMode) {
                    selectedPermissionMode = availablePermissionModes.first ?? ""
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
    let session: SessionSummary
    let isInputEnabled: Bool
    let canControlSession: Bool
    let isShowingStop: Bool
    let placeholder: String
    @State private var showAgentSettingsSheet = false
    @State private var showAttachmentSourceDialog = false
    @State private var showFileImporter = false
    @State private var showPhotoPicker = false
    @State private var selectedPhotoItem: PhotosPickerItem?
    @State private var isFetchingSettings = false

    private enum Layout {
        static let composerSpacing: CGFloat = 12
        static let attachmentButtonSize: CGFloat = 34
        static let attachmentIconSize: CGFloat = 14
        static let settingsButtonSize: CGFloat = 34
        static let settingsIconSize: CGFloat = 14
        static let controlsBorderOpacity: Double = 0.65
        static let attachmentChipSpacing: CGFloat = 8
        static let attachmentChipVerticalPadding: CGFloat = 4
        static let textFieldHorizontalPadding: CGFloat = 12
        static let textFieldVerticalPadding: CGFloat = 10
        static let textFieldCornerRadius: CGFloat = 18
        static let textFieldBorderOpacity: Double = 0.16
    }

    var body: some View {
        let isWorking = isShowingStop
        let trimmedMessage = model.messageText.trimmingCharacters(in: .whitespacesAndNewlines)
        let hasMessage = !trimmedMessage.isEmpty
        let composerAttachments = model.composerAttachments.filter { $0.sessionID == session.id }
        let hasReadyAttachments = composerAttachments.contains {
            $0.state == .ready
                && (($0.remotePath?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty) == false)
        }
        let hasSendableContent = hasMessage || hasReadyAttachments
        let ui = session.uiState
        let isOnline = (ui?.connected ?? false) && (ui?.online ?? false)
        let isLocked = (ui?.working ?? false)
        let canOpenAgentSettings = canControlSession && isOnline && !isLocked && !isFetchingSettings
        let canAddAttachments = isInputEnabled && canControlSession && isOnline && !isLocked

        VStack(alignment: .leading, spacing: Layout.attachmentChipVerticalPadding) {
            if !composerAttachments.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: Layout.attachmentChipSpacing) {
                        ForEach(composerAttachments) { attachment in
                            ComposerAttachmentChip(attachment: attachment) {
                                model.removeComposerAttachment(uploadID: attachment.id)
                            }
                        }
                    }
                    .padding(.horizontal, 1)
                }
            }

            HStack(spacing: Layout.composerSpacing) {
                Button {
                    showAttachmentSourceDialog = true
                } label: {
                    ZStack {
                        Circle()
                            .fill(Color(uiColor: .secondarySystemBackground))
                        Image(systemName: "paperclip")
                            .font(.system(size: Layout.attachmentIconSize, weight: .semibold))
                            .foregroundColor(canAddAttachments ? Theme.accent : Theme.mutedText)
                    }
                    .frame(width: Layout.attachmentButtonSize, height: Layout.attachmentButtonSize)
                    .overlay(
                        Circle()
                            .stroke(Color(uiColor: .separator).opacity(Layout.controlsBorderOpacity), lineWidth: 1)
                    )
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Add attachment")
                .disabled(!canAddAttachments)

                Button {
                    isFetchingSettings = true
                    model.fetchAgentCapabilities(sessionID: session.id, suppressErrors: false) {
                        isFetchingSettings = false
                        showAgentSettingsSheet = true
                    }
                } label: {
                    ZStack {
                        Circle()
                            .fill(Color(uiColor: .secondarySystemBackground))
                        if isFetchingSettings {
                            ProgressView()
                                .tint(Theme.accent)
                                .scaleEffect(0.75)
                        } else {
                            Image(systemName: "lightbulb")
                                .font(.system(size: Layout.settingsIconSize, weight: .semibold))
                                .foregroundColor(canOpenAgentSettings ? Theme.accent : Theme.mutedText)
                        }
                    }
                    .frame(width: Layout.settingsButtonSize, height: Layout.settingsButtonSize)
                    .overlay(
                        Circle()
                            .stroke(Color(uiColor: .separator).opacity(Layout.controlsBorderOpacity), lineWidth: 1)
                    )
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Agent settings")
                .disabled(!canOpenAgentSettings)

                TextField(text: $model.messageText, axis: .vertical) {
                    Text(placeholder)
                        .foregroundColor(Color(uiColor: .secondaryLabel))
                }
                .font(Theme.body)
                .foregroundColor(Theme.messageText)
                .tint(Theme.accent)
                .padding(.horizontal, Layout.textFieldHorizontalPadding)
                .padding(.vertical, Layout.textFieldVerticalPadding)
                .background(Color(uiColor: .secondarySystemBackground))
                .clipShape(RoundedRectangle(cornerRadius: Layout.textFieldCornerRadius, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: Layout.textFieldCornerRadius, style: .continuous)
                        .stroke(Theme.accent.opacity(Layout.textFieldBorderOpacity), lineWidth: 1)
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
                    .disabled(!canControlSession || model.sessionID.isEmpty)
                } else if hasSendableContent {
                    Button {
                        model.sendMessage()
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
                            || !hasSendableContent
                    )
                    .transition(.scale.combined(with: .opacity))
                }
            }
            .confirmationDialog("Add Attachment", isPresented: $showAttachmentSourceDialog, titleVisibility: .visible) {
                Button("Photo Library") {
                    showPhotoPicker = true
                }
                Button("Files") {
                    showFileImporter = true
                }
                Button("Cancel", role: .cancel) {}
            }
        }
        .padding()
        .animation(.easeInOut(duration: 0.15), value: hasSendableContent)
        .onChange(of: selectedPhotoItem) { newItem in
            guard let newItem else {
                return
            }
            Task {
                await importSelectedPhotoItem(newItem)
                await MainActor.run {
                    selectedPhotoItem = nil
                }
            }
        }
        .photosPicker(
            isPresented: $showPhotoPicker,
            selection: $selectedPhotoItem,
            matching: .images
        )
        .fileImporter(
            isPresented: $showFileImporter,
            allowedContentTypes: [.item],
            allowsMultipleSelection: false
        ) { result in
            handleFileImport(result)
        }
        .sheet(isPresented: $showAgentSettingsSheet) {
            let fresh = model.agentEngineSettings[session.id]
            TerminalAgentSettingsSheet(
                model: model,
                sessionID: session.id,
                currentModel: fresh?.desiredConfig.model?.trimmingCharacters(in: .whitespacesAndNewlines),
                currentEffort: fresh?.desiredConfig.reasoningEffort?.trimmingCharacters(in: .whitespacesAndNewlines),
                currentPermissionMode: fresh?.desiredConfig.permissionMode?.trimmingCharacters(in: .whitespacesAndNewlines),
                isLocked: isLocked,
                onApply: { modelSelection, effortSelection, permissionSelection in
                    model.setAgentConfig(
                        model: modelSelection,
                        permissionMode: permissionSelection,
                        reasoningEffort: effortSelection,
                        sessionID: session.id
                    )
                }
            )
        }
    }

    /// importSelectedPhotoItem converts one photo picker result into an upload.
    private func importSelectedPhotoItem(_ item: PhotosPickerItem) async {
        do {
            guard let data = try await item.loadTransferable(type: Data.self) else {
                return
            }
            let type = item.supportedContentTypes.first
            let ext = type?.preferredFilenameExtension ?? "jpg"
            let mime = type?.preferredMIMEType
            let filename = "photo-\(Int(Date().timeIntervalSince1970)).\(ext)"
            await MainActor.run {
                model.addComposerAttachmentFromData(data, fileName: filename, mimeType: mime)
            }
        } catch {
            return
        }
    }

    /// handleFileImport forwards one imported file URL to the attachment upload flow.
    private func handleFileImport(_ result: Result<[URL], Error>) {
        guard case .success(let urls) = result, let fileURL = urls.first else {
            return
        }
        model.addComposerAttachmentFromFileURL(fileURL)
    }
}

private struct ComposerAttachmentChip: View {
    let attachment: ComposerAttachment
    let onRemove: () -> Void

    private enum Layout {
        static let cornerRadius: CGFloat = 12
        static let horizontalPadding: CGFloat = 10
        static let verticalPadding: CGFloat = 6
        static let removeButtonSize: CGFloat = 20
    }

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: iconName(for: attachment))
                .font(.system(size: 12, weight: .semibold))
                .foregroundColor(iconColor(for: attachment))

            VStack(alignment: .leading, spacing: 1) {
                Text(attachment.fileName)
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundColor(Theme.messageText)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text(attachmentStatusText(for: attachment))
                    .font(.system(size: 11))
                    .foregroundColor(statusColor(for: attachment))
                    .lineLimit(1)
                    .truncationMode(.tail)
            }

            Button {
                onRemove()
            } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundColor(Theme.mutedText)
                    .frame(width: Layout.removeButtonSize, height: Layout.removeButtonSize)
                    .background(Color(uiColor: .tertiarySystemFill))
                    .clipShape(Circle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Remove \(attachment.fileName)")
        }
        .padding(.horizontal, Layout.horizontalPadding)
        .padding(.vertical, Layout.verticalPadding)
        .background(Color(uiColor: .secondarySystemBackground))
        .clipShape(RoundedRectangle(cornerRadius: Layout.cornerRadius, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: Layout.cornerRadius, style: .continuous)
                .stroke(Color(uiColor: .separator).opacity(0.45), lineWidth: 1)
        )
    }

    /// iconName returns a best-effort icon for the attachment media type.
    private func iconName(for attachment: ComposerAttachment) -> String {
        if attachment.mimeType.hasPrefix("image/") {
            return "photo"
        }
        if attachment.mimeType == "application/pdf" {
            return "doc.richtext"
        }
        return "doc"
    }

    /// iconColor maps attachment upload state to icon tint color.
    private func iconColor(for attachment: ComposerAttachment) -> Color {
        switch attachment.state {
        case .uploading:
            return Theme.accent
        case .ready:
            return Theme.success
        case .failed:
            return Theme.warning
        }
    }

    /// statusColor maps attachment upload state to subtitle text color.
    private func statusColor(for attachment: ComposerAttachment) -> Color {
        switch attachment.state {
        case .uploading:
            return Theme.mutedText
        case .ready:
            return Theme.success
        case .failed:
            return Theme.warning
        }
    }

    /// attachmentStatusText returns a compact status summary for one chip.
    private func attachmentStatusText(for attachment: ComposerAttachment) -> String {
        let total = ByteCountFormatter.string(fromByteCount: attachment.sizeBytes, countStyle: .file)
        switch attachment.state {
        case .uploading:
            let uploaded = ByteCountFormatter.string(fromByteCount: attachment.bytesUploaded, countStyle: .file)
            return "Uploading \(uploaded) / \(total)"
        case .ready:
            return "Ready • \(total)"
        case .failed:
            let error = attachment.errorMessage?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            if !error.isEmpty {
                return error
            }
            return "Upload failed"
        }
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

private struct TerminalGitStatus: Equatable {
    let inRepo: Bool
    let branch: String
    let added: Int
    let removed: Int
    let dirty: Bool
}

private func gitStatus(for session: SessionSummary, terminalsByID: [String: TerminalInfo]) -> TerminalGitStatus? {
    let terminalID = session.terminalID ?? session.metadata?.terminalId ?? ""
    guard !terminalID.isEmpty else { return nil }
    return gitStatus(for: terminalsByID[terminalID]?.metadata)
}

private func gitStatus(for metadata: TerminalMetadata?) -> TerminalGitStatus? {
    guard let metadata else { return nil }
    guard let inRepo = metadata.gitInRepo else { return nil }
    if !inRepo {
        return TerminalGitStatus(
            inRepo: false,
            branch: "",
            added: 0,
            removed: 0,
            dirty: false
        )
    }

    let rawBranch = metadata.gitBranch?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    return TerminalGitStatus(
        inRepo: true,
        branch: rawBranch.isEmpty ? "Git repo" : rawBranch,
        added: metadata.gitAdded ?? 0,
        removed: metadata.gitRemoved ?? 0,
        dirty: metadata.gitDirty == true
    )
}

private struct TerminalGroup: Identifiable {
    let id: String
    let name: String
    let items: [TerminalGroupItem]
}

private enum TerminalGroupItem: Identifiable {
    case session(SessionSummary)
    case pairedTerminalWithoutSessions(TerminalInfo)

    var id: String {
        switch self {
        case .session(let session):
            return "session:\(session.id)"
        case .pairedTerminalWithoutSessions(let terminal):
            return "terminal:\(terminal.id)"
        }
    }
}

private func terminalsListSortKey(for session: SessionSummary) -> (String, String, String) {
    let path = sessionDisplayPath(for: session)?.lowercased() ?? ""
    let agent = terminalAgentLabel(for: session).lowercased()
    return (path, agent, session.id)
}

private func terminalWithoutSessionsSortKey(for terminal: TerminalInfo) -> (String, String) {
    let host = terminal.metadata?.host?.lowercased() ?? ""
    return (host, terminal.id)
}

/// LastActiveFormat centralizes constants used for relative activity display.
private enum LastActiveFormat {
    /// millisecondsPerSecond converts Unix seconds to milliseconds.
    static let millisecondsPerSecond: Int64 = 1_000
    /// unixSecondsUpperBound is the highest reasonable Unix-seconds timestamp.
    static let unixSecondsUpperBound: Int64 = 9_999_999_999
    /// nowThresholdSeconds controls when we collapse relative strings to "now".
    static let nowThresholdSeconds: TimeInterval = 5
}

/// terminalRelativeTimeFormatter localizes relative activity timestamps.
private let terminalRelativeTimeFormatter: RelativeDateTimeFormatter = {
    let formatter = RelativeDateTimeFormatter()
    formatter.unitsStyle = .full
    return formatter
}()

/// lastActiveText builds a user-facing activity label for a session row.
private func sessionLastActivityText(
    for session: SessionSummary,
    lastMessageAtMs: Int64?,
    lastTurnCompletedAtMs: Int64?,
    now: Date = Date()
) -> String {
    // Intentionally avoid `session.updatedAt` / `session.activeAt` here because
    // keepalive polling can bump those timestamps without a real transcript
    // event. Only message/turn boundaries should drive "Last active".
    let messageAt = (lastMessageAtMs ?? 0) > 0 ? lastMessageAtMs : nil
    let turnAt = (lastTurnCompletedAtMs ?? 0) > 0 ? lastTurnCompletedAtMs : nil

    switch (messageAt, turnAt) {
    case let (.some(messageAtMs), .some(turnAtMs)):
        return relativeActivityText(prefix: "Last active", timestamp: max(messageAtMs, turnAtMs), now: now)
    case let (.some(messageAtMs), .none):
        return relativeActivityText(prefix: "Last active", timestamp: messageAtMs, now: now)
    case let (.none, .some(turnAtMs)):
        return relativeActivityText(prefix: "Last active", timestamp: turnAtMs, now: now)
    case (.none, .none):
        return "Last active unknown"
    }
}

/// lastActiveText builds a user-facing activity label for a paired-terminal row.
private func lastActiveText(for terminal: TerminalInfo, now: Date = Date()) -> String {
    return relativeActivityText(prefix: "Last active", timestamp: terminal.activeAt, now: now)
}

/// relativeActivityText resolves relative activity copy from a timestamp.
private func relativeActivityText(prefix: String, timestamp: Int64?, now: Date) -> String {
    guard let timestamp, let activeDate = dateFromFlexibleUnixTimestamp(timestamp) else {
        return "\(prefix) unknown"
    }
    let deltaSeconds = abs(now.timeIntervalSince(activeDate))
    if deltaSeconds <= LastActiveFormat.nowThresholdSeconds {
        return "\(prefix) now"
    }
    let relative = terminalRelativeTimeFormatter.localizedString(for: activeDate, relativeTo: now)
    return "\(prefix) \(relative)"
}

/// dateFromFlexibleUnixTimestamp supports both seconds and milliseconds inputs.
private func dateFromFlexibleUnixTimestamp(_ raw: Int64) -> Date? {
    if raw <= 0 {
        return nil
    }
    if raw <= LastActiveFormat.unixSecondsUpperBound {
        return Date(timeIntervalSince1970: TimeInterval(raw))
    }
    let seconds = Double(raw) / Double(LastActiveFormat.millisecondsPerSecond)
    return Date(timeIntervalSince1970: seconds)
}

private func terminalGroupItemSortKey(_ item: TerminalGroupItem) -> (Int, String, String, String) {
    switch item {
    case .session(let session):
        let key = terminalsListSortKey(for: session)
        return (0, key.0, key.1, key.2)
    case .pairedTerminalWithoutSessions(let terminal):
        let key = terminalWithoutSessionsSortKey(for: terminal)
        return (1, key.0, "", key.1)
    }
}

private func terminalGroups(
    from sessions: [SessionSummary],
    terminals: [TerminalInfo],
    pairingReceipt: TerminalPairingReceipt?
) -> [TerminalGroup] {
    let terminalsByID = Dictionary(uniqueKeysWithValues: terminals.map { ($0.id, $0) })
    let sessionsByTerminalID = Dictionary(grouping: sessions) { session in
        session.terminalID ?? session.metadata?.terminalId ?? ""
    }

    var grouped: [String: [TerminalGroupItem]] = [:]

    func appendItem(host: String, item: TerminalGroupItem) {
        grouped[host, default: []].append(item)
    }

    for session in sessions {
        let terminalID = session.terminalID ?? session.metadata?.terminalId ?? ""
        let host = terminalsByID[terminalID]?.metadata?.host?.trimmingCharacters(in: .whitespacesAndNewlines)
        let sessionHost = session.metadata?.host?.trimmingCharacters(in: .whitespacesAndNewlines)
        let resolvedHost = host?.isEmpty == false ? host! : (sessionHost?.isEmpty == false ? sessionHost! : "unknown")
        appendItem(host: resolvedHost, item: .session(session))
    }

    // Surface paired terminals even before they have a created/active session.
    for terminal in terminals {
        if let existing = sessionsByTerminalID[terminal.id], !existing.isEmpty {
            continue
        }
        let terminalHost = terminal.metadata?.host?.trimmingCharacters(in: .whitespacesAndNewlines)
        let resolvedHost = terminalHost?.isEmpty == false ? terminalHost! : "unknown"
        appendItem(host: resolvedHost, item: .pairedTerminalWithoutSessions(terminal))
    }

    // Surface the most recent pairing immediately, even before the CLI daemon
    // creates a terminal row on the server.
    if let pairingReceipt {
        let receiptTerminalID = pairingReceipt.terminalID?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let receiptHost = pairingReceipt.host?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""

        let hasRealTerminalByID = !receiptTerminalID.isEmpty && terminals.contains(where: { $0.id == receiptTerminalID })
        let hasSessionByTerminalID = !receiptTerminalID.isEmpty && (sessionsByTerminalID[receiptTerminalID]?.isEmpty == false)

        var hasHostCollision = false
        if !receiptHost.isEmpty {
            let normalizedReceiptHost = receiptHost.lowercased()
            hasHostCollision = terminals.contains {
                ($0.metadata?.host?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() ?? "") == normalizedReceiptHost
            }
        }

        if !hasRealTerminalByID && !hasSessionByTerminalID && !hasHostCollision {
            let syntheticIDSource = !receiptTerminalID.isEmpty ? receiptTerminalID : (receiptHost.isEmpty ? "unknown" : receiptHost)
            let syntheticTerminal = TerminalInfo(
                id: "paired-placeholder:\(syntheticIDSource)",
                metadata: TerminalMetadata(
                    host: receiptHost.isEmpty ? nil : receiptHost,
                    platform: nil,
                    cliVersion: nil,
                    homeDir: nil,
                    delightHomeDir: nil,
                    gitInRepo: nil,
                    gitBranch: nil,
                    gitAdded: nil,
                    gitRemoved: nil,
                    gitDirty: nil
                ),
                daemonState: nil,
                daemonStateVersion: 0,
                active: false,
                activeAt: nil
            )

            let resolvedHost = receiptHost.isEmpty ? "unknown" : receiptHost
            appendItem(host: resolvedHost, item: .pairedTerminalWithoutSessions(syntheticTerminal))
        }
    }

    return grouped
        .map { host, items in
            TerminalGroup(
                id: host,
                name: host,
                items: items.sorted(by: { terminalGroupItemSortKey($0) < terminalGroupItemSortKey($1) })
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
