import XCTest

@MainActor
final class DemoFlowTests: XCTestCase {
    func testNativeNavigationAndReviewSheet() throws {
        let app = XCUIApplication()
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
}
