import Foundation
import Darwin
import UIKit

enum CrashLogger {
    static let logURL: URL = {
        let dir = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first!
        return dir.appendingPathComponent("Delight_crash.log")
    }()

    private static var retainedHandle: FileHandle?
    private static let crashReportAckKey = "delight.crash_report_ack"
    private static let crashMarkerPrefix = "[DelightCrash]"
    private static let runStartMarkerPrefix = "---- Delight start "
    private static let crashLogTailBytes = 64_000
    private static let crashLogPreContextBytes = 2_048

    /// setup redirects stdout/stderr to a file so the UI can show crash logs.
    ///
    /// iOS doesn't reliably deliver termination callbacks (e.g. Xcode stop,
    /// the OS reclaiming memory), so we avoid "was running" heuristics that
    /// frequently cause false-positive crash reports.
    static func setup() {
        let existing = FileManager.default.fileExists(atPath: logURL.path)
        if !existing {
            FileManager.default.createFile(atPath: logURL.path, contents: nil, attributes: nil)
        }

        guard let handle = try? FileHandle(forWritingTo: logURL) else {
            return
        }
        handle.seekToEndOfFile()
        retainedHandle = handle
        dup2(handle.fileDescriptor, STDERR_FILENO)
        dup2(handle.fileDescriptor, STDOUT_FILENO)
        setvbuf(stderr, nil, _IONBF, 0)
        setvbuf(stdout, nil, _IONBF, 0)

        let timestamp = ISO8601DateFormatter().string(from: Date())
        writeToStderr("\(runStartMarkerPrefix)\(timestamp) ----\n")

        installSignalHandlers()
        NSSetUncaughtExceptionHandler(CrashLoggerHandleException)
    }

    fileprivate static func handleException(_ exception: NSException) {
        let reason = exception.reason ?? "unknown"
        let line = "\(crashMarkerPrefix) uncaught_exception name=\(exception.name.rawValue) reason=\(reason)\n"
        writeToStderr(line)
    }

    private static func writeToStderr(_ message: String) {
        message.withCString { ptr in
            _ = Darwin.write(STDERR_FILENO, ptr, strlen(ptr))
        }
    }

    /// consumeCrashReport returns the last crash marker log tail, at most once
    /// per crash marker occurrence.
    static func consumeCrashReport() -> String? {
        guard let marker = lastCrashMarker() else {
            return nil
        }
        let defaults = UserDefaults.standard
        let acked = defaults.string(forKey: crashReportAckKey)
        if acked == marker.signature {
            return nil
        }
        defaults.set(marker.signature, forKey: crashReportAckKey)
        defaults.synchronize()
        return marker.tail
    }

    private static func lastCrashMarker() -> (signature: String, tail: String)? {
        guard let handle = try? FileHandle(forReadingFrom: logURL) else {
            return nil
        }
        defer { try? handle.close() }

        // Keep the search window bounded so we don't slurp huge files.
        let fileSize = (try? handle.seekToEnd()) ?? 0
        let start = fileSize > UInt64(crashLogTailBytes) ? (fileSize - UInt64(crashLogTailBytes)) : 0
        try? handle.seek(toOffset: start)
        let data = (try? handle.readToEnd()) ?? Data()

        guard let markerData = crashMarkerPrefix.data(using: .utf8) else {
            return nil
        }
        guard let markerRange = data.range(of: markerData, options: .backwards) else {
            return nil
        }

        let absoluteMarkerOffset = UInt64(markerRange.lowerBound) + start

        guard let runStartMarkerData = runStartMarkerPrefix.data(using: .utf8) else {
            return nil
        }

        // If the app has launched since the crash, the log will contain a
        // run start marker after the crash marker. We only show the crash
        // chunk, not subsequent run logs.
        let searchStart = markerRange.upperBound
        let searchRange = searchStart..<data.endIndex
        let nextRunStartRange = data.range(of: runStartMarkerData, options: [], in: searchRange)
        let endIndex = nextRunStartRange?.lowerBound ?? data.endIndex

        let contextStart = markerRange.lowerBound > crashLogPreContextBytes
            ? markerRange.lowerBound - crashLogPreContextBytes
            : 0
        let snippet = data.subdata(in: contextStart..<endIndex)
        guard let text = String(data: snippet, encoding: .utf8) else {
            return nil
        }

        return (signature: String(absoluteMarkerOffset), tail: text)
    }

    private static func installSignalHandlers() {
        // The Go runtime and Swift can abort without producing an NSException.
        // Writing a unique marker lets the UI show a crash report without
        // relying on unreliable lifecycle heuristics.
        signal(SIGABRT, CrashLoggerHandleSignal)
        signal(SIGILL, CrashLoggerHandleSignal)
        signal(SIGTRAP, CrashLoggerHandleSignal)
        signal(SIGBUS, CrashLoggerHandleSignal)
        signal(SIGSEGV, CrashLoggerHandleSignal)
    }
}

private func CrashLoggerHandleException(_ exception: NSException) {
    CrashLogger.handleException(exception)
}

private func CrashLoggerHandleSignal(_ signal: Int32) {
    let message = "[DelightCrash] signal=\(signal)\n"
    message.withCString { ptr in
        _ = Darwin.write(STDERR_FILENO, ptr, strlen(ptr))
    }
    Darwin.signal(signal, SIG_DFL)
    Darwin.raise(signal)
}
