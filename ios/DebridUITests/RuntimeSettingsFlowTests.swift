import XCTest

@MainActor final class RuntimeSettingsFlowTests: XCTestCase {
    override func setUp() { super.setUp(); continueAfterFailure = false }
    private func launch(_ mode: String = "normal", large: Bool = false) -> XCUIApplication {
        let app=XCUIApplication();app.launchArguments=["-SettingsFixture",mode]
        if large { app.launchArguments += ["-UIPreferredContentSizeCategoryName","UICTContentSizeCategoryAccessibilityXXXL"] }
        app.launch();return app
    }
    private func reach(_ element: XCUIElement, _ app: XCUIApplication) {
        for _ in 0..<14 { if element.exists && element.isHittable { return };app.swipeUp() }
        XCTAssertTrue(element.isHittable)
    }
    private func capture(_ app: XCUIApplication, _ name: String) {
        Thread.sleep(forTimeInterval:0.6)
        let attachment=XCTAttachment(screenshot:app.screenshot());attachment.name=name;attachment.lifetime = .keepAlways;add(attachment)
    }
    private func editCategory(_ app: XCUIApplication) {
        let categories=app.buttons["settings-categories"];for _ in 0..<10 { if categories.waitForExistence(timeout:1) {break};app.swipeUp() };categories.tap()
        let field=app.textFields["category-name"];reach(field,app);field.tap();field.typeText("Travel")
        let add=app.buttons["category-add"];reach(add,app);add.tap()
    }
    private func review(_ app: XCUIApplication) {
        app.buttons["settings-review-toolbar"].tap()
        XCTAssertTrue(app.navigationBars["Review settings"].waitForExistence(timeout:5))
    }
    private func save(_ app: XCUIApplication) { let save=app.buttons["settings-save"];reach(save,app);save.tap() }
    func testCategoriesRequireReviewAndExplicitSave() {
        let app=launch();XCTAssertTrue(app.buttons["settings-categories"].waitForExistence(timeout:12));capture(app,"settings-home-dark")
        editCategory(app);capture(app,"settings-categories")
        review(app);capture(app,"settings-review")
        XCTAssertFalse(app.descendants(matching:.any).matching(identifier:"settings-saved").firstMatch.exists)
        app.buttons["Keep editing"].tap();review(app);save(app)
        XCTAssertTrue(app.descendants(matching:.any).matching(identifier:"settings-saved").firstMatch.waitForExistence(timeout:8));capture(app,"settings-saved")
    }
    func testPolicyAndLightOrganizationPreviewDoNotSave() {
        let app=launch("light");let policy=app.buttons["settings-defaults"];XCTAssertTrue(policy.waitForExistence(timeout:12));policy.tap()
        XCTAssertTrue(app.textFields["policy-exclude"].exists);capture(app,"settings-policy-light")
        app.navigationBars.buttons.element(boundBy:0).tap()
        let organization=app.buttons["settings-organization"];reach(organization,app);organization.tap()
        let preview=app.buttons["organization-preview"];reach(preview,app);preview.tap()
        XCTAssertTrue(app.staticTexts["organization-path"].waitForExistence(timeout:8));capture(app,"settings-organization-light")
        XCTAssertFalse(app.descendants(matching:.any).matching(identifier:"settings-saved").firstMatch.exists)
    }
    func testConflictPreservesDraftAndRequiresSectionChoice() {
        let app=launch("conflict");editCategory(app);review(app);save(app)
        let conflict=app.buttons["settings-conflict"];XCTAssertTrue(conflict.waitForExistence(timeout:8));conflict.tap()
        XCTAssertTrue(app.navigationBars["Review server changes"].waitForExistence(timeout:5));capture(app,"settings-conflict")
        let merge=app.buttons["settings-merge"];reach(merge,app);XCTAssertFalse(merge.isEnabled)
        app.buttons.containing(NSPredicate(format:"label CONTAINS %@","Use for Categories")).firstMatch.tap()
        app.buttons["My changes"].tap();reach(merge,app);merge.tap()
        XCTAssertFalse(app.descendants(matching:.any).matching(identifier:"settings-saved").firstMatch.exists)
        let review=app.buttons["settings-review"];reach(review,app);review.tap();save(app)
        XCTAssertTrue(app.descendants(matching:.any).matching(identifier:"settings-saved").firstMatch.waitForExistence(timeout:8))
    }
    func testLostSaveOnlyObservesCurrentState() {
        let app=launch("unknown");editCategory(app);review(app);save(app)
        let check=app.buttons["settings-check-result"];XCTAssertTrue(check.waitForExistence(timeout:8));capture(app,"settings-unknown");check.tap()
        let conflict=app.buttons["settings-conflict"];XCTAssertTrue(conflict.waitForExistence(timeout:8));conflict.tap()
        let merge=app.buttons["settings-merge"];reach(merge,app);merge.tap()
        XCTAssertFalse(app.descendants(matching:.any).matching(identifier:"settings-saved").firstMatch.exists)
        XCTAssertFalse(app.buttons["settings-review"].exists);capture(app,"settings-observed")
    }
    func testLargestTextReviewHasReachableSafeAndSaveActions() {
        let app=launch(large:true);editCategory(app);review(app);capture(app,"settings-review-largest")
        let keep=app.buttons["Keep editing"];reach(keep,app);XCTAssertTrue(keep.isHittable);capture(app,"settings-review-actions-largest");keep.tap()
        XCTAssertFalse(app.descendants(matching:.any).matching(identifier:"settings-saved").firstMatch.exists)
    }
    func testDeletionPolicyIsVisibleBeforeSaveAndCanBeDismissed() {
        let app=launch();let policy=app.buttons["settings-defaults"];XCTAssertTrue(policy.waitForExistence(timeout:12));policy.tap()
        let delay=app.textFields["policy-delete-after-error"];reach(delay,app);delay.tap();delay.typeText("24h")
        review(app)
        let warning=app.staticTexts.containing(NSPredicate(format:"label CONTAINS %@","Delete NAS files and provider copy after terminal error")).firstMatch
        reach(warning,app);capture(app,"settings-lifecycle-review")
        let keep=app.buttons["Keep editing"];reach(keep,app);keep.tap()
        XCTAssertFalse(app.descendants(matching:.any).matching(identifier:"settings-saved").firstMatch.exists)
    }
    func testUnsupportedAndOfflineStates() {
        var app=launch("unsupported");XCTAssertTrue(app.staticTexts["Server update needed"].waitForExistence(timeout:12));capture(app,"settings-update")
        app.terminate();app=launch("offline");XCTAssertTrue(app.buttons["Try again"].waitForExistence(timeout:12));capture(app,"settings-offline")
    }
}
