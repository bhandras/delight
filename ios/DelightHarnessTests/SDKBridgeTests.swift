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
                terminalID: "t1",
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

    func testParseTerminalsSetsMetadata() {
        let model = HarnessViewModel()
        let json = """
        [{"id":"t1","active":true,"metadata":"{\\"host\\":\\"m2.local\\",\\"platform\\":\\"darwin\\"}","daemonState":"{\\"pid\\":123,\\"status\\":\\"ok\\"}","daemonStateVersion":4}]
        """
        let expectation = expectation(description: "terminals parsed")
        model.parseTerminals(json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.terminals.count, 1)
            XCTAssertEqual(model.terminals.first?.metadata?.host, "m2.local")
            XCTAssertEqual(model.terminals.first?.daemonState?.pid, 123)
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

    func testHandlePermissionRequestUpdateQueuedWhenDesktopControls() {
        let model = HarnessViewModel()
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: nil,
                subtitle: nil,
                metadata: nil,
                agentState: SessionAgentState(
                    agentType: nil,
                    controlledByUser: true,
                    model: nil,
                    reasoningEffort: nil,
                    permissionMode: nil,
                    requests: [:]
                ),
                uiState: nil,
                thinking: false
            )
        ]
        let json = """
        {"type":"permission-request","id":"s1","requestId":"r1","toolName":"bash","input":"{\\"command\\":\\"ls\\"}"}
        """

        let expectation = expectation(description: "permission request queued but not shown")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.permissionQueue.count, 1)
            XCTAssertEqual(model.activePermissionRequest?.requestID, "r1")
            XCTAssertFalse(model.showPermissionPrompt)
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testHandlePermissionRequestUpdateShownWhenUIStateRemote() {
        let model = HarnessViewModel()
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: nil,
                subtitle: nil,
                metadata: nil,
                agentState: SessionAgentState(
                    agentType: nil,
                    controlledByUser: true,
                    model: nil,
                    reasoningEffort: nil,
                    permissionMode: nil,
                    requests: [:]
                ),
                uiState: SessionUIState(
                    state: "remote",
                    connected: true,
                    active: true,
                    controlledByUser: false,
                    switching: false,
                    transition: "",
                    canTakeControl: false,
                    canSend: true
                ),
                thinking: false
            )
        ]
        let json = """
        {"type":"permission-request","id":"s1","requestId":"r1","toolName":"bash","input":"{\\"command\\":\\"ls\\"}"}
        """

        let expectation = expectation(description: "permission request queued and shown")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.permissionQueue.count, 1)
            XCTAssertEqual(model.activePermissionRequest?.requestID, "r1")
            XCTAssertTrue(model.showPermissionPrompt)
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testCodexReasoningUpdateSetsThinkingTrue() {
        let model = HarnessViewModel()
        model.sessionID = "s1"
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: "codex",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: nil,
                thinking: false
            )
        ]

        let json = """
        {"body":{"t":"new-message","sid":"s1","message":{"id":"m1","content":{"role":"agent","content":{"type":"codex","data":{"type":"reasoning","message":"Evaluating minimal response"}}}}}}
        """

        let expectation = expectation(description: "thinking becomes true")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.sessions.first?.thinking, true)
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testCodexMessageUpdateClearsThinking() {
        let model = HarnessViewModel()
        model.sessionID = "s1"
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: "codex",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: nil,
                thinking: true
            )
        ]

        let json = """
        {"body":{"t":"new-message","sid":"s1","message":{"id":"m2","content":{"role":"agent","content":{"type":"codex","data":{"type":"message","message":"Done"}}}}}}
        """

        let expectation = expectation(description: "thinking becomes false")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.sessions.first?.thinking, false)
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testParseSessionsShowsQueuedPermissionWhenRemote() {
        let model = HarnessViewModel()
        model.permissionQueue = [
            PendingPermissionRequest(
                sessionID: "s1",
                requestID: "r1",
                toolName: "bash",
                input: "{\"command\":\"ls\"}",
                receivedAt: 1
            )
        ]
        model.activePermissionRequest = model.permissionQueue.first
        model.showPermissionPrompt = false

        let sessionsJSON = """
        {"sessions":[{"id":"s1","updatedAt":0,"active":true,"metadata":null,"agentState":"{\\"controlledByUser\\":false,\\"requests\\":{\\"r1\\":{\\"tool_name\\":\\"bash\\",\\"input\\":\\"{}\\",\\"created_at\\":1}}}"}],"version":1}
        """

        let expectation = expectation(description: "permission request becomes visible in remote UI")
        model.parseSessions(sessionsJSON)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertTrue(model.showPermissionPrompt)
            XCTAssertEqual(model.activePermissionRequest?.requestID, "r1")
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testSessionMetadataParsesBase64Payload() {
        let payload = """
        {"host":"m2.local","path":"/work/project","summary":{"agent":"claude","text":"summary"}}
        """
        let base64 = Data(payload.utf8).base64EncodedString()
        let metadata = SessionMetadata.fromJSON(base64)

        XCTAssertEqual(metadata?.host, "m2.local")
        XCTAssertEqual(metadata?.path, "/work/project")
        XCTAssertEqual(metadata?.agent, "claude")
        XCTAssertEqual(metadata?.summaryText, "summary")
    }

    func testSessionAgentStateParsesRequests() {
        let json = """
        {"controlledByUser":false,"requests":{"r1":{"tool_name":"bash","input":"{}","created_at":"123"}}}
        """
        let state = SessionAgentState.fromJSON(json)

        XCTAssertEqual(state?.controlledByUser, false)
        XCTAssertEqual(state?.requests["r1"]?.toolName, "bash")
        XCTAssertEqual(state?.requests["r1"]?.createdAt, 123)
    }

    func testTerminalMetadataParsesCliVersion() {
        let json = """
        {"host":"m2.local","platform":"darwin","cliVersion":"1.2.3","homeDir":"/Users/test"}
        """
        let metadata = TerminalMetadata.fromJSON(json)

        XCTAssertEqual(metadata?.cliVersion, "1.2.3")
        XCTAssertEqual(metadata?.homeDir, "/Users/test")
    }

    func testDaemonStateParsesNumericStrings() {
        let json = """
        {"status":"ok","pid":"42","startedAt":"456"}
        """
        let daemon = DaemonState.fromJSON(json)

        XCTAssertEqual(daemon?.pid, 42)
        XCTAssertEqual(daemon?.startedAt, 456)
    }
}
