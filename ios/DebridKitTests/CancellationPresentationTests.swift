import Foundation
import Testing
@testable import DebridKit

struct CancellationPresentationTests {
    @Test func cancelledManualHoldIsStoppedRatherThanSelectingOrComplete() throws {
        let transfer=try TransferFixtures.torrent(status:"cancelled",downloadState:"cancelled",overrides:["file_selection_state":"pending"])
        let item=TransferSummary(transfer)
        #expect(item.stage == .cancelled && !item.stage.isActive && !item.stage.needsAttention)
        #expect(item.progress == nil)
        let detail=TransferDetailSummary(transfer)
        #expect(detail.nasLabel == "Cancelled locally")
        #expect(detail.issue?.contains("provider may continue") == true)
        #expect(detail.providerFraction == nil)
        #expect(FileDownloadSummary(transfer.downloads[0]).label == "Cancelled locally")
    }
    @Test func cancelledFilterDoesNotLeakIntoActiveOrCompleted() throws {
        let cancelled=TransferSummary(try TransferFixtures.torrent(status:"cancelled",overrides:["id":"stopped"]))
        let active=TransferSummary(try TransferFixtures.torrent(overrides:["id":"active"]))
        let completed=TransferSummary(try TransferFixtures.torrent(status:"completed",overrides:["id":"done"]))
        let index=TransferSearchIndex(items:[cancelled,active,completed],accounts:[])
        var search=TransferSearch();search.scope = .cancelled
        #expect(index.results(for:search).map(\.id)==["stopped"])
        search.scope = .active;#expect(index.results(for:search).map(\.id)==["active"])
        search.scope = .completed;#expect(index.results(for:search).map(\.id)==["done"])
    }
}
