import SwiftUI
import DebridKit

@main
struct DebridApp: App {
    var body: some Scene {
        WindowGroup { DemoRoot() }
    }
}

private enum Desk {
    static let accent = Color("DeskAccent")
}

struct DemoRoot: View {
    var body: some View {
        TabView {
            Tab("Transfers", systemImage: "arrow.down.circle") { TransfersView() }
            Tab("Providers", systemImage: "externaldrive.connected.to.line.below") { ProvidersView() }
            Tab("Settings", systemImage: "gearshape") { SettingsView() }
        }
        .tint(Desk.accent)
    }
}

private struct DemoNotice: View {
    var body: some View {
        Label("Design preview · Sample data", systemImage: "viewfinder")
            .font(.footnote).foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityLabel("Design preview. All data is fictional. No server is connected.")
    }
}

struct TransfersView: View {
    @Environment(\.colorScheme) private var colorScheme
    @State private var search = ""
    @State private var showingAdd = false
    private var transfers: [TransferPreview] {
        TransferPreview.examples.filter { search.isEmpty || $0.name.localizedCaseInsensitiveContains(search) }
    }
    var body: some View {
        NavigationStack {
            List {
                Section { DemoNotice() }
                Section {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Making room for the next discovery.").font(.title2.weight(.semibold))
                        Text("1 downloading · 1 needs your choice").foregroundStyle(.secondary)
                        Text("Downloads continue on your server when you leave the app.")
                            .font(.footnote).foregroundStyle(.secondary)
                    }.padding(.vertical, 8)
                }
                Section("Your transfers") {
                    ForEach(transfers) { transfer in
                        NavigationLink { TransferDetail(transfer: transfer) } label: {
                            TransferRow(transfer: transfer)
                        }
                    }
                }
                if transfers.isEmpty {
                    ContentUnavailableView.search(text: search)
                }
            }
            .navigationTitle("Transfers")
            .toolbarColorScheme(colorScheme, for: .navigationBar)
            .searchable(text: $search, prompt: "Search transfers")
            .toolbar {
                ToolbarItem(placement: .primaryAction) {
                    Button("Add transfer", systemImage: "plus") { showingAdd = true }
                }
            }
            .sheet(isPresented: $showingAdd) { AddPreview() }
        }
    }
}

private struct TransferRow: View {
    let transfer: TransferPreview
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(transfer.name).font(.headline).foregroundStyle(.primary)
            Label(transfer.phase.rawValue, systemImage: transfer.phase == .completed ? "checkmark.circle" : "arrow.down.circle")
                .font(.subheadline).foregroundStyle(Desk.accent)
            if transfer.phase == .downloading {
                ProgressView(value: transfer.fraction)
                    .accessibilityLabel("Download progress")
            }
            Text(transfer.detail).font(.caption).foregroundStyle(.secondary)
        }.padding(.vertical, 8)
    }
}

private struct TransferDetail: View {
    @Environment(\.colorScheme) private var colorScheme
    let transfer: TransferPreview
    var body: some View {
        List {
            Section { DemoNotice(); TransferRow(transfer: transfer) }
            Section("Destination") {
                LabeledContent("Category", value: transfer.category)
                LabeledContent("Storage", value: "Your NAS")
            }
            Section("Files") {
                Label("File selection arrives in a later milestone", systemImage: "doc")
                    .foregroundStyle(.secondary)
            }
        }.toolbarColorScheme(colorScheme, for: .navigationBar)
        .navigationTitle(transfer.name).navigationBarTitleDisplayMode(.inline)
    }
}

private struct AddPreview: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.dismiss) private var dismiss
    @State private var category = "Documentaries"
    @State private var provider = "Server default"
    var body: some View {
        NavigationStack {
            Form {
                Section { DemoNotice() }
                Section("Review transfer") {
                    Label("The Open Ocean", systemImage: "link")
                    Text("Sample magnet · Metadata may arrive after adding.").font(.footnote).foregroundStyle(.secondary)
                }
                Section("Destination") {
                    Picker("Provider", selection: $provider) {
                        Text("Server default").tag("Server default")
                        Text("TorBox").tag("TorBox")
                    }
                    Picker("Category", selection: $category) {
                        Text("Documentaries").tag("Documentaries")
                        Text("Uncategorized").tag("Uncategorized")
                    }
                    LabeledContent("Files", value: "Choose when available")
                }
                Section {
                    Text("This preview cannot add transfers. The live flow will ask for confirmation before sending anything.")
                        .foregroundStyle(.secondary)
                }
            }
            .toolbarColorScheme(colorScheme, for: .navigationBar)
            .navigationTitle("Add transfer").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Done") { dismiss() } } }
        }
    }
}

private struct ProvidersView: View {
    @Environment(\.colorScheme) private var colorScheme
    var body: some View {
        NavigationStack {
            List {
                Section { DemoNotice() }
                Section("Connected accounts · Sample") {
                    Label("TorBox · Default", systemImage: "externaldrive.badge.checkmark")
                    Label("Real-Debrid", systemImage: "externaldrive")
                }
                Section { Text("Account health is checked on demand. A saved account is not proof that a provider is reachable.").foregroundStyle(.secondary) }
            }.toolbarColorScheme(colorScheme, for: .navigationBar).navigationTitle("Providers")
        }
    }
}

private struct SettingsView: View {
    @Environment(\.colorScheme) private var colorScheme
    var body: some View {
        NavigationStack {
            List {
                Section { DemoNotice() }
                Section("Server") {
                    LabeledContent("Connection", value: "Demo only")
                    Text("Pocket ID sign-in arrives in a future milestone.").foregroundStyle(.secondary)
                }
                Section("About") {
                    LabeledContent("Debrid", value: "0.1 · Design preview")
                    Text("Native on your phone. Downloads on your server.")
                }
            }.toolbarColorScheme(colorScheme, for: .navigationBar).navigationTitle("Settings")
        }
    }
}
