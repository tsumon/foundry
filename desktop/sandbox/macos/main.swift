import Darwin
import Foundation
import Virtualization

// This helper has exactly one application-owned invocation. It never opens a
// network device, directory share, disk, graphics device or management socket.
// Standard output belongs exclusively to the trusted guest serial protocol.
func fail(_ code: Int32, _ stage: String, _ error: Error? = nil) -> Never {
    // Only application-owned stage names and numeric native error codes leave
    // this process. Do not retain localized messages, input paths or user data.
    if code != 0 {
        var diagnostic: [String: Any] = ["schema_version": 1, "stage": stage, "exit_code": code]
        if let native = error as NSError? {
            let domains = ["VZErrorDomain", "NSPOSIXErrorDomain", "NSCocoaErrorDomain"]
            diagnostic["error_domain"] = domains.contains(native.domain) ? native.domain : "other"
            diagnostic["error_code"] = native.code
        }
        if var data = try? JSONSerialization.data(withJSONObject: diagnostic, options: [.sortedKeys]) {
            data.append(10)
            try? FileHandle.standardError.write(contentsOf: data)
        }
    }
    exit(code)
}

let args = CommandLine.arguments
guard args.count == 5, args[1] == "--kernel", args[3] == "--initrd" else { fail(64, "invalid_arguments") }
for path in [args[2], args[4]] {
    // Match the controller's physical canonical paths. Foundation's URL
    // standardization rewrites valid /private/tmp paths to the /tmp symlink.
    // Do not normalize and execute aliases: reject non-canonical input instead.
    let components = path.split(separator: "/", omittingEmptySubsequences: false)
    guard path.hasPrefix("/"), components.dropFirst().allSatisfy({ !$0.isEmpty && $0 != "." && $0 != ".." }) else { fail(64, "invalid_arguments") }
    var info = stat()
    guard lstat(path, &info) == 0, (info.st_mode & S_IFMT) == S_IFREG else { fail(65, "invalid_input") }
    guard let resolved = realpath(path, nil) else { fail(65, "invalid_input") }
    defer { free(resolved) }
    guard String(cString: resolved).utf8.elementsEqual(path.utf8) else { fail(64, "invalid_arguments") }
}

@available(macOS 12.0, *)
final class VMOwner: NSObject, VZVirtualMachineDelegate {
    private let vm: VZVirtualMachine
    private let input = Pipe()
    private var deadline: DispatchSourceTimer?
    private var stopping = false

    init(kernel: String, initrd: String) throws {
        let loader = VZLinuxBootLoader(kernelURL: URL(fileURLWithPath: kernel))
        loader.initialRamdiskURL = URL(fileURLWithPath: initrd)
        loader.commandLine = "console=hvc0 init=/init panic=1 quiet loglevel=0"
        let config = VZVirtualMachineConfiguration()
        config.bootLoader = loader
        config.cpuCount = 1
        config.memorySize = 2 * 1024 * 1024 * 1024
        let serial = VZVirtioConsoleDeviceSerialPortConfiguration()
        serial.attachment = VZFileHandleSerialPortAttachment(
            fileHandleForReading: input.fileHandleForReading,
            fileHandleForWriting: FileHandle.standardOutput)
        config.serialPorts = [serial]
        config.networkDevices = []
        config.storageDevices = []
        config.directorySharingDevices = []
        config.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
        config.memoryBalloonDevices = []
        try config.validate()
        vm = VZVirtualMachine(configuration: config)
        super.init()
        vm.delegate = self
    }

    func run() {
        let timer = DispatchSource.makeTimerSource(queue: .main)
        timer.schedule(deadline: .now() + 180)
        timer.setEventHandler { [weak self] in self?.stop() }
        timer.resume()
        deadline = timer

        // This is a separate pipe from the guest input. EOF means the exact
        // owning controller disappeared, even if its PID has been reused.
        let writer = input.fileHandleForWriting
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            var buffer = [UInt8](repeating: 0, count: 65536)
            var total = 0
            while true {
                let count = Darwin.read(STDIN_FILENO, &buffer, buffer.count)
                if count < 0 && errno == EINTR { continue }
                if count <= 0 { break }
                total += count
                if total > 16 * 1024 * 1024 + 64 { break }
                do { try writer.write(contentsOf: Data(buffer[0..<count])) }
                catch { break }
            }
            DispatchQueue.main.async { self?.stop() }
        }
        vm.start { result in
            if case .failure(let error) = result { fail(70, "start_failed", error) }
        }
    }

    private func stop() {
        guard !stopping else { return }
        stopping = true
        // force stop, not a guest-cooperative shutdown request
        vm.stop { error in
            if let error = error { fail(71, "stop_failed", error) }
            fail(71, "owner_stopped")
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) { fail(72, "stop_deadline") }
    }
    func guestDidStop(_ virtualMachine: VZVirtualMachine) { fail(0, "guest_stopped") }
    func virtualMachine(_ virtualMachine: VZVirtualMachine, didStopWithError error: Error) { fail(73, "guest_failed", error) }
}

if #available(macOS 12.0, *) {
    do {
        let owner = try VMOwner(kernel: args[2], initrd: args[4])
        owner.run()
        withExtendedLifetime(owner) { dispatchMain() }
    } catch { fail(70, "configuration_failed", error) }
} else { fail(69, "platform_unsupported") }
