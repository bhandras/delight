import Foundation

/// JSONCoding centralizes JSON encoding/decoding configuration for the harness.
///
/// The harness intentionally keeps JSON parsing out of view code. These helpers make it
/// straightforward to define small `Codable` DTOs for SDK payloads while keeping the
/// decoding behavior consistent across the app.
enum JSONCoding {
    /// encoder is the shared JSON encoder used for payloads we send to the Go SDK.
    static let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return encoder
    }()

    /// decoder is the shared JSON decoder used for payloads we receive from the Go SDK.
    static let decoder: JSONDecoder = {
        JSONDecoder()
    }()

    /// encode encodes an Encodable payload into a UTF-8 JSON string.
    static func encode<T: Encodable>(_ value: T) throws -> String {
        let data = try encoder.encode(value)
        guard let json = String(data: data, encoding: .utf8) else {
            throw JSONCodingError.invalidUTF8
        }
        return json
    }

    /// decode decodes a UTF-8 JSON string into the requested Decodable type.
    static func decode<T: Decodable>(_ type: T.Type, from json: String) throws -> T {
        guard let data = json.data(using: .utf8) else {
            throw JSONCodingError.invalidUTF8
        }
        return try decoder.decode(type, from: data)
    }

    /// prettyPrint returns a pretty-printed JSON string for display, or nil if parsing fails.
    static func prettyPrint(json: String) -> String? {
        // Prefer our Codable-based parsing so callers don't need to handle
        // Foundation's dynamically-typed JSONSerialization trees.
        guard let value = try? decode(JSONValue.self, from: json) else { return nil }

        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
        guard let prettyData = try? encoder.encode(value) else { return nil }
        guard let pretty = String(data: prettyData, encoding: .utf8) else { return nil }
        // Some tool payloads contain shell-escaped paths like "\/Users\/...".
        // They're valid, but visually noisy. Clean up common path-only escapes for display.
        return pretty.replacingOccurrences(of: "\\/", with: "/")
    }
}

/// JSONCodingError represents failures converting between strings and JSON payloads.
enum JSONCodingError: Error {
    case invalidUTF8
}

/// JSONValue is a lightweight, Codable representation of arbitrary JSON.
///
/// This is used for payloads that are genuinely dynamic (e.g. tool inputs) where a fully
/// typed model is not practical. Most SDK payloads should prefer strongly-typed DTOs.
enum JSONValue: Codable, Equatable {
    case null
    case bool(Bool)
    case number(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Double.self) {
            self = .number(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([JSONValue].self) {
            self = .array(value)
        } else if let value = try? container.decode([String: JSONValue].self) {
            self = .object(value)
        } else {
            throw DecodingError.typeMismatch(
                JSONValue.self,
                .init(codingPath: decoder.codingPath, debugDescription: "Unsupported JSON value")
            )
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .null:
            try container.encodeNil()
        case .bool(let value):
            try container.encode(value)
        case .number(let value):
            try container.encode(value)
        case .string(let value):
            try container.encode(value)
        case .array(let value):
            try container.encode(value)
        case .object(let value):
            try container.encode(value)
        }
    }

    /// string returns the underlying string, if this value is a string.
    var string: String? {
        if case .string(let value) = self { return value }
        return nil
    }

    /// bool returns the underlying bool, if this value is a bool.
    var bool: Bool? {
        if case .bool(let value) = self { return value }
        return nil
    }

    /// object returns the underlying object, if this value is an object.
    var object: [String: JSONValue]? {
        if case .object(let value) = self { return value }
        return nil
    }

    /// array returns the underlying array, if this value is an array.
    var array: [JSONValue]? {
        if case .array(let value) = self { return value }
        return nil
    }

    /// toAny converts JSONValue into a Foundation-compatible JSON tree.
    ///
    /// This is only used for compatibility with legacy parsing code that still expects
    /// `[String: Any]` trees (e.g. `SessionUIState.fromJSONDict`).
    func toAny() -> Any {
        switch self {
        case .null:
            return NSNull()
        case .bool(let value):
            return value
        case .number(let value):
            return value
        case .string(let value):
            return value
        case .array(let value):
            return value.map { $0.toAny() }
        case .object(let value):
            var dict: [String: Any] = [:]
            dict.reserveCapacity(value.count)
            for (key, entry) in value {
                dict[key] = entry.toAny()
            }
            return dict
        }
    }
}
