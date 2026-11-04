import DebridKit
import SwiftUI

struct LiveTransfers: View {
    let server: AuthenticatedServer
    let auth: SessionCoordinator?
    @State private var model: TransferListModel?
    @State private var setupFailed = false
    @State private var showingServer = false
    init(server: AuthenticatedServer, auth: SessionCoordinator) { self.server = server; self.auth = auth }
    #if DEBUG
    init(preview: LiveTransfersPreviewAPI) throws {
        let profile = try ServerProfile(server: ServerAddress("https://debrid.example.test"))
        server = AuthenticatedServer(profile: profile, username: "Sample User", credential: try .session("fixture_unused"))
        auth = nil
        _model = State(initialValue: TransferListModel(profile: profile, api: preview))
    }
    #endif
    var body: some View {
        NavigationStack {
            List {
                Section {
                    if auth == nil { Label("Preview · Sample transfers", systemImage: "viewfinder").font(.footnote).foregroundStyle(DebridStyle.secondary).listRowSeparator(.hidden) }
                    DebridWordmark().listRowSeparator(.hidden)
                    if let model { readout(model).listRowSeparator(.hidden) }
                }.listRowBackground(Color.clear)
                    .listRowInsets(EdgeInsets(top: 8, leading: 0, bottom: 8, trailing: 0))
                if let model {
                    if case .failed(let message) = model.phase {
                        Section {
                            Label(message, systemImage: "exclamationmark.triangle").foregroundStyle(DebridStyle.secondary)
                            if !model.items.isEmpty { Text("Showing the last loaded transfers.").font(.footnote).foregroundStyle(DebridStyle.secondary) }
                            Button("Try again") { Task { await reload(model) } }
                                .buttonStyle(.borderless).foregroundStyle(DebridStyle.tint)
                        }
                    }
                    if model.phase == .loading && model.items.isEmpty {
                        Section { ProgressView("Loading transfers…").frame(maxWidth: .infinity) }
                    } else if model.phase == .ready && model.items.isEmpty {
                        Section {
                            ContentUnavailableView("No transfers yet", systemImage: "tray", description: Text("Transfers added to your server will appear here."))
                        }.listRowBackground(Color.clear)
                    } else {
                        Section("Your transfers") {
                            ForEach(model.items) { item in
                                LiveTransferRow(item: item).listRowBackground(DebridStyle.surface)
                            }
                        }
                    }
                } else if setupFailed {
                    Section { Text("The server connection could not be prepared. Reconnect to try again.").foregroundStyle(DebridStyle.secondary) }
                } else { ProgressView("Preparing connection…") }
            }.modifier(DebridListStyle())
                .navigationTitle("Transfers")
                .toolbar {
                    ToolbarItem(placement: .primaryAction) { Button("Server", systemImage: "server.rack") { showingServer = true } }
                }
                .refreshable { if let model { await reload(model) } }
                .task {
                    do {
                        if model == nil { model = try TransferListModel(server: server) }
                        if let model { await reload(model) }
                    } catch { setupFailed = true }
                }
                .onDisappear { model?.invalidate() }
                .sheet(isPresented: $showingServer) { serverSheet }
        }.tint(DebridStyle.tint)
    }
    private func reload(_ model: TransferListModel) async {
        await model.reload()
        if !Task.isCancelled, model.requiresSignIn { await auth?.sessionRejected(for: model.profile.id) }
    }
    private func readout(_ model: TransferListModel) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("TRANSFER STATUS").font(.caption.monospaced().weight(.medium)).tracking(1)
            Text(model.updatedAt == nil ? "Transfer information unavailable" : "\(model.items.count) transfers · \(model.items.filter { $0.stage.isActive }.count) active")
                .font(.title3.weight(.semibold)).fixedSize(horizontal: false, vertical: true)
            let attention = model.items.filter { $0.stage == .failed || $0.stage == .selecting }.count
            if attention > 0 { Text("\(attention) need your attention").font(.subheadline) }
            if let date = model.updatedAt {
                HStack(spacing: 4) { Text("Updated"); Text(date, style: .relative); Text("ago") }.font(.footnote)
            } else { Text("Waiting for server").font(.footnote) }
        }.foregroundStyle(DebridStyle.readoutText)
            .padding(20).frame(maxWidth: .infinity, alignment: .leading)
            .background(DebridStyle.readout, in: .rect(cornerRadius: 20))
            .accessibilityElement(children: .combine)
    }
    private var serverSheet: some View {
        NavigationStack {
            Form {
                Section("Server") {
                    Text(server.profile.serverURL.absoluteString).textSelection(.enabled)
                    LabeledContent("Signed in as", value: server.username)
                }
                if let auth { Section {
                    Button("Sign out on this iPhone", role: .destructive) { Task { await auth.signOut() } }
                        .buttonStyle(.borderless).foregroundStyle(.red).disabled(auth.isBusy)
                    if auth.isBusy { ProgressView("Signing out…") }
                    if let message = auth.message { Text(message).foregroundStyle(DebridStyle.secondary) }
                } } else { Section { Text("Preview only. No server session exists.").foregroundStyle(DebridStyle.secondary) } }
            }.modifier(DebridListStyle()).navigationTitle("Server").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { showingServer = false } } }
        }.tint(DebridStyle.tint)
    }
}

private struct LiveTransferRow: View {
    let item: TransferSummary
    @Environment(\.dynamicTypeSize) private var typeSize
    private var color: Color {
        switch item.stage {
        case .failed: .red
        case .selecting: Color("StageAmber")
        case .completed: Color("StageGreen")
        case .unknown: DebridStyle.secondary
        default: Color("StageBlue")
        }
    }
    private var symbol: String {
        switch item.stage {
        case .failed: "exclamationmark.circle"
        case .selecting: "checklist"
        case .completed: "checkmark.circle"
        case .unpacking: "archivebox"
        case .unknown: "questionmark.circle"
        case .nasDownloading: "arrow.down.to.line"
        default: "arrow.down.circle"
        }
    }
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top, spacing: 12) {
                if !typeSize.isAccessibilitySize {
                    Image(systemName: symbol).font(.title3).foregroundStyle(color).frame(width: 32).accessibilityHidden(true)
                }
                VStack(alignment: .leading, spacing: 5) {
                    Text(item.name).font(.headline).foregroundStyle(DebridStyle.ink)
                    Label(item.stage.label, systemImage: symbol).font(.subheadline).foregroundStyle(color)
                }
            }
            if let progress = item.progress {
                TransferSegments(fraction: progress, color: color).accessibilityHidden(true)
                Text("\(item.progressLabel) · \(progress.formatted(.percent.precision(.fractionLength(0))))")
                    .font(.caption).monospacedDigit().foregroundStyle(DebridStyle.secondary)
            }
            HStack {
                if let category = item.transfer.category, !category.isEmpty { Text(category) }
                if item.transfer.size > 0 { Text(item.transfer.size.formatted(.byteCount(style: .file))) }
            }.font(.footnote).foregroundStyle(DebridStyle.secondary)
        }.fixedSize(horizontal: false, vertical: true).padding(.vertical, 10)
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("live-transfer-" + item.id)
    }
}

#if DEBUG
struct LiveTransfersPreviewAPI: TransferListing {
    enum State: Sendable { case filled, empty, offline }
    let state: State
    func torrents() async throws -> [TorrentDTO] {
        if state == .offline { throw APIError.connection(.notConnectedToInternet) }
        if state == .empty { return [] }
        let time = "2026-01-02T03:04:05Z"
        let base: [String:Any] = ["account_id":"sample-account","hash":String(repeating:"0",count:40),"provider_progress":0.42,"local_progress":0,"size":10_000_000_000,"speed":0,"seeders":0,"files":[],"settings":["download_retries":3,"torrent_retries":1,"unpack":false],"retry_count":0,"added_at":time,"downloads":[]]
        var provider = base
        provider["id"] = "sample-provider"; provider["name"] = "The Open Ocean"; provider["status"] = "downloading"; provider["category"] = "Documentaries"
        var nas = base
        nas["id"] = "sample-nas"; nas["name"] = "Earth from Above"; nas["status"] = "finished"; nas["local_progress"] = 0.64
        nas["downloads"] = [["id":"sample-download","torrent_id":"sample-nas","path":"Earth.mkv","filename":"Earth.mkv","size":10_000_000_000,"bytes_done":6_400_000_000,"progress":0.64,"state":"downloading","retry_count":0,"queued_at":time]]
        var selection = base
        selection["id"] = "sample-selection"; selection["name"] = "Field Recordings · Vol. 02"; selection["status"] = "waiting_selection"; selection["category"] = "Audio"
        let decoder = JSONDecoder(); decoder.keyDecodingStrategy = .convertFromSnakeCase; decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode([TorrentDTO].self, from: JSONSerialization.data(withJSONObject: [provider,nas,selection]))
    }
}
struct LiveTransfersPreview: View {
    var body: some View {
        if let view = try? LiveTransfers(preview: LiveTransfersPreviewAPI(state:
            ProcessInfo.processInfo.arguments.contains("-TransfersEmpty") ? .empty :
                (ProcessInfo.processInfo.arguments.contains("-TransfersOffline") ? .offline : .filled))) { view }
        else { ContentUnavailableView("Preview unavailable", systemImage:"viewfinder") }
    }
}
#endif
