import XCTest
@testable import DelightHarness

final class JSONCodingTests: XCTestCase {
    func testJSONValueRoundTrip() throws {
        let value: JSONValue = .object([
            "a": .int(1),
            "b": .string("two"),
            "c": .array([.bool(true), .null])
        ])

        let json = try JSONCoding.encode(value)
        let decoded = try JSONCoding.decode(JSONValue.self, from: json)

        XCTAssertEqual(decoded, value)
    }

    func testJSONCodingPrettyPrint() {
        let input = #"{"b":2,"a":1}"#
        let pretty = JSONCoding.prettyPrint(json: input)
        XCTAssertNotNil(pretty)
        XCTAssertTrue(pretty?.contains("\n") == true)
    }
}
