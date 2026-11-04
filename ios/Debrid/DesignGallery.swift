#if DEBUG
import SwiftUI

/// Native design routes use fictional data and have no transport or credential dependencies.
enum DesignScreen: String, CaseIterable, Identifiable {
    case connect, signIn = "sign-in", transfers, detail, files, selection
    case add, share, drafts, duplicate, uncertain, filters
    case providers, provider, account, settings, categories, policy, organization, overrides
    case removal, offline, empty, error, expired, diagnostics
    case cancellation, conflict = "settings-conflict", tls, update
    case intake, accountRemoval = "account-removal"
    var id: String { rawValue }
    var title: String {
        switch self {
        case .connect: "Connect your server"
        case .signIn: "Sign in"
        case .transfers: "Transfers"
        case .detail: "The Open Ocean"
        case .files: "Files"
        case .selection: "Choose files"
        case .add: "Add transfer"
        case .share: "Add to Debrid"
        case .drafts: "Saved drafts"
        case .duplicate: "Already in Debrid"
        case .uncertain: "Checking transfer"
        case .filters: "Filter transfers"
        case .providers: "Providers"
        case .provider: "TorBox"
        case .account: "Provider account"
        case .settings: "Settings"
        case .categories: "Categories"
        case .policy: "Download policy"
        case .organization: "Library organization"
        case .overrides: "Transfer settings"
        case .removal: "Remove transfer"
        case .offline: "Offline"
        case .empty: "Transfers"
        case .error: "Needs attention"
        case .expired: "Sign in again"
        case .diagnostics: "Diagnostics"
        case .cancellation: "Cancel transfer"
        case .conflict: "Settings changed"
        case .tls: "Secure connection"
        case .update: "Server update needed"
        case .intake: "New transfer"
        case .accountRemoval: "Remove account"
        }
    }
}

struct DesignGallery: View {
    @State private var selected: DesignScreen?
    var body: some View {
        NavigationStack {
            List(DesignScreen.allCases) { screen in
                Button(screen.title) { selected = screen }
            }
            .modifier(DebridListStyle())
            .navigationTitle("Native designs")
            .sheet(item: $selected) { DesignScene(screen: $0).presentationDragIndicator(.visible) }
        }.tint(DebridStyle.tint)
    }
}

/// Owns a navigation stack only at a standalone root or presentation boundary.
struct DesignScene: View {
    let screen: DesignScreen
    var body: some View {
        Group {
            if screen == .transfers { DemoRoot() }
            else { NavigationStack { DesignDestination(screen: screen) } }
        }.tint(DebridStyle.tint)
    }
}

/// Destinations participate in the current stack; never push a nested stack.
struct DesignDestination: View {
    let screen: DesignScreen
    var body: some View {
        content
            .navigationTitle(screen.title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Image(systemName: "viewfinder").accessibilityLabel("Design preview")
                }
            }
    }
    @ViewBuilder private var content: some View {
        switch screen {
        case .connect, .signIn: OnboardingDesign(screen: screen)
        case .detail, .files, .selection: TransferDesign(screen: screen)
        case .add, .share, .drafts, .duplicate, .uncertain, .filters, .intake: IntakeDesign(screen: screen)
        case .providers, .provider, .account: ProviderDesign(screen: screen)
        case .settings, .categories, .policy, .organization, .overrides, .diagnostics: SettingsDesign(screen: screen)
        case .removal, .offline, .empty, .error, .expired, .cancellation, .conflict, .tls, .update, .accountRemoval: RecoveryDesign(screen: screen)
        case .transfers: EmptyView()
        }
    }
}

struct DesignForm<Content: View>: View {
    @ViewBuilder var content: () -> Content
    var body: some View {
        Form {
            Section {
                Label("Design preview · Fictional data", systemImage: "viewfinder")
                    .font(.footnote).foregroundStyle(.secondary)
            }
            content()
        }.modifier(DebridListStyle())
    }
}

/// Actions provide honest preview feedback; no fake successful server mutations.
struct DesignAction: View {
    let title: String
    var role: ButtonRole? = nil
    var prominent = false
    var enabled = true
    @State private var showingNotice = false
    private var action: some View {
        Button(title, role: role) { showingNotice = true }
            .disabled(!enabled).frame(minHeight: 44)
    }
    var body: some View {
        Group {
            if prominent {
                action.buttonStyle(.glassProminent)
                    .tint(Color(red: 0.714, green: 0.216, blue: 0))
                    .foregroundStyle(.white)
            } else {
                action.buttonStyle(.borderless)
                    .foregroundStyle(role == .destructive ? Color.red : DebridStyle.tint)
            }
        }
        .alert("Preview only", isPresented: $showingNotice) {
            Button("OK", role: .cancel) {}
        } message: { Text("Nothing was sent or changed on your server.") }
    }

}

private struct OnboardingDesign: View {
    let screen: DesignScreen
    @State private var address = "https://debrid.example.ts.net"
    var body: some View {
        DesignForm {
            Section { DebridWordmark() }
            if screen == .connect {
                Section {
                    Text("Your server.\nYour transfers.").font(.largeTitle.bold())
                    Text("Manage your downloads here. Your server does the downloading.")
                        .foregroundStyle(.secondary)
                }
                Section {
                    TextField("HTTPS server address", text: $address)
                        .textContentType(.URL).keyboardType(.URL)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                    DesignAction(title: "Check server", prominent: true)
                } header: { Text("Server address") } footer: { Text("Use your HTTPS address. If your server is private, connect Tailscale first.") }
                Section { Label("Your saved drafts stay on this iPhone until you review and add them.", systemImage: "tray") }
            } else {
                Section {
                    Label("Server found", systemImage: "checkmark.shield")
                    Text("debrid.example.ts.net").font(.footnote.monospaced()).foregroundStyle(.secondary)
                }
                Section {
                    Text("Your usual sign-in.").font(.title2.bold())
                    Text("Continue in your browser to sign in with your passkey. You’ll return to Debrid when you’re done.")
                    DesignAction(title: "Continue with Pocket ID", prominent: true)
                } header: { Text("Pocket ID") } footer: { Text("No API key to copy. Your session is stored in this iPhone’s Keychain.") }
                Section { Label("Older server? Native sign-in needs a server update.", systemImage: "info.circle") }
            }
        }
    }
}

private struct TransferDesign: View {
    let screen: DesignScreen
    @State private var film = true
    @State private var notes = false
    var body: some View {
        DesignForm {
            if screen == .detail {
                Section {
                    Text("The Open Ocean").font(.title2.bold())
                    Label("Documentaries · TorBox", systemImage: "folder")
                }
                Section("Transfer journey") {
                    Label("Provider ready", systemImage: "checkmark.circle").foregroundStyle(Color("StageGreen"))
                    Label("Downloading to NAS · 64%", systemImage: "arrow.down.circle").foregroundStyle(Color("StageBlue"))
                    TransferSegments(fraction: 0.64, color: .cyan)
                    LabeledContent("Downloaded", value: "6.4 GB of 10 GB")
                }
                Section("Destination") {
                    LabeledContent("Storage", value: "Your NAS")
                    LabeledContent("Category", value: "Documentaries")
                }
                Section { NavigationLink("View files") { DesignDestination(screen: .files) } }
            } else if screen == .selection {
                Section {
                    Text("Choose what to download").font(.title2.bold())
                    Text("The provider has found the files. Select at least one to continue.")
                }
                Section("2 files at provider") {
                    Toggle("The Open Ocean.mkv · 9.8 GB", isOn: $film)
                    Toggle("Notes.pdf · 200 MB", isOn: $notes)
                }
                Section { DesignAction(title: "Download selected files", prominent: true, enabled: film || notes) }
            } else {
                Section {
                    VStack(alignment: .leading, spacing: 10) {
                        Label("The Open Ocean.mkv", systemImage: "film")
                        TransferSegments(fraction: 0.64, color: .cyan)
                        Text("6.2 GB of 9.8 GB · Downloading to NAS").font(.footnote).foregroundStyle(.secondary)
                    }
                    Label("Notes.pdf · On your NAS", systemImage: "checkmark.circle")
                } header: { Text("The Open Ocean") } footer: { Text("These files are on your server, not stored on this iPhone.") }
                Section("When a file fails") {
                    Label("Interview.mp4 · Provider link expired", systemImage: "exclamationmark.circle")
                    DesignAction(title: "Retry this file")
                }
            }
        }
    }
}

private struct IntakeDesign: View {
    let screen: DesignScreen
    @State private var provider = "Server default"
    @State private var category = "Documentaries"
    @State private var selection = "Choose when available"
    @State private var link = ""
    var body: some View {
        DesignForm {
            if screen == .intake {
                Section {
                    TextField("Paste a magnet link", text: $link, axis: .vertical)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                    PasteButton(payloadType: String.self) { items in link = items.first ?? "" }
                    NavigationLink("Review sample transfer") { DesignDestination(screen: .add) }
                } header: { Text("Magnet link") } footer: { Text("Clipboard access happens only when you tap Paste. This design uses a sample transfer for review.") }
                Section("Or import") { DesignAction(title: "Import a .torrent file") }
                Section { NavigationLink("Saved drafts") { DesignDestination(screen: .drafts) } }
            } else if screen == .add || screen == .share {
                Section("Review transfer") {
                    Label("The Open Ocean", systemImage: "link")
                    Text("Name supplied by the magnet link").font(.footnote).foregroundStyle(.secondary)
                }
                Section {
                    Picker("Provider", selection: $provider) {
                        Text("Server default").tag("Server default")
                        Text("TorBox").tag("TorBox")
                        Text("Real-Debrid").tag("Real-Debrid")
                    }
                    Picker("Category", selection: $category) {
                        Text("Documentaries").tag("Documentaries")
                        Text("Uncategorized").tag("Uncategorized")
                    }
                    Picker("Files", selection: $selection) {
                        Text("Choose when available").tag("Choose when available")
                        Text("Server defaults").tag("Server defaults")
                    }
                } header: { Text("Destination") } footer: { Text("File names may arrive after the provider fetches metadata. Your original magnet link is preserved.") }
                if screen == .share {
                    Section {
                        Label("Can’t reach your server", systemImage: "wifi.slash")
                        Text("Save a draft, then open Debrid when you’re connected.")
                        DesignAction(title: "Save draft", prominent: true)
                    } footer: { Text("Saving is not adding. Drafts are never submitted automatically.") }
                } else {
                    Section { DesignAction(title: "Add transfer", prominent: true) }
                    Section { Label("Nothing is sent until you confirm.", systemImage: "hand.raised") }
                }
            } else if screen == .drafts {
                Section {
                    Text("Ready when you are.").font(.title2.bold())
                    Text("Saved on this iPhone. Review each draft before sending it.")
                }
                Section("1 saved draft") {
                    NavigationLink { DesignDestination(screen: .add) } label: {
                        VStack(alignment: .leading, spacing: 6) {
                            Text("The Open Ocean").font(.headline)
                            Text("Magnet · Shared today · Not submitted").font(.footnote).foregroundStyle(.secondary)
                        }
                    }
                }
            } else if screen == .duplicate {
                Section {
                    Label("This transfer already exists", systemImage: "checkmark.circle")
                    Text("The Open Ocean").font(.title2.bold())
                    Text("Open the existing transfer to check its progress. We haven’t added another copy.")
                    NavigationLink("Open existing transfer") { DesignDestination(screen: .detail) }
                }
            } else if screen == .uncertain {
                Section {
                    Label("The connection ended before confirmation", systemImage: "network")
                    Text("Checking whether it was added").font(.title2.bold())
                    Text("The server may have received your request. Check its transfer list before trying again.")
                    DesignAction(title: "Check server transfers")
                } footer: { Text("Your original draft is safe. We won’t resend it automatically.") }
            } else {
                Section("Show") {
                    Picker("Status", selection: $selection) {
                        Text("All transfers").tag("Choose when available")
                        Text("Needs attention").tag("attention")
                        Text("Downloading to NAS").tag("downloading")
                    }
                    Picker("Provider", selection: $provider) {
                        Text("All providers").tag("Server default")
                        Text("TorBox").tag("TorBox")
                    }
                    Picker("Category", selection: $category) {
                        Text("Documentaries").tag("Documentaries")
                        Text("All categories").tag("all")
                    }
                }
                Section { DesignAction(title: "Show matching transfers") }
            }
        }
    }
}
#endif
