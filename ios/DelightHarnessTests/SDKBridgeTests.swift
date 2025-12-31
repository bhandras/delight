import XCTest
@testable import DelightHarness

final class SDKBridgeTests: XCTestCase {
    func testParseMessagesContentStringFallback() {
        let model = HarnessViewModel()
        model.sessionID = "session-1"
        let json = """
        {"messages":[{"id":"m1","createdAt":123,"message":{"content":[{"content":"total 3\\n-rw file.txt"}]}}]}
        """
        let expectation = expectation(description: "messages parsed")
        model.parseMessages(json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.messages.count, 1)
            if let first = model.messages.first {
                XCTAssertEqual(first.role, .unknown)
                XCTAssertTrue(first.blocks.contains { block in
                    if case let .text(text) = block {
                        return text.contains("total 3")
                    }
                    return false
                })
            }
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testParseSessionsUsesAgentForTitle() {
        let model = HarnessViewModel()
        let json = """
        {"sessions":[{"id":"s1","updatedAt":1,"active":true,"activeAt":1,"metadata":"{\\"agent\\":\\"claude\\",\\"path\\":\\"/work/project\\",\\"host\\":\\"m2.local\\"}"}]}
        """
        let expectation = expectation(description: "sessions parsed")
        model.parseSessions(json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.sessions.count, 1)
            XCTAssertEqual(model.sessions.first?.title, "claude")
            XCTAssertEqual(model.sessions.first?.metadata?.path, "/work/project")
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testHandleActivityUpdateThinking() {
        let model = HarnessViewModel()
        model.sessions = [
            SessionSummary(
                id: "s1",
                updatedAt: 1,
                active: true,
                activeAt: nil,
                title: "agent",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: nil,
                thinking: false
            )
        ]
        let json = """
        {"type":"activity","id":"s1","thinking":true}
        """
        let expectation = expectation(description: "activity applied")
        model.handleActivityUpdate(json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.sessions.first?.thinking, true)
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testParseMachinesSetsMetadata() {
        let model = HarnessViewModel()
        let json = """
        [{"id":"m1","active":true,"metadata":"{\\"host\\":\\"m2.local\\",\\"platform\\":\\"darwin\\"}","daemonState":"{\\"pid\\":123,\\"status\\":\\"ok\\"}","daemonStateVersion":4}]
        """
        let expectation = expectation(description: "machines parsed")
        model.parseMachines(json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.machines.count, 1)
            XCTAssertEqual(model.machines.first?.metadata?.host, "m2.local")
            XCTAssertEqual(model.machines.first?.daemonState?.pid, 123)
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testHandlePermissionRequestUpdateBodyEnvelope() {
        let model = HarnessViewModel()
        let json = """
        {"body":{"type":"permission-request","id":"s1","requestId":"r1","toolName":"bash","input":"{\\"command\\":\\"ls\\"}"}}
        """

        let expectation = expectation(description: "permission request queued")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.permissionQueue.count, 1)
            XCTAssertEqual(model.permissionQueue.first?.sessionID, "s1")
            XCTAssertEqual(model.permissionQueue.first?.requestID, "r1")
            XCTAssertEqual(model.permissionQueue.first?.toolName, "bash")
            XCTAssertEqual(model.activePermissionRequest?.requestID, "r1")
            XCTAssertTrue(model.showPermissionPrompt)
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testHandlePermissionRequestUpdateRootEnvelope() {
        let model = HarnessViewModel()
        let json = """
        {"type":"permission-request","id":"s1","requestId":"r1","toolName":"bash","input":"{\\"command\\":\\"ls\\"}"}
        """

        let expectation = expectation(description: "permission request queued")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.permissionQueue.count, 1)
            XCTAssertEqual(model.permissionQueue.first?.requestID, "r1")
            XCTAssertTrue(model.showPermissionPrompt)
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }
}
