// Host-only probe: compiles the same storage/parser sources; never linked into the app.
import Darwin
import Foundation

@main enum PendingInboxProbe {
    struct Input: Decodable { var uri:String?; var id:UUID?; var token:UUID? }
    struct Output: Encodable { var id:UUID?; var token:UUID?; var count:Int?; var duplicate:Bool?; var error:String? }
    static func emit(_ output:Output) throws {
        var data = try JSONEncoder().encode(output); data.append(10)
        FileHandle.standardOutput.write(data)
    }
    static func main() async {
        do {
            guard CommandLine.arguments.count == 3 else { throw PendingInboxError.unavailable }
            let command = CommandLine.arguments[1], directory = URL(fileURLWithPath:CommandLine.arguments[2],isDirectory:true)
            let store = try PendingInbox(directory:directory)
            if command == "hold" {
                _ = try await store.snapshot()
                let fd = Darwin.open(directory.appendingPathComponent(".inbox.lock").path,O_RDWR|O_EXLOCK|O_CLOEXEC,mode_t(0o600))
                guard fd >= 0 else { throw PendingInboxError.unavailable }
                defer { Darwin.close(fd) }
                try emit(Output(count:0)); try await Task.sleep(for:.seconds(120)); return
            }
            if command == "flood" {
                for index in 0..<20 {
                    let magnet = try MagnetLink("magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&dn=Fixture\(index)&x="+String(repeating:"x",count:32_000))
                    _ = try await store.enqueue(magnet,source:.share)
                    try emit(Output(count:index+1)); try await Task.sleep(for:.milliseconds(5))
                }
                return
            }
            if command == "snapshot" { try emit(Output(count:try await store.snapshot().count)); return }
            let input = try JSONDecoder().decode(Input.self,from:FileHandle.standardInput.readDataToEndOfFile())
            switch command {
            case "enqueue":
                guard let uri = input.uri else { throw PendingInboxError.unavailable }
                let receipt = try await store.enqueue(MagnetLink(uri),source:.share)
                try emit(Output(id:receipt.id,duplicate:receipt.wasDuplicate))
            case "claim":
                guard let id = input.id else { throw PendingInboxError.unavailable }
                let claim = try await store.claim(id); try emit(Output(id:claim.item.id,token:claim.token))
            case "release":
                guard let id = input.id, let token = input.token else { throw PendingInboxError.unavailable }
                try await store.release(id,token:token); try emit(Output(count:try await store.snapshot().count))
            default: throw PendingInboxError.unavailable
            }
        } catch let error as PendingInboxError { try? emit(Output(error:String(describing:error))) }
        catch { try? emit(Output(error:"unexpected")) }
    }
}
