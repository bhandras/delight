import SwiftUI
import UIKit

/// A chat transcript view backed by `UITableView` for reliable:
/// - scrolling to bottom on open / new messages
/// - fetching older messages when reaching the top (infinite scroll)
/// - preserving scroll position when prepending older pages
struct TerminalMessagesView: UIViewRepresentable {
    typealias UIViewType = UITableView

    let messages: [MessageItem]
    let hasMoreHistory: Bool
    let isLoadingHistory: Bool
    let onLoadOlder: () -> Void
    let onDoubleTap: () -> Void

    /// Optional external scroll request (e.g. scroll-to-bottom). The view consumes the request.
    let scrollRequest: ScrollRequest?
    let onConsumeScrollRequest: () -> Void
    let fontSize: CGFloat

    /// Layout holds display constants for the transcript list.
    enum Layout {
        /// transcriptTopBottomInset keeps a small amount of breathing room so
        /// the first/last bubble isn't glued to the edge.
        static let transcriptTopBottomInset: CGFloat = 4

        /// messageVerticalPadding is the spacing between message rows.
        static let messageVerticalPadding: CGFloat = 2

        /// historyLoadTriggerOffset is how close to the top we start fetching
        /// older messages.
        static let historyLoadTriggerOffset: CGFloat = 4

        /// estimatedRowHeight provides a baseline size hint for autosizing rows.
        static let estimatedRowHeight: CGFloat = 120

        /// nearBottomThreshold is the max distance (in points) from the bottom
        /// that still counts as "near bottom" for auto-scroll behavior.
        static let nearBottomThreshold: CGFloat = 80

        /// scrollSecondPassDelaySeconds is a small delay used for a second
        /// scroll-to-bottom attempt after hosted SwiftUI content settles row
        /// heights (Markdown rendering, dynamic type).
        static let scrollSecondPassDelaySeconds: TimeInterval = 0.08
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(parent: self)
    }

    func makeUIView(context: Context) -> UITableView {
        let tableView = UITableView(frame: .zero, style: .plain)
        tableView.separatorStyle = .none
        tableView.backgroundColor = .clear
        tableView.showsVerticalScrollIndicator = true
        tableView.keyboardDismissMode = .interactive
        tableView.allowsSelection = false
        tableView.estimatedRowHeight = Layout.estimatedRowHeight
        tableView.rowHeight = UITableView.automaticDimension
        tableView.contentInset = UIEdgeInsets(
            top: Layout.transcriptTopBottomInset,
            left: 0,
            bottom: Layout.transcriptTopBottomInset,
            right: 0
        )

        tableView.dataSource = context.coordinator
        tableView.delegate = context.coordinator

        let refresh = UIRefreshControl()
        refresh.addTarget(context.coordinator, action: #selector(Coordinator.onRefreshControl), for: .valueChanged)
        tableView.refreshControl = refresh

        let doubleTap = UITapGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.onDoubleTap))
        doubleTap.numberOfTapsRequired = 2
        doubleTap.cancelsTouchesInView = false
        doubleTap.delegate = context.coordinator
        tableView.addGestureRecognizer(doubleTap)

        return tableView
    }

    func updateUIView(_ uiView: UITableView, context: Context) {
        context.coordinator.parent = self

        // Keep refresh control in sync with `isLoadingHistory`.
        if isLoadingHistory {
            if uiView.refreshControl?.isRefreshing != true {
                uiView.refreshControl?.beginRefreshing()
            }
        } else {
            if uiView.refreshControl?.isRefreshing == true {
                uiView.refreshControl?.endRefreshing()
            }
        }

        let previousFirstID = context.coordinator.lastFirstMessageID
        let previousLastID = context.coordinator.lastLastMessageID
        let newFirstID = messages.first?.id
        let newLastID = messages.last?.id

        let fontSizeChanged = Int(context.coordinator.lastFontSize) != Int(fontSize)
        let oldCount = context.coordinator.lastMessageCount
        let newCount = messages.count
        let messageCountChanged = oldCount != newCount
        let messageEdgeChanged = (previousFirstID != newFirstID) || (previousLastID != newLastID)
        let shouldReload = fontSizeChanged || messageCountChanged || messageEdgeChanged

        // Fast path: append-only updates are common during streaming agent output.
        // Avoid `reloadData()` (which can be O(n) and cause long UI stalls for
        // large transcripts) when we can safely insert the new rows.
        if !fontSizeChanged,
           oldCount > 0,
           newCount > oldCount,
           previousFirstID == newFirstID,
           let previousLastID,
           oldCount - 1 < messages.count,
           messages[oldCount - 1].id == previousLastID {
            let newIndexPaths = (oldCount..<newCount).map { IndexPath(row: $0, section: 0) }
            uiView.performBatchUpdates({
                uiView.insertRows(at: newIndexPaths, with: .none)
            }, completion: nil)
            uiView.layoutIfNeeded()

            // If the user was near the bottom, keep them pinned as new content arrives.
            if context.coordinator.wasNearBottomBeforeUpdate {
                context.coordinator.scheduleScrollToBottom(uiView, animated: true)
            }

            context.coordinator.lastFirstMessageID = newFirstID
            context.coordinator.lastLastMessageID = newLastID
            context.coordinator.lastMessageCount = newCount
            context.coordinator.lastFontSize = fontSize
            for idx in oldCount..<newCount {
                context.coordinator.lastIndexByID[messages[idx].id] = idx
            }
        } else if shouldReload {
            // Detect whether we're prepending older messages by checking if the previous first
            // message still exists but moved down (index increased).
            let oldContentHeight = uiView.contentSize.height
            let oldOffsetY = uiView.contentOffset.y

            uiView.reloadData()

            // `UITableView` can keep cached row heights when only the hosted SwiftUI
            // content changes (e.g. font size). `beginUpdates/endUpdates` forces a
            // fresh layout pass.
            if fontSizeChanged {
                uiView.beginUpdates()
                uiView.endUpdates()
            }
            uiView.layoutIfNeeded()

            if let previousFirstID,
               let oldIndex = context.coordinator.lastIndexByID[previousFirstID],
               let newIndex = messages.firstIndex(where: { $0.id == previousFirstID }),
               newIndex > oldIndex {
                // We prepended content (older messages).
                // Preserve the current viewport by shifting the content offset down by the
                // delta in content height (standard "infinite scroll" behavior).
                //
                // This keeps the user's reading position stable while injecting older messages
                // above, letting them scroll up to see the newly loaded content.
                let newContentHeight = uiView.contentSize.height
                let delta = newContentHeight - oldContentHeight
                uiView.setContentOffset(CGPoint(x: 0, y: oldOffsetY + delta), animated: false)
            } else if !context.coordinator.didInitialScrollToBottom,
                      !messages.isEmpty {
                // First non-empty render: scroll to bottom.
                context.coordinator.scheduleScrollToBottom(uiView, animated: false)
                context.coordinator.didInitialScrollToBottom = true
            } else if let previousLastID,
                      previousLastID != newLastID,
                      context.coordinator.wasNearBottomBeforeUpdate {
                // New messages appended while the user is (roughly) at the bottom -> keep pinned.
                context.coordinator.scheduleScrollToBottom(uiView, animated: true)
            }

            context.coordinator.lastFirstMessageID = newFirstID
            context.coordinator.lastLastMessageID = newLastID
            context.coordinator.lastMessageCount = messages.count
            context.coordinator.lastFontSize = fontSize
            // Message IDs are expected to be unique, but during reconciling
            // (optimistic sends, ui.event rows, pagination merges) we can
            // temporarily observe duplicates. Avoid crashing by keeping the
            // last observed index for each id.
            var indexByID: [String: Int] = [:]
            indexByID.reserveCapacity(messages.count)
            for (idx, message) in messages.enumerated() {
                indexByID[message.id] = idx
            }
            context.coordinator.lastIndexByID = indexByID
        }

        // Consume external scroll requests.
        if let request = scrollRequest {
            if context.coordinator.lastHandledScrollRequestID == request.id {
                return
            }
            context.coordinator.lastHandledScrollRequestID = request.id
            switch request.target {
            case .bottom:
                context.coordinator.scheduleScrollToBottom(uiView, animated: true)
            case .message(let id, _):
                context.coordinator.scrollToMessageID(uiView, id: id)
            }
            DispatchQueue.main.async {
                onConsumeScrollRequest()
            }
        }
    }

    // MARK: - Coordinator

    final class Coordinator: NSObject, UITableViewDataSource, UITableViewDelegate, UIScrollViewDelegate, UIGestureRecognizerDelegate {
        var parent: TerminalMessagesView

        var didInitialScrollToBottom: Bool = false
        var lastFirstMessageID: String?
        var lastLastMessageID: String?
        var lastIndexByID: [String: Int] = [:]
        var lastMessageCount: Int = 0
        var lastFontSize: CGFloat = 0
        var lastHandledScrollRequestID: UUID?
        private var pendingScrollWorkItem: DispatchWorkItem?
        private var pendingSecondScrollWorkItem: DispatchWorkItem?

        // Set during scroll events; used by updateUIView to decide whether to auto-scroll.
        var wasNearBottomBeforeUpdate: Bool = true

        init(parent: TerminalMessagesView) {
            self.parent = parent
        }

        func gestureRecognizer(_ gestureRecognizer: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith otherGestureRecognizer: UIGestureRecognizer) -> Bool {
            true
        }

        @objc func onDoubleTap() {
            parent.onDoubleTap()
        }

        func tableView(_ tableView: UITableView, numberOfRowsInSection section: Int) -> Int {
            parent.messages.count
        }

        func tableView(_ tableView: UITableView, cellForRowAt indexPath: IndexPath) -> UITableViewCell {
            let message = parent.messages[indexPath.row]

            // Include font size in the reuse identifier so changing the
            // transcript font forces a fresh hosting configuration instead of
            // reusing a cached view hierarchy.
            let reuseID = "MessageCell-\(Int(parent.fontSize))"
            let cell = tableView.dequeueReusableCell(withIdentifier: reuseID) ?? UITableViewCell(style: .default, reuseIdentifier: reuseID)
            cell.backgroundColor = .clear
            cell.contentView.backgroundColor = .clear
            cell.selectionStyle = .none

            if #available(iOS 16.0, *) {
                cell.contentConfiguration = UIHostingConfiguration {
                    MessageBubble(message: message, fontSize: parent.fontSize)
                        .padding(.vertical, Layout.messageVerticalPadding)
                        .padding(.horizontal, MessageBubble.Layout.cellHorizontalPadding)
                }
            } else {
                // iOS 16+ only in this app, but keep a safe fallback.
                cell.textLabel?.text = ""
            }

            return cell
        }

        func scrollViewWillBeginDragging(_ scrollView: UIScrollView) {
            wasNearBottomBeforeUpdate = isNearBottom(scrollView)
        }

        func scrollViewDidScroll(_ scrollView: UIScrollView) {
            wasNearBottomBeforeUpdate = isNearBottom(scrollView)

            // Trigger infinite-scroll history fetch when reaching the top.
            guard parent.hasMoreHistory else { return }
            guard !parent.isLoadingHistory else { return }

            // Treat "near the top" as a trigger (not just overscroll), so users don't have to
            // do the awkward "scroll down then up" dance to load the next page.
            let topY = -scrollView.adjustedContentInset.top
            if scrollView.contentOffset.y <= (topY + Layout.historyLoadTriggerOffset) {
                // Make the refresh control visible even when we trigger based on position.
                if let tableView = scrollView as? UITableView,
                   let refresh = tableView.refreshControl,
                   !refresh.isRefreshing {
                    refresh.beginRefreshing()
                    // Pull the content down enough to reveal the spinner.
                    let spinnerY = topY - refresh.bounds.height
                    tableView.setContentOffset(CGPoint(x: 0, y: spinnerY), animated: true)
                }

                parent.onLoadOlder()
            }
        }

        @objc func onRefreshControl() {
            guard parent.hasMoreHistory else { return }
            guard !parent.isLoadingHistory else { return }
            parent.onLoadOlder()
        }

        func scheduleScrollToBottom(_ tableView: UITableView, animated: Bool) {
            pendingScrollWorkItem?.cancel()
            pendingSecondScrollWorkItem?.cancel()

            let first = DispatchWorkItem { [weak self, weak tableView] in
                guard let self, let tableView else { return }
                self.scrollToBottomNow(tableView, animated: animated)
            }
            pendingScrollWorkItem = first
            DispatchQueue.main.async(execute: first)

            // A second pass catches late row-height changes from hosted SwiftUI
            // content (Markdown, dynamic type) without building up an unbounded
            // queue when messages stream in quickly.
            let second = DispatchWorkItem { [weak self, weak tableView] in
                guard let self, let tableView else { return }
                self.scrollToBottomNow(tableView, animated: animated)
            }
            pendingSecondScrollWorkItem = second
            DispatchQueue.main.asyncAfter(
                deadline: .now() + Layout.scrollSecondPassDelaySeconds,
                execute: second
            )
        }

        func scrollToBottomNow(_ tableView: UITableView, animated: Bool) {
            let count = parent.messages.count
            guard count > 0 else { return }
            let indexPath = IndexPath(row: count - 1, section: 0)
            // When the transcript is large, or when hosted SwiftUI content is still
            // settling row heights, a single `scrollToRow` can land slightly above
            // the true bottom. Scroll after layout, then also clamp to the absolute
            // bottom offset as a fallback.
            tableView.layoutIfNeeded()
            guard tableView.numberOfRows(inSection: 0) > indexPath.row else { return }
            tableView.scrollToRow(at: indexPath, at: .bottom, animated: animated)

            let visibleHeight = tableView.bounds.height
                - tableView.adjustedContentInset.top
                - tableView.adjustedContentInset.bottom
            if visibleHeight <= 0 { return }
            let bottomY = max(
                -tableView.adjustedContentInset.top,
                tableView.contentSize.height - visibleHeight
            )
            tableView.setContentOffset(CGPoint(x: 0, y: bottomY), animated: animated)
        }

        func scrollToMessageID(_ tableView: UITableView, id: String) {
            guard let index = parent.messages.firstIndex(where: { $0.id == id }) else { return }
            let indexPath = IndexPath(row: index, section: 0)
            tableView.scrollToRow(at: indexPath, at: .top, animated: true)
        }

        private func isNearBottom(_ scrollView: UIScrollView, threshold: CGFloat = Layout.nearBottomThreshold) -> Bool {
            let visibleHeight = scrollView.bounds.height - scrollView.adjustedContentInset.top - scrollView.adjustedContentInset.bottom
            if visibleHeight <= 0 { return true }
            let y = scrollView.contentOffset.y + scrollView.adjustedContentInset.top
            let bottomY = scrollView.contentSize.height - visibleHeight
            return (bottomY - y) <= threshold
        }
    }
}
