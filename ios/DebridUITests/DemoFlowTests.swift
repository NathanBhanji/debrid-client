import XCTest

@MainActor
final class DemoFlowTests: XCTestCase {
    func testNativeNavigationAndReviewSheet() throws {
        let app = XCUIApplication()
        app.launchArguments += ["-Demo"]
        app.launch()
        XCTAssertTrue(app.staticTexts["TRANSFER STATUS · DEMO"].waitForExistence(timeout: 10))
        app.buttons["add-transfer"].tap()
        XCTAssertTrue(app.navigationBars["Add transfer"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["This preview cannot add transfers. The live flow will ask for confirmation before sending anything."].exists)
        app.buttons["Done"].tap()
        let transfer = app.buttons["transfer-ocean"]
        XCTAssertTrue(transfer.waitForExistence(timeout: 5))
        transfer.tap()
        XCTAssertTrue(app.navigationBars["The Open Ocean"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Storage"].exists)
        app.tabBars.buttons["Providers"].tap()
        XCTAssertTrue(app.navigationBars["Providers"].waitForExistence(timeout: 5))
        app.tabBars.buttons["Settings"].tap()
        XCTAssertTrue(app.staticTexts["Pocket ID sign-in arrives in a future milestone."].waitForExistence(timeout: 5))
    }

    func testAccessibilityTextCanReachTransferAndDismissSheet() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        app.launchArguments += ["-Demo"]
        app.launch()
        XCTAssertTrue(app.staticTexts["TRANSFER STATUS · DEMO"].waitForExistence(timeout: 10))
        for _ in 0..<3 where !app.buttons["transfer-ocean"].isHittable { app.swipeUp() }
        XCTAssertTrue(app.buttons["transfer-ocean"].isHittable)
        app.buttons["add-transfer"].tap()
        XCTAssertTrue(app.navigationBars["Add transfer"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["Done"].isHittable)
        app.buttons["Done"].tap()
        XCTAssertTrue(app.navigationBars["Transfers"].waitForExistence(timeout: 5))
    }
    func testNativeGalleryKeepsActionsExplicitlyLocal() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-DesignScreen", "sign-in"]
        app.launchArguments += ["-Demo"]
        app.launch()
        let signIn = app.buttons["Continue with Pocket ID"]
        XCTAssertTrue(signIn.waitForExistence(timeout: 10))
        signIn.tap()
        XCTAssertTrue(app.alerts["Preview only"].waitForExistence(timeout: 5))
        app.alerts.buttons["OK"].tap()
        app.terminate()
        app.launchArguments = ["-DesignScreen", "selection"]
        app.launchArguments += ["-Demo"]
        app.launch()
        XCTAssertTrue(app.navigationBars["Choose files"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.switches["The Open Ocean.mkv · 9.8 GB"].exists)
        app.switches["The Open Ocean.mkv · 9.8 GB"].coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5)).tap()
        XCTAssertEqual(app.switches["The Open Ocean.mkv · 9.8 GB"].value as? String, "0")
        let disabled = NSPredicate(format: "enabled == false")
        expectation(for: disabled, evaluatedWith: app.buttons["Download selected files"])
        waitForExpectations(timeout: 5)
        app.terminate()
        app.launchArguments = ["-DesignScreen", "removal"]
        app.launchArguments += ["-Demo"]
        app.launch()
        XCTAssertTrue(app.navigationBars["Remove transfer"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.switches["Downloaded files on your NAS"].value as? String, "0")
        XCTAssertEqual(app.switches["At the debrid provider"].value as? String, "0")
    }

}

@MainActor
final class ConnectionFlowTests: XCTestCase {
    func testNativePocketIDActionWithFixtureHasHonestPreviewAndNoBrowser() {
        let app = XCUIApplication()
        app.launchArguments = ["-ConnectionPreview"]
        app.launch()
        let signIn = app.buttons["pocket-id-sign-in"]
        XCTAssertTrue(signIn.waitForExistence(timeout: 10))
        if !signIn.isHittable { app.swipeUp() }
        signIn.tap()
        XCTAssertTrue(app.alerts["Preview only"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["This sample server cannot sign in or save credentials."].exists)
    }

    func testInvalidAddressAndNativeConnectionForm() {
        let app = XCUIApplication()
        app.launch()
        XCTAssertTrue(app.navigationBars["Connect"].waitForExistence(timeout: 10))
        let field = app.textFields["server-address"]
        XCTAssertTrue(field.exists)
        field.tap(); field.typeText("http://invalid.example.test")
        app.buttons["check-connection"].tap()
        XCTAssertTrue(app.otherElements["connection-error"].waitForExistence(timeout: 5) || app.staticTexts["connection-error"].exists)
        XCTAssertTrue(app.staticTexts["Enter a full HTTPS server address without credentials, a query or a fragment."].exists)
    }
}

@MainActor
final class LiveTransferFlowTests: XCTestCase {
    func testLiveListFixtureShowsDistinctStagesAndHonestServerSheet() {
        let app = XCUIApplication(); app.launchArguments = ["-TransfersPreview"]; app.launch()
        XCTAssertTrue(app.staticTexts["Downloading at provider"].waitForExistence(timeout:10))
        XCTAssertTrue(app.staticTexts["Downloading to NAS"].exists)
        app.buttons["Server"].tap()
        XCTAssertTrue(app.staticTexts["Preview only. No server session exists."].waitForExistence(timeout:5))
        app.buttons["Done"].tap()
        XCTAssertTrue(app.navigationBars["Transfers"].waitForExistence(timeout:5))
    }
    func testLiveListEmptyAndOfflineDoNotLookLikeLoadedTransfers() {
        let app = XCUIApplication(); app.launchArguments = ["-TransfersPreview","-TransfersEmpty"]; app.launch()
        XCTAssertTrue(app.staticTexts["No transfers yet"].waitForExistence(timeout:10))
        app.terminate(); app.launchArguments = ["-TransfersPreview","-TransfersOffline"]; app.launch()
        XCTAssertTrue(app.staticTexts["You’re offline. Reconnect and try again."].waitForExistence(timeout:10))
        XCTAssertFalse(app.staticTexts["No transfers yet"].exists)
    }
}

@MainActor
final class TransferSearchFlowTests: XCTestCase {
    func testNativeSearchFindsTransferAndShowsNoMatchState() {
        let app = XCUIApplication(); app.launchArguments = ["-TransfersPreview"]; app.launch()
        XCTAssertTrue(app.staticTexts["The Open Ocean"].waitForExistence(timeout:10))
        let search = app.searchFields.firstMatch
        if !search.isHittable { app.swipeDown() }
        XCTAssertTrue(search.waitForExistence(timeout:5))
        search.tap(); search.typeText("missing documentary")
        XCTAssertTrue(app.staticTexts["No matching transfers"].waitForExistence(timeout:5))
        XCTAssertFalse(app.staticTexts["No transfers yet"].exists)
    }
    func testStatusFilterCanBeSelectedAndClearedWithNativePicker() {
        let app = XCUIApplication(); app.launchArguments = ["-TransfersPreview"]; app.launch()
        XCTAssertTrue(app.staticTexts["The Open Ocean"].waitForExistence(timeout:10))
        app.buttons["Filters"].tap()
        let picker = app.descendants(matching:.any).matching(identifier:"filter-status").firstMatch
        XCTAssertTrue(picker.waitForExistence(timeout:5)); picker.tap()
        let attention = app.descendants(matching:.any).matching(NSPredicate(format:"label == %@", "Needs attention")).firstMatch
        XCTAssertTrue(attention.waitForExistence(timeout:5),app.debugDescription)
        attention.tap()
        XCTAssertTrue(app.navigationBars["Filters"].waitForExistence(timeout:5))
        app.buttons["Done"].tap()
        XCTAssertTrue(app.staticTexts["Field Recordings · Vol. 02"].waitForExistence(timeout:5))
        XCTAssertFalse(app.staticTexts["The Open Ocean"].exists)
        app.buttons["Filters"].tap(); app.buttons["Clear search and filters"].tap(); app.buttons["Done"].tap()
        XCTAssertTrue(app.staticTexts["The Open Ocean"].waitForExistence(timeout:5))
    }
}

@MainActor
final class TransferDetailFlowTests: XCTestCase {
    func testLiveDetailShowsTwoStagesAndBackPreservesSearch() {
        let app = XCUIApplication()
        app.launchArguments = ["-TransfersPreview"]
        app.launch()
        let search = app.searchFields["Search transfers"]
        XCTAssertTrue(search.waitForExistence(timeout:10))
        search.tap(); search.typeText("Earth")
        let transfer = app.buttons["live-transfer-sample-nas"]
        XCTAssertTrue(transfer.waitForExistence(timeout:5))
        transfer.tap()
        XCTAssertTrue(app.navigationBars["Transfer"].waitForExistence(timeout:5))
        XCTAssertTrue(app.staticTexts["Available at provider"].exists)
        XCTAssertTrue(app.staticTexts["Downloading to NAS"].exists)
        XCTAssertTrue(app.staticTexts["Preview · Sample transfer"].exists)
        app.navigationBars.buttons.element(boundBy:0).tap()
        XCTAssertTrue(app.buttons["live-transfer-sample-nas"].waitForExistence(timeout:5))
        XCTAssertFalse(app.navigationBars["Transfer"].exists)
        XCTAssertEqual(app.searchFields["Search transfers"].value as? String,"Earth")
        XCTAssertFalse(app.buttons["live-transfer-sample-provider"].exists)
    }
    func testMissingTransferHasExplicitBackAction() {
        let app = XCUIApplication()
        app.launchArguments = ["-TransfersPreview","-TransfersEmpty","-DetailPreview"]
        app.launch()
        XCTAssertTrue(app.staticTexts["Transfer no longer listed"].waitForExistence(timeout:10))
        app.buttons["Back to transfers"].tap()
        XCTAssertTrue(app.staticTexts["No transfers yet"].waitForExistence(timeout:5))
    }
    func testDetailAtAccessibilitySizeCanReachServerStorage() {
        let app = XCUIApplication()
        app.launchArguments = ["-TransfersPreview","-DetailPreview","-UIPreferredContentSizeCategoryName","UICTContentSizeCategoryAccessibilityXXXL"]
        app.launch()
        XCTAssertTrue(app.navigationBars["Transfer"].waitForExistence(timeout:10))
        for _ in 0..<8 where !app.staticTexts["Folder reported by server"].isHittable { app.swipeUp() }
        XCTAssertTrue(app.staticTexts["Folder reported by server"].isHittable)
    }
}

@MainActor
final class TransferFileFlowTests: XCTestCase {
    func testNativeFolderAndFileDetailShowIndependentNASProgress() {
        let app = XCUIApplication(); app.launchArguments = ["-TransfersPreview","-FilesPreview"]; app.launch()
        XCTAssertTrue(app.buttons["Video"].waitForExistence(timeout:10)); app.buttons["Video"].tap()
        XCTAssertTrue(app.buttons["file-row-video"].waitForExistence(timeout:5)); app.buttons["file-row-video"].tap()
        XCTAssertTrue(app.navigationBars["File"].waitForExistence(timeout:5))
        // Native LabeledContent can expose its value through the row’s accessibility element.
        let selected = app.descendants(matching:.any).matching(NSPredicate(format:"label CONTAINS %@ OR value == %@", "Selected", "Selected")).firstMatch
        XCTAssertTrue(selected.exists)
        XCTAssertTrue(app.staticTexts["Video/Earth.mkv"].exists)
        XCTAssertTrue(app.staticTexts["Downloading to NAS"].exists)
        XCTAssertTrue(app.staticTexts["64%"].exists)
    }
    func testSearchFindsUnselectedFileAcrossFoldersAndNoMatch() {
        let app = XCUIApplication(); app.launchArguments = ["-TransfersPreview","-FilesPreview"]; app.launch()
        let search = app.searchFields["Search file paths"]
        XCTAssertTrue(search.waitForExistence(timeout:10)); search.tap(); search.typeText("Readme")
        XCTAssertTrue(app.buttons["file-row-notes"].waitForExistence(timeout:5))
        XCTAssertTrue(app.staticTexts["Not selected"].exists)
        search.typeText("no-match")
        XCTAssertTrue(app.staticTexts["No matching files"].waitForExistence(timeout:5))
    }
}

@MainActor
final class MagnetHandoffFlowTests: XCTestCase {
    func testNativeURLHandoffReviewsAndKeepsWithoutAddingTransfer() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-DebridInboxTestNamespace",UUID().uuidString]
        let raw = "magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&dn=Fixture+Ocean&tr=udp%3a%2f%2ftracker.example.test"
        app.open(try XCTUnwrap(URL(string:raw)))
        XCTAssertTrue(app.buttons["review-received-link"].waitForExistence(timeout:10))
        XCTAssertTrue(app.staticTexts["Preview · Sample server"].waitForExistence(timeout:5))
        app.buttons["review-received-link"].tap()
        XCTAssertTrue(app.navigationBars["Saved item"].waitForExistence(timeout:5))
        XCTAssertTrue(app.staticTexts["Fixture Ocean"].waitForExistence(timeout:5))
        XCTAssertTrue(app.staticTexts["No transfer has been added."].waitForExistence(timeout:5))
        app.buttons["Keep for later"].tap()
        XCTAssertTrue(app.navigationBars["Pending items"].waitForExistence(timeout:5))
        XCTAssertTrue(app.staticTexts["Fixture Ocean"].exists)
        app.buttons["Done"].tap()
        XCTAssertTrue(app.navigationBars["Connect"].waitForExistence(timeout:5))
        app.buttons["pending-links"].tap()
        XCTAssertTrue(app.staticTexts["Fixture Ocean"].waitForExistence(timeout:5))
        let row = app.buttons.containing(.staticText,identifier:"Fixture Ocean").firstMatch
        row.swipeLeft()
        app.buttons["Discard saved item"].tap()
        app.sheets.buttons["Discard saved item"].tap()
        XCTAssertTrue(app.staticTexts["No pending links"].waitForExistence(timeout:5))
    }
    func testReceivedBannerAndReviewAtLargestAccessibilitySize() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-DebridInboxTestNamespace",UUID().uuidString,"-UIPreferredContentSizeCategoryName","UICTContentSizeCategoryAccessibilityXXXL"]
        let raw = "magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&dn=Accessibility+fixture"
        app.open(try XCTUnwrap(URL(string:raw)))
        let review = app.buttons["review-received-link"]
        XCTAssertTrue(review.waitForExistence(timeout:10)); XCTAssertTrue(review.isHittable); review.tap()
        XCTAssertTrue(app.navigationBars["Saved item"].waitForExistence(timeout:5))
        for _ in 0..<6 where !app.buttons["Keep for later"].isHittable { app.swipeUp() }
        XCTAssertTrue(app.buttons["Keep for later"].isHittable)
    }

}

/// Only the delivery harness selects this bounded helper. It confirms the real iOS app-open prompt,
/// not permissions or arbitrary alerts, while the host sends fixture URLs through simctl.
@MainActor
final class SystemOpenConfirmationTests: XCTestCase {
    func testConfirmOnlyDebridAppOpenPrompts() async throws {
        guard ProcessInfo.processInfo.environment["DEBRID_SYSTEM_OPEN_HELPER"] == "1" else { throw XCTSkip("Selected only by the OS delivery harness") }
        executionTimeAllowance = 140
        let springboard = XCUIApplication(bundleIdentifier:"com.apple.springboard")
        FileHandle.standardOutput.write(Data("DEBRID_OPEN_HELPER_READY\n".utf8))
        let deadline = ContinuousClock.now.advanced(by:.seconds(100))
        var confirmations = 0
        while ContinuousClock.now < deadline {
            let alert = springboard.alerts.firstMatch
            if alert.waitForExistence(timeout:1) {
                let title = alert.label + " " + alert.staticTexts.allElementsBoundByIndex.map(\.label).joined(separator:" ")
                if title.contains("Open in"), title.contains("Debrid"), !title.contains("DebridUITests"), alert.buttons["Open"].exists {
                    alert.buttons["Open"].tap(); confirmations += 1
                } else { try await Task.sleep(for:.milliseconds(300)) }
            }
        }
        print("Confirmed Debrid app-open prompts: \(confirmations)")
    }
}

@MainActor
final class DocumentIntakeFlowTests: XCTestCase {
    func testNativePasteValidationSaveAndDocumentPickerCancellation() async throws {
        let app = XCUIApplication(); app.launchArguments = ["-DebridInboxTestNamespace",UUID().uuidString]; app.launch()
        XCTAssertTrue(app.buttons["pending-links"].waitForExistence(timeout:10)); app.buttons["pending-links"].tap()
        app.buttons["paste-magnet-links"].tap()
        XCTAssertTrue(app.navigationBars["Paste links"].waitForExistence(timeout:5))
        try await Task.sleep(for:.seconds(10))
        let shot = XCTAttachment(screenshot:app.screenshot()); shot.name = "paste-native"; shot.lifetime = .keepAlways; add(shot)
        let editor = app.textViews["magnet-paste-editor"]; editor.tap(); editor.typeText("invalid")
        app.buttons["save-pasted-links"].tap()
        XCTAssertTrue(app.staticTexts["Use a complete magnet link beginning with magnet:?."].waitForExistence(timeout:5))
        XCTAssertTrue(editor.value as? String == "invalid")
        editor.tap(); editor.typeText(String(repeating:XCUIKeyboardKey.delete.rawValue,count:7))
        editor.typeText("magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&dn=Pasted+fixture")
        app.buttons["save-pasted-links"].tap()
        XCTAssertTrue(app.navigationBars["Pending items"].waitForExistence(timeout:5))
        XCTAssertTrue(app.staticTexts["Pasted fixture"].waitForExistence(timeout:5))
        app.buttons["import-torrent-file"].tap()
        let cancel = app.buttons["Cancel"].firstMatch
        XCTAssertTrue(cancel.waitForExistence(timeout:8)); cancel.tap()
        XCTAssertTrue(app.navigationBars["Pending items"].waitForExistence(timeout:5))
        XCTAssertTrue(app.staticTexts["Pasted fixture"].exists)
    }
    func testPasteControlsRemainReachableAtLargestAccessibilitySize() throws {
        let app = XCUIApplication(); app.launchArguments = ["-DebridInboxTestNamespace",UUID().uuidString,"-UIPreferredContentSizeCategoryName","UICTContentSizeCategoryAccessibilityXXXL"]; app.launch()
        XCTAssertTrue(app.buttons["pending-links"].waitForExistence(timeout:10)); app.buttons["pending-links"].tap()
        let paste = app.buttons["paste-magnet-links"]
        XCTAssertTrue(paste.waitForExistence(timeout:5)); paste.tap()
        let editor = app.textViews["magnet-paste-editor"]
        XCTAssertTrue(editor.waitForExistence(timeout:5)); editor.tap(); editor.typeText("invalid")
        XCTAssertTrue(app.buttons["save-pasted-links"].isHittable); app.buttons["save-pasted-links"].tap()
        XCTAssertEqual(editor.value as? String,"invalid")
        XCTAssertTrue(app.buttons["Cancel"].isHittable); app.buttons["Cancel"].tap()
        XCTAssertTrue(app.navigationBars["Pending items"].waitForExistence(timeout:5))
    }
    func testCopiedTorrentUsesActualNativeReviewWithoutAddingTransfer() async throws {
        let app = XCUIApplication(); app.launchArguments = ["-DebridInboxTestNamespace",UUID().uuidString,"-DebridInboxCaptureMode","torrent"]; app.launch()
        XCTAssertTrue(app.navigationBars["Saved item"].waitForExistence(timeout:10))
        XCTAssertTrue(app.staticTexts["Fixture Ocean.mkv"].waitForExistence(timeout:5))
        XCTAssertTrue(app.staticTexts["No transfer has been added."].waitForExistence(timeout:5))
        XCTAssertTrue(app.staticTexts["An exact copy is saved on this iPhone. The original file is unchanged."].exists)
        try await Task.sleep(for:.seconds(10))
        let shot = XCTAttachment(screenshot:app.screenshot()); shot.name = "torrent-native"; shot.lifetime = .keepAlways; add(shot)
        app.buttons["Keep for later"].tap()
        XCTAssertTrue(app.navigationBars["Pending items"].waitForExistence(timeout:5))
        XCTAssertTrue(app.staticTexts["Fixture Ocean.mkv"].exists)
    }
}

@MainActor
final class NativeSubmissionFlowTests:XCTestCase {
    private func launch(_ mode:String,large:Bool = false) -> XCUIApplication {
        let app=XCUIApplication();app.launchArguments=["-DebridInboxTestNamespace",UUID().uuidString,"-DebridInboxCaptureMode",mode]
        if large { app.launchArguments += ["-UIPreferredContentSizeCategoryName","UICTContentSizeCategoryAccessibilityXXXL"] }
        app.launch();return app
    }
    private func reach(_ element:XCUIElement,in app:XCUIApplication,swipes:Int = 16) {
        for _ in 0..<swipes where !element.isHittable { app.swipeUp() }
        XCTAssertTrue(element.isHittable)
    }
    func testNativeReviewRequiresExplicitAddAndShowsConfirmation() async throws {
        let app=launch("submit")
        XCTAssertTrue(app.navigationBars["Saved item"].waitForExistence(timeout:12))
        XCTAssertTrue(app.staticTexts["No transfer has been added."].waitForExistence(timeout:5))
        let provider=app.buttons["add-provider"]
        reach(provider,in:app)
        XCTAssertTrue(app.staticTexts["Preview · Fictional submission"].exists)
        try await Task.sleep(for:.seconds(10))
        let review=XCTAttachment(screenshot:app.screenshot());review.name="add-review-native";review.lifetime = .keepAlways;add(review)
        let addTransfer=app.buttons["add-reviewed-transfer"];reach(addTransfer,in:app)
        XCTAssertTrue(app.staticTexts["Automatic lifecycle rules"].exists)
        XCTAssertTrue(app.staticTexts["Adds this transfer using the reviewed settings. Files download to your server."].exists)
        XCTAssertFalse(app.otherElements["submission-accepted"].exists)
        try await Task.sleep(for:.seconds(10))
        let primary=XCTAttachment(screenshot:app.screenshot());primary.name="add-primary-native";primary.lifetime = .keepAlways;add(primary)
        addTransfer.tap()
        let accepted=app.descendants(matching:.any).matching(identifier:"submission-accepted").firstMatch
        XCTAssertTrue(accepted.waitForExistence(timeout:8))
        reach(app.buttons["view-added-transfer"],in:app)
        try await Task.sleep(for:.seconds(10))
        let confirmation=XCTAttachment(screenshot:app.screenshot());confirmation.name="add-confirmed-native";confirmation.lifetime = .keepAlways;add(confirmation)
        app.buttons["view-added-transfer"].tap()
        XCTAssertTrue(app.navigationBars["Transfer"].waitForExistence(timeout:8))
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format:"label CONTAINS %@","The Open Ocean")).firstMatch.exists)
    }
    func testUncertainResponseOffersReceiptCheckWithoutNewAdd() throws {
        let app=launch("uncertain")
        XCTAssertTrue(app.navigationBars["Saved item"].waitForExistence(timeout:12))
        let addTransfer=app.buttons["add-reviewed-transfer"];reach(addTransfer,in:app);addTransfer.tap()
        let check=app.buttons["check-submission-result"]
        XCTAssertTrue(check.waitForExistence(timeout:8));reach(check,in:app)
        XCTAssertTrue(app.buttons["retry-saved-submission"].exists)
        XCTAssertFalse(app.buttons["add-reviewed-transfer"].exists)
        check.tap()
        XCTAssertTrue(app.descendants(matching:.any).matching(identifier:"submission-accepted").firstMatch.waitForExistence(timeout:8))
    }
    func testLargestTextCanReviewAndAddWithoutClippedAction() throws {
        let app=launch("submit",large:true)
        XCTAssertTrue(app.navigationBars["Saved item"].waitForExistence(timeout:12))
        let addTransfer=app.buttons["add-reviewed-transfer"];reach(addTransfer,in:app,swipes:25)
        XCTAssertTrue(addTransfer.isEnabled);addTransfer.tap()
        XCTAssertTrue(app.descendants(matching:.any).matching(identifier:"submission-accepted").firstMatch.waitForExistence(timeout:8))
        let finish=app.buttons["Done"];reach(finish,in:app);finish.tap()
        XCTAssertTrue(app.navigationBars["Transfers"].waitForExistence(timeout:8))
    }
}

@MainActor final class NativeFileSelectionFlowTests:XCTestCase {
    private func reach(_ item:XCUIElement,in app:XCUIApplication) {
        for _ in 0..<18 where !item.isHittable {app.swipeUp()}
        XCTAssertTrue(item.isHittable)
    }
    private func capture(_ app:XCUIApplication,_ name:String) {
        let shot=XCTAttachment(screenshot:app.screenshot());shot.name=name;shot.lifetime = .keepAlways;add(shot)
    }
    func testFoldersRequireExplicitSelectionAndFinalConfirmation() throws {try exercise()}
    func testLargestTextCanReachFinalConfirmation() throws {try exercise(large:true)}
    func testInterruptedResponseCanCheckConfirmedResult() throws {try exercise(uncertain:true)}
    private func exercise(large:Bool = false,uncertain:Bool = false) throws {
        let app=XCUIApplication()
        app.launchArguments=["-DebridInboxTestNamespace",UUID().uuidString,"-DebridInboxCaptureMode",uncertain ? "selection-uncertain" : "selection"]
        if large {app.launchArguments += ["-UIPreferredContentSizeCategoryName","UICTContentSizeCategoryAccessibilityXXXL"]}
        app.launch()
        XCTAssertTrue(app.navigationBars["Choose files"].waitForExistence(timeout:12))
        XCTAssertFalse(app.buttons["review-file-selection"].isEnabled)
        capture(app,"selection-folders")
        let folder=app.buttons["Season 1"];reach(folder,in:app);folder.tap()
        XCTAssertTrue(app.navigationBars["Season 1"].waitForExistence(timeout:5))
        let file=app.buttons.containing(.staticText,identifier:"01 — The Open Ocean.mkv").firstMatch
        XCTAssertTrue(file.waitForExistence(timeout:5));reach(file,in:app);file.tap()
        let review=app.buttons["review-file-selection"]
        XCTAssertTrue(review.isEnabled);capture(app,"selection-files");review.tap()
        XCTAssertTrue(app.navigationBars["Review selection"].waitForExistence(timeout:5))
        let confirm=app.buttons["confirm-file-selection"];reach(confirm,in:app)
        XCTAssertTrue(confirm.isEnabled);capture(app,"selection-confirmation");confirm.tap()
        if uncertain {
            XCTAssertTrue(app.staticTexts["Result not confirmed"].waitForExistence(timeout:8))
            let check=app.buttons["Check again"];reach(check,in:app);check.tap()
        }
        XCTAssertTrue(app.staticTexts["Selection confirmed"].waitForExistence(timeout:8))
    }
}
