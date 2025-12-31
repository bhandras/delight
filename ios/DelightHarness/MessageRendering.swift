import SwiftUI

/// MessageBubble renders a single chat message.
///
/// The view is used by `TerminalMessagesView` inside a `UITableView` cell.
struct MessageBubble: View {
    let message: MessageItem

    var body: some View {
        HStack(alignment: .top, spacing: 0) {
            if message.role == .user { Spacer(minLength: 48) }
            VStack(alignment: .leading, spacing: 8) {
                ForEach(Array(message.blocks.enumerated()), id: \.offset) { _, block in
                    MessageBlockView(block: block)
                }
            }
            .padding(message.role == .user ? 12 : 0)
            .padding(.leading, message.role == .user ? 0 : 4)
            .background(message.role == .user ? Theme.userBubble : Color.clear)
            .clipIf(message.role == .user) {
                $0.clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
            }
            if message.role != .user { Spacer(minLength: 48) }
        }
        .frame(maxWidth: .infinity, alignment: message.role == .user ? .trailing : .leading)
        .padding(.horizontal, 4)
    }
}

private struct MessageBlockView: View {
    let block: MessageBlock

    var body: some View {
        switch block {
        case .text(let text):
            MarkdownText(text: text)
                .font(Theme.body)
                .foregroundColor(Theme.messageText)
        case .code(let language, let content):
            CodeBlockView(language: language, content: content)
        case .toolCall(let summary):
            ToolChipView(summary: summary)
        }
    }
}

private struct MarkdownText: View {
    let text: String

    var body: some View {
        let lines = text.split(whereSeparator: \.isNewline).map(String.init)
        if looksLikeList(text) {
            VStack(alignment: .leading, spacing: 6) {
                ForEach(lines.indices, id: \.self) { index in
                    listLineView(lines[index])
                }
            }
        } else if text.count <= 4000, let attributed = try? AttributedString(markdown: text) {
            Text(attributed)
        } else {
            Text(text)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private func looksLikeList(_ text: String) -> Bool {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.hasPrefix("- ") || trimmed.hasPrefix("* ") {
            return true
        }
        if text.contains("\n- ") || text.contains("\n* ") {
            return true
        }
        if hasNumberedListPrefix(trimmed) {
            return true
        }
        if text.split(whereSeparator: \.isNewline).contains(where: { hasNumberedListPrefix(String($0)) }) {
            return true
        }
        return false
    }

    @ViewBuilder
    private func listLineView(_ line: String) -> some View {
        if line.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            Text(" ")
        } else {
            let parsed = parseListLine(line)
            if let marker = parsed.marker {
                HStack(alignment: .top, spacing: 8) {
                    Text(marker)
                        .font(Theme.body)
                        .foregroundColor(Theme.messageText)
                    inlineMarkdown(parsed.content)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(.leading, parsed.indent)
            } else {
                inlineMarkdown(parsed.content)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private func inlineMarkdown(_ content: String) -> Text {
        if content.count <= 4000, let attributed = try? AttributedString(markdown: content) {
            return Text(attributed)
        }
        return Text(content)
    }

    private func parseListLine(_ line: String) -> (indent: CGFloat, marker: String?, content: String) {
        let indentCount = line.prefix { $0 == " " || $0 == "\t" }.count
        let indent = CGFloat(indentCount) * 4
        let trimmed = line.trimmingCharacters(in: .whitespaces)

        if trimmed.hasPrefix("- ") || trimmed.hasPrefix("* ") {
            let start = trimmed.index(trimmed.startIndex, offsetBy: 2)
            return (indent, "•", String(trimmed[start...]))
        }

        if let numbered = parseNumberedListLine(trimmed) {
            return (indent, numbered.marker, numbered.content)
        }

        return (indent, nil, trimmed)
    }

    private func hasNumberedListPrefix(_ text: String) -> Bool {
        return parseNumberedListLine(text) != nil
    }

    private func parseNumberedListLine(_ text: String) -> (marker: String, content: String)? {
        var index = text.startIndex
        var hasDigits = false
        while index < text.endIndex, text[index].isNumber {
            hasDigits = true
            index = text.index(after: index)
        }
        guard hasDigits, index < text.endIndex, text[index] == "." else {
            return nil
        }
        let afterDot = text.index(after: index)
        guard afterDot < text.endIndex, text[afterDot] == " " else {
            return nil
        }
        let marker = String(text[text.startIndex...index])
        let contentStart = text.index(after: afterDot)
        let content = String(text[contentStart...])
        return (marker, content)
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

