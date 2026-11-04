import DebridKit
import Observation
import SwiftUI
import UIKit

@Observable @MainActor final class ShareSession {
    var model:SharedDraftModel?
    var message:String?
    var loading=true
    private var task:Task<Void,Never>?
    func load(_ items:[NSExtensionItem]) {
        guard task==nil else {return}
        task=Task {
            do {
                guard items.count<=16 else {throw SharedDraftError.tooMany}
                let providers=items.flatMap {$0.attachments ?? []}
                let drafts=try await ShareItemLoader.load(providers,fallbackText:providers.isEmpty ? items.compactMap {$0.attributedContentText?.string} : [])
                try Task.checkCancellation()
                model=try SharedDraftModel(drafts:drafts,store:PendingInbox.shared())
                loading=false
            } catch {
                guard !Task.isCancelled else {return}
                loading=false;message=(error as? LocalizedError)?.errorDescription ?? "These items could not be opened. Share a magnet link or torrent file."
            }
            task=nil
        }
    }
    func save() {guard task==nil,let model else {return};task=Task {await model.save();task=nil}}
    func cancel() {task?.cancel();task=nil}
}
final class ShareViewController:UIViewController {
    private let session=ShareSession()
    override func viewDidLoad() {
        super.viewDidLoad()
        let root=ShareReviewView(session:session,finish:{[weak self] in self?.finish()},cancel:{[weak self] in self?.cancel()})
        let host=UIHostingController(rootView:root)
        addChild(host);view.addSubview(host.view);host.view.translatesAutoresizingMaskIntoConstraints=false
        NSLayoutConstraint.activate([host.view.leadingAnchor.constraint(equalTo:view.leadingAnchor),host.view.trailingAnchor.constraint(equalTo:view.trailingAnchor),host.view.topAnchor.constraint(equalTo:view.topAnchor),host.view.bottomAnchor.constraint(equalTo:view.bottomAnchor)])
        host.didMove(toParent:self)
        session.load(extensionContext?.inputItems as? [NSExtensionItem] ?? [])
    }
    private func finish() {session.cancel();extensionContext?.completeRequest(returningItems:nil)}
    private func cancel() {
        session.cancel()
        extensionContext?.cancelRequest(withError:NSError(domain:NSCocoaErrorDomain,code:NSUserCancelledError))
    }
}
private struct ShareReviewView:View {
    let session:ShareSession
    let finish:()->Void
    let cancel:()->Void
    var body:some View {
        NavigationStack {
            List {
                Section {DebridWordmark();Text("Save on this iPhone, then open Debrid to choose a server and review before adding.").font(.subheadline).foregroundStyle(DebridStyle.secondary)}
                if session.loading {Section {ProgressView("Reading shared items…")}}
                if let message=session.message {Section {Label(message,systemImage:"exclamationmark.triangle").fixedSize(horizontal:false,vertical:true)}}
                if let model=session.model {
                    Section("Shared items") {ForEach(Array(model.drafts.enumerated()),id:\.offset) {_,draft in
                        Label(draft.name,systemImage: {if case .torrent=draft {return "doc"};return "link"}()).fixedSize(horizontal:false,vertical:true)
                    }}
                    if let message=model.message {Section {Text(message).foregroundStyle(DebridStyle.secondary)}}
                    if model.phase == .saving {Section {ProgressView("Saving to protected inbox…")}}
                    else if model.phase == .saved {
                        Section {Label("Saved for review",systemImage:"checkmark.circle.fill").foregroundStyle(DebridStyle.tint);Text("\(model.savedCount) saved · \(model.duplicateCount) already in Debrid").font(.footnote);Button("Done",action:finish).foregroundStyle(DebridStyle.tint)}
                    } else {Section {
                        Button(model.phase == .partial ? "Save remaining items" : "Save to Debrid") {session.save()}
                            .buttonStyle(.glassProminent).tint(Color(red:0.62,green:0.17,blue:0)).foregroundStyle(.white).controlSize(.large).frame(maxWidth:.infinity)
                    } footer: {Text("Saving does not start a transfer or download.")}}
                }
            }.modifier(DebridListStyle()).navigationTitle("Save to Debrid").navigationBarTitleDisplayMode(.inline)
                .toolbar {ToolbarItem(placement:.cancellationAction) {Button("Cancel",action:cancel)}}
        }.tint(DebridStyle.tint)
    }
}
