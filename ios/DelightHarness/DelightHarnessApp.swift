import SwiftUI

@main
struct DelightHarnessApp: App {
    init() {
        CrashLogger.setup()
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
        }
    }
}
