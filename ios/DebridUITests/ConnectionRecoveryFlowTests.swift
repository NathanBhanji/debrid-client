import XCTest

@MainActor final class ConnectionRecoveryFlowTests: XCTestCase {
    override func setUp() { super.setUp(); continueAfterFailure = false }
    private func launch(_ mode: String, large: Bool = false) -> XCUIApplication {
        let app = XCUIApplication(); app.launchArguments = ["-RecoveryFixture", mode]
        if large { app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] }
        app.launch(); return app
    }
    private func capture(_ app: XCUIApplication, _ name: String) {
        Thread.sleep(forTimeInterval: 0.6)
        let attachment = XCTAttachment(screenshot: app.screenshot()); attachment.name = name; attachment.lifetime = .keepAlways; add(attachment)
    }
    private func reach(_ element: XCUIElement, _ app: XCUIApplication) {
        for _ in 0..<16 { if element.exists && element.isHittable { return }; app.swipeUp() }
        XCTAssertTrue(element.isHittable)
    }
    func testExplicitPublicCheckAndReviewedRedactedReport() {
        let app = launch("light")
        let check = app.buttons["diagnostics-check"]; XCTAssertTrue(check.waitForExistence(timeout: 10))
        XCTAssertFalse(app.descendants(matching: .any).matching(identifier: "diagnostics-result").firstMatch.exists)
        capture(app, "connection-help-light"); check.tap()
        XCTAssertTrue(app.staticTexts["Server reachable"].waitForExistence(timeout: 5)); capture(app, "connection-reachable")
        XCTAssertFalse(app.buttons["diagnostics-share"].exists)
        let review = app.buttons["Review report"]; reach(review, app); review.tap()
        let report = app.staticTexts["diagnostics-report"]; reach(report, app)
        XCTAssertTrue(report.label.contains("Session acceptance: not tested")); XCTAssertFalse(report.label.contains("private.example.test"))
        let share = app.buttons["diagnostics-share"]; reach(share, app); XCTAssertTrue(share.isEnabled); capture(app, "connection-report")
    }
    func testOfflineDNSAndTLSHaveSeparateGuidance() {
        for (mode, title) in [("offline", "You’re offline"), ("dns", "Server name unavailable"), ("tls", "Secure connection unavailable")] {
            let app = launch(mode); let check = app.buttons["diagnostics-check"]; XCTAssertTrue(check.waitForExistence(timeout: 10)); check.tap()
            let result = app.staticTexts[title]; XCTAssertTrue(result.waitForExistence(timeout: 5)); reach(result, app); capture(app, "connection-" + mode)
            app.terminate()
        }
    }
    func testLargestTextKeepsCheckAndReportReachable() {
        let app = launch("server", large: true)
        XCTAssertTrue(app.navigationBars["Connection help"].waitForExistence(timeout: 10))
        let check = app.buttons["diagnostics-check"]; reach(check, app); check.tap()
        let result = app.staticTexts["Server needs attention"]; reach(result, app); capture(app, "connection-server-largest")
        let review = app.buttons["Review report"]; reach(review, app); review.tap()
        let share = app.buttons["diagnostics-share"]; reach(share, app); XCTAssertTrue(share.isEnabled); capture(app, "connection-report-largest")
    }
    func testOfflineTransfersRetainLastConfirmedSnapshotAndTimestamp() {
        let app = launch("stale")
        XCTAssertTrue(app.staticTexts["Showing the last loaded transfers."].waitForExistence(timeout: 12))
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label BEGINSWITH %@", "Last confirmed ")).firstMatch.exists)
        capture(app, "transfers-stale-offline")
    }
}
