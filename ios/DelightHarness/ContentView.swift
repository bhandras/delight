import SwiftUI

struct ContentView: View {
    @StateObject private var model = HarnessViewModel()
    @State private var showScanner = false

    var body: some View {
        NavigationView {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    GroupBox(label: Text("Connection")) {
                        VStack(alignment: .leading, spacing: 8) {
                            TextField("Server URL", text: $model.serverURL)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled(true)
                                .textFieldStyle(.roundedBorder)
                            Text("Status: \(model.status)")
                                .font(.caption)
                            HStack {
                                Button("Connect") { model.connect() }
                                Button("Disconnect") { model.disconnect() }
                            }
                        }
                        .padding(.vertical, 4)
                    }

                    GroupBox(label: Text("Auth")) {
                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Button("Generate Keys") { model.generateKeys() }
                                Button("Auth") { model.authWithKeypair() }
                            }
                            TextField("Public Key (base64)", text: $model.publicKey)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled(true)
                                .textFieldStyle(.roundedBorder)
                            TextField("Private Key (base64)", text: $model.privateKey)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled(true)
                                .textFieldStyle(.roundedBorder)
                            TextField("Token", text: $model.token)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled(true)
                                .textFieldStyle(.roundedBorder)
                            TextField("Master Key (base64)", text: $model.masterKey)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled(true)
                                .textFieldStyle(.roundedBorder)
                        }
                        .padding(.vertical, 4)
                    }

                    GroupBox(label: Text("Pair Terminal")) {
                        VStack(alignment: .leading, spacing: 8) {
                            TextField("QR URL (delight://terminal?...)", text: $model.terminalURL)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled(true)
                                .textFieldStyle(.roundedBorder)
                            HStack {
                                Button("Scan QR") { showScanner = true }
                                Button("Approve Terminal") { model.approveTerminal() }
                            }
                        }
                        .padding(.vertical, 4)
                    }

                    GroupBox(label: Text("Sessions")) {
                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Button("List Sessions") { model.listSessions() }
                                Button("Fetch Messages") { model.fetchMessages() }
                            }
                            TextField("Session ID", text: $model.sessionID)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled(true)
                                .textFieldStyle(.roundedBorder)

                            if !model.sessions.isEmpty {
                                VStack(alignment: .leading, spacing: 6) {
                                    ForEach(model.sessions) { session in
                                        VStack(alignment: .leading, spacing: 4) {
                                            Text(session.id)
                                                .font(.footnote)
                                                .textSelection(.enabled)
                                            Text(session.statusText)
                                                .font(.caption)
                                                .foregroundColor(.secondary)
                                            Button("Use Session") {
                                                model.sessionID = session.id
                                            }
                                            .font(.caption)
                                        }
                                        Divider()
                                    }
                                }
                            }
                        }
                        .padding(.vertical, 4)
                    }

                    GroupBox(label: Text("Send Message")) {
                        VStack(alignment: .leading, spacing: 8) {
                            TextField("Message", text: $model.messageText)
                                .textInputAutocapitalization(.sentences)
                                .textFieldStyle(.roundedBorder)
                            Button("Send") { model.sendMessage() }
                        }
                        .padding(.vertical, 4)
                    }

                    GroupBox(label: Text("Logs")) {
                        TextEditor(text: $model.logs)
                            .frame(minHeight: 200)
                            .font(.system(.footnote, design: .monospaced))
                    }
                }
                .padding()
            }
            .navigationTitle("Delight Harness")
        }
        .sheet(isPresented: $showScanner) {
            QRScannerView { result in
                model.terminalURL = result
                showScanner = false
            }
        }
    }
}
