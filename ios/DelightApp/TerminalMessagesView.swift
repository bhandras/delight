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
        tableView.estimatedRowHeight = 120
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
        let messageCountChanged = context.coordinator.lastMessageCount != messages.count
        let messageEdgeChanged = (previousFirstID != newFirstID) || (previousLastID != newLastID)
        let shouldReload = fontSizeChanged || messageCountChanged || messageEdgeChanged

        if shouldReload {
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
                context.coordinator.scrollToBottom(uiView, animated: false)
                context.coordinator.didInitialScrollToBottom = true
            } else if let previousLastID,
                      previousLastID != newLastID,
                      context.coordinator.wasNearBottomBeforeUpdate {
                // New messages appended while the user is (roughly) at the bottom -> keep pinned.
                context.coordinator.scrollToBottom(uiView, animated: true)
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
            switch request.target {
            case .bottom:
                context.coordinator.scrollToBottom(uiView, animated: true)
            case .message(let id, _):
                context.coordinator.scrollToMessageID(uiView, id: id)
            }
            DispatchQueue.main.async {
                onConsumeScrollRequest()
            }
        }
    }

    // MARK: - Coordinator

    final class Coordinator: NSObject, UITableViewDataSource, UITableViewDelegate, UIScrollViewDelegate {
        var parent: TerminalMessagesView

        var didInitialScrollToBottom: Bool = false
        var lastFirstMessageID: String?
        var lastLastMessageID: String?
        var lastIndexByID: [String: Int] = [:]
        var lastMessageCount: Int = 0
        var lastFontSize: CGFloat = 0

        // Set during scroll events; used by updateUIView to decide whether to auto-scroll.
        var wasNearBottomBeforeUpdate: Bool = true

        init(parent: TerminalMessagesView) {
            self.parent = parent
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

        func scrollToBottom(_ tableView: UITableView, animated: Bool) {
            let count = parent.messages.count
            guard count > 0 else { return }
            let indexPath = IndexPath(row: count - 1, section: 0)
            tableView.scrollToRow(at: indexPath, at: .bottom, animated: animated)
        }

        func scrollToMessageID(_ tableView: UITableView, id: String) {
            guard let index = parent.messages.firstIndex(where: { $0.id == id }) else { return }
            let indexPath = IndexPath(row: index, section: 0)
            tableView.scrollToRow(at: indexPath, at: .top, animated: true)
        }

        private func isNearBottom(_ scrollView: UIScrollView, threshold: CGFloat = 80) -> Bool {
            let visibleHeight = scrollView.bounds.height - scrollView.adjustedContentInset.top - scrollView.adjustedContentInset.bottom
            if visibleHeight <= 0 { return true }
            let y = scrollView.contentOffset.y + scrollView.adjustedContentInset.top
            let bottomY = scrollView.contentSize.height - visibleHeight
            return (bottomY - y) <= threshold
        }
    }
}
