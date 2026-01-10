import SwiftUI
import UIKit

/// TerminalTranscriptCollectionView renders the transcript using a
/// `UICollectionView` with self-sizing cells.
///
/// Rationale:
/// SwiftUI's `ScrollView` + `LazyVStack` can exhibit scroll indicator ("hatch")
/// instability when displaying large transcripts with variable-height Markdown
/// blocks. A UIKit-backed list has a more deterministic sizing and reuse model,
/// which can make scrolling feel calmer under heavy, heterogeneous content.
struct TerminalTranscriptCollectionView: UIViewRepresentable {
    typealias UIViewType = UICollectionView

    let messages: [MessageItem]
    let hasMoreHistory: Bool
    let isLoadingHistory: Bool
    let isLoadingLatest: Bool
    let onLoadOlder: () -> Void
    let onDoubleTap: () -> Void
    let scrollRequest: ScrollRequest?
    let onConsumeScrollRequest: () -> Void
    let fontSize: CGFloat

    /// Layout holds sizing constants for the collection transcript.
    enum Layout {
        /// transcriptTopBottomInset keeps bubbles slightly away from edges.
        static let transcriptTopBottomInset: CGFloat = 4

        /// transcriptHorizontalPaddingMin is the minimum horizontal padding
        /// applied inside each transcript row.
        static let transcriptHorizontalPaddingMin: CGFloat = 8

        /// transcriptHorizontalPaddingMax is the maximum horizontal padding
        /// applied inside each transcript row.
        static let transcriptHorizontalPaddingMax: CGFloat = 20

        /// transcriptHorizontalPaddingEmMultiplier scales the horizontal padding
        /// relative to the font size (~1em by default).
        static let transcriptHorizontalPaddingEmMultiplier: CGFloat = 1.0

        /// horizontalPadding returns the per-row horizontal padding, sized in
        /// "em" units and clamped to keep it subtle.
        static func horizontalPadding(fontSize: CGFloat) -> CGFloat {
            let em = max(0, fontSize) * transcriptHorizontalPaddingEmMultiplier
            return min(max(em, transcriptHorizontalPaddingMin), transcriptHorizontalPaddingMax)
        }

        /// estimatedRowHeight is a baseline hint for self-sizing.
        static let estimatedRowHeight: CGFloat = 120

        /// nearBottomThreshold determines if we should auto-pin to bottom.
        static let nearBottomThreshold: CGFloat = 80

        /// scrollSecondPassDelaySeconds retries scroll-to-bottom after hosted
        /// SwiftUI content has settled its final height.
        static let scrollSecondPassDelaySeconds: TimeInterval = 0.08

        static let sectionID = 0
        static let bottomAnchorID = "__terminalTranscriptBottomAnchor__"
        static let loadingID = "__terminalTranscriptLoading__"
        static let historyHintID = "__terminalTranscriptHistoryHint__"
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(parent: self)
    }

    func makeUIView(context: Context) -> UICollectionView {
        var listConfig = UICollectionLayoutListConfiguration(appearance: .plain)
        listConfig.showsSeparators = false
        let layout = UICollectionViewCompositionalLayout.list(using: listConfig)

        let collectionView = UICollectionView(frame: .zero, collectionViewLayout: layout)
        collectionView.backgroundColor = .clear
        collectionView.showsVerticalScrollIndicator = true
        collectionView.showsHorizontalScrollIndicator = false
        collectionView.keyboardDismissMode = .interactive
        collectionView.delegate = context.coordinator
        collectionView.alwaysBounceVertical = true
        collectionView.alwaysBounceHorizontal = false
        collectionView.contentInset = UIEdgeInsets(
            top: Layout.transcriptTopBottomInset,
            left: 0,
            bottom: Layout.transcriptTopBottomInset,
            right: 0
        )

        let refresh = UIRefreshControl()
        refresh.addTarget(context.coordinator, action: #selector(Coordinator.onRefreshControl(_:)), for: .valueChanged)
        collectionView.refreshControl = refresh

        let doubleTap = UITapGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.onDoubleTap))
        doubleTap.numberOfTapsRequired = 2
        doubleTap.cancelsTouchesInView = false
        collectionView.addGestureRecognizer(doubleTap)

        context.coordinator.configureDataSource(collectionView: collectionView)
        return collectionView
    }

    func updateUIView(_ uiView: UICollectionView, context: Context) {
        context.coordinator.parent = self

        let changedMessageIDs = context.coordinator.updateItems(messages: messages)

        // Build the list of rendered ids. We intentionally include a stable
        // bottom anchor so scroll-to-bottom doesn't depend on the last message
        // row being realized.
        var ids: [String] = []
        ids.reserveCapacity(messages.count + 3)

        if isLoadingLatest, messages.isEmpty {
            ids.append(Layout.loadingID)
        }
        if hasMoreHistory {
            ids.append(Layout.historyHintID)
        }
        ids.append(contentsOf: messages.map(\.id))
        ids.append(Layout.bottomAnchorID)

        let previousLastID = context.coordinator.lastLastID
        let newLastID = messages.last?.id
        let previousFirstID = context.coordinator.lastFirstID
        let newFirstID = messages.first?.id
        let previousMessageCount = context.coordinator.lastMessageCount
        let newMessageCount = messages.count
        let oldCount = context.coordinator.lastRenderedCount
        let newCount = ids.count
        let fontSizeChanged = Int(context.coordinator.lastFontSize) != Int(fontSize)
        let isLoadingHistoryChanged = context.coordinator.lastIsLoadingHistory != isLoadingHistory
        let isLoadingLatestChanged = context.coordinator.lastIsLoadingLatest != isLoadingLatest

        var reconfigureIDs = changedMessageIDs
        if isLoadingHistoryChanged, ids.contains(Layout.historyHintID) {
            reconfigureIDs.append(Layout.historyHintID)
        }
        if isLoadingLatestChanged, ids.contains(Layout.loadingID) {
            reconfigureIDs.append(Layout.loadingID)
        }

        // When we prepend older history (pull-to-refresh), preserve the visible
        // scroll position so the list extends upward without "jumping" the user
        // to the top of the newly loaded page.
        let isHistoryPrepend =
            !fontSizeChanged &&
            oldCount != 0 &&
            previousMessageCount != 0 &&
            newMessageCount > previousMessageCount &&
            previousFirstID != nil &&
            newFirstID != nil &&
            previousFirstID != newFirstID &&
            previousLastID == newLastID

        let anchor = isHistoryPrepend ? context.coordinator.captureScrollAnchor(collectionView: uiView) : nil
        let wasRefreshing = uiView.refreshControl?.isRefreshing == true

        context.coordinator.applySnapshot(
            ids: ids,
            reconfigureIDs: reconfigureIDs,
            collectionView: uiView,
            reload: fontSizeChanged || oldCount == 0
        ) { [weak uiView] in
            guard let uiView else { return }
            if let anchor {
                context.coordinator.restoreScrollAnchor(collectionView: uiView, anchor: anchor)

                // Second pass: hosted SwiftUI content (Markdown) may settle its
                // final height after the first layout pass, which can otherwise
                // introduce a small jump right after the history prepend.
                DispatchQueue.main.asyncAfter(
                    deadline: .now() + Layout.scrollSecondPassDelaySeconds
                ) { [weak uiView] in
                    guard let uiView else { return }
                    context.coordinator.restoreScrollAnchor(collectionView: uiView, anchor: anchor)
                }
            }

            // Avoid forcing `beginRefreshing()` programmatically. Doing so
            // without adjusting the content offset can leave the refresh
            // control stuck. We only stop the spinner once loading finishes
            // (or when the user pulls despite there being no more history).
            if wasRefreshing, (!context.coordinator.parent.isLoadingHistory ||
                               !context.coordinator.parent.hasMoreHistory) {
                uiView.refreshControl?.endRefreshing()
            }
        }

        context.coordinator.lastRenderedCount = newCount
        context.coordinator.lastLastID = newLastID
        context.coordinator.lastFirstID = newFirstID
        context.coordinator.lastMessageCount = newMessageCount
        context.coordinator.lastFontSize = fontSize
        context.coordinator.lastIsLoadingHistory = isLoadingHistory
        context.coordinator.lastIsLoadingLatest = isLoadingLatest

        // Initial scroll to bottom once we have content.
        if !context.coordinator.didInitialScrollToBottom,
           !messages.isEmpty {
            context.coordinator.scheduleScrollToBottom(collectionView: uiView, animated: false)
            context.coordinator.didInitialScrollToBottom = true
        } else if let previousLastID,
                  previousLastID != newLastID,
                  context.coordinator.wasNearBottomBeforeUpdate {
            // Append-only updates: keep pinned when near the bottom.
            context.coordinator.scheduleScrollToBottom(collectionView: uiView, animated: true)
        }

        // Consume external scroll requests.
        if let request = scrollRequest {
            if context.coordinator.lastHandledScrollRequestID == request.id {
                return
            }
            context.coordinator.lastHandledScrollRequestID = request.id
            switch request.target {
            case .bottom:
                context.coordinator.scheduleScrollToBottom(collectionView: uiView, animated: true)
            case .message(let id, _):
                context.coordinator.scrollToMessageID(collectionView: uiView, id: id)
            }
            DispatchQueue.main.async {
                onConsumeScrollRequest()
            }
        }
    }

    // MARK: - Coordinator

    final class Coordinator: NSObject, UICollectionViewDelegate {
        var parent: TerminalTranscriptCollectionView

        var didInitialScrollToBottom: Bool = false
        var lastHandledScrollRequestID: UUID?
        var lastLastID: String?
        var lastFirstID: String?
        var lastMessageCount: Int = 0
        var lastRenderedCount: Int = 0
        var lastFontSize: CGFloat = 0
        var lastIsLoadingHistory: Bool = false
        var lastIsLoadingLatest: Bool = false
        var wasNearBottomBeforeUpdate: Bool = true

        private var itemsByID: [String: MessageItem] = [:]
        private var dataSource: UICollectionViewDiffableDataSource<Int, String>?
        private var pendingScrollWorkItem: DispatchWorkItem?
        private var pendingSecondScrollWorkItem: DispatchWorkItem?

        init(parent: TerminalTranscriptCollectionView) {
            self.parent = parent
        }

        func configureDataSource(collectionView: UICollectionView) {
            let cellRegistration = UICollectionView.CellRegistration<UICollectionViewListCell, String> { [weak self] cell, _, id in
                guard let self else { return }
                cell.backgroundConfiguration = UIBackgroundConfiguration.clear()
                let horizontalPadding = Layout.horizontalPadding(fontSize: self.parent.fontSize)

                if id == Layout.loadingID {
                    cell.contentConfiguration = UIHostingConfiguration {
                        ProgressView()
                            .padding(.vertical, 12)
                            .padding(.horizontal, horizontalPadding)
                    }
                    return
                }
                if id == Layout.historyHintID {
                    cell.contentConfiguration = UIHostingConfiguration {
                        TerminalHistoryHint(isLoadingHistory: self.parent.isLoadingHistory)
                            .padding(.horizontal, horizontalPadding)
                    }
                    return
                }
                if id == Layout.bottomAnchorID {
                    cell.contentConfiguration = UIHostingConfiguration {
                        Color.clear.frame(height: 1)
                    }
                    return
                }
                guard let item = self.itemsByID[id] else {
                    cell.contentConfiguration = UIHostingConfiguration {
                        EmptyView()
                    }
                    return
                }

                cell.contentConfiguration = UIHostingConfiguration {
                    MessageBubble(message: item, fontSize: self.parent.fontSize)
                        .padding(.horizontal, horizontalPadding)
                }
                .margins(.all, 0)
            }

            dataSource = UICollectionViewDiffableDataSource<Int, String>(collectionView: collectionView) { collectionView, indexPath, id in
                collectionView.dequeueConfiguredReusableCell(using: cellRegistration, for: indexPath, item: id)
            }
        }

        /// updateItems replaces the backing map used by the cell provider and
        /// returns any message ids whose content changed in-place.
        ///
        /// Rationale:
        /// `UICollectionViewDiffableDataSource` is keyed on ids. When a message
        /// is updated without changing its id (e.g. a tool UI event transitions
        /// from "start" to "end" with the same event id), we must explicitly
        /// reconfigure the corresponding cell or the UI will remain stale.
        func updateItems(messages: [MessageItem]) -> [String] {
            var dict: [String: MessageItem] = [:]
            dict.reserveCapacity(messages.count)
            var changed: [String] = []
            changed.reserveCapacity(4)
            for item in messages {
                if let existing = itemsByID[item.id], existing != item {
                    changed.append(item.id)
                }
                dict[item.id] = item
            }
            itemsByID = dict
            return changed
        }

        func applySnapshot(
            ids: [String],
            reconfigureIDs: [String],
            collectionView: UICollectionView,
            reload: Bool,
            completion: (() -> Void)? = nil
        ) {
            guard let dataSource else { return }
            var snapshot = NSDiffableDataSourceSnapshot<Int, String>()
            snapshot.appendSections([Layout.sectionID])
            snapshot.appendItems(ids, toSection: Layout.sectionID)
            // Avoid animations: we apply very frequently during streaming.
            if reload {
                dataSource.applySnapshotUsingReloadData(snapshot, completion: completion)
            } else {
                if !reconfigureIDs.isEmpty {
                    snapshot.reconfigureItems(reconfigureIDs)
                }
                dataSource.apply(snapshot, animatingDifferences: false, completion: completion)
            }
        }

        struct ScrollAnchor {
            let id: String
            let offsetFromTop: CGFloat
        }

        func captureScrollAnchor(collectionView: UICollectionView) -> ScrollAnchor? {
            guard let dataSource else { return nil }
            collectionView.layoutIfNeeded()

            let visible = collectionView.indexPathsForVisibleItems
            if visible.isEmpty { return nil }

            // Choose the top-most visible "real" message row as our anchor.
            // This avoids anchoring to the history hint / bottom anchor rows.
            var best: (id: String, minY: CGFloat, offsetFromTop: CGFloat)?

            for indexPath in visible {
                guard let id = dataSource.itemIdentifier(for: indexPath) else { continue }
                if id == Layout.historyHintID || id == Layout.loadingID || id == Layout.bottomAnchorID {
                    continue
                }
                guard let attrs = collectionView.layoutAttributesForItem(at: indexPath) else { continue }
                let minY = attrs.frame.minY
                let offset = minY - collectionView.contentOffset.y
                if let current = best {
                    if minY < current.minY {
                        best = (id: id, minY: minY, offsetFromTop: offset)
                    }
                } else {
                    best = (id: id, minY: minY, offsetFromTop: offset)
                }
            }

            // If we only saw sentinel rows (e.g. pulling from the top), fall back
            // to anchoring the first visible row.
            if best == nil {
                let fallback = visible.sorted().first
                if let fallback,
                   let id = dataSource.itemIdentifier(for: fallback),
                   let attrs = collectionView.layoutAttributesForItem(at: fallback) {
                    let offset = attrs.frame.minY - collectionView.contentOffset.y
                    best = (id: id, minY: attrs.frame.minY, offsetFromTop: offset)
                }
            }

            guard let best else { return nil }
            return ScrollAnchor(id: best.id, offsetFromTop: best.offsetFromTop)
        }

        func restoreScrollAnchor(collectionView: UICollectionView, anchor: ScrollAnchor) {
            guard let dataSource else { return }
            collectionView.layoutIfNeeded()

            guard let indexPath = dataSource.indexPath(for: anchor.id),
                  let attrs = collectionView.layoutAttributesForItem(at: indexPath) else {
                return
            }

            let desiredOffsetY = attrs.frame.minY - anchor.offsetFromTop
            let minOffsetY = -collectionView.adjustedContentInset.top
            let maxOffsetY = max(
                minOffsetY,
                collectionView.contentSize.height - collectionView.bounds.height + collectionView.adjustedContentInset.bottom
            )
            let clamped = min(max(desiredOffsetY, minOffsetY), maxOffsetY)
            collectionView.setContentOffset(CGPoint(x: collectionView.contentOffset.x, y: clamped), animated: false)
        }

        @objc func onRefreshControl(_ sender: UIRefreshControl) {
            // If there's no more history (or we're already loading), stop the
            // spinner promptly. Without this, the refresh animation can remain
            // visible even though no request will be issued.
            guard parent.hasMoreHistory, !parent.isLoadingHistory else {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) {
                    sender.endRefreshing()
                }
                return
            }

            // If the request is fast, applying the snapshot immediately can
            // feel like the refresh "pops" in. Give the spinner a brief moment
            // to animate before starting the load.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.25) { [weak self] in
                guard let self else { return }
                guard self.parent.hasMoreHistory, !self.parent.isLoadingHistory else {
                    sender.endRefreshing()
                    return
                }
                self.parent.onLoadOlder()
            }
        }

        @objc func onDoubleTap() {
            parent.onDoubleTap()
        }

        func scrollViewDidScroll(_ scrollView: UIScrollView) {
            // Keep a best-effort "near bottom" bit so updates can keep the view pinned.
            let visibleHeight = scrollView.bounds.height
            let contentHeight = scrollView.contentSize.height
            let offsetY = scrollView.contentOffset.y
            let distanceFromBottom = contentHeight - (offsetY + visibleHeight)
            wasNearBottomBeforeUpdate = distanceFromBottom < Layout.nearBottomThreshold
        }

        func scrollToMessageID(collectionView: UICollectionView, id: String) {
            guard let dataSource else { return }
            guard let indexPath = dataSource.indexPath(for: id) else { return }
            collectionView.scrollToItem(at: indexPath, at: .top, animated: true)
        }

        func scheduleScrollToBottom(collectionView: UICollectionView, animated: Bool) {
            pendingScrollWorkItem?.cancel()
            pendingSecondScrollWorkItem?.cancel()

            let work = DispatchWorkItem { [weak self, weak collectionView] in
                guard let self, let collectionView else { return }
                self.scrollToBottomNow(collectionView: collectionView, animated: animated)

                // Second pass after a brief delay to account for hosted SwiftUI
                // content settling its final height (Markdown).
                let second = DispatchWorkItem { [weak self, weak collectionView] in
                    guard let self, let collectionView else { return }
                    self.scrollToBottomNow(collectionView: collectionView, animated: false)
                }
                self.pendingSecondScrollWorkItem = second
                DispatchQueue.main.asyncAfter(deadline: .now() + Layout.scrollSecondPassDelaySeconds, execute: second)
            }

            pendingScrollWorkItem = work
            DispatchQueue.main.async(execute: work)
        }

        private func scrollToBottomNow(collectionView: UICollectionView, animated: Bool) {
            guard let dataSource else { return }
            guard let indexPath = dataSource.indexPath(for: Layout.bottomAnchorID) else { return }
            collectionView.scrollToItem(at: indexPath, at: .bottom, animated: animated)
        }
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
