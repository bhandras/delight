import SwiftUI
import UIKit

/// ActiveSheet identifies which full-screen sheet the root ContentView should present.
enum ActiveSheet: Identifiable {
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

/// CrashReportSheet displays the last crash report captured by CrashLogger.
struct CrashReportSheet: View {
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

/// AccountCreatedSheet displays the account receipt after creating a new account.
struct AccountCreatedSheet: View {
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

/// TerminalPairingReceiptSheet displays the receipt after approving a terminal QR URL.
struct TerminalPairingReceiptSheet: View {
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
                        Section("Terminal") {
                            CopyableValueRow(title: "Host", value: receipt.host ?? "Not available yet")
                            CopyableValueRow(title: "Terminal ID", value: receipt.terminalID ?? "Not available yet")
                            Text("Host and Terminal ID are only known once the terminal connects and reports its metadata.")
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

/// LogoutConfirmSheet confirms logging out and clearing the token/session state.
struct LogoutConfirmSheet: View {
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
                    Text("This will remove local session state and hide terminals until you create an account again.")
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

/// PermissionPromptSheet renders the current permission request and allows the user
/// to approve or deny tool usage.
struct PermissionPromptSheet: View {
    @ObservedObject var model: HarnessViewModel
    let onDismiss: () -> Void

    @State private var message: String = ""

    /// infoRow renders a two-column row inside the permission request card.
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
	            .dismissKeyboardOnTap()
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
