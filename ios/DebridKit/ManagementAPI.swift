public protocol ManagementAPI:CancellationAPI,RemovalAPI {}
extension DebridAPIClient:ManagementAPI {}
