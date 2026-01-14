import XCTest
import Combine
@testable import DelightApp

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

    func testHandleActivityUpdateUpdatesActiveFlag() {
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
                uiState: nil
            )
        ]
        let json = """
        {"type":"activity","id":"s1","active":false,"activeAt":123}
        """
        let expectation = expectation(description: "activity applied")
        model.handleActivityUpdate(json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.sessions.first?.active, false)
            XCTAssertEqual(model.sessions.first?.activeAt, 123)
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testSessionUIUpdateOfflineUpdatesState() {
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
                uiState: SessionUIState(
                    connected: true,
                    online: true,
                    working: false,
                    mode: "remote",
                    switching: false,
                    transition: ""
                )
            )
        ]

        let update = """
        {"body":{"t":"session-ui","sid":"s1","ui":{"connected":true,"online":false,"working":false,"mode":"","switching":false,"transition":""}}}
        """

        let expectation = expectation(description: "offline updates ui state")
        model.onUpdate(nil, updateJSON: update)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.sessions.first?.uiState?.state, "offline")
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testParseSessionsOfflineUpdatesUIState() {
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
                uiState: nil
            )
        ]

        let sessionsJSON = """
        {"sessions":[{"id":"s1","updatedAt":1,"active":true,"activeAt":1,"metadata":"{\\"agent\\":\\"codex\\",\\"path\\":\\"/work/project\\",\\"host\\":\\"m2.local\\"}","ui":{"connected":true,"online":false,"working":false,"mode":"","switching":false,"transition":""}}]}
        """

        let expectation = expectation(description: "parse sessions applies ui state")
        model.parseSessions(sessionsJSON)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.sessions.first?.uiState?.state, "offline")
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testSessionUIUpdateConnectedFalseUpdatesState() {
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
                uiState: SessionUIState(
                    connected: true,
                    online: true,
                    working: false,
                    mode: "remote",
                    switching: false,
                    transition: ""
                )
            )
        ]

        let update = """
        {"body":{"t":"session-ui","sid":"s1","ui":{"connected":false,"online":true,"working":false,"mode":"remote","switching":false,"transition":""}}}
        """

        let expectation = expectation(description: "connected=false updates ui state")
        model.onUpdate(nil, updateJSON: update)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.sessions.first?.uiState?.state, "disconnected")
            XCTAssertEqual(model.sessions.first?.uiState?.connected, false)
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testParseSessionsConnectedFalseUpdatesUIState() {
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
                uiState: nil
            )
        ]

        let sessionsJSON = """
        {"sessions":[{"id":"s1","updatedAt":1,"active":true,"activeAt":1,"metadata":"{\\"agent\\":\\"codex\\",\\"path\\":\\"/work/project\\",\\"host\\":\\"m2.local\\"}","ui":{"connected":false,"online":true,"working":false,"mode":"remote","switching":false,"transition":""}}]}
        """

        let expectation = expectation(description: "parse sessions applies ui state")
        model.parseSessions(sessionsJSON)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.sessions.first?.uiState?.connected, false)
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
                agentState: nil,
                uiState: SessionUIState(
                    connected: true,
                    online: true,
                    working: false,
                    mode: "remote",
                    switching: false,
                    transition: ""
                )
            )
        ]
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
                agentState: nil,
                uiState: SessionUIState(
                    connected: true,
                    online: true,
                    working: false,
                    mode: "remote",
                    switching: false,
                    transition: ""
                )
            )
        ]
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
                uiState: nil
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
                    connected: true,
                    online: true,
                    working: false,
                    mode: "remote",
                    switching: false,
                    transition: ""
                )
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

    func testUIEventThinkingStartAddsTranscriptMessage() {
        let model = HarnessViewModel()
        model.sessionID = "s1"
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: "agent",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: nil
            )
        ]

        let json = """
        {"type":"ui.event","id":"s1","eventId":"thinking-1","kind":"thinking","phase":"start","status":"running","briefMarkdown":"Thinking…","fullMarkdown":"### Thinking\\n- step 1","atMs":123}
        """

        let expectation = expectation(description: "thinking ui event rendered")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertTrue(model.messages.contains(where: { $0.id == "ui-thinking-1" }))
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testUIEventThinkingEndRemovesMessageWhenEmpty() {
        let model = HarnessViewModel()
        model.sessionID = "s1"
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: "agent",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: nil
            )
        ]

        let start = """
        {"type":"ui.event","id":"s1","eventId":"thinking-1","kind":"thinking","phase":"start","status":"running","briefMarkdown":"Thinking…","fullMarkdown":"### Thinking\\n- step 1","atMs":123}
        """
        let end = """
        {"type":"ui.event","id":"s1","eventId":"thinking-1","kind":"thinking","phase":"end","status":"ok","briefMarkdown":"","fullMarkdown":"","atMs":124}
        """

        let expectation = expectation(description: "thinking message removed")
        model.onUpdate(nil, updateJSON: start)
        model.onUpdate(nil, updateJSON: end)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
            XCTAssertFalse(model.messages.contains(where: { $0.id == "ui-thinking-1" }))
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testParseMessagesDoesNotInferThinkingFromTranscript() {
        let model = HarnessViewModel()
        model.sessionID = "s1"
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: "agent",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: SessionUIState(
                    connected: true,
                    online: true,
                    working: true,
                    mode: "remote",
                    switching: false,
                    transition: ""
                )
            )
        ]

        // Now simulate fetching messages after reconnect/sleep, where the newest
        // message is a completed assistant reply.
        let messagesJSON = """
        {"messages":[{"id":"m1","createdAt":123,"message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}]}
        """

        let expectation = expectation(description: "messages fetch does not clear working")
        model.parseMessages(messagesJSON)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertEqual(model.sessions.first?.uiState?.working, true)
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testAppDidBecomeActiveRefreshesLatestMessages() {
        let model = HarnessViewModel()
        let expectation = expectation(description: "wake triggers fetch")
        var cancellable: AnyCancellable?
        DispatchQueue.main.async {
            model.sessionID = "s1"
            cancellable = model.$isLoadingLatest.sink { isLoading in
                if isLoading {
                    expectation.fulfill()
                }
            }
            model.onAppDidBecomeActive()
        }
        waitForExpectations(timeout: 1.0)
        cancellable?.cancel()
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
        {"sessions":[{"id":"s1","updatedAt":0,"active":true,"metadata":null,"agentState":"{\\"controlledByUser\\":false,\\"requests\\":{\\"r1\\":{\\"tool_name\\":\\"bash\\",\\"input\\":\\"{}\\",\\"created_at\\":1}}}","ui":{"connected":true,"online":true,"working":false,"mode":"remote","switching":false,"transition":""}}],"version":1}
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

    func testUIEventReasoningUsesFullMarkdownWhenBriefIsHeadingOnly() {
        let model = HarnessViewModel()
        model.sessionID = "s1"
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: "agent",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: nil
            )
        ]

        let json = """
        {"type":"ui.event","id":"s1","eventId":"reasoning-1","kind":"reasoning","phase":"update","status":"ok","briefMarkdown":"Reasoning","fullMarkdown":"Reasoning\\n\\n- step 1","atMs":123}
        """

        let expectation = expectation(description: "reasoning callout includes content")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            guard let item = model.messages.first(where: { $0.id == "ui-reasoning-1" }) else {
                XCTFail("expected ui event message to be present")
                expectation.fulfill()
                return
            }
            XCTAssertTrue(item.blocks.contains(where: { block in
                if case let .callout(summary) = block {
                    return summary.content.contains("- step 1")
                }
                return false
            }))
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testUIEventReasoningSuppressesEmptyHeadingOnlyPayload() {
        let model = HarnessViewModel()
        model.sessionID = "s1"
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: "agent",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: nil
            )
        ]

        let json = """
        {"type":"ui.event","id":"s1","eventId":"reasoning-2","kind":"reasoning","phase":"update","status":"ok","briefMarkdown":"Reasoning","fullMarkdown":"Reasoning","atMs":123}
        """

        let expectation = expectation(description: "heading-only reasoning omitted")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            XCTAssertFalse(model.messages.contains(where: { $0.id == "ui-reasoning-2" }))
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testUsageUpdateStoresSnapshot() {
        let model = HarnessViewModel()
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: "agent",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: nil
            )
        ]

        let json = """
        {"type":"usage","id":"s1","key":"codex","tokens":{"total":10,"input":4,"output":6},"cost":{"total":0.01},"timestamp":123}
        """

        let expectation = expectation(description: "usage stored")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            guard let usage = model.usageBySessionID["s1"] else {
                XCTFail("expected usage snapshot for session")
                expectation.fulfill()
                return
            }
            XCTAssertEqual(usage.key, "codex")
            XCTAssertEqual(usage.tokensTotal, 10)
            XCTAssertEqual(usage.tokensInput, 4)
            XCTAssertEqual(usage.tokensOutput, 6)
            XCTAssertEqual(usage.costTotal, 0.01)
            XCTAssertEqual(usage.timestampMs, 123)
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testToolUIEventUsesFullCommandWithoutOutputWhenDisabled() {
        let model = HarnessViewModel()
        model.sessionID = "s1"
        model.showToolUseInTranscript = true
        model.showToolOutputInTranscript = false
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: "agent",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: nil
            )
        ]

        let fullCommand = "echo hello && echo world"
        let json = """
        {"type":"ui.event","id":"s1","eventId":"tool-1","kind":"tool","phase":"update","status":"running","briefMarkdown":"Tool: `echo hello`","fullMarkdown":"Tool: shell\\n\\n```sh\\n\(fullCommand)\\n```\\n\\nOutput:\\n\\n```\\nhello\\nworld\\n```","atMs":123}
        """

        let expectation = expectation(description: "tool event renders command but hides output")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            guard let item = model.messages.first(where: { $0.id == "ui-tool-1" }) else {
                XCTFail("expected tool ui event to be present")
                expectation.fulfill()
                return
            }

            XCTAssertTrue(item.blocks.contains(where: { block in
                if case let .toolCallout(summary) = block {
                    return summary.command.contains(fullCommand)
                }
                return false
            }))

            let text = item.blocks.compactMap { block -> String? in
                if case let .toolCallout(summary) = block { return summary.command }
                if case let .text(value) = block { return value }
                return nil
            }.joined(separator: "\n")
            XCTAssertFalse(text.contains("Output:"))
            // The command itself contains "hello" and "world"; assert only that
            // the output block isn't present.
            XCTAssertFalse(text.contains("hello\nworld"))
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testToolUIEventShowsIndicatorWhenToolUseHidden() {
        let model = HarnessViewModel()
        model.sessionID = "s1"
        model.showToolUseInTranscript = false
        model.showToolOutputInTranscript = false
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: "agent",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: nil
            )
        ]

        let json = """
        {"type":"ui.event","id":"s1","eventId":"tool-hidden-1","kind":"tool","phase":"update","status":"running","briefMarkdown":"Tool: `echo hello`","fullMarkdown":"Tool: shell\\n\\n```sh\\necho hello\\n```\\n\\nOutput:\\n\\n```\\nhello\\n```","atMs":123}
        """

        let expectation = expectation(description: "tool event shows minimal indicator")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            guard let item = model.messages.first(where: { $0.id == "ui-tool-hidden-1" }) else {
                XCTFail("expected tool ui event to be present")
                expectation.fulfill()
                return
            }

            XCTAssertTrue(item.blocks.contains(where: { block in
                if case let .callout(summary) = block {
                    return summary.title == "Tool use"
                }
                return false
            }))

            XCTAssertFalse(item.blocks.contains(where: { block in
                if case .toolCallout = block { return true }
                return false
            }))
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testToolUIEventRendersOutputWhenEnabled() {
        let model = HarnessViewModel()
        model.sessionID = "s1"
        model.showToolUseInTranscript = true
        model.showToolOutputInTranscript = true
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: "agent",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: nil
            )
        ]

        let fullCommand = "echo hello && echo world"
        let json = """
        {"type":"ui.event","id":"s1","eventId":"tool-2","kind":"tool","phase":"update","status":"running","briefMarkdown":"Tool: `echo hello`","fullMarkdown":"Tool: shell\\n\\n```sh\\n\(fullCommand)\\n```\\n\\nOutput:\\n\\n```\\nhello\\nworld\\n```","atMs":123}
        """

        let expectation = expectation(description: "tool event renders command and output")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            guard let item = model.messages.first(where: { $0.id == "ui-tool-2" }) else {
                XCTFail("expected tool ui event to be present")
                expectation.fulfill()
                return
            }

            guard let toolSummary = item.blocks.compactMap({ block -> ToolCalloutSummary? in
                if case let .toolCallout(summary) = block { return summary }
                return nil
            }).first else {
                XCTFail("expected tool callout block")
                expectation.fulfill()
                return
            }

            XCTAssertTrue(toolSummary.command.contains(fullCommand))
            XCTAssertTrue(toolSummary.output.contains(where: { block in
                if case let .code(_, content) = block {
                    return content.contains("hello\nworld")
                }
                return false
            }))
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testUIEventReasoningPrefersFullMarkdownOverTruncatedBrief() {
        let model = HarnessViewModel()
        model.sessionID = "s1"
        model.sessions = [
            SessionSummary(
                id: "s1",
                terminalID: "t1",
                updatedAt: 0,
                active: true,
                activeAt: nil,
                title: "agent",
                subtitle: nil,
                metadata: nil,
                agentState: nil,
                uiState: nil
            )
        ]

        let json = """
        {"type":"ui.event","id":"s1","eventId":"reasoning-3","kind":"reasoning","phase":"update","status":"ok","briefMarkdown":"Reasoning\\n\\n- step 1...","fullMarkdown":"Reasoning\\n\\n- step 1\\n- step 2","atMs":123}
        """

        let expectation = expectation(description: "reasoning uses full text")
        model.onUpdate(nil, updateJSON: json)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            guard let item = model.messages.first(where: { $0.id == "ui-reasoning-3" }) else {
                XCTFail("expected ui event message to be present")
                expectation.fulfill()
                return
            }
            XCTAssertTrue(item.blocks.contains(where: { block in
                if case let .callout(summary) = block {
                    return summary.content.contains("- step 2")
                }
                return false
            }))
            expectation.fulfill()
        }
        waitForExpectations(timeout: 1.0)
    }

    func testTerminalComposerStateAllowsInputWhenNotWorking() {
        let ui = SessionUIState(
            connected: true,
            online: true,
            working: false,
            mode: "remote",
            switching: false,
            transition: ""
        )

        let state = TerminalDetailView.TerminalComposerState.make(
            ui: ui,
            controlledByDesktop: false
        )

        XCTAssertTrue(state.isInputEnabled)
        XCTAssertTrue(state.isHistoryEnabled)
        XCTAssertFalse(state.isShowingStop)
    }

    func testTerminalComposerStateBusyFromUIWorking() {
        let ui = SessionUIState(
            connected: true,
            online: true,
            working: true,
            mode: "remote",
            switching: false,
            transition: ""
        )

        let state = TerminalDetailView.TerminalComposerState.make(
            ui: ui,
            controlledByDesktop: false
        )

        XCTAssertFalse(state.isInputEnabled)
        XCTAssertTrue(state.isHistoryEnabled)
        XCTAssertTrue(state.isShowingStop)
    }

    func testTerminalComposerStateDesktopControlledDisablesStop() {
        let ui = SessionUIState(
            connected: true,
            online: true,
            working: false,
            mode: "local",
            switching: false,
            transition: ""
        )

        let state = TerminalDetailView.TerminalComposerState.make(
            ui: ui,
            controlledByDesktop: true
        )

        XCTAssertFalse(state.isInputEnabled)
        XCTAssertFalse(state.isHistoryEnabled)
        XCTAssertFalse(state.isShowingStop)
    }
}
