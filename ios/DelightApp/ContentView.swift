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
                            FeatureListCard {
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
                                    AccountDetailView(model: model)
                                } label: {
                                    SettingMenuRow(
                                        title: "Account",
                                        subtitle: "Server URL and login",
                                        systemImage: "person.circle",
                                        tint: Color(red: 0.18, green: 0.52, blue: 0.96)
                                    )
                                }
                                if isLoggedIn {
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

private struct ParsedConnectionStatus: Equatable {
    let isConnected: Bool
    let headline: String
    let detail: String?
    let tint: Color
}

/// parseConnectionStatus normalizes the SDK status string into something we can
/// render consistently.
private func parseConnectionStatus(_ value: String) -> ParsedConnectionStatus {
    let normalized = value.trimmingCharacters(in: .whitespacesAndNewlines)
    if normalized.lowercased() == "connected" {
        return ParsedConnectionStatus(
            isConnected: true,
            headline: "Connected",
            detail: nil,
            tint: Theme.success
        )
    }
    if normalized.lowercased().hasPrefix("disconnected") {
        return ParsedConnectionStatus(
            isConnected: false,
            headline: "Disconnected",
            detail: nil,
            tint: Theme.danger
        )
    }
    if normalized.isEmpty {
        return ParsedConnectionStatus(
            isConnected: false,
            headline: "Unknown",
            detail: nil,
            tint: Theme.mutedText
        )
    }
    return ParsedConnectionStatus(
        isConnected: false,
        headline: "Unknown",
        detail: normalized,
        tint: Theme.mutedText
    )
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
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    FeatureListCard {
                        VStack(alignment: .leading, spacing: 14) {
                            Text("Reset Encryption Keys")
                                .font(Theme.sectionTitle)
                                .foregroundColor(Theme.messageText)
                                .padding(.vertical, 6)

                            Text("This will remove your current keys, disconnect the app, and require re-approving terminals.")
                                .font(Theme.body)
                                .foregroundColor(Theme.mutedText)

                            Divider()

                            ActionButton(title: "Reset Keys", systemImage: "key.fill") {
                                model.resetKeys()
                                statusText = "Keys reset. Re-approve your terminal."
                            }
                            .foregroundColor(Theme.warning)

                            if !statusText.isEmpty {
                                Divider()
                                Text(statusText)
                                    .font(Theme.caption)
                                    .foregroundColor(Theme.success)
                            }
                        }
                        .padding(.vertical, 8)
                    }
                }
                .padding()
            }
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
    @FocusState private var isServerFieldFocused: Bool

    var body: some View {
        let isLoggedIn = !model.token.isEmpty
        let parsedStatus = parseConnectionStatus(model.status)
        let trimmedURL = model.serverURL.trimmingCharacters(in: .whitespacesAndNewlines)
        let canConnect = !trimmedURL.isEmpty && !parsedStatus.isConnected

        ZStack {
            Theme.background.ignoresSafeArea()
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    FeatureListCard {
                        VStack(alignment: .leading, spacing: 14) {
                            VStack(alignment: .leading, spacing: 8) {
                                Text("Server URL")
                                    .font(Theme.caption)
                                    .foregroundColor(Theme.mutedText)
                                TextField("http://localhost:3005", text: $model.serverURL)
                                    .textInputAutocapitalization(.never)
                                    .autocorrectionDisabled(true)
                                    .textContentType(.URL)
                                    .keyboardType(.URL)
                                    .submitLabel(.done)
                                    .focused($isServerFieldFocused)
                                    .onSubmit {
                                        isServerFieldFocused = false
                                    }
                                    .textFieldStyle(.roundedBorder)
                            }

                            if parsedStatus.isConnected {
                                ActionButton(title: "Disconnect", systemImage: "pause.circle.fill") {
                                    model.disconnect()
                                }
                                .tint(Theme.muted)
                            } else {
                                ActionButton(title: "Connect", systemImage: "bolt.horizontal.circle.fill") {
                                    model.connect()
                                }
                                .disabled(!canConnect)
                            }

                            HStack(spacing: 10) {
                                Circle()
                                    .fill(parsedStatus.tint)
                                    .frame(width: 10, height: 10)
                                Text(parsedStatus.headline)
                                    .font(Theme.caption)
                                    .foregroundColor(Theme.mutedText)
                                Spacer()
                            }
                        }
                        .padding(.vertical, 8)
                    }

                    if isLoggedIn {
                        FeatureListCard {
                            VStack(alignment: .leading, spacing: 14) {
                                Text("Account Details")
                                    .font(Theme.sectionTitle)
                                    .foregroundColor(Theme.messageText)
                                    .padding(.vertical, 6)
                                Divider()
                                AccountDetailRow(title: "Public Key", value: model.publicKey)
                                Divider()
                                AccountDetailRow(title: "Token", value: model.token)
                                Divider()
                                ActionButton(
                                    title: "Log Out",
                                    systemImage: "rectangle.portrait.and.arrow.right"
                                ) {
                                    model.showLogoutConfirm = true
                                }
                                .padding(.bottom, 8)
                            }
                        }
                    } else {
                        FeatureListCard {
                            VStack(alignment: .leading, spacing: 14) {
                                Text("Account Details")
                                    .font(Theme.sectionTitle)
                                    .foregroundColor(Theme.messageText)
                                    .padding(.vertical, 6)
                                Divider()
                                AccountDetailRow(title: "Public Key", value: "")
                                Divider()
                                AccountDetailRow(title: "Token", value: "")
                                Divider()
                                Text("Not logged in. Set your server URL and connect to create an account.")
                                    .font(Theme.body)
                                    .foregroundColor(Theme.mutedText)
                                    .padding(.vertical, 6)
                            }
                        }
                    }
                }
                .padding()
            }
        }
        .navigationTitle("Account")
        .toolbar {
            ToolbarItemGroup(placement: .keyboard) {
                Spacer()
                Button("Done") {
                    isServerFieldFocused = false
                }
            }
        }
        .onChange(of: model.token) { newValue in
            if newValue.isEmpty {
                dismiss()
            }
        }
    }
}

private struct AppearanceDetailView: View {
    @ObservedObject var model: HarnessViewModel

    /// verboseBinding maps the boolean toggle to the stored detail level enum.
    private var verboseBinding: Binding<Bool> {
        Binding(
            get: { model.transcriptDetailLevel == .full },
            set: { enabled in
                model.transcriptDetailLevel = enabled ? .full : .brief
            }
        )
    }

    var body: some View {
        ZStack {
            Theme.background.ignoresSafeArea()
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    FeatureListCard {
                        NavigationLink {
                            AppearanceModeSelectionView(model: model)
                        } label: {
                            SettingValueRow(
                                title: "Appearance",
                                subtitle: appearanceSubtitle(for: model.appearanceMode),
                                systemImage: "circle.lefthalf.filled",
                                tint: Theme.accent,
                                value: appearanceValue(for: model.appearanceMode)
                            )
                        }
                        Divider()
                        NavigationLink {
                            TerminalTextSizeView(model: model)
                        } label: {
                            SettingValueRow(
                                title: "Text Size",
                                subtitle: "Terminal transcript",
                                systemImage: "textformat.size",
                                tint: Color(red: 0.4, green: 0.36, blue: 0.9),
                                value: "\(Int(model.terminalFontSize))"
                            )
                        }
                        Divider()
                        SettingToggleRow(
                            title: "Verbose transcript",
                            subtitle: "Show tool and thinking logs",
                            systemImage: "text.bubble",
                            tint: Theme.accent,
                            isOn: verboseBinding
                        )
                    }
                    .padding(.horizontal, 16)
                    .padding(.top, 12)
                }
            }
        }
        .navigationTitle("Appearance")
    }

    /// appearanceValue renders the short label used by the row's trailing value.
    private func appearanceValue(for mode: AppearanceMode) -> String {
        switch mode {
        case .system:
            return "Adaptive"
        case .light:
            return "Light"
        case .dark:
            return "Dark"
        }
    }

    /// appearanceSubtitle renders the row subtitle based on the chosen mode.
    private func appearanceSubtitle(for mode: AppearanceMode) -> String {
        switch mode {
        case .system:
            return "Match system settings"
        case .light:
            return "Always use light mode"
        case .dark:
            return "Always use dark mode"
        }
    }
}

struct TerminalTextSizeView: View {
    @ObservedObject var model: HarnessViewModel

    var body: some View {
        ZStack {
            Theme.background.ignoresSafeArea()
            VStack(alignment: .leading, spacing: 16) {
                FeatureListCard {
                    VStack(alignment: .leading, spacing: 12) {
                        HStack {
                            Text("Text Size")
                                .font(Theme.body)
                                .foregroundColor(Theme.messageText)
                            Spacer()
                            Text("\(Int(model.terminalFontSize))")
                                .font(Theme.caption)
                                .foregroundColor(Theme.mutedText)
                        }
                        Slider(
                            value: $model.terminalFontSize,
                            in: TerminalAppearance.minFontSize...TerminalAppearance.maxFontSize,
                            step: TerminalAppearance.fontSizeStep
                        )
                        VStack(alignment: .leading, spacing: 10) {
                            Text("Preview")
                                .font(Theme.caption)
                                .foregroundColor(Theme.mutedText)
                            Text("The quick brown fox jumps over the lazy dog.")
                                .font(.custom("AvenirNext-Regular", size: CGFloat(model.terminalFontSize)))
                                .foregroundColor(Theme.messageText)
                            Text("echo \"hello\"")
                                .font(.system(size: CGFloat(TerminalAppearance.codeFontSize(for: model.terminalFontSize)), weight: .regular, design: .monospaced))
                                .foregroundColor(Theme.codeText)
                                .padding(10)
                                .background(Theme.codeBackground)
                                .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                        }
                    }
                    .padding(.vertical, 8)
                }
                .padding(.horizontal, 16)
                Spacer()
            }
            .padding(.top, 12)
        }
        .navigationTitle("Text Size")
        .navigationBarTitleDisplayMode(.inline)
    }
}

private struct SettingValueRow: View {
    let title: String
    let subtitle: String
    let systemImage: String
    let tint: Color
    let value: String

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
            Text(value)
                .foregroundColor(Theme.mutedText)
            Image(systemName: "chevron.right")
                .foregroundColor(Theme.mutedText)
        }
        .padding(.vertical, 12)
    }
}

private struct SettingToggleRow: View {
    let title: String
    let subtitle: String
    let systemImage: String
    let tint: Color
    let isOn: Binding<Bool>

    var body: some View {
        Toggle(isOn: isOn) {
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
            }
            .padding(.vertical, 12)
        }
        .tint(Theme.accent)
    }
}

private struct AppearanceModeSelectionView: View {
    @ObservedObject var model: HarnessViewModel

    var body: some View {
        ZStack {
            Theme.background.ignoresSafeArea()
            List {
                Section {
                    AppearanceModeOptionRow(title: "Adaptive", subtitle: "Match system settings", isSelected: model.appearanceMode == .system) {
                        model.appearanceMode = .system
                    }
                    AppearanceModeOptionRow(title: "Light", subtitle: "Always use light mode", isSelected: model.appearanceMode == .light) {
                        model.appearanceMode = .light
                    }
                    AppearanceModeOptionRow(title: "Dark", subtitle: "Always use dark mode", isSelected: model.appearanceMode == .dark) {
                        model.appearanceMode = .dark
                    }
                }
            }
            .scrollContentBackground(.hidden)
            .listStyle(.insetGrouped)
        }
        .navigationTitle("Appearance")
        .navigationBarTitleDisplayMode(.inline)
    }
}

private struct AppearanceModeOptionRow: View {
    let title: String
    let subtitle: String
    let isSelected: Bool
    let onSelect: () -> Void

    var body: some View {
        Button(action: onSelect) {
            HStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(title)
                        .font(Theme.body)
                        .foregroundColor(Theme.messageText)
                    Text(subtitle)
                        .font(Theme.caption)
                        .foregroundColor(Theme.mutedText)
                }
                Spacer()
                if isSelected {
                    Image(systemName: "checkmark")
                        .font(.system(size: 16, weight: .semibold))
                        .foregroundColor(Theme.accent)
                }
            }
            .padding(.vertical, 4)
        }
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

enum Theme {
    static let accent = Color(red: 0.1, green: 0.45, blue: 0.6)
    static let success = Color(red: 0.16, green: 0.65, blue: 0.45)
    static let danger = Color(red: 0.9, green: 0.25, blue: 0.25)
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
