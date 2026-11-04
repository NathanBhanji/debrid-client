import XCTest

@MainActor final class ProviderEditingFlowTests: XCTestCase {
    override func setUp() { super.setUp(); continueAfterFailure = false }
    private func launch(_ mode: String = "normal", large: Bool = false) -> XCUIApplication {
        let app=XCUIApplication(); app.launchArguments=["-ProvidersFixture",mode]; if large { app.launchArguments += ["-UIPreferredContentSizeCategoryName","UICTContentSizeCategoryAccessibilityXXXL"] };app.launch();return app
    }
    private func reach(_ element: XCUIElement, _ app: XCUIApplication) {
        for _ in 0..<10 { if element.exists && element.isHittable { return };app.swipeUp() }
        XCTAssertTrue(element.isHittable)
    }
    private func capture(_ app:XCUIApplication,_ name:String) {
        let shot=XCTAttachment(screenshot:app.screenshot());shot.name=name;shot.lifetime = .keepAlways;add(shot)
    }
    private func openEdit(_ app:XCUIApplication) {
        let row=app.buttons["provider-fixture-torbox"];for _ in 0..<10 { if row.waitForExistence(timeout:1) {break};app.swipeUp() };row.tap()
        app.buttons["provider-edit"].tap();XCTAssertTrue(app.navigationBars["Edit provider"].waitForExistence(timeout:5))
    }
    func testAddRequiresMaskedCredentialAndExplicitSave() {
        let app=launch();let add=app.buttons["provider-add"];XCTAssertTrue(add.waitForExistence(timeout:12));add.tap()
        let name=app.textFields["provider-name"];XCTAssertTrue(name.waitForExistence(timeout:5));name.tap();name.typeText("Travel library")
        let secret=app.secureTextFields["provider-secret"];reach(secret,app);secret.tap();secret.typeText("fixture-unused")
        app.swipeUp();capture(app,"provider-add-masked")
        let save=app.buttons["provider-save"];reach(save,app);XCTAssertTrue(save.isEnabled);save.tap()
        XCTAssertTrue(app.descendants(matching:.any).matching(identifier:"provider-saved").firstMatch.waitForExistence(timeout:8))
        capture(app,"provider-add-saved")
    }
    func testEditDoesNotExposeOrReplaceExistingSecret() {
        let app=launch();openEdit(app)
        XCTAssertFalse(app.secureTextFields["provider-secret"].exists)
        capture(app,"provider-edit-defaults")
        let toggle=app.switches["provider-enabled"];reach(toggle,app);toggle.coordinate(withNormalizedOffset:CGVector(dx:0.93,dy:0.5)).tap()
        let save=app.buttons["provider-save"];reach(save,app);save.tap()
        XCTAssertTrue(app.descendants(matching:.any).matching(identifier:"provider-saved").firstMatch.waitForExistence(timeout:8))
    }
    func testRemovalDefaultsSafeAndRequiresFinalConfirmation() {
        let app=launch();openEdit(app)
        let force=app.switches["provider-remove-transfers"];reach(force,app);XCTAssertEqual(force.value as? String,"0")
        capture(app,"provider-remove-options")
        let remove=app.buttons["provider-remove"];reach(remove,app);remove.tap()
        app.buttons["Keep account"].tap()
        XCTAssertFalse(app.descendants(matching:.any).matching(identifier:"provider-removed").firstMatch.exists)
        remove.tap();capture(app,"provider-remove-confirmation");app.buttons["Confirm removal"].tap()
        XCTAssertTrue(app.descendants(matching:.any).matching(identifier:"provider-removed").firstMatch.waitForExistence(timeout:8))
    }
    func testLargestForceRemovalKeepsSafeAlternativeVisible() {
        let app=launch(large:true);openEdit(app)
        let force=app.switches["provider-remove-transfers"];reach(force,app)
        force.coordinate(withNormalizedOffset:CGVector(dx:0.93,dy:0.5)).tap();XCTAssertEqual(force.value as? String,"1")
        let remove=app.buttons["provider-remove"];reach(remove,app);remove.tap()
        capture(app,"provider-force-review-largest")
        let keep=app.buttons["Keep account"];reach(keep,app);XCTAssertTrue(keep.isHittable)
        capture(app,"provider-force-dialog-largest");keep.tap()
        XCTAssertFalse(app.descendants(matching:.any).matching(identifier:"provider-removed").firstMatch.exists)
        reach(remove,app);remove.tap();let confirm=app.buttons["Remove"];reach(confirm,app);confirm.tap()
        XCTAssertTrue(app.descendants(matching:.any).matching(identifier:"provider-removed").firstMatch.waitForExistence(timeout:8))
    }
    func testLostSaveResponseChecksWithoutResending() {
        let app=launch("mutation-unknown");openEdit(app)
        let toggle=app.switches["provider-enabled"];reach(toggle,app);toggle.coordinate(withNormalizedOffset:CGVector(dx:0.93,dy:0.5)).tap()
        let save=app.buttons["provider-save"];reach(save,app);save.tap()
        let check=app.buttons["provider-check-result"];XCTAssertTrue(check.waitForExistence(timeout:8));capture(app,"provider-save-unknown")
        XCTAssertFalse(save.exists);check.tap()
        XCTAssertTrue(app.descendants(matching:.any).matching(identifier:"provider-observed").firstMatch.waitForExistence(timeout:8))
        capture(app,"provider-save-observed")
    }
}
