import SwiftUI

struct ContentView: View {
    @StateObject private var model = HarnessViewModel()
    @State private var showScanner = false
    @State private var selectedTab: Int = 0
    @State private var activeSheet: ActiveSheet?

    var body: some View {
        TabView(selection: $selectedTab) {
            TerminalsView(model: model, showScanner: $showScanner)
                .tabItem {
                    Image(systemName: "terminal")
                    Text("Terminals")
                }
                .tag(0)

            SettingsView(model: model, showScanner: $showScanner)
                .tabItem {
                    Image(systemName: "slider.horizontal.3")
                    Text("Settings")
                }
                .tag(1)
        }
        .tint(Theme.accent)
        .preferredColorScheme(model.appearanceMode.preferredColorScheme)
        .onAppear {
            model.startup()
        }
        .onChange(of: showScanner) { newValue in
            if newValue {
                activeSheet = .scanner
            } else if activeSheet == .scanner {
                activeSheet = nil
            }
        }
        .onChange(of: model.showCrashReport) { newValue in
            if newValue {
                showScanner = false
                activeSheet = .crashReport
            } else if activeSheet == .crashReport {
                activeSheet = nil
            }
        }
        .onChange(of: model.showAccountCreatedReceipt) { newValue in
            if newValue {
                showScanner = false
                activeSheet = .accountCreatedReceipt
            } else if activeSheet == .accountCreatedReceipt {
                activeSheet = nil
            }
        }
        .onChange(of: model.showTerminalPairingReceipt) { newValue in
            if newValue {
                showScanner = false
                activeSheet = .terminalPairingReceipt
            } else if activeSheet == .terminalPairingReceipt {
                activeSheet = nil
            }
        }
        .onChange(of: model.showLogoutConfirm) { newValue in
            if newValue {
                showScanner = false
                activeSheet = .logoutConfirm
            } else if activeSheet == .logoutConfirm {
                activeSheet = nil
            }
        }
        .onChange(of: model.showPermissionPrompt) { newValue in
            if newValue {
                showScanner = false
                activeSheet = .permissionPrompt
            } else if activeSheet == .permissionPrompt {
                activeSheet = nil
            }
        }
        .sheet(item: $activeSheet) { sheet in
            switch sheet {
            case .scanner:
                QRScannerView { result in
                    model.terminalURL = result
                    showScanner = false
                    model.approveTerminal()
                }
            case .crashReport:
                CrashReportSheet(model: model) {
                    model.showCrashReport = false
                }
            case .accountCreatedReceipt:
                AccountCreatedSheet(model: model) {
                    model.showAccountCreatedReceipt = false
                }
            case .terminalPairingReceipt:
                TerminalPairingReceiptSheet(model: model) {
                    model.showTerminalPairingReceipt = false
                }
            case .logoutConfirm:
                LogoutConfirmSheet(model: model) {
                    model.showLogoutConfirm = false
                }
            case .permissionPrompt:
                PermissionPromptSheet(model: model) {
                    model.dismissPermissionPrompt()
                }
            }
        }
    }
}

private enum ActiveSheet: Identifiable {
    case scanner
    case crashReport
    case accountCreatedReceipt
    case terminalPairingReceipt
    case logoutConfirm
    case permissionPrompt

    var id: Int {
        switch self {
        case .scanner: return 0
        case .crashReport: return 1
        case .accountCreatedReceipt: return 2
        case .terminalPairingReceipt: return 3
        case .logoutConfirm: return 4
        case .permissionPrompt: return 5
        }
    }
}

private struct CrashReportSheet: View {
    @ObservedObject var model: HarnessViewModel
    let onDismiss: () -> Void

    var body: some View {
        NavigationStack {
            ZStack {
                Theme.background.ignoresSafeArea()
                VStack(alignment: .leading, spacing: 16) {
                    Text("The app appears to have crashed during the previous run.")
                        .font(Theme.body)
                        .foregroundColor(Theme.messageText)

                    VStack(alignment: .leading, spacing: 8) {
                        Text("Crash Log Path")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundColor(Theme.mutedText)
                        Text(model.crashLogPath)
                            .font(.system(.footnote, design: .monospaced))
                            .foregroundColor(Theme.messageText)
                            .textSelection(.enabled)
                    }

                    VStack(alignment: .leading, spacing: 8) {
                        Text("Crash Log Tail")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundColor(Theme.mutedText)
                        ScrollView {
                            Text(model.crashReportText)
                                .font(.system(.footnote, design: .monospaced))
                                .foregroundColor(Theme.messageText)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .textSelection(.enabled)
                                .padding(8)
                        }
                        .frame(maxHeight: 320)
                        .background(Theme.codeBackground)
                        .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                    }

                    HStack(spacing: 12) {
                        ActionButton(title: "Copy Tail", systemImage: "doc.on.doc") {
                            UIPasteboard.general.string = model.crashReportText
                        }
                        ActionButton(title: "Copy Path", systemImage: "link") {
                            UIPasteboard.general.string = model.crashLogPath
                        }
                    }

                    Text("You can also open Settings → Debug → Logs to inspect the full logs and start the log server.")
                        .font(Theme.caption)
                        .foregroundColor(Theme.mutedText)

                    Spacer()
                }
                .padding()
            }
            .navigationTitle("Crash Report")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done", action: onDismiss)
                }
            }
        }
    }
}

private struct AccountCreatedSheet: View {
    @ObservedObject var model: HarnessViewModel
    let onDismiss: () -> Void

    var body: some View {
        NavigationStack {
            ZStack {
                Theme.background.ignoresSafeArea()
                List {
                    Section {
                        Text("Account successfully created.")
                            .font(Theme.body)
                            .foregroundColor(Theme.messageText)
                    }
                    if let receipt = model.lastAccountCreatedReceipt {
                        Section("Server") {
                            CopyableValueRow(title: "Server URL", value: receipt.serverURL)
                        }
                        Section("Keys") {
                            CopyableValueRow(title: "Master Key", value: receipt.masterKey)
                            CopyableValueRow(title: "Public Key", value: receipt.publicKey)
                            CopyableValueRow(title: "Private Key", value: receipt.privateKey)
                        }
                        Section("Token") {
                            CopyableValueRow(title: "Token", value: receipt.token)
                        }
                    } else {
                        Section {
                            Text("No receipt details available.")
                                .font(Theme.caption)
                                .foregroundColor(Theme.mutedText)
                        }
                    }
                }
                .scrollContentBackground(.hidden)
                .listStyle(.insetGrouped)
            }
            .navigationTitle("Create Account")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Close", action: onDismiss)
                }
            }
        }
    }
}

private struct TerminalPairingReceiptSheet: View {
    @ObservedObject var model: HarnessViewModel
    let onDismiss: () -> Void

    var body: some View {
        NavigationStack {
            ZStack {
                Theme.background.ignoresSafeArea()
                List {
                    Section {
                        Text("Terminal approved.")
                            .font(Theme.body)
                            .foregroundColor(Theme.messageText)
                        Text("If the terminal is running, it should appear in Terminals shortly.")
                            .font(Theme.caption)
                            .foregroundColor(Theme.mutedText)
                    }
                    if let receipt = model.lastTerminalPairingReceipt {
                        Section("Machine") {
                            CopyableValueRow(title: "Host", value: receipt.host ?? "Not available yet")
                            CopyableValueRow(title: "Machine ID", value: receipt.machineID ?? "Not available yet")
                            Text("Host and Machine ID are only known once the terminal's machine connects and reports its metadata.")
                                .font(Theme.caption)
                                .foregroundColor(Theme.mutedText)
                        }
                        Section("Terminal") {
                            CopyableValueRow(title: "Terminal Key", value: receipt.terminalKey)
                        }
                        Section("Server") {
                            CopyableValueRow(title: "Server URL", value: receipt.serverURL)
                        }
                    } else {
                        Section {
                            Text("No pairing details available.")
                                .font(Theme.caption)
                                .foregroundColor(Theme.mutedText)
                        }
                    }
                }
                .scrollContentBackground(.hidden)
                .listStyle(.insetGrouped)
            }
            .navigationTitle("Pair Terminal")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Close", action: onDismiss)
                }
            }
        }
    }
}

private struct LogoutConfirmSheet: View {
    @ObservedObject var model: HarnessViewModel
    let onDismiss: () -> Void

    var body: some View {
        NavigationStack {
            ZStack {
                Theme.background.ignoresSafeArea()
                VStack(alignment: .leading, spacing: 16) {
                    Text("Log out?")
                        .font(Theme.title)
                        .foregroundColor(Theme.messageText)
                    Text("This will remove local session state and hide machines and terminals until you create an account again.")
                        .font(Theme.body)
                        .foregroundColor(Theme.mutedText)

                    if model.isLoggingOut {
                        HStack(spacing: 12) {
                            ProgressView()
                            Text("Logging out…")
                                .font(Theme.body)
                                .foregroundColor(Theme.messageText)
                        }
                        .padding(.vertical, 8)
                    }

                    Spacer()

                    VStack(spacing: 10) {
                        Button {
                            model.logout()
                        } label: {
                            Text(model.isLoggingOut ? "Logging Out…" : "Log Out")
                        }
                        .buttonStyle(PillButtonStyle(fill: Theme.warning))
                        .disabled(model.isLoggingOut)

                        Button("Cancel", action: onDismiss)
                            .buttonStyle(GhostButtonStyle(tint: Theme.accent))
                            .disabled(model.isLoggingOut)
                    }
                }
                .padding()
            }
            .navigationTitle("Confirm")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Close", action: onDismiss)
                        .disabled(model.isLoggingOut)
                }
            }
        }
    }
}

private struct PermissionPromptSheet: View {
    @ObservedObject var model: HarnessViewModel
    let onDismiss: () -> Void

    @State private var message: String = ""

    private func infoRow(label: String, value: String, valueWeight: Font.Weight) -> some View {
        Grid(horizontalSpacing: 14, verticalSpacing: 0) {
            GridRow {
                Text(label)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(Theme.mutedText)
                    .frame(width: 80, alignment: .leading)
                Text(value)
                    .font(.system(size: 15, weight: valueWeight))
                    .foregroundColor(Theme.messageText)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .multilineTextAlignment(.trailing)
                    .frame(maxWidth: .infinity, alignment: .trailing)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
    }

    var body: some View {
        NavigationStack {
            ZStack {
                Theme.background.ignoresSafeArea()

                VStack(alignment: .leading, spacing: 16) {
                    if let req = model.activePermissionRequest {
                        Text("A paired terminal is asking permission to use a tool.")
                            .font(Theme.body)
                            .foregroundColor(Theme.messageText)

                        FeatureListCard {
                            VStack(spacing: 0) {
                                infoRow(label: "Tool", value: req.toolName, valueWeight: .semibold)
                                if let title = model.sessionTitle(for: req.sessionID) {
                                    Divider()
                                        .overlay(Theme.codeBorder.opacity(0.35))
                                    infoRow(label: "Terminal", value: title, valueWeight: .regular)
                                }
                            }
                        }

                        if let pretty = model.prettyPrintedJSON(fromJSONString: req.input) {
                            VStack(alignment: .leading, spacing: 8) {
                                Text("Request")
                                    .font(Theme.caption)
                                    .foregroundColor(Theme.mutedText)
                                ScrollView {
                                    Text(pretty)
                                        .font(Theme.codeFont)
                                        .foregroundColor(Theme.codeText)
                                        .frame(maxWidth: .infinity, alignment: .leading)
                                        .padding(12)
                                }
                                .frame(maxHeight: 240)
                                .background(Theme.codeBackground)
                                .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                                .overlay(
                                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                                        .stroke(Theme.codeBorder, lineWidth: 1)
                                )
                            }
                        }

                        VStack(alignment: .leading, spacing: 8) {
                            Text("Optional message")
                                .font(Theme.caption)
                                .foregroundColor(Theme.mutedText)
                            TextField("Add a note…", text: $message, axis: .vertical)
                                .textInputAutocapitalization(.sentences)
                                .disableAutocorrection(false)
                                .lineLimit(2, reservesSpace: true)
                                .font(Theme.body)
                                .padding(.horizontal, 12)
                                .padding(.vertical, 10)
                                .background(Color(uiColor: .secondarySystemBackground))
                                .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
                        }

                        Spacer(minLength: 8)

                        VStack(spacing: 12) {
                            Button {
                                model.submitPermissionDecision(allow: true, message: message)
                            } label: {
                                ZStack {
                                    Text(model.isRespondingToPermission ? "Allowing…" : "Allow")
                                        .frame(maxWidth: .infinity)
                                    if model.isRespondingToPermission {
                                        HStack {
                                            Spacer()
                                            ProgressView()
                                                .progressViewStyle(.circular)
                                        }
                                    }
                                }
                            }
                            .buttonStyle(PillButtonStyle(fill: Theme.accent))
                            .disabled(model.isRespondingToPermission)

                            Button {
                                model.submitPermissionDecision(allow: false, message: message)
                            } label: {
                                Text("Reject")
                                    .frame(maxWidth: .infinity)
                            }
                            .buttonStyle(GhostButtonStyle(tint: Theme.messageText))
                            .disabled(model.isRespondingToPermission)
                        }
                    } else {
                        Spacer()
                        Text("No pending permission request.")
                            .font(Theme.body)
                            .foregroundColor(Theme.mutedText)
                        Spacer()
                    }
                }
                .padding(16)
            }
            .navigationTitle("Permission")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Close") {
                        // Conservative default: deny on explicit dismissal.
                        model.submitPermissionDecision(
                            allow: false,
                            message: message.isEmpty ? "Dismissed from the mobile app." : message
                        )
                        onDismiss()
                    }
                    .disabled(model.isRespondingToPermission)
                }
            }
        }
        .interactiveDismissDisabled(true)
        .onAppear {
            message = ""
        }
    }
}

private struct TerminalsView: View {
    @ObservedObject var model: HarnessViewModel
    @Binding var showScanner: Bool

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
        }
        .onAppear {
            if !model.token.isEmpty {
                model.listSessions()
            }
        }
    }
}

private struct SettingsView: View {
    @ObservedObject var model: HarnessViewModel
    @Binding var showScanner: Bool

    var body: some View {
        let isLoggedIn = !model.token.isEmpty
        let machines = machineSummaries(from: model.sessions, machines: model.machines)
        NavigationStack {
            ZStack {
                Theme.background.ignoresSafeArea()
                ScrollView {
                    VStack(spacing: 16) {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("FEATURES")
                                .font(.system(size: 13, weight: .semibold))
                                .foregroundColor(Theme.mutedText)
                                .padding(.horizontal, 4)
                            if isLoggedIn {
                                FeatureListCard {
                                    NavigationLink {
                                        AccountDetailView(model: model)
                                    } label: {
                                        SettingMenuRow(
                                            title: "Account",
                                            subtitle: "Manage your account details",
                                            systemImage: "person.circle",
                                            tint: Color(red: 0.18, green: 0.52, blue: 0.96)
                                        )
                                    }
                                    Divider()
                                    NavigationLink {
                                        AppearanceDetailView(model: model)
                                    } label: {
                                        SettingMenuRow(
                                            title: "Appearance",
                                            subtitle: "Customize how the app looks",
                                            systemImage: "paintpalette",
                                            tint: Color(red: 0.4, green: 0.36, blue: 0.9)
                                        )
                                    }
                                    Divider()
                                    NavigationLink {
                                        ResetKeysView(model: model)
                                    } label: {
                                        SettingMenuRow(
                                            title: "Security",
                                            subtitle: "Reset encryption keys",
                                            systemImage: "key.fill",
                                            tint: Theme.warning
                                        )
                                    }
                                }
                            } else {
                                ActionButton(
                                    title: model.isCreatingAccount ? "Creating…" : "Create Account",
                                    systemImage: model.isCreatingAccount ? "hourglass" : "person.crop.circle.badge.plus"
                                ) {
                                    model.createAccount()
                                }
                                .disabled(model.isCreatingAccount)
                            }
                        }

                        if isLoggedIn {
                            VStack(alignment: .leading, spacing: 8) {
                                Text("MACHINES")
                                    .font(.system(size: 13, weight: .semibold))
                                    .foregroundColor(Theme.mutedText)
                                    .padding(.horizontal, 4)
                                FeatureListCard {
                                    if machines.isEmpty {
                                        HStack(spacing: 12) {
                                            Image(systemName: "desktopcomputer")
                                                .font(.system(size: 22))
                                                .foregroundColor(Theme.mutedText)
                                            Text("No machines connected yet.")
                                                .font(Theme.caption)
                                                .foregroundColor(Theme.mutedText)
                                            Spacer()
                                        }
                                        .padding(.vertical, 12)
                                    } else {
                                        ForEach(Array(machines.enumerated()), id: \.element.id) { index, machine in
                                            NavigationLink {
                                                MachineDetailView(model: model, machine: machine)
                                            } label: {
                                                MachineRow(machine: machine)
                                            }
                                            if index != machines.count - 1 {
                                                Divider()
                                            }
                                        }
                                    }
                                }
                            }
                        }

                        SettingSection(title: "Connection") {
                            TextField("Server URL", text: $model.serverURL)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled(true)
                                .textFieldStyle(.roundedBorder)
                            HStack(spacing: 12) {
                                ActionButton(title: "Connect", systemImage: "bolt.horizontal.circle") {
                                    model.connect()
                                }
                                ActionButton(title: "Disconnect", systemImage: "pause.circle") {
                                    model.disconnect()
                                }
                                .tint(Theme.muted)
                            }
                            Text("Status: \(model.status)")
                                .font(Theme.caption)
                                .foregroundColor(Theme.mutedText)
                        }

                        if isLoggedIn && model.sessions.isEmpty {
                            SettingSection(title: "Pair Terminal") {
                                PairTerminalForm(model: model, showScanner: $showScanner)
                            }
                        }

                        VStack(alignment: .leading, spacing: 8) {
                            Text("DEBUG")
                                .font(.system(size: 13, weight: .semibold))
                                .foregroundColor(Theme.mutedText)
                                .padding(.horizontal, 4)
                            FeatureListCard {
                                NavigationLink {
                                    DebugView(model: model)
                                } label: {
                                    SettingMenuRow(
                                        title: "Logs",
                                        subtitle: "Inspect session and crash logs",
                                        systemImage: "doc.text.magnifyingglass",
                                        tint: Theme.mutedText
                                    )
                                }
                            }
                        }
                    }
                    .padding()
                }
            }
            .navigationTitle("Settings")
        }
    }
}

private struct LoggedOutTerminalEmptyState: View {
    @ObservedObject var model: HarnessViewModel

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("You’re logged out.")
                .font(Theme.title)
                .foregroundColor(Theme.messageText)
            Text("Create an account to pair a terminal and see your terminal sessions here.")
                .font(Theme.body)
                .foregroundColor(Theme.mutedText)
            ActionButton(
                title: model.isCreatingAccount ? "Creating…" : "Create Account",
                systemImage: model.isCreatingAccount ? "hourglass" : "person.crop.circle.badge.plus"
            ) {
                model.createAccount()
            }
            .disabled(model.isCreatingAccount)
        }
        .padding()
        .background(Theme.cardBackground)
        .clipShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
        .shadow(color: Theme.shadow, radius: 8, x: 0, y: 4)
    }
}

private struct PairTerminalForm: View {
    @ObservedObject var model: HarnessViewModel
    @Binding var showScanner: Bool

    var body: some View {
        TextField("QR URL (delight://terminal?...)", text: $model.terminalURL)
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled(true)
            .textFieldStyle(.roundedBorder)
        HStack(spacing: 12) {
            ActionButton(title: "Scan QR", systemImage: "qrcode.viewfinder") {
                showScanner = true
            }
            .disabled(model.isApprovingTerminal)

            ActionButton(
                title: model.isApprovingTerminal ? "Approving…" : "Approve",
                systemImage: model.isApprovingTerminal ? "hourglass" : "checkmark.seal"
            ) {
                model.approveTerminal()
            }
            .disabled(model.isApprovingTerminal || model.terminalURL.isEmpty)
        }
    }
}

private struct CopyableValueRow: View {
    let title: String
    let value: String

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(title)
                    .font(Theme.caption)
                    .foregroundColor(Theme.mutedText)
                Spacer()
                Button {
                    UIPasteboard.general.string = value
                } label: {
                    Label("Copy", systemImage: "doc.on.doc")
                        .font(.system(size: 12, weight: .semibold))
                }
                .buttonStyle(.bordered)
                .disabled(value.isEmpty || value == "—")
            }
            Text(value.isEmpty ? "—" : value)
                .font(.system(.footnote, design: .monospaced))
                .foregroundColor(Theme.messageText)
                .textSelection(.enabled)
        }
        .padding(.vertical, 4)
    }
}

private struct HeaderCard: View {
    let status: String

    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 6) {
                Text("Control Room")
                    .font(Theme.title)
                Text(status)
                    .font(Theme.caption)
                    .foregroundColor(Theme.mutedText)
            }
            Spacer()
            Circle()
                .fill(status.contains("connected") ? Theme.success : Theme.warning)
                .frame(width: 14, height: 14)
                .overlay(Circle().stroke(Color(uiColor: .systemBackground), lineWidth: 2))
        }
        .padding()
        .background(Theme.cardGradient)
        .clipShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
        .shadow(color: Theme.shadow, radius: 10, x: 0, y: 6)
    }
}

private struct TerminalRow: View {
    let session: SessionSummary

    var body: some View {
        let status = statusInfo(for: session)
        HStack(spacing: 12) {
            Circle()
                .fill(status.dotColor)
                .frame(width: 12, height: 12)
            VStack(alignment: .leading, spacing: 4) {
                Text(session.title ?? session.id)
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

private struct TerminalDetailView: View {
    @ObservedObject var model: HarnessViewModel
    let session: SessionSummary
    @State private var initialScrollDone: Bool = false

    var body: some View {
        let currentSession = model.sessions.first(where: { $0.id == session.id }) ?? session
        let ui = currentSession.uiState
        let uiState = ui?.state ?? "disconnected"
        // The phone should only send input when it controls the session.
        // Even if the backend accidentally marks `canSend=true` while in local mode,
        // keep the UX consistent: user must tap "Take Control" first.
        let controlledByDesktop = ui?.controlledByUser ?? (currentSession.agentState?.controlledByUser ?? true)
        let isComposerEnabled = (ui?.canSend ?? false) && !controlledByDesktop
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
                    onConsumeScrollRequest: { model.scrollRequest = nil }
                )
                .contentShape(Rectangle())
                .onTapGesture {
                    model.scrollRequest = ScrollRequest(target: .bottom)
                }

                ConnectionStatusRow(status: statusInfo(for: currentSession), activityText: currentSession.thinking ? vibingMessage(for: currentSession.id) : nil)
                    .background(Theme.cardBackground)
                MessageComposer(model: model, isEnabled: isComposerEnabled, placeholder: placeholder)
                    .background(Theme.cardBackground)
            }
        }
        .navigationTitle(session.title ?? "Terminal")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .principal) {
                VStack(spacing: 2) {
                    Text(session.title ?? "Terminal")
                        .font(.system(size: 17, weight: .semibold))
                        .foregroundColor(Theme.messageText)
                    if let path = sessionDisplayPath(for: session) {
                        Text(path)
                            .font(.system(size: 12))
                            .foregroundColor(Theme.mutedText)
                    }
                }
            }
            ToolbarItem(placement: .topBarTrailing) {
                Button {
                    model.fetchMessages()
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
            }
        }
        .onAppear {
            initialScrollDone = false
            model.selectSession(session.id)
        }
    }
}

struct MessageBubble: View {
    let message: MessageItem

    var body: some View {
        HStack(alignment: .top, spacing: 0) {
            if message.role == .user { Spacer(minLength: 48) }
            VStack(alignment: .leading, spacing: 8) {
                ForEach(Array(message.blocks.enumerated()), id: \.offset) { _, block in
                    MessageBlockView(block: block)
                }
            }
            .padding(message.role == .user ? 12 : 0)
            .padding(.leading, message.role == .user ? 0 : 4)
            .background(message.role == .user ? Theme.userBubble : Color.clear)
            .clipIf(message.role == .user) {
                $0.clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
            }
            if message.role != .user { Spacer(minLength: 48) }
        }
        .frame(maxWidth: .infinity, alignment: message.role == .user ? .trailing : .leading)
        .padding(.horizontal, 4)
    }
}

private struct MessageBlockView: View {
    let block: MessageBlock

    var body: some View {
        switch block {
        case .text(let text):
            MarkdownText(text: text)
                .font(Theme.body)
                .foregroundColor(Theme.messageText)
        case .code(let language, let content):
            CodeBlockView(language: language, content: content)
        case .toolCall(let summary):
            ToolChipView(summary: summary)
        }
    }
}

private struct MarkdownText: View {
    let text: String

    var body: some View {
        let lines = text.split(whereSeparator: \.isNewline).map(String.init)
        if looksLikeList(text) {
            VStack(alignment: .leading, spacing: 6) {
                ForEach(lines.indices, id: \.self) { index in
                    listLineView(lines[index])
                }
            }
        } else if text.count <= 4000, let attributed = try? AttributedString(markdown: text) {
            Text(attributed)
        } else {
            Text(text)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private func looksLikeList(_ text: String) -> Bool {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.hasPrefix("- ") || trimmed.hasPrefix("* ") {
            return true
        }
        if text.contains("\n- ") || text.contains("\n* ") {
            return true
        }
        if hasNumberedListPrefix(trimmed) {
            return true
        }
        if text.split(whereSeparator: \.isNewline).contains(where: { hasNumberedListPrefix(String($0)) }) {
            return true
        }
        return false
    }

    @ViewBuilder
    private func listLineView(_ line: String) -> some View {
        if line.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            Text(" ")
        } else {
            let parsed = parseListLine(line)
            if let marker = parsed.marker {
                HStack(alignment: .top, spacing: 8) {
                    Text(marker)
                        .font(Theme.body)
                        .foregroundColor(Theme.messageText)
                    inlineMarkdown(parsed.content)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(.leading, parsed.indent)
            } else {
                inlineMarkdown(parsed.content)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private func inlineMarkdown(_ content: String) -> Text {
        if content.count <= 4000, let attributed = try? AttributedString(markdown: content) {
            return Text(attributed)
        }
        return Text(content)
    }

    private func parseListLine(_ line: String) -> (indent: CGFloat, marker: String?, content: String) {
        let indentCount = line.prefix { $0 == " " || $0 == "\t" }.count
        let indent = CGFloat(indentCount) * 4
        let trimmed = line.trimmingCharacters(in: .whitespaces)

        if trimmed.hasPrefix("- ") || trimmed.hasPrefix("* ") {
            let start = trimmed.index(trimmed.startIndex, offsetBy: 2)
            return (indent, "•", String(trimmed[start...]))
        }

        if let numbered = parseNumberedListLine(trimmed) {
            return (indent, numbered.marker, numbered.content)
        }

        return (indent, nil, trimmed)
    }

    private func hasNumberedListPrefix(_ text: String) -> Bool {
        return parseNumberedListLine(text) != nil
    }

    private func parseNumberedListLine(_ text: String) -> (marker: String, content: String)? {
        var index = text.startIndex
        var hasDigits = false
        while index < text.endIndex, text[index].isNumber {
            hasDigits = true
            index = text.index(after: index)
        }
        guard hasDigits, index < text.endIndex, text[index] == "." else {
            return nil
        }
        let afterDot = text.index(after: index)
        guard afterDot < text.endIndex, text[afterDot] == " " else {
            return nil
        }
        let marker = String(text[text.startIndex...index])
        let contentStart = text.index(after: afterDot)
        let content = String(text[contentStart...])
        return (marker, content)
    }
}

private struct CodeBlockView: View {
    let language: String?
    let content: String

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let language, !language.isEmpty {
                Text(language)
                    .font(Theme.codeLabel)
                    .foregroundColor(Theme.accent)
            }
            Text(content)
                .font(Theme.codeFont)
                .foregroundColor(Theme.codeText)
        }
        .padding(12)
        .background(Theme.codeBackground)
        .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .stroke(Theme.codeBorder, lineWidth: 1)
        )
    }
}

private struct ToolChipView: View {
    let summary: ToolCallSummary

    var body: some View {
        if summary.icon == "sparkles" {
            ActivityChip(text: summary.title)
        } else {
        HStack(spacing: 6) {
            Image(systemName: summary.icon)
                .font(.system(size: 12, weight: .semibold))
            Text(summary.title)
                .font(Theme.caption)
                .lineLimit(1)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .background(Theme.toolChipBackground)
        .foregroundColor(Theme.toolChipText)
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
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

    var body: some View {
        HStack(spacing: 8) {
            StatusDot(color: status.dotColor, isPulsing: status.isPulsing, size: 7)
            Text(status.text)
                .font(Theme.caption)
                .foregroundColor(status.textColor)
            Spacer()
            if let activityText {
                ActivityChip(text: activityText)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 6)
    }
}

private struct ActivityChip: View {
    let text: String

    var body: some View {
        let label = stripTrailingEllipsis(text)
        HStack {
            Text(label)
                .font(Theme.caption)
                .foregroundColor(Theme.accent)
            AnimatedDots(color: Theme.accent)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .background(Theme.toolChipBackground)
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}

private func stripTrailingEllipsis(_ input: String) -> String {
    var value = input.trimmingCharacters(in: .whitespacesAndNewlines)
    if value.hasSuffix("...") {
        value = String(value.dropLast(3)).trimmingCharacters(in: .whitespacesAndNewlines)
    } else if value.hasSuffix("…") {
        value = String(value.dropLast(1)).trimmingCharacters(in: .whitespacesAndNewlines)
    }
    return value
}

private extension View {
    @ViewBuilder
    func clipIf(_ condition: Bool, transform: (Self) -> some View) -> some View {
        if condition {
            transform(self)
        } else {
            self
        }
    }
}

private struct AnimatedDots: View {
    let color: Color
    @State private var isAnimating = false

    var body: some View {
        HStack(spacing: 3) {
            ForEach(0..<3, id: \.self) { index in
                Circle()
                    .fill(color)
                    .frame(width: 4, height: 4)
                    .opacity(isAnimating ? 1.0 : 0.35)
                    .scaleEffect(isAnimating ? 1.1 : 0.9)
                    .animation(
                        .easeInOut(duration: 0.6).repeatForever(autoreverses: true).delay(Double(index) * 0.2),
                        value: isAnimating
                    )
            }
        }
        .onAppear {
            isAnimating = true
        }
        .onDisappear {
            isAnimating = false
        }
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
            TextField(
                placeholder,
                text: $model.messageText,
                axis: .vertical
            )
                .font(Theme.body)
                .padding(.horizontal, 12)
                .padding(.vertical, 10)
                .background(Color(uiColor: .secondarySystemBackground))
                .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
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

private struct SettingSection<Content: View>: View {
    let title: String
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(title)
                .font(Theme.sectionTitle)
            content
        }
        .padding()
        .background(Theme.cardBackground)
        .clipShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
        .shadow(color: Theme.shadow, radius: 8, x: 0, y: 4)
    }
}

private struct ActionButton: View {
    let title: String
    let systemImage: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Label(title, systemImage: systemImage)
                .font(.system(size: 15, weight: .semibold))
                .frame(maxWidth: .infinity)
                .padding(.vertical, 12)
                .background(Color(uiColor: .secondarySystemBackground))
                .clipShape(Capsule())
                .overlay(
                    Capsule()
                        .stroke(Color(uiColor: .separator).opacity(0.6), lineWidth: 1)
                )
        }
        .buttonStyle(.plain)
    }
}

private struct PillButtonStyle: ButtonStyle {
    let fill: Color

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 17, weight: .semibold))
            .frame(maxWidth: .infinity)
            .padding(.vertical, 14)
            .foregroundColor(.white)
            .background(Capsule().fill(fill))
            .opacity(configuration.isPressed ? 0.88 : 1.0)
            .scaleEffect(configuration.isPressed ? 0.99 : 1.0)
    }
}

private struct GhostButtonStyle: ButtonStyle {
    let tint: Color

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 17, weight: .semibold))
            .frame(maxWidth: .infinity)
            .padding(.vertical, 10)
            .foregroundColor(tint.opacity(configuration.isPressed ? 0.7 : 1.0))
    }
}

private struct FeatureListCard<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        VStack(spacing: 0) {
            content
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 4)
        .background(Theme.cardBackground)
        .clipShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
        .shadow(color: Theme.shadow, radius: 8, x: 0, y: 4)
    }
}

private struct SettingMenuRow: View {
    let title: String
    let subtitle: String
    let systemImage: String
    let tint: Color

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: systemImage)
                .font(.system(size: 26))
                .foregroundColor(tint)
            VStack(alignment: .leading, spacing: 4) {
                Text(title)
                    .font(Theme.body)
                    .foregroundColor(Theme.messageText)
                Text(subtitle)
                    .font(Theme.caption)
                    .foregroundColor(Theme.mutedText)
            }
            Spacer()
            Image(systemName: "chevron.right")
                .foregroundColor(Theme.mutedText)
        }
        .padding(.vertical, 12)
    }
}

private struct ResetKeysView: View {
    @ObservedObject var model: HarnessViewModel
    @State private var statusText: String = ""

    var body: some View {
        ZStack {
            Theme.background.ignoresSafeArea()
            List {
                Section("Reset Encryption Keys") {
                    Text("This will remove your current keys, disconnect the app, and require re-approving terminals.")
                        .font(Theme.caption)
                        .foregroundColor(Theme.mutedText)
                }
                Section {
                    Button {
                        model.resetKeys()
                        statusText = "Keys reset. Re-approve your terminal."
                    } label: {
                        Text("Reset Keys")
                            .foregroundColor(Theme.warning)
                    }
                }
                if !statusText.isEmpty {
                    Section {
                        Text(statusText)
                            .font(Theme.caption)
                            .foregroundColor(Theme.success)
                    }
                }
            }
            .scrollContentBackground(.hidden)
            .listStyle(.insetGrouped)
        }
        .navigationTitle("Reset Keys")
    }
}

private struct DebugView: View {
    @ObservedObject var model: HarnessViewModel

    var body: some View {
        let lines = model.logs.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        ZStack {
            Theme.background.ignoresSafeArea()
            GeometryReader { proxy in
                let logHeight = max(220, proxy.size.height * 0.45)
                List {
                    Section("Actions") {
                        DebugActionRow(title: "Copy Last Log", systemImage: "doc.on.doc") {
                            UIPasteboard.general.string = model.lastLogLine
                        }
                        DebugActionRow(title: "Clear Logs", systemImage: "trash", tint: Theme.warning) {
                            model.clearLogs()
                        }
                        if model.logServerRunning {
                            DebugActionRow(title: "Stop Log Server", systemImage: "stop.circle", tint: Theme.warning) {
                                model.stopLogServer()
                            }
                        } else {
                            DebugActionRow(title: "Start Log Server", systemImage: "dot.radiowaves.left.and.right") {
                                model.startLogServer()
                            }
                        }
                        DebugActionRow(title: "Copy Crash Log Path", systemImage: "link") {
                            UIPasteboard.general.string = model.crashLogPath
                        }
                        DebugActionRow(title: "Copy Crash Log Tail", systemImage: "doc.text") {
                            UIPasteboard.general.string = model.crashLogTail()
                        }
                    }
                    if model.logServerRunning {
                        Section("Log Server") {
                            Text(model.logServerURL)
                                .font(.system(.footnote, design: .monospaced))
                                .foregroundColor(Theme.messageText)
                                .textSelection(.enabled)
                            DebugActionRow(title: "Copy Log Server URL", systemImage: "doc.on.doc") {
                                UIPasteboard.general.string = model.logServerURL
                            }
                        }
                    }
                    Section("Recent Logs") {
                        ScrollViewReader { scrollProxy in
                            ScrollView {
                                LazyVStack(alignment: .leading, spacing: 6) {
                                    ForEach(Array(lines.enumerated()), id: \.offset) { index, line in
                                        Text(line.isEmpty ? " " : line)
                                            .font(.system(.footnote, design: .monospaced))
                                            .foregroundColor(Theme.messageText)
                                            .id(index)
                                    }
                                }
                                .frame(maxWidth: .infinity, alignment: .leading)
                            }
                            .frame(height: logHeight)
                            .padding(8)
                            .background(Theme.codeBackground)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                            .onAppear {
                                DispatchQueue.main.async {
                                    if let last = lines.indices.last {
                                        scrollProxy.scrollTo(last, anchor: .bottom)
                                    }
                                }
                            }
                        }
                    }
                }
                .scrollContentBackground(.hidden)
                .listStyle(.insetGrouped)
            }
        }
        .navigationTitle("Debug Logs")
    }
}

private struct DebugActionRow: View {
    let title: String
    let systemImage: String
    var tint: Color = Theme.messageText
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Image(systemName: systemImage)
                    .font(.system(size: 18, weight: .semibold))
                    .foregroundColor(tint)
                Text(title)
                    .font(Theme.body)
                    .foregroundColor(Theme.messageText)
                Spacer()
            }
            .padding(.vertical, 6)
        }
    }
}

private struct AccountDetailView: View {
    @ObservedObject var model: HarnessViewModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        ZStack {
            Theme.background.ignoresSafeArea()
            List {
                Section("Account Details") {
                    AccountDetailRow(title: "Public Key", value: model.publicKey)
                    AccountDetailRow(title: "Token", value: model.token)
                }
                Section {
                    Button {
                        model.showLogoutConfirm = true
                    } label: {
                        Text("Log Out")
                            .foregroundColor(Theme.warning)
                    }
                }
            }
            .scrollContentBackground(.hidden)
            .listStyle(.insetGrouped)
        }
        .navigationTitle("Account")
        .onChange(of: model.token) { newValue in
            if newValue.isEmpty {
                dismiss()
            }
        }
    }
}

private struct AppearanceDetailView: View {
    @ObservedObject var model: HarnessViewModel

    var body: some View {
        ZStack {
            Theme.background.ignoresSafeArea()
            List {
                Section("Appearance") {
                    Picker("Theme", selection: $model.appearanceMode) {
                        ForEach(AppearanceMode.allCases) { mode in
                            Text(mode.title).tag(mode)
                        }
                    }
                    .pickerStyle(.segmented)

                    Text("System follows your device appearance setting.")
                        .font(Theme.caption)
                        .foregroundColor(Theme.mutedText)
                }
            }
            .scrollContentBackground(.hidden)
            .listStyle(.insetGrouped)
        }
        .navigationTitle("Appearance")
    }
}

private struct MachineSummary: Identifiable {
    let id: String
    let host: String
    let flavor: String
    let online: Bool
    let daemonPid: Int?
    let daemonStateVersion: Int64?
    let daemonStatus: String?
    let sessions: [SessionSummary]
}

private struct MachineRow: View {
    let machine: MachineSummary

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: "desktopcomputer")
                .font(.system(size: 22))
                .foregroundColor(Theme.success)
            VStack(alignment: .leading, spacing: 4) {
                Text(machine.host)
                    .font(Theme.body)
                    .foregroundColor(Theme.messageText)
                Text("\(machine.flavor) • \(machine.online ? "online" : "offline")")
                    .font(Theme.caption)
                    .foregroundColor(Theme.mutedText)
            }
            Spacer()
            Image(systemName: "chevron.right")
                .foregroundColor(Theme.mutedText)
        }
        .padding(.vertical, 12)
    }
}

private struct MachineDetailView: View {
    @ObservedObject var model: HarnessViewModel
    let machine: MachineSummary
    @State private var customPath: String = ""

    var body: some View {
        ZStack {
            Theme.background.ignoresSafeArea()
            List {
                Section {
                    HStack(spacing: 10) {
                        Circle()
                            .fill(machine.online ? Theme.success : Theme.muted)
                            .frame(width: 8, height: 8)
                        Text(machine.online ? "online" : "offline")
                            .font(Theme.caption)
                            .foregroundColor(Theme.mutedText)
                    }
                }

                Section("Launch New Session in Directory") {
                    HStack {
                        TextField("Enter custom path", text: $customPath)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled(true)
                        Button {
                        } label: {
                            Image(systemName: "play.fill")
                                .foregroundColor(Theme.mutedText)
                        }
                        .disabled(true)
                    }
                    let knownPaths = Array(Set(machine.sessions.compactMap { $0.metadata?.path }))
                    if !knownPaths.isEmpty {
                        ForEach(knownPaths.sorted(), id: \.self) { path in
                            HStack(spacing: 8) {
                                Image(systemName: "folder")
                                    .foregroundColor(Theme.mutedText)
                                Text(path)
                                    .font(Theme.caption)
                                    .foregroundColor(Theme.messageText)
                            }
                        }
                    }
                }

                Section("Daemon") {
                    HStack {
                        Text("Status")
                        Spacer()
                        Text(machine.daemonStatus ?? (machine.online ? "likely alive" : "offline"))
                            .foregroundColor(machine.online ? Theme.success : Theme.mutedText)
                    }
                    Button("Stop Daemon") {}
                        .foregroundColor(Theme.warning)
                        .disabled(true)
                    HStack {
                        Text("Last Known PID")
                        Spacer()
                        Text(machine.daemonPid.map { String($0) } ?? "—")
                            .foregroundColor(Theme.mutedText)
                    }
                    HStack {
                        Text("Daemon State Version")
                        Spacer()
                        Text(machine.daemonStateVersion.map { String($0) } ?? "—")
                            .foregroundColor(Theme.mutedText)
                    }
                }

                Section("Previous Sessions (Up to 5 Most Recent)") {
                    let recent = machine.sessions.sorted(by: { $0.updatedAt > $1.updatedAt }).prefix(5)
                    if recent.isEmpty {
                        Text("No recent sessions.")
                            .font(Theme.caption)
                            .foregroundColor(Theme.mutedText)
                    } else {
                        ForEach(Array(recent)) { session in
                            NavigationLink {
                                TerminalDetailView(model: model, session: session)
                            } label: {
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(session.title ?? session.id)
                                        .font(Theme.body)
                                    Text(session.metadata?.path ?? "—")
                                        .font(Theme.caption)
                                        .foregroundColor(Theme.mutedText)
                                }
                            }
                        }
                    }
                }

                Section("Machine") {
                    HStack {
                        Text("Host")
                        Spacer()
                        Text(machine.host)
                            .foregroundColor(Theme.mutedText)
                    }
                    HStack {
                        Text("Flavor")
                        Spacer()
                        Text(machine.flavor)
                            .foregroundColor(Theme.mutedText)
                    }
                }
            }
            .scrollContentBackground(.hidden)
            .listStyle(.insetGrouped)
        }
        .navigationTitle(machine.host)
    }
}

private struct AccountDetailRow: View {
    let title: String
    let value: String

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title)
                .font(Theme.caption)
                .foregroundColor(Theme.mutedText)
            Text(value.isEmpty ? "—" : value)
                .font(.system(.footnote, design: .monospaced))
                .foregroundColor(Theme.messageText)
                .lineLimit(2)
                .textSelection(.enabled)
        }
        .padding(.vertical, 4)
    }
}

private func machineSummaries(from sessions: [SessionSummary], machines: [MachineInfo]) -> [MachineSummary] {
    let machinesById = Dictionary(uniqueKeysWithValues: machines.map { ($0.id, $0) })
    let grouped = Dictionary(grouping: sessions) { session in
        session.metadata?.machineId ?? session.metadata?.host ?? session.subtitle ?? "unknown"
    }

    var summaries: [MachineSummary] = []

    for (machineKey, groupedSessions) in grouped {
        let machineInfo = machinesById[machineKey]
        let host = machineInfo?.metadata?.host
            ?? groupedSessions.compactMap { $0.metadata?.host }.first
            ?? machineKey
        let flavor = groupedSessions.compactMap { $0.metadata?.flavor }.first
            ?? machineInfo?.metadata?.platform
            ?? "unknown"
        let online = machineInfo?.active ?? groupedSessions.contains(where: { $0.active })
        let daemonPid = machineInfo?.daemonState?.pid
        let daemonStateVersion = machineInfo?.daemonStateVersion
        let daemonStatus = machineInfo?.daemonState?.status
        let id = machineInfo?.id ?? machineKey
        summaries.append(
            MachineSummary(
                id: id,
                host: host,
                flavor: flavor,
                online: online,
                daemonPid: daemonPid,
                daemonStateVersion: daemonStateVersion,
                daemonStatus: daemonStatus,
                sessions: groupedSessions
            )
        )
    }

    for machine in machines where grouped[machine.id] == nil {
        let host = machine.metadata?.host ?? machine.id
        let flavor = machine.metadata?.platform ?? "unknown"
        summaries.append(
            MachineSummary(
                id: machine.id,
                host: host,
                flavor: flavor,
                online: machine.active,
                daemonPid: machine.daemonState?.pid,
                daemonStateVersion: machine.daemonStateVersion,
                daemonStatus: machine.daemonState?.status,
                sessions: []
            )
        )
    }

    return summaries.sorted(by: { $0.host < $1.host })
}

private struct SessionStatusInfo {
    let text: String
    let dotColor: Color
    let textColor: Color
    let isPulsing: Bool
}

private func statusInfo(for session: SessionSummary) -> SessionStatusInfo {
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
    if session.thinking {
        return SessionStatusInfo(
            text: "online",
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
        session.metadata?.host
            ?? session.metadata?.machineId
            ?? session.subtitle
            ?? "unknown"
    }
    return grouped
        .map { key, value in
            TerminalGroup(
                id: key,
                name: key,
                sessions: value.sorted(by: { ($0.title ?? $0.id) < ($1.title ?? $1.id) })
            )
        }
        .sorted(by: { $0.name < $1.name })
}

private enum Theme {
    static let accent = Color(red: 0.1, green: 0.45, blue: 0.6)
    static let success = Color(red: 0.16, green: 0.65, blue: 0.45)
    static let warning = Color(red: 0.9, green: 0.35, blue: 0.25)
    static let muted = Color.secondary.opacity(0.8)
    static let mutedText = Color.secondary
    static let shadow = Color.black.opacity(0.12)

    static let background = LinearGradient(
        colors: [
            Color(uiColor: .systemGroupedBackground),
            Color(uiColor: .secondarySystemGroupedBackground)
        ],
        startPoint: .topLeading,
        endPoint: .bottomTrailing
    )

    static let cardBackground = Color(uiColor: .secondarySystemGroupedBackground)
    static let cardGradient = LinearGradient(
        colors: [
            Color(uiColor: .secondarySystemGroupedBackground),
            Color(uiColor: .tertiarySystemGroupedBackground)
        ],
        startPoint: .topLeading,
        endPoint: .bottomTrailing
    )

    static let title = Font.custom("AvenirNext-Bold", size: 24)
    static let sectionTitle = Font.custom("AvenirNext-DemiBold", size: 18)
    static let body = Font.custom("AvenirNext-Regular", size: 16)
    static let caption = Font.custom("AvenirNext-Medium", size: 12)
    static let codeFont = Font.system(size: 14, weight: .regular, design: .monospaced)
    static let codeLabel = Font.system(size: 11, weight: .semibold, design: .monospaced)

    static let messageText = Color.primary
    static let userBubble = Color(uiColor: .tertiarySystemFill)
    static let toolChipBackground = Color(uiColor: .tertiarySystemGroupedBackground)
    static let toolChipText = Color.primary
    static let codeBackground = Color(uiColor: .secondarySystemBackground)
    static let codeBorder = Color(uiColor: .separator)
    static let codeText = Color.primary
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
