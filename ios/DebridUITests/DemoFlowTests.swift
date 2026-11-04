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
