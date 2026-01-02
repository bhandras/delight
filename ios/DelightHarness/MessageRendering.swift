import SwiftUI
import MarkdownUI

/// MessageBubble renders a single chat message.
///
/// The view is used by `TerminalMessagesView` inside a `UITableView` cell.
struct MessageBubble: View {
    let message: MessageItem

    /// Layout holds display constants for the terminal transcript.
    enum Layout {
        /// cellHorizontalPadding is the inset applied by the host table cell.
        static let cellHorizontalPadding: CGFloat = 2

        /// bubbleHorizontalPadding is the small gutter between the bubble and
        /// the cell edges.
        static let bubbleHorizontalPadding: CGFloat = 0.5

        /// oppositeSideSpacerMinimum keeps bubbles from spanning the full
        /// screen width while avoiding excessive whitespace on the other side.
        static let oppositeSideSpacerMinimum: CGFloat = 6

        /// incomingLeadingTextInset provides a small left inset so incoming
        /// text doesn't hug the edge when not using a colored bubble.
        static let incomingLeadingTextInset: CGFloat = 0.5
    }

    var body: some View {
        HStack(alignment: .top, spacing: 0) {
            if message.role == .user { Spacer(minLength: Layout.oppositeSideSpacerMinimum) }
            VStack(alignment: .leading, spacing: 8) {
                ForEach(Array(message.blocks.enumerated()), id: \.offset) { _, block in
                    MessageBlockView(block: block)
                }
            }
            .padding(message.role == .user ? 8 : 0)
            .padding(.leading, message.role == .user ? 0 : Layout.incomingLeadingTextInset)
            .background(message.role == .user ? Theme.userBubble : Color.clear)
            .clipIf(message.role == .user) {
                $0.clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
            }
            if message.role != .user { Spacer(minLength: Layout.oppositeSideSpacerMinimum) }
        }
        .frame(maxWidth: .infinity, alignment: message.role == .user ? .trailing : .leading)
        .padding(.horizontal, Layout.bubbleHorizontalPadding)
    }
}

private struct MessageBlockView: View {
    let block: MessageBlock

    var body: some View {
        switch block {
        case .text(let text):
            MarkdownText(text: text)
        case .code(let language, let content):
            CodeBlockView(language: language, content: content)
        case .toolCall(let summary):
            ToolChipView(summary: summary)
        }
    }
}

private struct MarkdownText: View {
    let text: String

    /// Typography holds constants for Markdown rendering in the terminal
    /// transcript.
    enum Typography {
        /// bodyFontFamily is the family name used by MarkdownUI's font system.
        ///
        /// MarkdownUI composes font families with weight/style dynamically,
        /// which is different from `Font.custom` (that uses a PostScript face
        /// name like "AvenirNext-Regular").
        static let bodyFontFamily: String = "Avenir Next"

        /// bodyFontSize matches `Theme.body` (see `ContentView.swift`).
        static let bodyFontSize: CGFloat = 16

        /// paragraphBottomMarginEm keeps paragraphs readable without the large
        /// default Markdown spacing.
        static let paragraphBottomMarginEm: Double = 0.35

        /// paragraphLineSpacingEm keeps agent output compact while avoiding
        /// dense text blocks.
        static let paragraphLineSpacingEm: Double = 0.08

        /// headingTopMarginEm is the small top inset for headings.
        static let headingTopMarginEm: Double = 0.2

        /// headingBottomMarginEm separates headings from following text.
        static let headingBottomMarginEm: Double = 0.15
    }

    var body: some View {
        Markdown(formatMarkdownForDisplay(text))
            // MarkdownUI's built-in GitHub theme applies a background color to
            // all text, which reads like a "highlight" behind every paragraph
            // in our chat UI. Keep the rendering minimal and let the bubble
            // backgrounds define the container instead.
            .markdownTheme(.basic)
            .markdownTextStyle(\.text) {
                FontFamily(.custom(Typography.bodyFontFamily))
                FontSize(Typography.bodyFontSize)
                ForegroundColor(Theme.messageText)
                BackgroundColor(nil)
            }
            .markdownTextStyle(\.code) {
                FontFamilyVariant(.monospaced)
                FontSize(.em(0.9))
                ForegroundColor(Theme.codeText)
                BackgroundColor(Theme.codeBackground)
            }
            // The default MarkdownUI theme treats headings as large display
            // elements. In chat transcripts we want headings to read as
            // emphasized lines rather than changing the overall typography.
            .markdownBlockStyle(\.heading1, body: compactHeading)
            .markdownBlockStyle(\.heading2, body: compactHeading)
            .markdownBlockStyle(\.heading3, body: compactHeading)
            .markdownBlockStyle(\.heading4, body: compactHeading)
            .markdownBlockStyle(\.heading5, body: compactHeading)
            .markdownBlockStyle(\.heading6, body: compactHeading)
            .markdownBlockStyle(\.paragraph) { configuration in
                configuration.label
                    .fixedSize(horizontal: false, vertical: true)
                    .relativeLineSpacing(.em(Typography.paragraphLineSpacingEm))
                    .markdownMargin(top: .zero, bottom: .em(Typography.paragraphBottomMarginEm))
            }
    }

    /// compactHeading renders all Markdown heading levels using the transcript
    /// body font size so agent output remains readable and visually consistent.
    private func compactHeading(_ configuration: BlockConfiguration) -> some View {
        configuration.label
            .markdownMargin(
                top: .em(Typography.headingTopMarginEm),
                bottom: .em(Typography.headingBottomMarginEm)
            )
            .markdownTextStyle {
                FontWeight(.semibold)
                FontSize(.em(1))
            }
    }

    /// formatMarkdownForDisplay converts soft line breaks (single `\n`) into hard
    /// breaks so SwiftUI doesn't collapse them into spaces.
    ///
    /// This keeps agent output readable on iOS while preserving fenced code
    /// blocks as-is (no trailing whitespace injected into code).
    private func formatMarkdownForDisplay(_ raw: String) -> String {
        let normalized = raw
            .replacingOccurrences(of: "\r\n", with: "\n")
            .replacingOccurrences(of: "\r", with: "\n")

        // Split while preserving empty lines so we can keep paragraph breaks.
        let lines = normalized
            .split(omittingEmptySubsequences: false, whereSeparator: \.isNewline)
            .map(String.init)
        if lines.count <= 1 {
            return normalized
        }

        // Rebuild content inserting either:
        // - "\n" for paragraph breaks / code fences, or
        // - "  \n" for soft line breaks we want to keep visually.
        var result = ""
        result.reserveCapacity(normalized.count + lines.count*2)

        var inFence = false
        for i in lines.indices {
            let line = lines[i]
            result += line

            if i == lines.index(before: lines.endIndex) {
                continue
            }

            let trimmed = line.trimmingCharacters(in: CharacterSet.whitespaces)
            let isFenceLine = trimmed.hasPrefix("```")
            let nextLine = lines[lines.index(after: i)]

            // Fence delimiter lines should never get hard-break whitespace.
            if isFenceLine {
                result += "\n"
                inFence.toggle()
                continue
            }

            // Preserve true paragraph breaks as-is.
            if line.isEmpty || nextLine.isEmpty {
                result += "\n"
                continue
            }

            // Never inject hard-break whitespace inside fenced code blocks.
            if inFence {
                result += "\n"
                continue
            }

            // Two spaces before newline is a Markdown hard line break.
            result += "  \n"
        }

        return result
    }
}

private struct CodeBlockView: View {
    let language: String?
    let content: String

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let language, !language.isEmpty {
                Text(language)
                    .font(Theme.codeLabel)
                    .foregroundColor(Theme.accent)
            }
            Text(content)
                .font(Theme.codeFont)
                .foregroundColor(Theme.codeText)
        }
        .padding(12)
        .background(Theme.codeBackground)
        .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .stroke(Theme.codeBorder, lineWidth: 1)
        )
    }
}

private struct ToolChipView: View {
    let summary: ToolCallSummary

    var body: some View {
        if summary.icon == "sparkles" {
            ActivityChip(text: summary.title)
        } else {
            HStack(spacing: 6) {
                Image(systemName: summary.icon)
                    .font(.system(size: 12, weight: .semibold))
                Text(summary.title)
                    .font(Theme.caption)
                    .lineLimit(1)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(Theme.toolChipBackground)
            .foregroundColor(Theme.toolChipText)
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        }
    }
}

/// ActivityChip renders a short "thinking"/activity label with animated trailing dots.
struct ActivityChip: View {
    let text: String

    var body: some View {
        let label = stripTrailingEllipsis(text)
        HStack {
            Text(label)
                .font(Theme.caption)
                .foregroundColor(Theme.accent)
            AnimatedDots(color: Theme.accent)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .background(Theme.toolChipBackground)
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}

private func stripTrailingEllipsis(_ input: String) -> String {
    var value = input.trimmingCharacters(in: .whitespacesAndNewlines)
    if value.hasSuffix("...") {
        value = String(value.dropLast(3)).trimmingCharacters(in: .whitespacesAndNewlines)
    } else if value.hasSuffix("…") {
        value = String(value.dropLast(1)).trimmingCharacters(in: .whitespacesAndNewlines)
    }
    return value
}

private extension View {
    @ViewBuilder
    func clipIf(_ condition: Bool, transform: (Self) -> some View) -> some View {
        if condition {
            transform(self)
        } else {
            self
        }
    }
}

private struct AnimatedDots: View {
    let color: Color
    @State private var isAnimating = false

    var body: some View {
        HStack(spacing: 3) {
            ForEach(0..<3, id: \.self) { index in
                Circle()
                    .fill(color)
                    .frame(width: 4, height: 4)
                    .opacity(isAnimating ? 1.0 : 0.35)
                    .scaleEffect(isAnimating ? 1.1 : 0.9)
                    .animation(
                        .easeInOut(duration: 0.6).repeatForever(autoreverses: true).delay(Double(index) * 0.2),
                        value: isAnimating
                    )
            }
        }
        .onAppear {
            isAnimating = true
        }
        .onDisappear {
            isAnimating = false
        }
    }
}
