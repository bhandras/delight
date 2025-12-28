import SwiftUI

struct ContentView: View {
    @StateObject private var model = HarnessViewModel()
    @State private var showScanner = false

    var body: some View {
        TabView {
            TerminalsView(model: model)
                .tabItem {
                    Image(systemName: "terminal")
                    Text("Terminals")
                }

            SettingsView(model: model, showScanner: $showScanner)
                .tabItem {
                    Image(systemName: "slider.horizontal.3")
                    Text("Settings")
                }
        }
        .tint(Theme.accent)
        .onAppear {
            model.startup()
        }
        .sheet(isPresented: $showScanner) {
            QRScannerView { result in
                model.terminalURL = result
                showScanner = false
            }
        }
    }
}

private struct TerminalsView: View {
    @ObservedObject var model: HarnessViewModel

    var body: some View {
        NavigationStack {
            ZStack {
                Theme.background.ignoresSafeArea()
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        HStack {
                            Text("MACHINES")
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
                                            .stroke(Color.black.opacity(0.06), lineWidth: 1)
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
                    .padding()
                }
            }
            .navigationTitle("Terminals")
        }
        .onAppear {
            model.listSessions()
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
                                        AppearanceDetailView()
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
                                ActionButton(title: "Create Account", systemImage: "person.crop.circle.badge.plus") {
                                    model.createAccount()
                                }
                            }
                        }

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

                        SettingSection(title: "Pair Terminal") {
                            TextField("QR URL (delight://terminal?...)", text: $model.terminalURL)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled(true)
                                .textFieldStyle(.roundedBorder)
                            HStack(spacing: 12) {
                                ActionButton(title: "Scan QR", systemImage: "qrcode.viewfinder") {
                                    showScanner = true
                                }
                                ActionButton(title: "Approve", systemImage: "checkmark.seal") {
                                    model.approveTerminal()
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
                .overlay(Circle().stroke(Color.white, lineWidth: 2))
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
        .background(Color.white.opacity(0.7))
        .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
    }
}

private struct TerminalDetailView: View {
    @ObservedObject var model: HarnessViewModel
    let session: SessionSummary

    var body: some View {
        ZStack {
            Theme.background.ignoresSafeArea()
            VStack(spacing: 0) {
                if session.agentState?.controlledByUser == true {
                    ControlStatusBanner()
                }
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 12) {
                            ForEach(Array(model.messages.enumerated()), id: \.offset) { _, message in
                                MessageBubble(message: message)
                                    .id(message.id)
                            }
                        }
                        .padding()
                    }
                    .onChange(of: model.messages.count) { _ in
                        if let last = model.messages.last {
                            proxy.scrollTo(last.id, anchor: .bottom)
                        }
                    }
                }

                ConnectionStatusRow(status: statusInfo(for: session), activityText: session.thinking ? vibingMessage(for: session.id) : nil)
                    .background(Color.white.opacity(0.9))
                MessageComposer(model: model)
                    .background(Color.white.opacity(0.9))
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
            model.selectSession(session.id)
        }
    }
}

private struct MessageBubble: View {
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
    var body: some View {
        HStack(spacing: 8) {
            StatusDot(color: Theme.success, isPulsing: false, size: 7)
            Text("terminal control - permission prompts are not displayed")
                .font(Theme.caption)
                .foregroundColor(Theme.success)
            Spacer()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .background(Color.white.opacity(0.85))
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

    var body: some View {
        HStack(spacing: 12) {
            TextField("Type a message...", text: $model.messageText, axis: .vertical)
                .font(Theme.body)
                .padding(.horizontal, 12)
                .padding(.vertical, 10)
                .background(Color.white)
                .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
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
                .font(Theme.caption)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 10)
                .background(Color.white.opacity(0.9))
                .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
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
        .background(Color.white.opacity(0.95))
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
                            .background(Color.white.opacity(0.6))
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
                        model.logout()
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
    }
}

private struct AppearanceDetailView: View {
    var body: some View {
        ZStack {
            Theme.background.ignoresSafeArea()
            List {
                Section("Appearance") {
                    Text("Coming soon.")
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
            text: vibingMessage(for: session.id),
            dotColor: Theme.accent,
            textColor: Theme.accent,
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
    static let muted = Color.gray.opacity(0.6)
    static let mutedText = Color.gray.opacity(0.7)
    static let shadow = Color.black.opacity(0.08)

    static let background = LinearGradient(
        colors: [
            Color(red: 0.95, green: 0.95, blue: 0.98),
            Color(red: 0.88, green: 0.94, blue: 0.97)
        ],
        startPoint: .topLeading,
        endPoint: .bottomTrailing
    )

    static let cardBackground = Color.white.opacity(0.85)
    static let cardGradient = LinearGradient(
        colors: [
            Color.white,
            Color(red: 0.85, green: 0.95, blue: 0.98)
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

    static let messageText = Color(red: 0.12, green: 0.12, blue: 0.12)
    static let userBubble = Color(red: 0.92, green: 0.91, blue: 0.86)
    static let toolChipBackground = Color(red: 0.94, green: 0.94, blue: 0.94)
    static let toolChipText = Color(red: 0.15, green: 0.15, blue: 0.15)
    static let codeBackground = Color(red: 0.95, green: 0.95, blue: 0.95)
    static let codeBorder = Color(red: 0.9, green: 0.9, blue: 0.9)
    static let codeText = Color(red: 0.18, green: 0.18, blue: 0.18)
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
