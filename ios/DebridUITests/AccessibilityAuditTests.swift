import XCTest

@MainActor final class AccessibilityAuditTests: XCTestCase {
    override func setUp() { super.setUp(); continueAfterFailure = false }
    override func tearDown() {
        XCUIDevice.shared.orientation = .portrait
        super.tearDown()
    }
    private func launch(_ arguments: [String], large: Bool = false) -> XCUIApplication {
        XCUIDevice.shared.orientation = .portrait
        let app = XCUIApplication(); app.launchArguments = arguments
        if large { app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] }
        app.launch(); return app
    }
    private func audit(_ app: XCUIApplication, _ name: String) throws {
        Thread.sleep(forTimeInterval: 0.7)
        var issues: [String] = []
        try app.performAccessibilityAudit(for: [.hitRegion, .sufficientElementDescription]) { issue in
            issues.append(String((String(issue.auditType.rawValue) + ": " + issue.compactDescription + " · " + (issue.element?.label ?? "unidentified") + " · " + issue.detailedDescription).prefix(1024)))
            return true // Record every finding below once, keeping generated screenshot volume bounded.
        }
        let report = XCTAttachment(string: issues.isEmpty ? "No findings for hit-region and sufficient-description audit types." : issues.prefix(50).joined(separator: "\n"))
        report.name = name + "-audit"; report.lifetime = .keepAlways; add(report)
        if name.contains("landscape") { Thread.sleep(forTimeInterval: 1.2) }
        let shot = XCTAttachment(screenshot: name.contains("landscape") ? XCUIScreen.main.screenshot() : app.screenshot()); shot.name = name; shot.lifetime = .keepAlways; add(shot)
        XCTAssertTrue(issues.isEmpty, "\(name): \(issues.prefix(12).joined(separator: "\n"))")
    }
    func testOnboardingAudit() throws {
        let app = launch(["-ConnectionPreview"])
        XCTAssertTrue(app.navigationBars["Connect"].waitForExistence(timeout: 12))
        try audit(app, "audit-connect")
    }
    func testTransfersAndDetailAudits() throws {
        let app = launch(["-TransfersPreview"])
        XCTAssertTrue(app.buttons["live-transfer-sample-nas"].waitForExistence(timeout: 12))
        try audit(app, "audit-transfers")
        let row = app.buttons["live-transfer-sample-nas"]
        for _ in 0..<10 { if row.isHittable { break }; app.swipeUp() }
        XCTAssertTrue(row.isHittable); row.tap()
        XCTAssertTrue(app.navigationBars["Transfer"].waitForExistence(timeout: 5))
        try audit(app, "audit-detail")
    }
    func testProviderAuditLight() throws {
        let app = launch(["-ProvidersFixture", "light"])
        XCTAssertTrue(app.buttons["provider-fixture-torbox"].waitForExistence(timeout: 12))
        try audit(app, "audit-providers-light")
    }
    func testSettingsAuditDark() throws {
        let app = launch(["-SettingsFixture", "normal"])
        XCTAssertTrue(app.buttons["settings-defaults"].waitForExistence(timeout: 12))
        try audit(app, "audit-settings-dark")
    }
    func testLargestTextLandscapeAndKeyboard() throws {
        let app = launch(["-TransfersPreview"], large: true)
        XCTAssertTrue(app.searchFields["Search transfers"].waitForExistence(timeout: 12))
        XCUIDevice.shared.orientation = .landscapeLeft
        let search = app.searchFields["Search transfers"]; search.tap(); search.typeText("Earth\n")
        let row = app.buttons["live-transfer-sample-nas"]
        for _ in 0..<12 { if row.exists && row.isHittable { break }; app.swipeUp() }
        XCTAssertTrue(row.isHittable); row.tap()
        XCTAssertTrue(app.navigationBars["Transfer"].waitForExistence(timeout: 5))
        try audit(app, "audit-landscape-largest")
        app.navigationBars.buttons.element(boundBy: 0).tap()
        XCTAssertTrue(app.buttons["live-transfer-sample-nas"].waitForExistence(timeout: 5))
        XCTAssertEqual(app.searchFields["Search transfers"].value as? String, "Earth")
    }
    func testDynamicTypeGrowsActualTransferRow() throws {
        var heights: [CGFloat] = []
        for large in [false, true] {
            let app = launch(["-TransfersPreview"], large: large)
            let search = app.searchFields["Search transfers"]; XCTAssertTrue(search.waitForExistence(timeout: 12))
            search.tap(); search.typeText("Earth\n")
            let row = app.buttons["live-transfer-sample-nas"]; XCTAssertTrue(row.waitForExistence(timeout: 5))
            heights.append(row.frame.height)
            XCTAssertTrue(row.label.contains("Downloading to NAS") && row.label.contains("64%"))
            try audit(app, large ? "audit-transfers-largest" : "audit-transfers-regular")
            app.terminate()
        }
        XCTAssertGreaterThan(heights[1], heights[0] * 1.4)
    }
    func testFileSelectionExposesBothStatesWithoutSubmitting() throws {
        let app = launch(["-DebridInboxTestNamespace", UUID().uuidString, "-DebridInboxCaptureMode", "selection"], large: true)
        XCTAssertTrue(app.navigationBars["Choose files"].waitForExistence(timeout: 12))
        let folder = app.buttons["Season 1"]
        for _ in 0..<14 { if folder.isHittable { break }; app.swipeUp() }; folder.tap()
        let file = app.buttons["01 — The Open Ocean.mkv"]
        for _ in 0..<14 { if file.exists && file.isHittable { break }; app.swipeUp() }
        XCTAssertTrue(file.isHittable)
        XCTAssertTrue((file.value as? String)?.hasPrefix("Not selected.") == true)
        file.tap(); XCTAssertTrue((file.value as? String)?.hasPrefix("Selected.") == true)
        XCTAssertTrue(app.buttons["review-file-selection"].isEnabled)
        let shot = XCTAttachment(screenshot: app.screenshot()); shot.name = "selection-accessibility-state-largest"; shot.lifetime = .keepAlways; add(shot)
        file.tap(); XCTAssertTrue((file.value as? String)?.hasPrefix("Not selected.") == true)
        XCTAssertFalse(app.buttons["review-file-selection"].isEnabled)
    }
    func testUnobscuredFilteredTransferContrast() throws {
        let app = launch(["-TransfersPreview"])
        let search = app.searchFields["Search transfers"]; XCTAssertTrue(search.waitForExistence(timeout: 12)); search.tap(); search.typeText("Earth\n")
        XCTAssertTrue(app.buttons["live-transfer-sample-nas"].waitForExistence(timeout: 5))
        try app.performAccessibilityAudit(for: [.contrast])
    }
    private func systemMotionToggle() throws -> (XCUIApplication, XCUIElement) {
        let settings = XCUIApplication(bundleIdentifier: "com.apple.Preferences")
        settings.launch()
        let toggle = settings.switches["Reduce Motion"]
        if !toggle.exists {
            let accessibility = settings.staticTexts["Accessibility"]
            for _ in 0..<14 { if accessibility.exists && accessibility.isHittable { break }; settings.swipeUp() }
            guard accessibility.exists && accessibility.isHittable else { throw XCTSkip("System Accessibility settings could not be reached on this runtime.") }
            accessibility.tap()
            let motion = settings.staticTexts["Motion"]
            for _ in 0..<12 { if motion.exists && motion.isHittable { break }; settings.swipeUp() }
            guard motion.exists && motion.isHittable else { throw XCTSkip("System Motion settings could not be reached on this runtime.") }
            motion.tap()
        }
        return (settings, toggle)
    }
    func testNavigationWithSystemReduceMotionEnabled() throws {
        // This flow owns temporary system settings; assertions must return through cleanup.
        continueAfterFailure = true
        defer { continueAfterFailure = false }
        let voiceOver = XCUIDevice.shared.voiceOverService, oldVoiceOver = XCUIDevice.shared.voiceOverService.isEnabled
        if oldVoiceOver { try voiceOver.disable() }
        defer { if oldVoiceOver { try? voiceOver.enable() } }
        let (_, toggle) = try systemMotionToggle()
        guard toggle.waitForExistence(timeout: 5), let old = toggle.value as? String, ["0", "1"].contains(old) else { throw XCTSkip("System Reduce Motion switch unavailable.") }
        defer {
            do {
                // Reopen Preferences to discard stale switch geometry after the app transition.
                let (_, restoreToggle) = try systemMotionToggle()
                if restoreToggle.value as? String != old { restoreToggle.coordinate(withNormalizedOffset: CGVector(dx: restoreToggle.value as? String == "1" ? 0.97 : 0.9, dy: 0.5)).tap() }
                let restored = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", old), object: restoreToggle)
                XCTAssertEqual(XCTWaiter.wait(for: [restored], timeout: 5), .completed, "Restore the shared simulator’s original Reduce Motion setting")
            } catch { XCTFail("Restore the shared simulator’s Motion settings: settings UI unavailable") }
        }
        if old == "0" { toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)).tap() }
        let enabled = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", "1"), object: toggle)
        guard XCTWaiter.wait(for: [enabled], timeout: 5) == .completed else { XCTFail("Reduce Motion switch did not enable"); return }
        let app = launch(["-TransfersPreview"])
        let search = app.searchFields["Search transfers"]; XCTAssertTrue(search.waitForExistence(timeout: 12)); search.tap(); search.typeText("Earth\n")
        let row = app.buttons["live-transfer-sample-nas"]; XCTAssertTrue(row.waitForExistence(timeout: 5)); row.tap()
        XCTAssertTrue(app.navigationBars["Transfer"].waitForExistence(timeout: 5))
        let shot = XCTAttachment(screenshot: app.screenshot()); shot.name = "system-reduce-motion-detail"; shot.lifetime = .keepAlways; add(shot)
        app.navigationBars.buttons.element(boundBy: 0).tap()
        XCTAssertEqual(app.searchFields["Search transfers"].value as? String, "Earth")
    }
    func testVoiceOverTransferSummary() throws {
        continueAfterFailure = true
        defer { continueAfterFailure = false }
        let app = launch(["-TransfersPreview"])
        XCTAssertTrue(app.buttons["live-transfer-sample-nas"].waitForExistence(timeout: 12))
        let service = XCUIDevice.shared.voiceOverService
        let previouslyEnabled = service.isEnabled
        if !previouslyEnabled { try service.enable() }
        defer { if !previouslyEnabled { try? service.disable() } }
        var speech: [String] = []
        for _ in 0..<30 {
            let output = try service.moveForward()
            speech.append(output.utterance)
            if output.utterance.contains("Earth from Above") { break }
        }
        let attachment = XCTAttachment(string: speech.joined(separator: "\n")); attachment.name = "voiceover-transfer-navigation"; attachment.lifetime = .keepAlways; add(attachment)
        XCTAssertTrue(speech.contains { $0.contains("Earth from Above") && $0.contains("Downloading to NAS") })
    }
}
