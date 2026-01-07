import SwiftUI

/// TerminalTranscriptLazyView renders the transcript using SwiftUI's
/// `ScrollView` + `LazyVStack` so only visible message rows are built.
///
/// This is an experimental alternative to `TerminalMessagesView` (UITableView)
/// intended to reduce work for large transcripts while keeping scroll-to-bottom
/// and "load older" behaviors.
struct TerminalTranscriptLazyView: View {
    let messages: [MessageItem]
    let hasMoreHistory: Bool
    let isLoadingHistory: Bool
    let isLoadingLatest: Bool
    let onLoadOlder: () -> Void
    let onDoubleTap: () -> Void
    let scrollRequest: ScrollRequest?
    let onConsumeScrollRequest: () -> Void
    let fontSize: CGFloat

    /// Layout holds constants for the lazy transcript.
    enum Layout {
        /// messageSpacing is the vertical distance between message bubbles.
        static let messageSpacing: CGFloat = 2

        /// transcriptPadding keeps content slightly away from the edges.
        static let transcriptPadding: CGFloat = 4

        /// bottomAnchorID is a stable scroll target at the bottom of the transcript.
        ///
        /// We avoid scrolling to the last message row directly because `LazyVStack`
        /// may not have materialized that row yet, which can make `scrollTo(...)`
        /// unreliable.
        static let bottomAnchorID = "terminalTranscriptBottomAnchor"

        /// nearBottomThreshold controls when we consider the user "near bottom"
        /// for auto-scroll as new messages arrive.
        static let nearBottomThreshold: CGFloat = 120
    }

    @State private var didInitialScrollToBottom: Bool = false
    @State private var lastHandledScrollRequestID: UUID?
    @State private var lastMessageID: String?
    @State private var isNearBottom: Bool = true

    var body: some View {
        GeometryReader { proxy in
            ScrollViewReader { scrollViewProxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: Layout.messageSpacing) {
                        if isLoadingLatest, messages.isEmpty {
                            ProgressView()
                                .padding(.vertical, 12)
                        }
                        if hasMoreHistory {
                            TerminalHistoryHint(isLoadingHistory: isLoadingHistory)
                        }
                        ForEach(messages) { message in
                            MessageBubbleRow(message: message, fontSize: fontSize)
                                .id(message.id)
                        }
                        .padding(.horizontal, Layout.transcriptPadding)

                        // Stable bottom anchor used for deterministic scroll-to-bottom.
                        Color.clear
                            .frame(height: 1)
                            .id(Layout.bottomAnchorID)
                            .background(
                                GeometryReader { rowProxy in
                                    Color.clear.preference(
                                        key: TranscriptLastRowMaxYPreferenceKey.self,
                                        value: rowProxy.frame(in: .named(TranscriptScrollCoordinateSpace.name)).maxY
                                    )
                                }
                            )
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .coordinateSpace(name: TranscriptScrollCoordinateSpace.name)
                .contentShape(Rectangle())
                .highPriorityGesture(
                    TapGesture(count: 2).onEnded {
                        onDoubleTap()
                        jumpToBottom(scrollViewProxy, animated: false)
                    }
                )
                .onPreferenceChange(TranscriptLastRowMaxYPreferenceKey.self) { lastMaxY in
                    let visibleHeight = proxy.size.height
                    let nearBottom = (lastMaxY - visibleHeight) < Layout.nearBottomThreshold
                    if isNearBottom != nearBottom {
                        isNearBottom = nearBottom
                    }
                }
                .onAppear {
                    // Re-arm the initial jump every time the view appears.
                    didInitialScrollToBottom = false
                    lastHandledScrollRequestID = nil
                    lastMessageID = nil

                    // If we already have content (e.g. cached transcript), jump on
                    // the next runloop so the scroll targets have been laid out.
                    if !messages.isEmpty {
                        DispatchQueue.main.async {
                            jumpToBottom(scrollViewProxy, animated: false)
                            didInitialScrollToBottom = true
                            lastMessageID = messages.last?.id
                        }
                    }
                }
                .onChange(of: messages.last?.id) { newLastID in
                    guard let newLastID else { return }
                    defer { lastMessageID = newLastID }

                    if !didInitialScrollToBottom {
                        // First time we receive transcript content (fresh load).
                        // Defer the jump to allow MarkdownUI to finish sizing rows;
                        // otherwise we can land slightly above the bottom.
                        DispatchQueue.main.async {
                            jumpToBottom(scrollViewProxy, animated: false)
                            didInitialScrollToBottom = true
                        }
                        return
                    }

                    // Append-only updates: keep pinned if the user is near the bottom.
                    if lastMessageID != newLastID, isNearBottom {
                        jumpToBottom(scrollViewProxy, animated: true)
                    }
                }
                .onChange(of: scrollRequest?.id) { _ in
                    guard let request = scrollRequest else { return }
                    if lastHandledScrollRequestID == request.id {
                        return
                    }
                    lastHandledScrollRequestID = request.id
                    switch request.target {
                    case .bottom:
                        jumpToBottom(scrollViewProxy, animated: true)
                        // Follow-up correction on the next runloop so dynamic
                        // content sizing (Markdown) doesn't leave us slightly above
                        // the bottom.
                        DispatchQueue.main.async {
                            jumpToBottom(scrollViewProxy, animated: false)
                        }
                    case .message(let id, let anchor):
                        scrollViewProxy.scrollTo(id, anchor: anchor)
                    }
                    DispatchQueue.main.async {
                        onConsumeScrollRequest()
                    }
                }
                .refreshable {
                    guard hasMoreHistory, !isLoadingHistory else { return }
                    onLoadOlder()
                }
            }
        }
    }

    private func jumpToBottom(_ proxy: ScrollViewProxy, animated: Bool) {
        if animated {
            withAnimation(.easeOut(duration: 0.2)) {
                proxy.scrollTo(Layout.bottomAnchorID, anchor: .bottom)
            }
            return
        }
        var transaction = Transaction()
        transaction.disablesAnimations = true
        withTransaction(transaction) {
            proxy.scrollTo(Layout.bottomAnchorID, anchor: .bottom)
        }
    }
}

private enum TranscriptScrollCoordinateSpace {
    static let name = "terminalTranscriptScroll"
}

private struct TranscriptLastRowMaxYPreferenceKey: PreferenceKey {
    static var defaultValue: CGFloat = 0

    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) {
        value = nextValue()
    }
}

private struct TerminalHistoryHint: View {
    let isLoadingHistory: Bool

    /// Layout holds display constants for the history sentinel.
    enum Layout {
        static let padding: CGFloat = 10
        static let spinnerScale: CGFloat = 0.9
        static let arrowSize: CGFloat = 12
    }

    var body: some View {
        HStack(spacing: 8) {
            Spacer()
            if isLoadingHistory {
                ProgressView()
                    .scaleEffect(Layout.spinnerScale)
            } else {
                Image(systemName: "arrow.down")
                    .font(.system(size: Layout.arrowSize, weight: .semibold))
                    .foregroundColor(Theme.mutedText)
                Text("Pull to load older messages")
                    .font(Theme.caption)
                    .foregroundColor(Theme.mutedText)
            }
            Spacer()
        }
        .padding(.vertical, Layout.padding)
    }
}

private struct MessageBubbleRow: View, Equatable {
    let message: MessageItem
    let fontSize: CGFloat

    static func == (lhs: MessageBubbleRow, rhs: MessageBubbleRow) -> Bool {
        lhs.message == rhs.message && Int(lhs.fontSize) == Int(rhs.fontSize)
    }

    var body: some View {
        MessageBubble(message: message, fontSize: fontSize)
    }
}
