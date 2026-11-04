import Foundation
import Testing
@testable import DebridKit

struct ServerAddressTests {
    @Test func keepsReverseProxyPrefix() throws {
        let server = try ServerAddress(" https://example.test/debrid/ \n")
        #expect(try server.endpoint("torrents", "abc").absoluteString == "https://example.test/debrid/api/v1/torrents/abc")
    }

    @Test(arguments: ["http://example.test", "https://user:secret@example.test", "https://example.test?api_key=secret", "https://example.test#fragment", "not a host"])
    func rejectsUnsafeOrAmbiguousAddress(_ address: String) {
        #expect(throws: ServerAddress.ValidationError.self) { try ServerAddress(address) }
    }

    @Test(arguments: ["", ".", "..", "a/b", "../auth/me", "%2e%2e", "a%2fb", "a\\b", "id?key=value", "id#fragment"])
    func rejectsPathSyntax(_ component: String) throws {
        let server = try ServerAddress("https://example.test/debrid")
        #expect(throws: ServerAddress.ValidationError.invalidPathComponent) {
            try server.endpoint("torrents", component)
        }
    }

    @Test func encodesResourceNameAsOneComponent() throws {
        let server = try ServerAddress("https://example.test/debrid")
        #expect(try server.endpoint("accounts", "Home account").absoluteString.hasSuffix("/accounts/Home%20account"))
    }

    @Test func supportsTailnetDNSAndIPv6() throws {
        #expect(try ServerAddress("https://debrid.bullhead-cloud.ts.net").endpoint("health").host == "debrid.bullhead-cloud.ts.net")
        #expect(try ServerAddress("https://[::1]:8080").endpoint("health").port == 8080)
    }
}
