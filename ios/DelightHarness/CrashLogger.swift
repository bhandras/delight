import Foundation
import Darwin

enum CrashLogger {
    static let logURL: URL = {
        let dir = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first!
        return dir.appendingPathComponent("delight_harness_crash.log")
    }()

    private static var retainedHandle: FileHandle?

    static func setup() {
        FileManager.default.createFile(atPath: logURL.path, contents: nil, attributes: nil)
        guard let handle = try? FileHandle(forWritingTo: logURL) else {
            return
        }
        handle.seekToEndOfFile()
        retainedHandle = handle
        dup2(handle.fileDescriptor, STDERR_FILENO)
        dup2(handle.fileDescriptor, STDOUT_FILENO)
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
}

private func CrashLoggerHandleException(_ exception: NSException) {
    CrashLogger.handleException(exception)
}
