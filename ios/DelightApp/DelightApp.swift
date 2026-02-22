import Foundation
import SwiftUI
import UIKit
import UserNotifications
import CryptoKit

@main
struct DelightApp: App {
    @UIApplicationDelegateAdaptor(DelightAppDelegate.self) private var appDelegate

    init() {
        _ = appDelegate
        CrashLogger.setup()
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
        }
    }
}

/// DelightPushManager owns APNs registration, token upload, and payload decrypt.
final class DelightPushManager {
    static let shared = DelightPushManager()

    private init() {}

    private enum Constants {
        static let settingsKeyPrefix = "delight.harness."
        static let serverURLKey = settingsKeyPrefix + "serverURL"
        static let tokenKey = settingsKeyPrefix + "token"
        static let storedDeviceTokenKey = settingsKeyPrefix + "pushDeviceToken"
        static let pushKeyUsage = "Delight Push"
        static let pushKeyPath = "notifications"
    }

    /// registerDeviceToken stores the APNs token and attempts server registration.
    func registerDeviceToken(_ tokenHex: String) {
        let trimmed = tokenHex.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        UserDefaults.standard.set(trimmed, forKey: Constants.storedDeviceTokenKey)
        registerStoredTokenIfPossible()
    }

    /// registerStoredTokenIfPossible retries push-token upload if auth is ready.
    func registerStoredTokenIfPossible() {
        let defaults = UserDefaults.standard
        guard let token = defaults.string(forKey: Constants.storedDeviceTokenKey), !token.isEmpty else {
            return
        }
        guard let serverURL = defaults.string(forKey: Constants.serverURLKey), !serverURL.isEmpty else {
            return
        }
        guard let authToken = defaults.string(forKey: Constants.tokenKey), !authToken.isEmpty else {
            return
        }

        guard let url = URL(string: serverURL.trimmingCharacters(in: .whitespacesAndNewlines) + "/v1/push-tokens") else {
            return
        }

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("Bearer \(authToken)", forHTTPHeaderField: "Authorization")
        request.timeoutInterval = 8

        let body = ["token": token]
        request.httpBody = try? JSONSerialization.data(withJSONObject: body)

        URLSession.shared.dataTask(with: request).resume()
    }

    /// handleEncryptedCiphertext decrypts ciphertext and posts a local alert.
    func handleEncryptedCiphertext(_ ciphertextB64: String) {
        guard let payload = decryptPayload(ciphertextB64) else {
            return
        }
        postLocalNotification(payload)
    }

    /// decryptPayload decrypts an encrypted push payload using the master key.
    private func decryptPayload(_ ciphertextB64: String) -> PushPayload? {
        guard let masterKeyB64 = KeychainStore.string(for: "masterKey"),
              let masterData = Data(base64Encoded: masterKeyB64),
              let key = derivePushKey(master: masterData),
              let raw = Data(base64Encoded: ciphertextB64),
              raw.count >= 1 + 12 + 16,
              raw.first == 0 else {
            return nil
        }

        let nonceData = raw.subdata(in: 1..<13)
        let combined = raw.subdata(in: 13..<raw.count)
        guard combined.count > 16 else { return nil }
        let cipherData = Data(combined.prefix(combined.count - 16))
        let tagData = Data(combined.suffix(16))

        guard let nonce = try? AES.GCM.Nonce(data: nonceData),
              let sealed = try? AES.GCM.SealedBox(nonce: nonce, ciphertext: cipherData, tag: tagData),
              let plaintext = try? AES.GCM.open(sealed, using: SymmetricKey(data: key)),
              let decoded = try? JSONDecoder().decode(PushPayload.self, from: plaintext) else {
            return nil
        }

        return decoded
    }

    /// derivePushKey derives the deterministic push key from the master key.
    private func derivePushKey(master: Data) -> Data? {
        guard let usageData = (Constants.pushKeyUsage + " Master Seed").data(using: .utf8) else {
            return nil
        }
        let root = hmacSHA512(key: usageData, message: master)
        guard root.count == 64 else { return nil }

        let chainCode = root.suffix(32)

        guard let indexData = Constants.pushKeyPath.data(using: .utf8) else {
            return nil
        }

        var childInput = Data([0x00])
        childInput.append(indexData)

        let child = hmacSHA512(key: Data(chainCode), message: childInput)
        guard child.count == 64 else { return nil }
        return Data(child.prefix(32))
    }

    /// hmacSHA512 computes HMAC-SHA512(key, message).
    private func hmacSHA512(key: Data, message: Data) -> Data {
        let mac = HMAC<SHA512>.authenticationCode(for: message, using: SymmetricKey(data: key))
        return Data(mac)
    }

    /// postLocalNotification renders a decrypted push payload for the user.
    private func postLocalNotification(_ payload: PushPayload) {
        let content = UNMutableNotificationContent()
        content.sound = .default
        content.title = notificationTitle(for: payload.event)
        content.body = notificationBody(for: payload)
        content.userInfo = [
            "event": payload.event,
            "sessionId": payload.sessionID ?? "",
            "terminalId": payload.terminalID ?? ""
        ]

        let requestID = "delight.push.\(payload.timestamp).\(UUID().uuidString)"
        let request = UNNotificationRequest(identifier: requestID, content: content, trigger: nil)
        UNUserNotificationCenter.current().add(request)
    }

    /// notificationTitle maps push event keys to user-facing titles.
    private func notificationTitle(for event: String) -> String {
        switch event {
        case "attention":
            return "Delight: Needs attention"
        case "turn-complete":
            return "Delight: Turn finished"
        default:
            return "Delight update"
        }
    }

    /// notificationBody builds a compact context string from decrypted metadata.
    private func notificationBody(for payload: PushPayload) -> String {
        var parts: [String] = []
        let trimmedLabel = payload.label.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedLabel.isEmpty {
            parts.append(trimmedLabel)
        }
        let trimmedAgent = payload.agent.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedAgent.isEmpty {
            parts.append(trimmedAgent)
        }
        let trimmedHost = payload.host.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedHost.isEmpty {
            parts.append(trimmedHost)
        }
        let trimmedPath = payload.path.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedPath.isEmpty {
            parts.append(trimmedPath)
        }
        if let toolName = payload.toolName?.trimmingCharacters(in: .whitespacesAndNewlines), !toolName.isEmpty {
            parts.append(toolName)
        }
        if parts.isEmpty {
            return "Open Delight for details"
        }
        return parts.joined(separator: " | ")
    }
}

/// PushPayload is the decrypted push payload schema sent by the CLI.
private struct PushPayload: Decodable {
    let version: Int
    let event: String
    let agent: String
    let host: String
    let path: String
    let label: String
    let sessionID: String?
    let sessionTag: String?
    let terminalID: String?
    let toolName: String?
    let timestamp: Int64

    private enum CodingKeys: String, CodingKey {
        case version
        case event
        case agent
        case host
        case path
        case label
        case sessionID = "sessionId"
        case sessionTag
        case terminalID = "terminalId"
        case toolName
        case timestamp
    }
}

/// DelightAppDelegate wires APNs callbacks into DelightPushManager.
final class DelightAppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    /// application sets up notification permissions and delegate wiring.
    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        _ = launchOptions
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        center.requestAuthorization(options: [.alert, .badge, .sound]) { _, _ in
            DispatchQueue.main.async {
                application.registerForRemoteNotifications()
                DelightPushManager.shared.registerStoredTokenIfPossible()
            }
        }
        return true
    }

    /// application retries push-token upload whenever the app becomes active.
    func applicationDidBecomeActive(_ application: UIApplication) {
        _ = application
        DelightPushManager.shared.registerStoredTokenIfPossible()
    }

    /// application stores the APNs device token and uploads it to the server.
    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        _ = application
        let tokenHex = deviceToken.map { String(format: "%02x", $0) }.joined()
        DelightPushManager.shared.registerDeviceToken(tokenHex)
    }

    /// application handles encrypted push payloads delivered in background mode.
    func application(
        _ application: UIApplication,
        didReceiveRemoteNotification userInfo: [AnyHashable: Any],
        fetchCompletionHandler completionHandler: @escaping (UIBackgroundFetchResult) -> Void
    ) {
        _ = application
        if let ciphertext = extractCiphertext(from: userInfo) {
            DelightPushManager.shared.handleEncryptedCiphertext(ciphertext)
            completionHandler(.newData)
            return
        }
        completionHandler(.noData)
    }

    /// userNotificationCenter keeps notifications visible while app is foregrounded.
    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        _ = center
        _ = notification
        completionHandler([.banner, .list, .sound])
    }

    /// extractCiphertext reads the encrypted payload from APNs userInfo.
    private func extractCiphertext(from userInfo: [AnyHashable: Any]) -> String? {
        guard let delight = userInfo["delight"] as? [String: Any],
              let ciphertext = delight["ciphertext"] as? String else {
            return nil
        }
        return ciphertext
    }
}
