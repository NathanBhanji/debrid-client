import SwiftUI
import DebridKit

@main
struct DebridApp: App {
    var body: some Scene {
        WindowGroup {
            #if DEBUG
            if let index = ProcessInfo.processInfo.arguments.firstIndex(of: "-RecoveryFixture"), ProcessInfo.processInfo.arguments.indices.contains(index + 1) {
                RecoveryFixtureRoot(mode: ProcessInfo.processInfo.arguments[index + 1])
            } else if let index = ProcessInfo.processInfo.arguments.firstIndex(of: "-SettingsFixture"), ProcessInfo.processInfo.arguments.indices.contains(index + 1) {
                SettingsFixtureRoot(mode: ProcessInfo.processInfo.arguments[index + 1])
            } else if let index = ProcessInfo.processInfo.arguments.firstIndex(of: "-ProvidersFixture"), ProcessInfo.processInfo.arguments.indices.contains(index + 1) {
                ProviderFixtureRoot(mode: ProcessInfo.processInfo.arguments[index + 1])
            } else if let index = ProcessInfo.processInfo.arguments.firstIndex(of: "-DesignScreen"),
               ProcessInfo.processInfo.arguments.indices.contains(index + 1),
               let screen = DesignScreen(rawValue: ProcessInfo.processInfo.arguments[index + 1]) {
                DesignScene(screen: screen)
            } else if ProcessInfo.processInfo.arguments.contains("-DesignGallery") {
                DesignGallery()
            } else if ProcessInfo.processInfo.arguments.contains("-TransfersPreview") {
                LiveTransfersPreview()
            } else if ProcessInfo.processInfo.arguments.contains("-ConnectionPreview") {
                ConnectionRoot(discoveryPreview: SignInDiscoveryPreview())
            } else if ProcessInfo.processInfo.arguments.contains("-Demo") { DemoRoot() }
            else { NativeAppRoot() }
            #else
            NativeAppRoot()
            #endif
        }
    }
}

struct DemoRoot: View {
    var body: some View {
        TabView {
            Tab("Transfers", systemImage: "arrow.down.circle") { TransfersView() }
            Tab("Providers", systemImage: "externaldrive.connected.to.line.below") { ProvidersView() }
            Tab("Settings", systemImage: "gearshape") { SettingsView() }
        }
        .tint(DebridStyle.tint)
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
                Section {
                    DebridWordmark()
                        .listRowSeparator(.hidden)
                        .listRowBackground(Color.clear)
                        .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
                    TransferReadout()
                        .listRowSeparator(.hidden)
                        .listRowBackground(Color.clear)
                        .listRowInsets(EdgeInsets(top: 8, leading: 0, bottom: 0, trailing: 0))
                }
                Section("Your transfers") {
                    ForEach(transfers) { transfer in
                        NavigationLink { TransferDetail(transfer: transfer) } label: {
                            TransferRow(transfer: transfer)
                        }
                        .listRowBackground(DebridStyle.surface)
                        .accessibilityIdentifier("transfer-" + transfer.id)
                    }
                }
                if transfers.isEmpty {
                    ContentUnavailableView.search(text: search)
                }
            }
            .modifier(DebridListStyle())
            .navigationTitle("Transfers")
            .toolbarColorScheme(colorScheme, for: .navigationBar)
            .searchable(text: $search, prompt: "Search transfers")
            .toolbar {
                ToolbarItem(placement: .primaryAction) {
                    Button("Add transfer", systemImage: "plus") { showingAdd = true }
                        .accessibilityIdentifier("add-transfer")
                }
            }
            .sheet(isPresented: $showingAdd) {
                AddPreview().presentationDetents([.large]).presentationDragIndicator(.visible)
            }
        }
    }
}

private struct TransferRow: View {
    let transfer: TransferPreview
    @Environment(\.dynamicTypeSize) private var typeSize
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top, spacing: 12) {
                if !typeSize.isAccessibilitySize { TransferLamp(phase: transfer.phase) }
                VStack(alignment: .leading, spacing: 5) {
                    Text(transfer.name).font(.headline).foregroundStyle(DebridStyle.ink)
                    Label(transfer.phase.rawValue, systemImage: transfer.phase.symbol)
                        .font(.subheadline).foregroundStyle(transfer.phase.ink)
                }
            }
            if transfer.phase == .downloading {
                TransferSegments(fraction: transfer.fraction, color: transfer.phase.lamp)
            }
            Text(transfer.detail).font(.footnote).monospacedDigit().foregroundStyle(DebridStyle.secondary)
        }
        .fixedSize(horizontal: false, vertical: true)
        .padding(.vertical, 10)
        .accessibilityElement(children: .combine)
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
        }.modifier(DebridListStyle())
        .toolbarColorScheme(colorScheme, for: .navigationBar)
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
            .modifier(DebridListStyle())
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
            }.modifier(DebridListStyle())
            .toolbarColorScheme(colorScheme, for: .navigationBar)
            .navigationTitle("Providers")
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
            }.modifier(DebridListStyle())
            .toolbarColorScheme(colorScheme, for: .navigationBar)
            .navigationTitle("Settings")
        }
    }
}
