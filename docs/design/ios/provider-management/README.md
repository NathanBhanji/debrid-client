# Provider management · actual SwiftUI captures

These PNGs come from fixture UI tests on the existing iOS 27.0 Debrid shared simulator. They use production account forms and confirmations with an in-memory API. No real provider was contacted. Credential entry is privacy-sensitive and may be blanked by screenshot capture. See captures.json for test/result provenance and [behavior and checks](../../../plan/ios-provider-management.md).

Add, edit, safe removal, unknown/observed save and the accessibility-size force confirmation are included. The accessibility confirmation uses a scrollable native sheet because the system's narrow popover clipped content at the largest Dynamic Type size. Physical-device and complete VoiceOver acceptance remain separate.
