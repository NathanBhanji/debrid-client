import Foundation
import Testing
@testable import DebridKit

private final class MagnetFixtureBundle: NSObject {}
struct MagnetLinkTests {
    private let hash = "0123456789abcdef0123456789abcdef01234567"
    private var base: String { "magnet:?xt=urn:btih:"+hash }
    private struct CorpusCase: Decodable { let name:String; let uri:String; let nativeAccepted:Bool; let serverAccepted:Bool; let hash:String?; let displayName:String? }
    @Test func realGoParserCorpusDefinesCompatibleSubset() throws {
        let url = try #require(Bundle(for:MagnetFixtureBundle.self).url(forResource:"magnets",withExtension:"json",subdirectory:"ios-api"))
        let decoder = JSONDecoder(); decoder.keyDecodingStrategy = .convertFromSnakeCase
        let cases = try decoder.decode([CorpusCase].self,from:Data(contentsOf:url))
        #expect(cases.count == 24)
        for fixture in cases {
            if fixture.nativeAccepted {
                #expect(fixture.serverAccepted,Comment(rawValue:fixture.name))
                let link = try MagnetLink(fixture.uri)
                #expect(link.infoHash == fixture.hash,Comment(rawValue:fixture.name))
                #expect((link.displayName ?? "") == (fixture.displayName ?? ""),Comment(rawValue:fixture.name))
                #expect(link.rawURI == fixture.uri.trimmingCharacters(in:.whitespacesAndNewlines))
            } else { #expect(throws:MagnetError.self) { _ = try MagnetLink(fixture.uri) } }
        }
    }
    @Test func originalEncodingOrderUnknownFieldsAndTrackersRoundTrip() throws {
        let uri = "magnet:?dn=Caf%C3%A9%2bShow&xt=urn%3Abtih%3A"+hash.uppercased()+"&tr=https%3a%2f%2ftracker.example.test%2Fa%3Fkey%3Dfixture%26other%3Dx&tr=udp://tracker.example.test:80&x.pe=peer.example.test%3A80&unknown=a%2526b%253Dc"
        let link = try MagnetLink(uri)
        #expect(link.rawURI == uri && link.infoHash == hash)
        #expect(link.displayName == "Café+Show" && link.trackerCount == 2)
        #expect(try MagnetLink(link.rawURI) == link)
    }
    @Test func equivalentHashNeverRewritesOrDropsLinkOptions() throws {
        let hex = try MagnetLink("magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&dn=First")
        let base32 = try MagnetLink("magnet:?xt=urn:btih:"+String(repeating:"A",count:32)+"&dn=Second&tr=udp://tracker.example.test")
        #expect(hex.infoHash == base32.infoHash)
        #expect(hex.rawURI != base32.rawURI && hex != base32)
        #expect(throws:MagnetError.invalidHash) { _ = try MagnetLink("magnet:?xt=urn:btih:"+String(repeating:"a",count:32)) }
    }
    @Test func hybridPolicyRejectsV2OnlyDuplicatesAndNoncanonicalHashes() throws {
        let v2 = "urn:btmh:1220"+String(repeating:"a",count:64)
        #expect(try MagnetLink(base+"&xt="+v2).isHybrid)
        #expect(throws:MagnetError.unsupportedV2Only) { _ = try MagnetLink("magnet:?xt="+v2) }
        #expect(throws:MagnetError.duplicateHash) { _ = try MagnetLink(base+"&xt=urn:btih:"+hash) }
        #expect(throws:MagnetError.duplicateHash) { _ = try MagnetLink(base+"&xt="+v2+"&xt="+v2) }
        #expect(throws:MagnetError.invalidV2Hash) { _ = try MagnetLink(base+"&xt=urn:btmh:1120"+String(repeating:"a",count:64)) }
    }
    @Test func invalidEscapesUnicodeControlsAndAmbiguousURIFormsFailSafely() throws {
        for extra in ["%","%a","%gg","%ff","%c0%af","%ed%a0%80","%00","%0d%0a","%C2%85"] {
            #expect(throws:MagnetError.self) { _ = try MagnetLink(base+"&dn="+extra) }
        }
        for uri in [base+"#ignored",base+"&dn=raw space",base+"&x=a;b",base+"\tinside","magnet://?xt=urn:btih:"+hash,"magnet:?xt=URN:BTIH:"+hash] {
            #expect(throws:MagnetError.self) { _ = try MagnetLink(uri) }
        }
        #expect(try MagnetLink(base+"&dn=encoded%20space%3B%23").displayName == "encoded space;#")
    }
    @Test func byteAndParameterLimitsAcceptBoundaryAndRejectOverflow() throws {
        let exact = base+"&x="+String(repeating:"a",count:MagnetLink.byteLimit-base.utf8.count-3)
        #expect(try MagnetLink(exact).rawURI.utf8.count == MagnetLink.byteLimit)
        #expect(throws:MagnetError.tooLarge) { _ = try MagnetLink(exact+"a") }
        #expect(throws:MagnetError.tooLarge) { _ = try MagnetLink(base+String(repeating:"é",count:MagnetLink.byteLimit/2)) }
        let fields = base+String(repeating:"&x=a",count:MagnetLink.parameterLimit-1)
        #expect(try MagnetLink(fields).rawURI == fields)
        #expect(throws:MagnetError.tooManyParameters) { _ = try MagnetLink(fields+"&x=a") }
    }
    @Test func displayMetadataIsBoundedWithoutChangingRawURI() throws {
        let uri = base+"&dn=\u{202e}"+String(repeating:"é",count:2_000)+"&dn=Ignored"
        let link = try MagnetLink(uri)
        #expect(link.rawURI == uri && link.displayName?.unicodeScalars.count == 256)
        #expect(link.displayName?.contains("\u{202e}") == false)
        #expect(try MagnetLink(base+"&dn=&dn=Second").displayName == nil)
    }
    @Test func descriptionsReflectionAndErrorsNeverExposePrivateTrackers() throws {
        let secret = "fixture-private-passkey"
        let link = try MagnetLink(base+"&dn="+secret+"&tr=https://tracker.example.test/"+secret)
        var reflected = ""; dump(link,to:&reflected)
        for output in [String(describing:link),String(reflecting:link),reflected] { #expect(!output.contains(secret) && !output.contains(hash)) }
        do { _ = try MagnetLink(base+"&tr="+secret+"%zz"); Issue.record("Invalid link accepted") }
        catch { #expect(!error.localizedDescription.contains(secret)) }
    }
    @Test func previewEscapesInvisibleFormattingWithoutChangingStoredURI() throws {
        let raw = base+"&dn=Safe\u{202e}Spoof\u{200b}"
        let link = try MagnetLink(raw)
        #expect(link.rawURI == raw)
        #expect(!link.preview().unicodeScalars.contains { $0.properties.generalCategory == .format })
        #expect(link.preview().contains("\\u{202E}") && link.preview().contains("\\u{200B}"))
        #expect(link.preview(maxScalars:-1).isEmpty)
    }

}
