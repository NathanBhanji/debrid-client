import Foundation

struct LiveUpdateTiming: Sendable {
    var coalesce: Duration = .milliseconds(350)
    var periodic: Duration = .seconds(45)
    var sleep: @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }
    var jitter: @Sendable () -> Double = { Double.random(in: 0.8...1.2) }
}

public enum SSEReconnectPolicy {
    public static func delay(failures: Int, jitter: Double, retryAfter: TimeInterval? = nil) -> Duration {
        let multiplier = jitter.isFinite ? min(1.2,max(0.8,jitter)) : 1
        var seconds = min(30, pow(2, Double(min(5,max(0,failures - 1))))) * multiplier
        seconds = min(30,seconds)
        if let retryAfter, retryAfter.isFinite, retryAfter >= 0 { seconds = max(seconds,min(86400,retryAfter)) }
        return .seconds(seconds)
    }
}
