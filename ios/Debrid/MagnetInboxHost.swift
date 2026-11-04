import DebridKit
import SwiftUI
import UniformTypeIdentifiers

extension EnvironmentValues {
    @Entry var magnetIntake: MagnetIntakeCoordinator? = nil
}
#if DEBUG
nonisolated private func intakeTestConfiguration() -> (id:UUID,mode:String?)? {
    if let value = UserDefaults.standard.string(forKey:"DebridInboxTestNamespace"), let id = UUID(uuidString:value) {
        return (id,UserDefaults.standard.string(forKey:"DebridInboxCaptureMode"))
    }
    guard let base = FileManager.default.urls(for:.applicationSupportDirectory,in:.userDomainMask).first else { return nil }
    let file = base.appendingPathComponent("DebridIntakeTest.json")
    guard let size = try? file.resourceValues(forKeys:[.fileSizeKey]).fileSize, size <= 1024,
          let data = try? Data(contentsOf:file), data.count <= 1024,
          let values = try? JSONSerialization.jsonObject(with:data) as? [String:String], let value = values["id"], let id = UUID(uuidString:value) else { return nil }
    return (id,values["mode"])
}
#endif
nonisolated private func openPendingInboxForApp() throws -> any PendingInboxAccess {
    #if DEBUG
    // Isolated simulator/UI test namespace. Release ignores all test configuration.
    if let configuration = intakeTestConfiguration() {
        guard let root = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier:PendingInbox.appGroup) else { throw PendingInboxError.sharedContainerUnavailable }
        return try PendingInbox(directory:root.appendingPathComponent("InboxTests-"+configuration.id.uuidString,isDirectory:true))
    }
    #endif
    return try PendingInbox.shared()
}
#if DEBUG
nonisolated func openPendingInboxForNativeFixture() throws -> (any PendingInboxAccess)? {
    guard intakeTestConfiguration() != nil else {return nil}
    return try openPendingInboxForApp()
}
#endif
@MainActor private func makeSubmissionAPIForApp(_ server:AuthenticatedServer) throws -> any TransferSubmissionAPI {
    #if DEBUG
    if let configuration=intakeTestConfiguration(),["submit","uncertain"].contains(configuration.mode ?? "") {
        return SubmissionFixtureAPI(serverURL:server.profile.serverURL,dropFirst:configuration.mode=="uncertain")
    }
    #endif
    return try DebridAPIClient(server:ServerAddress(server.profile.serverURL.absoluteString),credential:server.credential)
}
/// Normal app root; validated Debug intake fixtures use the existing no-network Connect fixture.
struct NativeAppRoot: View {
    var body: some View {
        MagnetInboxHost {
            #if DEBUG
            if let configuration=intakeTestConfiguration(),["cancel","cancel-uncertain","remove","remove-uncertain","remove-light"].contains(configuration.mode ?? "") {ManagementFixtureRoot(kind:configuration.mode?.hasPrefix("remove")==true ? .remove:.cancel,uncertain:configuration.mode?.hasSuffix("uncertain")==true).preferredColorScheme(configuration.mode=="remove-light" ? .light:.dark)}
            else if let configuration=intakeTestConfiguration(),["retry","retry-file","retry-uncertain","retry-light"].contains(configuration.mode ?? "") { RetryFixtureRoot(kind:configuration.mode=="retry-file" ? .download : .torrent,uncertain:configuration.mode=="retry-uncertain").preferredColorScheme(configuration.mode=="retry-light" ? .light : nil) }
            else if let configuration=intakeTestConfiguration(),configuration.mode=="share-host" { ShareHostFixture() }
            else if let configuration=intakeTestConfiguration(),configuration.mode=="selection" || configuration.mode=="selection-uncertain" { SelectionFixtureRoot(uncertain:configuration.mode=="selection-uncertain") }
            else if let configuration=intakeTestConfiguration(),["submit","uncertain"].contains(configuration.mode ?? "") { LiveTransfersPreview() }
            else if intakeTestConfiguration() != nil { ConnectionRoot(discoveryPreview:SignInDiscoveryPreview()) }
            else { ConnectionRoot() }
            #else
            ConnectionRoot()
            #endif
        }
    }
}
struct MagnetInboxHost<Content:View>: View {
    let content: Content
    @State private var intake = MagnetIntakeCoordinator(openInbox:openPendingInboxForApp,makeSubmissionAPI:makeSubmissionAPIForApp)
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.dynamicTypeSize) private var typeSize
    init(@ViewBuilder content:() -> Content) { self.content = content() }
    var body: some View {
        content.environment(\.magnetIntake,intake)
            .safeAreaInset(edge:.bottom) { banner }
            .onOpenURL { url in Task { await intake.receive(url) } }
            .task(id:scenePhase) { if scenePhase == .active { await intake.refresh() } }
            .sheet(isPresented:$intake.isPresented) { PendingLinksSheet(intake:intake) }
            #if DEBUG
            .task {
                // A UUID-isolated Debug-only fixture exercises the real copy/store/review path.
                guard let configuration=intakeTestConfiguration() else { return }
                if ["submit","uncertain"].contains(configuration.mode ?? "") {
                    do {
                        let profile=try ServerProfile(server:ServerAddress("https://debrid.example.test"))
                        intake.setConnection(AuthenticatedServer(profile:profile,username:"Sample User",credential:try .session("fixture_unused")))
                        if let url=URL(string:"magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&dn=The+Open+Ocean&tr=https%3a%2f%2ffixture.example.test%2fannounce") { await intake.receive(url) }
                    } catch { }
                    return
                }
                guard configuration.mode=="torrent" else { return }
                let file = FileManager.default.temporaryDirectory.appendingPathComponent("DebridFixture-"+configuration.id.uuidString+".torrent")
                defer { try? FileManager.default.removeItem(at:file) }
                let bytes = Data("d4:infod6:lengthi1234e4:name17:Fixture Ocean.mkv12:piece lengthi16384e6:pieces20:00000000000000000000ee".utf8)
                do { try bytes.write(to:file); await intake.importDocument(file) } catch { }
            }
            .onChange(of:intake.notice) {
                guard intake.notice != nil, let configuration = intakeTestConfiguration() else { return }
                switch configuration.mode {
                case "review", "torrent", "submit", "uncertain": intake.reviewReceivedLink()
                case "list": intake.present()
                default: break
                }
            }
            #endif
    }
    @ViewBuilder private var banner: some View {
        if intake.failedCount > 0 || intake.intakeMessage != nil {
            VStack(alignment:.leading,spacing:10) {
                Text(intake.intakeMessage ?? "Some received items have not been saved.").font(.footnote)
                if intake.failedCount > 0 {
                    Button(intake.isRetrying ? "Retrying…" : "Retry saving \(intake.failedCount) item(s)") { Task { await intake.retryFailed() } }.disabled(intake.isRetrying)
                } else { Button("Dismiss") { intake.dismissNotice() } }
            }.padding().frame(maxWidth:.infinity,alignment:.leading).background(.regularMaterial,in:.rect(cornerRadius:16)).padding(.horizontal).padding(.bottom,8)
        } else if let notice = intake.notice {
            let label = Label(notice,systemImage:"tray.and.arrow.down.fill").font(.subheadline).fixedSize(horizontal:false,vertical:true)
            let controls = HStack {
                Button("Review") { intake.reviewReceivedLink() }.buttonStyle(.glass).controlSize(.large).accessibilityIdentifier("review-received-link")
                Button("Dismiss",systemImage:"xmark") { intake.dismissNotice() }.labelStyle(.iconOnly).frame(minWidth:44,minHeight:44)
            }
            Group {
                if typeSize.isAccessibilitySize { VStack(alignment:.leading,spacing:10) { label; HStack { Spacer(); controls } } }
                else { HStack { label; Spacer(); controls } }
            }.padding(12).background(.regularMaterial,in:.rect(cornerRadius:16)).padding(.horizontal).padding(.bottom,8)
        } else if intake.savingCount > 0 { HStack { ProgressView(); Text("Saving item…").font(.footnote) }.padding() }
    }
}
struct PendingLinksButton: View {
    let intake: MagnetIntakeCoordinator
    var body: some View {
        Button("Pending items",systemImage:"tray.and.arrow.down") { intake.present() }
            .accessibilityValue("\(intake.items.count) saved items")
            .accessibilityIdentifier("pending-links")
    }
}
extension UTType { static let debridTorrent = UTType(importedAs:"org.bittorrent.torrent",conformingTo:.data) }
struct PendingLinksSheet: View {
    let intake: MagnetIntakeCoordinator
    @State private var path: [UUID]
    @State private var selectionModel:FileSelectionModel?
    @State private var retryModel:RetryModel?
    @State private var managementModel:ManagementModel?
    @State private var selectionMessage:String?
    @State private var showingPaste = false
    @State private var showingImporter = false
    @State private var importURL: URL?
    @State private var importError = false
    private enum Action {
        case keep(PendingItem), discard(PendingItem)
        var item:PendingItem { switch self { case .keep(let item),.discard(let item): item } }
        var isKeep:Bool { if case .keep = self { true } else { false } }
    }
    @State private var action: Action?
    @Environment(\.dismiss) private var dismiss
    init(intake:MagnetIntakeCoordinator) { self.intake = intake; _path = State(initialValue:intake.focusID.map { [$0] } ?? []) }
    var body: some View {
        NavigationStack(path:$path) {
            pendingList.modifier(DebridListStyle()).navigationTitle("Pending items")
                .toolbar { ToolbarItem(placement:.confirmationAction) { Button("Done") { dismiss() } } }
                .refreshable { await intake.refresh() }
                .navigationDestination(for:UUID.self) { id in PendingItemReview(intake:intake,id:id) }
        }.tint(DebridStyle.tint)
            .task { await intake.refresh() }
            .sheet(isPresented:Binding(get:{selectionModel != nil},set:{if !$0 {selectionModel=nil}}),onDismiss:{Task {await intake.refresh()}}) {
                if let selectionModel {FileSelectionSheet(model:selectionModel,connectionID:intake.connection?.connectionID).onChange(of:selectionModel.requiresSignIn) {if selectionModel.requiresSignIn {Task {await intake.rejectConnection(selectionModel.connectionID)}}}}
            }
            .sheet(isPresented:Binding(get:{managementModel != nil},set:{if !$0 {managementModel=nil}}),onDismiss:{Task {await intake.refresh()}}) {
                if let managementModel {ManagementSheet(model:managementModel,connectionID:intake.connection?.connectionID).onChange(of:managementModel.requiresSignIn) {if managementModel.requiresSignIn {Task {await intake.rejectConnection(managementModel.connectionID)}}}}
            }
            .sheet(isPresented:Binding(get:{retryModel != nil},set:{if !$0 {retryModel=nil}}),onDismiss:{Task {await intake.refresh()}}) {
                if let retryModel {RetrySheet(model:retryModel,connectionID:intake.connection?.connectionID).onChange(of:retryModel.requiresSignIn) {if retryModel.requiresSignIn {Task {await intake.rejectConnection(retryModel.connectionID)}}}}
            }
            .alert("Saved action",isPresented:Binding(get:{selectionMessage != nil},set:{if !$0 {selectionMessage=nil}})) {Button("OK",role:.cancel) {selectionMessage=nil}} message: {Text(selectionMessage ?? "")}
            .sheet(isPresented:$showingPaste) { MagnetPasteSheet(intake:intake) }
            .fileImporter(isPresented:$showingImporter,allowedContentTypes:[.debridTorrent],allowsMultipleSelection:false) { result in
                switch result {
                case .success(let urls): importURL = urls.first
                case .failure(let error): if (error as NSError).code != NSUserCancelledError { importError = true }
                }
            }
            .alert("File unavailable",isPresented:$importError) { Button("OK",role:.cancel) {} } message: { Text("The document provider could not open this file. Try again in Files.") }
            .task(id:importURL) { if let url = importURL { await intake.importDocument(url); if !Task.isCancelled, importURL == url { importURL = nil } } }
            .confirmationDialog(action?.isKeep == true ? "Keep this saved item?" : "Discard this saved item?",isPresented:Binding(get:{ action != nil },set:{ if !$0 { action = nil } }),titleVisibility:.visible) {
                if let selected = action {
                    if selected.isKeep { Button("Keep for another 30 days") { action = nil; Task { await intake.confirmKeep(selected.item.id) } } }
                    else { Button("Discard saved item",role:.destructive) { action = nil; Task { _ = await intake.discardSaved(selected.item.id) } } }
                }
                Button("Cancel",role:.cancel) { action = nil }
            } message: { Text(action?.isKeep == true ? "This extends storage on this iPhone without adding a transfer." : "This removes only the saved item. Server transfers are unchanged.") }
    }
    private var pendingList:some View {
        List {
                Section {
                    Button("Paste magnet links",systemImage:"document.on.clipboard") { intake.dismissNotice(); showingPaste = true }.accessibilityIdentifier("paste-magnet-links")
                    Button("Import torrent file",systemImage:"doc.badge.plus") { showingImporter = true }.disabled(intake.isImporting).accessibilityIdentifier("import-torrent-file")
                    if intake.isImporting { HStack { ProgressView("Copying torrent file…"); Spacer(); Button("Cancel") { importURL = nil } } }
                    if let message = intake.intakeMessage { Label(message,systemImage:"exclamationmark.triangle").font(.footnote) }
                } footer: { Text("Save links or a torrent file on this iPhone, then review before adding a transfer.") }
                if let message = intake.storageMessage { Section { Label(message,systemImage:"exclamationmark.triangle"); Button("Try again") { Task { await intake.refresh() } } } }
                if intake.items.isEmpty && intake.storageMessage != nil { ContentUnavailableView("Saved items unavailable",systemImage:"lock",description:Text("Try again when protected storage is available.")) }
                else if (!intake.hasSnapshot || intake.isLoading) && intake.items.isEmpty { ProgressView("Loading saved items…") }
                else if intake.items.isEmpty && intake.savedSelections.isEmpty && intake.savedRetries.isEmpty && intake.savedManagement.isEmpty { ContentUnavailableView("No pending links",systemImage:"tray",description:Text("Links and torrent files you save in Debrid appear here for review.")) }
                savedSelectionRows
                savedRetryRows
                savedManagementRows
                ForEach(intake.items) { item in
                    Group {
                        if item.state == .expired { Button { action = .keep(item) } label: { row(item) } }
                        else { NavigationLink(value:item.id) { row(item) } }
                    }.swipeActions(edge:.trailing,allowsFullSwipe:false) {
                        Button("Discard saved item",systemImage:"trash",role:.destructive) { action = .discard(item) }
                    }.accessibilityAction(named:"Discard saved item") { action = .discard(item) }
                }
            }
    }
    @ViewBuilder private var savedSelectionRows:some View {
        if !intake.savedSelections.isEmpty {Section("Saved file selections") {ForEach(intake.savedSelections) {saved in
                    Button {
                        do {
                            selectionModel=try intake.fileSelectionModel(transferID:saved.transferID,name:saved.name,serverURL:saved.serverURL)
                            if selectionModel==nil {selectionMessage="Connect to "+saved.serverURL.absoluteString+" to check this saved selection."}
                        } catch {selectionMessage="The saved selection could not be opened. Try again when protected storage is available."}
                    } label: {VStack(alignment:.leading,spacing:4) {Text(saved.name).font(.headline);Text("Check saved selection").font(.subheadline);Text(saved.serverURL.absoluteString).font(.footnote)}.foregroundStyle(DebridStyle.ink)}
                }}}
    }
    @ViewBuilder private var savedManagementRows:some View {
        if !intake.savedManagement.isEmpty {Section("Saved transfer actions") {ForEach(intake.savedManagement) {saved in
            Button {
                do {managementModel=try intake.managementModel(kind:saved.kind,resourceID:saved.resourceID,name:saved.name,serverURL:saved.serverURL);if managementModel==nil {selectionMessage="Connect to "+saved.serverURL.absoluteString+" to check this saved action."}}
                catch{selectionMessage="This saved action could not be opened. Its request is kept."}
            } label: {VStack(alignment:.leading,spacing:5) {Label(saved.name,systemImage:saved.kind == .cancel ? "stop.circle":"trash");Text(saved.kind == .cancel ? "Check saved cancellation":"Check saved removal").font(.footnote);Text(saved.serverURL.absoluteString).font(.caption).foregroundStyle(DebridStyle.secondary)}}
        }}}
    }
    @ViewBuilder private var savedRetryRows:some View {
        if !intake.savedRetries.isEmpty {Section("Saved retry requests") {ForEach(intake.savedRetries) {saved in
            Button {
                do {
                    retryModel=try intake.retryModel(kind:saved.kind,resourceID:saved.resourceID,torrentID:saved.torrentID,name:saved.name,serverURL:saved.serverURL)
                    if retryModel==nil {selectionMessage="Connect to "+saved.serverURL.absoluteString+" to check this saved retry."}
                } catch {selectionMessage="The saved retry could not be opened. Try again when protected storage is available."}
            } label: {VStack(alignment:.leading,spacing:4) {Text(saved.name).font(.headline);Text(saved.kind == .torrent ? "Check transfer retry" : "Check file retry").font(.subheadline);Text(saved.serverURL.absoluteString).font(.footnote)}.foregroundStyle(DebridStyle.ink)}
        }}}
    }
    private func row(_ item:PendingItem) -> some View {
        VStack(alignment:.leading,spacing:6) {
            Text(item.displayName).font(.headline).foregroundStyle(DebridStyle.ink)
            if item.torrent != nil { Label("Torrent file",systemImage:"doc").font(.caption).foregroundStyle(DebridStyle.secondary) }
            Text(item.state == .expired ? "Confirm to keep" : item.submission?.phase == .accepted ? "Added to server" : item.submission?.phase == .prepared || item.submission?.phase == .blocked ? "Check saved submission" : item.state == .reviewing ? "Being reviewed" : "Saved for review").font(.subheadline).foregroundStyle(DebridStyle.secondary)
            Text(item.createdAt,format:.dateTime.day().month().hour().minute()).font(.footnote).foregroundStyle(DebridStyle.secondary)
        }.padding(.vertical,6)
    }
}
private struct PendingItemReview: View {
    let intake: MagnetIntakeCoordinator
    let id: UUID
    @State private var addModel:TransferAddModel?
    @State private var addSetupMessage:String?
    @State private var request:ReviewRequest?
    private struct ReviewRequest:Identifiable { let id=UUID();let action:SubmissionReviewSections.Action }
    private struct ReviewContext:Hashable { let token:UUID?;let connection:UUID?;let active:Bool }
    @State private var owner = UUID()
    private enum Confirmation { case discard, keep }
    @State private var confirmation: Confirmation?
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.dismiss) private var dismiss
    private func hash(_ value:String) -> some View {
        VStack(alignment:.leading,spacing:6) { Text("Info hash").font(.subheadline); Text(value).font(.footnote.monospaced()).textSelection(.enabled) }
    }
    var body: some View {
        List {
            if intake.isReviewLoading { ProgressView("Opening saved item…") }
            if let message = intake.reviewMessage { Section {
                Label(message,systemImage:"exclamationmark.triangle")
                if intake.reviewNeedsConfirmation { Button("Keep saved item") { confirmation = .keep } }
                else { Button("Try again") { Task { await intake.beginReview(id,owner:owner) } } }
            } }
            if let claim = intake.activeClaim, claim.item.id == id {
                Section {
                    Text(claim.item.displayName).font(.title2.weight(.semibold))
                    if let model=addModel,case .accepted = model.phase {
                        Label("Server confirmed",systemImage:"checkmark.circle.fill").foregroundStyle(DebridStyle.tint)
                    } else if addModel?.attempt?.phase == .prepared || addModel?.attempt?.phase == .blocked || claim.item.submission?.phase == .prepared || claim.item.submission?.phase == .blocked {
                        Label("Saved submission",systemImage:"clock").foregroundStyle(DebridStyle.secondary)
                    } else { Label("Saved for review",systemImage:"tray.and.arrow.down.fill").foregroundStyle(DebridStyle.tint) }
                    if let model=addModel,case .accepted = model.phase {
                        Text("The server has confirmed this submission.").font(.subheadline).foregroundStyle(DebridStyle.secondary)
                    } else if claim.item.submission?.phase == .prepared || claim.item.submission?.phase == .blocked || addModel?.attempt?.phase == .prepared || addModel?.attempt?.phase == .blocked {
                        Text("This item has a saved submission to check.").font(.subheadline).foregroundStyle(DebridStyle.secondary)
                    } else if claim.item.submission?.phase == .accepted {
                        Text("The server has confirmed this submission.").font(.subheadline).foregroundStyle(DebridStyle.secondary)
                    } else { Text("No transfer has been added.").font(.subheadline).foregroundStyle(DebridStyle.secondary) }
                }.listRowBackground(Color.clear).listRowSeparator(.hidden)
                if let magnet = claim.item.magnet {
                    Section("Magnet") {
                        LabeledContent("Trackers",value:"\(magnet.trackerCount)")
                        LabeledContent("Format",value:magnet.isHybrid ? "BitTorrent v1 + v2" : "BitTorrent v1")
                        hash(claim.item.infoHash)
                        DisclosureGroup("Link preview") {
                            Text(magnet.preview()).font(.caption.monospaced()).textSelection(.enabled)
                            if magnet.rawURI.unicodeScalars.count > 2048 { Text("Preview shortened. The complete link is saved.").font(.footnote).foregroundStyle(DebridStyle.secondary) }
                        }
                    }
                } else if let torrent = claim.item.torrent {
                    Section("Torrent file") {
                        LabeledContent("Files",value:"\(torrent.fileCount)")
                        LabeledContent("Size",value:ByteCountFormatter.string(fromByteCount:torrent.totalBytes,countStyle:.file))
                        LabeledContent("Format",value:torrent.isHybrid ? "BitTorrent v1 + v2" : "BitTorrent v1")
                        hash(torrent.infoHash)
                        Text("An exact copy is saved on this iPhone. The original file is unchanged.").font(.footnote).foregroundStyle(DebridStyle.secondary)
                    }
                }
                if let target = claim.item.targetServerURL { Section("Saved server") { Text(target.absoluteString).font(.footnote).textSelection(.enabled) } }
                if let addSetupMessage { Section { Label(addSetupMessage,systemImage:"exclamationmark.triangle") } }
                if let addModel { SubmissionReviewSections(model:addModel) { action in request=ReviewRequest(action:action) } }
                else if intake.connection == nil { Section { Text("Connect to your server to add this saved item.");Button("Connect to server") { intake.isPresented=false } } }
                Section {
                    Button("Keep for later") { dismiss() }.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint)
                    Button("Discard saved item",role:.destructive) { confirmation = .discard }.buttonStyle(DebridRowActionStyle()).foregroundStyle(Color("DangerInk")).disabled(addModel.map { !$0.canDiscard } ?? (claim.item.submission?.phase == .prepared || claim.item.submission?.phase == .blocked))
                }
            }
        }.modifier(DebridListStyle()).navigationTitle("Saved item").navigationBarTitleDisplayMode(.inline)
            .task(id:scenePhase) { if scenePhase == .active { await intake.beginReview(id,owner:owner) } else { intake.endReview(owner:owner) } }
            .task(id:ReviewContext(token:intake.activeClaim?.token,connection:intake.connection?.connectionID,active:scenePhase == .active)) {
                request=nil;addModel=nil;addSetupMessage=nil
                guard scenePhase == .active,let claim=intake.activeClaim,claim.item.id==id else { return }
                do { let created=try intake.submissionModel(claim:claim);addModel=created;await created?.load() }
                catch { if !Task.isCancelled { addSetupMessage="This add form could not be prepared. Reconnect to your server and try again." } }
            }
            .task(id:request?.id) {
                guard let request,let model=addModel else { return }
                switch request.action {
                case .add:await model.submit()
                case .check:await model.reconcile()
                case .retry:await model.retrySameSubmission()
                case .reload:await model.load()
                case .finish:
                    if model.durableAcceptance { if await intake.discardReview(owner:owner) { intake.isPresented=false } }
                    else { intake.isPresented=false }
                case .view(let transferID):
                    if model.durableAcceptance { _ = await intake.discardReview(owner:owner) }
                    intake.viewTransfer(transferID,serverURL:model.serverURL)
                }
                await intake.refresh()
                if model.requiresSignIn { await intake.rejectConnection(model.connectionID) }
            }
            .onDisappear { request=nil;intake.endReview(owner:owner) }
            .confirmationDialog(confirmation == .keep ? "Keep this saved item?" : "Discard this saved item?",isPresented:Binding(get:{ confirmation != nil },set:{ if !$0 { confirmation = nil } }),titleVisibility:.visible) {
                if confirmation == .keep {
                    Button("Keep for another 30 days") { confirmation = nil; Task { if await intake.confirmKeep(id) { await intake.beginReview(id,owner:owner) } } }
                } else {
                    Button("Discard saved item",role:.destructive) { confirmation = nil; Task { if await intake.discardReview(owner:owner) { dismiss() } } }
                }
                Button("Cancel",role:.cancel) { confirmation = nil }
            } message: { Text(confirmation == .keep ? "This extends storage on this iPhone without adding a transfer." : "This removes only the saved item on this iPhone. Server transfers are unchanged.") }
    }
}

private struct MagnetPasteSheet: View {
    let intake: MagnetIntakeCoordinator
    @State private var text = ""
    @State private var saving = false
    @State private var request = false
    @Environment(\.dismiss) private var dismiss
    var body: some View {
        NavigationStack {
            Form {
                Section {
                    PasteButton(payloadType:String.self) { values in text = values.joined(separator:"\n") }.disabled(saving)
                    TextEditor(text:$text).frame(minHeight:180).font(.body.monospaced()).textInputAutocapitalization(.never).autocorrectionDisabled()
                        .accessibilityLabel("Magnet links").accessibilityIdentifier("magnet-paste-editor").disabled(saving)
                } header: { Text("Magnet links") } footer: { Text("Paste up to 16 links, one per line. Each full link is saved exactly as entered, without surrounding whitespace.") }
                if let message = intake.intakeMessage { Section { Label(message,systemImage:"exclamationmark.triangle") } }
                Section { Text("Links are saved for review. No transfer is added until you confirm it.").font(.footnote).foregroundStyle(DebridStyle.secondary) }
            }.modifier(DebridListStyle()).navigationTitle("Paste links").navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement:.cancellationAction) { Button("Cancel") { dismiss() } }
                    ToolbarItem(placement:.confirmationAction) { Button(saving ? "Saving…" : "Save") { saving = true; request = true }.disabled(saving || text.trimmingCharacters(in:.whitespacesAndNewlines).isEmpty).accessibilityLabel("Save links for review").accessibilityIdentifier("save-pasted-links") }
                }
                .task(id:request) {
                    guard request else { return }; let saved = await intake.paste(text)
                    saving = false; request = false
                    if saved, !Task.isCancelled { dismiss() }
                }
        }.tint(DebridStyle.tint)
    }
}

#if DEBUG
private struct ShareHostFixture:View {
    private let raw="magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&dn=Share+Fixture"
    var body:some View {
        NavigationStack {
            List {Section {
                Label("Preview · Fictional share host",systemImage:"viewfinder")
                ShareLink("Share sample magnet",item:raw).accessibilityIdentifier("share-fixture")
                Text("Use Cancel in the extension. No test transfer is submitted.")
            }}.modifier(DebridListStyle()).navigationTitle("Share preview")
        }.tint(DebridStyle.tint)
    }
}
#endif
