import DebridKit
import SwiftUI

struct TransferFiltersView: View {
    @Bindable var model: TransferListModel
    var preview = false
    @Environment(\.dismiss) private var dismiss
    var body: some View {
        NavigationStack {
            Form {
                if preview { Section { Label("Preview · Sample filters", systemImage:"viewfinder").font(.footnote).foregroundStyle(DebridStyle.secondary) } }
                Section {
                    Picker("Status", selection:$model.search.scope) {
                        ForEach(TransferSearch.Scope.allCases,id:\.self) { Text($0.label).tag($0) }
                    }.pickerStyle(.navigationLink).accessibilityIdentifier("filter-status")
                    Picker("Category", selection:$model.search.category) {
                        Text("All categories").tag(TransferSearch.Category.all)
                        Text("Uncategorized").tag(TransferSearch.Category.uncategorized)
                        ForEach(model.categories,id:\.self) { category in
                            Text(TransferSearchIndex.bounded(category,limit:128)+(category == "Uncategorized" ? " (category)" : ""))
                                .tag(TransferSearch.Category.named(category))
                        }
                    }.pickerStyle(.navigationLink).accessibilityIdentifier("filter-category")
                    Picker("Provider account", selection:$model.search.accountID) {
                        Text("All provider accounts").tag(String?.none)
                        ForEach(model.accountOptions) { Text($0.label).tag(Optional($0.id)) }
                    }.pickerStyle(.navigationLink).accessibilityIdentifier("filter-account")
                } header: { Text("Show") } footer: { Text("Search, status, category and account filters apply together.") }
                Section {
                    Text("\(model.visibleItems.count) of \(model.items.count) transfers match").monospacedDigit()
                    Button("Clear search and filters") { model.resetSearch() }
                        .buttonStyle(.borderless).foregroundStyle(DebridStyle.tint)
                        .disabled(!model.search.hasCriteria)
                }
                if let message = model.accountsMessage {
                    Section {
                        Text(message).foregroundStyle(DebridStyle.secondary)
                        Button("Reload account labels") { model.reloadAccounts() }.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint)
                    }
                }
                if let message = model.preferencesMessage { Section { Text(message).foregroundStyle(DebridStyle.secondary) } }
            }.modifier(DebridListStyle())
                .navigationTitle("Filters").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement:.confirmationAction) { Button("Done") { dismiss() } } }
        }.tint(DebridStyle.tint)
    }
}
