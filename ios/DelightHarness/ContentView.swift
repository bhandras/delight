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

struct LoggedOutTerminalEmptyState: View {
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

struct PairTerminalForm: View {
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

struct CopyableValueRow: View {
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
struct SettingSection<Content: View>: View {
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

struct ActionButton: View {
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

struct PillButtonStyle: ButtonStyle {
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

struct GhostButtonStyle: ButtonStyle {
    let tint: Color

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 17, weight: .semibold))
            .frame(maxWidth: .infinity)
            .padding(.vertical, 10)
            .foregroundColor(tint.opacity(configuration.isPressed ? 0.7 : 1.0))
    }
}

struct FeatureListCard<Content: View>: View {
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

enum Theme {
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
