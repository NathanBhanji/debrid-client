import XCTest

@MainActor final class ProviderFlowTests: XCTestCase {
    override func setUp() { super.setUp(); continueAfterFailure = false }
    private func launch(_ mode: String = "normal", large: Bool = false) -> XCUIApplication {
        let app = XCUIApplication(); app.launchArguments = ["-ProvidersFixture", mode]
        if large { app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] }
        app.launch(); return app
    }
    private func capture(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot()); shot.name = name; shot.lifetime = .keepAlways; add(shot)
    }
    private func reach(_ element: XCUIElement, in app: XCUIApplication) {
        for _ in 0..<15 {
            if element.exists, element.isHittable, element.frame.minY > 105, (element.frame.maxY < app.frame.maxY - 95 || element.frame.height > app.frame.height - 210) { break }
            if element.exists, element.frame.minY < 105 { app.swipeDown() } else { app.swipeUp() }
        }
        XCTAssertTrue(element.isHittable)
    }
    func testExplicitHealthAndNativeTabs() {
        let app = launch()
        let row = app.buttons["provider-fixture-torbox"]
        XCTAssertTrue(row.waitForExistence(timeout: 12))
        XCTAssertFalse(app.staticTexts["Credentials checked"].exists)
        capture(app, "providers-dark")
        row.tap()
        let check = app.buttons["provider-check"]; XCTAssertTrue(check.waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts["Premium"].exists)
        capture(app, "provider-unchecked")
        check.tap()
        let membership = app.descendants(matching: .any).matching(identifier: "provider-membership").firstMatch
        XCTAssertTrue(membership.waitForExistence(timeout: 8)); XCTAssertEqual(membership.value as? String, "Premium")
        capture(app, "provider-checked-dark")
        app.tabBars.buttons["Settings"].tap()
        XCTAssertTrue(app.navigationBars["Settings"].waitForExistence(timeout: 5))
        XCTAssertEqual(app.descendants(matching: .any).matching(identifier: "settings-identity").firstMatch.value as? String, "Sample User")
        capture(app, "connection-settings")
        app.tabBars.buttons["Providers"].tap()
        XCTAssertTrue(membership.waitForExistence(timeout: 5))
    }
    func testLightAccountsAndSearch() {
        let app = launch("light")
        XCTAssertTrue(app.buttons["provider-fixture-torbox"].waitForExistence(timeout: 12))
        capture(app, "providers-light")
        let search = app.searchFields.firstMatch
        if !search.exists { app.swipeDown() }
        XCTAssertTrue(search.waitForExistence(timeout: 5))
        search.tap(); search.typeText("not-a-provider")
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "provider-no-results").firstMatch.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["provider-fixture-torbox"].exists)
        capture(app, "providers-search-empty")
    }
    func testFailedCheckAndLargestText() {
        let app = launch("failure", large: true)
        let row = app.buttons["provider-fixture-torbox"]; for _ in 0..<10 { if row.exists { break }; app.swipeUp() }; row.tap()
        let check = app.buttons["provider-check"]; reach(check, in: app); check.tap()
        let error = app.descendants(matching: .any).matching(identifier: "provider-check-error").firstMatch
        XCTAssertTrue(error.waitForExistence(timeout: 8)); reach(error, in: app)
        capture(app, "provider-error-largest")
        XCTAssertFalse(app.staticTexts["Premium"].exists)
    }
    func testEmptyAndOfflineStates() {
        var app = launch("empty")
        XCTAssertTrue(app.staticTexts["No provider accounts"].waitForExistence(timeout: 12)); capture(app, "providers-empty")
        app.terminate(); app = launch("offline")
        XCTAssertTrue(app.buttons["Try again"].waitForExistence(timeout: 12)); capture(app, "providers-offline")
        XCTAssertFalse(app.staticTexts["No provider accounts"].exists)
    }
}
