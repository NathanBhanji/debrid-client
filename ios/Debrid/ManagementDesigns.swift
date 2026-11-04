#if DEBUG
import SwiftUI

struct ProviderDesign: View {
    let screen: DesignScreen
    @State private var enabled = true
    @State private var kind = "TorBox"
    @State private var name = "Home account"
    @State private var token = ""
    var body: some View {
        DesignForm {
            if screen == .providers {
                Section("Connected accounts") {
                    NavigationLink { DesignDestination(screen: .provider) } label: {
                        VStack(alignment: .leading, spacing: 8) {
                            Label("TorBox · Default", systemImage: "externaldrive.badge.checkmark").font(.headline)
                            Text("Connection checked just now").font(.footnote).foregroundStyle(Color("StageGreen"))
                        }
                    }
                    Label("Real-Debrid · Not checked", systemImage: "externaldrive")
                }
                Section { NavigationLink("Add provider account") { DesignDestination(screen: .account) } }
                Section { Text("Saved credentials don’t confirm provider health. Run a check when you need one.").foregroundStyle(.secondary) }
            } else if screen == .provider {
                Section { Label("TorBox", systemImage: "externaldrive.connected.to.line.below").font(.title2.bold()) }
                Section("Last check · Sample") {
                    Label("Connection verified", systemImage: "checkmark.circle").foregroundStyle(Color("StageGreen"))
                    LabeledContent("Checked", value: "Just now")
                    LabeledContent("Plan", value: "Premium")
                    LabeledContent("Expires", value: "Not reported")
                    DesignAction(title: "Check connection")
                }
                Section("Account") {
                    Toggle("Enabled", isOn: $enabled)
                    LabeledContent("Default", value: "Yes")
                    NavigationLink("Edit account") { DesignDestination(screen: .account) }
                }
                Section { NavigationLink("Remove account…") { DesignDestination(screen: .accountRemoval) }.foregroundStyle(Color("DangerInk")) }
            } else {
                Section {
                    Picker("Service", selection: $kind) {
                        ForEach(["TorBox", "Real-Debrid", "AllDebrid", "Premiumize", "Debrid-Link"], id: \.self) { Text($0) }
                    }
                    TextField("Account name", text: $name)
                    SecureField("Provider API token", text: $token).textInputAutocapitalization(.never).autocorrectionDisabled()
                } header: { Text("Provider") } footer: { Text("This is the provider’s token, not your Debrid server key. It is sent only to your own server when you save.") }
                Section { Toggle("Enabled", isOn: $enabled); DesignAction(title: "Verify and save account", prominent: true) }
                Section { Text("Preview only. Don’t enter a real token in the design gallery.").foregroundStyle(.secondary) }
            }
        }
    }
}

struct SettingsDesign: View {
    let screen: DesignScreen
    @State private var enabled = false
    @State private var unpack = true
    @State private var category = "Documentaries"
    @State private var movie = "{title} ({year})"
    @State private var tv = "{title} ({year})/Season {season:02}"
    @State private var retries = 3
    var body: some View {
        DesignForm {
            switch screen {
            case .settings:
                Section("Connection") {
                    LabeledContent("Server", value: "Debrid NAS · Sample")
                    LabeledContent("Sign-in", value: "Pocket ID")
                    DesignAction(title: "Sign out")
                }
                Section("On your server") {
                    NavigationLink("Categories") { DesignDestination(screen: .categories) }
                    NavigationLink("Download policy") { DesignDestination(screen: .policy) }
                    NavigationLink("Library organization") { DesignDestination(screen: .organization) }
                }
                Section("About") {
                    NavigationLink("Diagnostics & help") { DesignDestination(screen: .diagnostics) }
                    LabeledContent("Debrid for iPhone", value: "Design preview")
                }
            case .categories:
                Section("Categories") {
                    Label("Documentaries", systemImage: "folder")
                    Label("Audio", systemImage: "folder")
                    Label("Uncategorized", systemImage: "tray")
                }
                Section("New category") { TextField("Category name", text: $category); DesignAction(title: "Add category") }
                Section { Text("Categories organize future transfers on your server. Editing them won’t move existing files.").foregroundStyle(.secondary) }
            case .policy:
                Section("Defaults for new transfers") {
                    Toggle("Extract archives", isOn: $unpack)
                    Stepper("Download retries: \(retries)", value: $retries, in: 0...10)
                    LabeledContent("After completion", value: "Keep at provider")
                    LabeledContent("Minimum file size", value: "No minimum")
                }
                Section { DesignAction(title: "Review and save defaults", prominent: true) }
                Section { Label("If another client changes settings, you’ll review the conflict before saving.", systemImage: "arrow.triangle.2.circlepath") }
            case .organization:
                Section { Toggle("Organize your library", isOn: $enabled) }
                Section("Folder templates") {
                    TextField("Movie template", text: $movie).textInputAutocapitalization(.never)
                    TextField("TV template", text: $tv).textInputAutocapitalization(.never)
                }
                Section("Preview · Sample") {
                    Label("The Open Ocean (2025)", systemImage: "folder")
                    Text("New transfers only. Existing folders stay where they are.").font(.footnote).foregroundStyle(.secondary)
                }
                Section { DesignAction(title: "Review and save", prominent: true) }
            case .overrides:
                Section("The Open Ocean") { Toggle("Extract archives", isOn: $unpack); Stepper("Download retries: \(retries)", value: $retries, in: 0...10) }
                Section { DesignAction(title: "Review transfer settings", prominent: true) }
                Section { Text("Applies only to this transfer. Other transfers keep their defaults. File selection is managed separately.").foregroundStyle(.secondary) }
            default:
                Section("Connection · Sample") {
                    LabeledContent("Server version", value: "0.2.1")
                    LabeledContent("Last reachable", value: "14:32")
                    LabeledContent("Network", value: "Unavailable")
                }
                Section { DesignAction(title: "Export redacted diagnostics") }
                Section { Text("Credentials, magnet links and file names are excluded from diagnostic exports.").foregroundStyle(.secondary) }
            }
        }
    }
}

struct RecoveryDesign: View {
    let screen: DesignScreen
    @State private var provider = false
    @State private var files = false
    var body: some View {
        DesignForm {
            switch screen {
            case .accountRemoval:
                Section {
                    Text("Remove Home account?").font(.title2.bold())
                    Text("Debrid will stop using this provider account. If it still has transfers, review those before removing it.")
                }
                Section {
                    Toggle("This account’s transfers from Debrid", isOn: $provider)
                } header: { Text("Also remove") } footer: { Text("This extra choice removes transfer records locally. Removing an account does not delete files from your NAS.") }
                Section { DesignAction(title: "Remove account", role: .destructive) }
            case .cancellation:
                Section {
                    Text("Cancel The Open Ocean?").font(.title2.bold())
                    Text("Stop work on this transfer. Files already downloaded to your NAS will be kept.")
                    DesignAction(title: "Cancel transfer", role: .destructive)
                } footer: { Text("Design for a future capability. This action is offered only when your server supports cancellation; server 0.2.1 does not.") }
            case .conflict:
                Section {
                    Label("Settings changed on another client", systemImage: "arrow.triangle.2.circlepath").font(.title2.bold())
                    Text("Your edits are saved here. Compare them with the latest server settings before saving again.")
                }
                Section("Download retries · Sample conflict") {
                    LabeledContent("Your edit", value: "3 retries")
                    LabeledContent("Latest on server", value: "5 retries")
                    DesignAction(title: "Review latest settings", prominent: true)
                }
            case .tls:
                Section {
                    Label("Couldn’t verify this server", systemImage: "lock.trianglebadge.exclamationmark").font(.title2.bold())
                    Text("The HTTPS certificate isn’t trusted. Check your server address, certificate and this iPhone’s date and time.")
                    DesignAction(title: "Check again")
                } footer: { Text("Debrid won’t send your credentials over an unverified connection.") }
            case .update:
                Section {
                    Label("Your server needs an update", systemImage: "server.rack").font(.title2.bold())
                    Text("This server doesn’t support native Pocket ID sign-in yet. Update it, then check again.")
                    DesignAction(title: "Check server again")
                } footer: { Text("Your existing web client keeps working. No API key is required for the normal iPhone sign-in flow.") }
            case .removal:
                Section {
                    Text("Remove The Open Ocean?").font(.title2.bold())
                    Text("This removes the transfer from Debrid. Choose separately whether to remove provider data or downloaded files.")
                }
                Section("Also remove") {
                    Toggle("At the debrid provider", isOn: $provider)
                    Toggle("Downloaded files on your NAS", isOn: $files)
                }
                Section { DesignAction(title: "Remove transfer", role: .destructive) }
                Section { Text("Keeping both switches off preserves provider data and downloaded files. Removal is not pause.").foregroundStyle(.secondary) }
            case .offline:
                Section {
                    Label("Can’t reach your server", systemImage: "wifi.slash").font(.title2.bold())
                    Text("Check your connection and Tailscale. Last updated at 14:32.")
                    DesignAction(title: "Try again")
                }
                Section("Last known progress") {
                    Text("The Open Ocean").font(.headline)
                    Text("Last seen downloading · 6.4 GB of 10 GB").foregroundStyle(.secondary)
                }
                Section { NavigationLink("Review 1 saved draft") { DesignDestination(screen: .drafts) } }
            case .empty:
                Section {
                    ContentUnavailableView("No transfers yet", systemImage: "arrow.down.circle", description: Text("Add a magnet link or import a torrent file. Downloads will run on your server."))
                    NavigationLink("Add your first transfer") { DesignDestination(screen: .add) }
                }
            case .expired:
                Section {
                    Label("Your session has expired", systemImage: "person.crop.circle.badge.exclamationmark").font(.title2.bold())
                    Text("Sign in with Pocket ID to reconnect. Your server keeps working, and your unsent drafts are safe.")
                    NavigationLink("Continue with Pocket ID") { DesignDestination(screen: .signIn) }
                }
            default:
                Section {
                    Label("One file needs attention", systemImage: "exclamationmark.triangle").font(.title2.bold())
                    Text("The provider link expired before this file finished. Other completed files are safe.")
                }
                Section("Interview.mp4") {
                    LabeledContent("Last progress", value: "1.2 GB of 2 GB")
                    DesignAction(title: "Retry file")
                }
                Section { DesignAction(title: "Try server again") }
                Section { Text("For a temporary server error, wait and retry. TLS failures never offer an insecure bypass.").foregroundStyle(.secondary) }
            }
        }
    }
}
#endif
