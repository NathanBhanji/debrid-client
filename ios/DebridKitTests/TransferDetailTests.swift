import Foundation
import Testing
@testable import DebridKit

struct TransferDetailTests {
    @Test func providerReadyStillHasIndependentNASProgress() throws {
        let detail = TransferDetailSummary(try TransferFixtures.torrent(overrides:["size":100_000_000_000,"local_progress":0.99]))
        #expect(detail.providerFraction == 1)
        #expect(detail.item.stage == .nasDownloading)
        #expect(detail.downloadBytes != 100_000_000_000)
        #expect(detail.nasFraction != 0.99) // selected-download bytes, not whole torrent size or a stale aggregate
        #expect(detail.downloadedBytes != nil && detail.downloadBytes != nil)
        #expect(detail.nasLabel == "Downloading to NAS")
    }
    @Test func finalizationAndUnpackingAreNotCompletion() throws {
        let unpacking = TransferDetailSummary(try TransferFixtures.torrent(downloadState:"unpacking"))
        #expect(unpacking.item.stage == .unpacking && unpacking.nasLabel.contains("Unpacking"))
        let finalizing = TransferDetailSummary(try TransferFixtures.torrent(downloadState:"downloaded"))
        #expect(finalizing.item.stage == .nasFinishing && finalizing.nasLabel.contains("Finalizing"))
        let complete = TransferDetailSummary(try TransferFixtures.torrent(status:"completed",downloadState:nil))
        #expect(complete.nasFraction == 1 && complete.providerFraction == 1)
        #expect(complete.nasLabel == "Complete on NAS")
    }
    @Test func noDownloadsOrUnknownSizeHasNoInventedPercentage() throws {
        let pending = TransferDetailSummary(try TransferFixtures.torrent(status:"queued",downloadState:nil))
        #expect(pending.providerFraction == nil && pending.nasFraction == nil)
        #expect(pending.downloadBytes == nil && pending.downloadedBytes == nil)
        let unknownSize = TransferDetailSummary(try TransferFixtures.torrent(status:"downloading",overrides:["size":0,"provider_progress":0]))
        #expect(unknownSize.providerFraction == nil)
        let reported = TransferDetailSummary(try TransferFixtures.torrent(status:"downloading",overrides:["size":0,"provider_progress":0.4]))
        #expect(reported.providerFraction == 0.4)
        let clamped = TransferDetailSummary(try TransferFixtures.torrent(status:"downloading",overrides:["provider_progress":2]))
        #expect(clamped.providerFraction == 1)
    }
    private func downloads(_ values:[(Int64,Int64,String)]) -> [[String:Any]] {
        values.enumerated().map { index,value in
            ["id":"d\(index)","torrent_id":"torrent","path":"file\(index)","filename":"file\(index)","size":value.0,"bytes_done":value.1,"progress":0,"state":value.2,"retry_count":0,"queued_at":"2026-01-02T03:04:05Z"]
        }
    }
    @Test func bytesClampAndCompletedDownloadsUseFullSize() throws {
        let detail = TransferDetailSummary(try TransferFixtures.torrent(overrides:["downloads":downloads([(100,-20,"downloading"),(200,999,"downloading"),(300,0,"done")])]))
        #expect(detail.downloadBytes == 600 && detail.downloadedBytes == 500)
        #expect(detail.nasFraction == 500.0/600)
        let zero = TransferDetailSummary(try TransferFixtures.torrent(overrides:["downloads":downloads([(0,0,"downloading")])]))
        #expect(zero.downloadBytes == nil && zero.nasFraction == nil)
    }
    @Test func mixedUnpackingAndDownloadNeverClaimsDownloadFinished() throws {
        let detail = TransferDetailSummary(try TransferFixtures.torrent(overrides:["downloads":downloads([(100,100,"unpacking"),(900,100,"downloading")])]))
        #expect(detail.item.stage == .unpacking)
        #expect(detail.nasFraction == 0.2)
        #expect(detail.nasLabel == "Unpacking on NAS")
        #expect(!detail.nasLabel.contains("finished"))
    }
    @Test func invalidAndOverflowingByteCountsRemainUnknown() throws {
        for values in [[(Int64.max,Int64.max,"done"),(Int64.max,Int64.max,"done")],[(-1,10,"downloading")]] {
            let detail = TransferDetailSummary(try TransferFixtures.torrent(overrides:["downloads":downloads(values)]))
            #expect(detail.downloadBytes == nil && detail.downloadedBytes == nil && detail.nasFraction == nil)
        }
    }
    @Test func unsafeProviderMessagesNeverBecomePresentationText() throws {
        let secret = "https://user:password@example.test/file?token=secret Bearer private-value"
        let detail = TransferDetailSummary(try TransferFixtures.torrent(status:"error",overrides:["error":secret,"status_reason":secret,"dir_name":String(repeating:"a",count:5000)]))
        #expect(detail.issue != nil && detail.issue?.contains("secret") == false)
        #expect(detail.serverFolder?.unicodeScalars.count == 1024)
        let disabled = TransferDetailSummary(try TransferFixtures.torrent(status:"queued",overrides:["status_reason":"account disabled"]))
        #expect(disabled.issue?.contains("disabled") == true)
        let unknown = TransferDetailSummary(try TransferFixtures.torrent(status:"future_state"))
        #expect(unknown.issue?.contains("does not recognize") == true)
    }
}
