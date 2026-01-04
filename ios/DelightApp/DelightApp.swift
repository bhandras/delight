import SwiftUI

@main
struct DelightApp: App {
    init() {
        CrashLogger.setup()
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
        }
    }
}
