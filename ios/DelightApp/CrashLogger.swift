import Foundation
import Darwin
import UIKit

enum CrashLogger {
    static let logURL: URL = {
        let dir = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first!
        return dir.appendingPathComponent("delight_harness_crash.log")
    }()

    private static var retainedHandle: FileHandle?
    private static let runningFlagKey = "delight.harness.running"
    private static let crashFlagKey = "delight.harness.crashed"

    static func setup() {
        let defaults = UserDefaults.standard
        let wasRunning = defaults.bool(forKey: runningFlagKey)
        if wasRunning {
            defaults.set(true, forKey: crashFlagKey)
        }
        defaults.set(true, forKey: runningFlagKey)
        defaults.synchronize()
        NotificationCenter.default.addObserver(
            forName: UIApplication.willTerminateNotification,
            object: nil,
            queue: .main
        ) { _ in
            clearRunningFlag()
        }

        FileManager.default.createFile(atPath: logURL.path, contents: nil, attributes: nil)
        guard let handle = try? FileHandle(forWritingTo: logURL) else {
            return
        }
        handle.seekToEndOfFile()
        retainedHandle = handle
        dup2(handle.fileDescriptor, STDERR_FILENO)
        dup2(handle.fileDescriptor, STDOUT_FILENO)
        setvbuf(stderr, nil, _IONBF, 0)
        setvbuf(stdout, nil, _IONBF, 0)
        writeToStderr("CrashLogger setup (wasRunning=\(wasRunning))\n")
        NSSetUncaughtExceptionHandler(CrashLoggerHandleException)
    }

    fileprivate static func handleException(_ exception: NSException) {
        let reason = exception.reason ?? "unknown"
        let line = "Uncaught exception: \(exception.name.rawValue) \(reason)\n"
        writeToStderr(line)
    }

    private static func writeToStderr(_ message: String) {
        message.withCString { ptr in
            _ = Darwin.write(STDERR_FILENO, ptr, strlen(ptr))
        }
    }

    static func consumeCrashFlag() -> Bool {
        let defaults = UserDefaults.standard
        let crashed = defaults.bool(forKey: crashFlagKey)
        if crashed {
            defaults.set(false, forKey: crashFlagKey)
            defaults.synchronize()
        }
        return crashed
    }

    private static func clearRunningFlag() {
        let defaults = UserDefaults.standard
        defaults.set(false, forKey: runningFlagKey)
        defaults.synchronize()
    }
}

private func CrashLoggerHandleException(_ exception: NSException) {
    CrashLogger.handleException(exception)
}
