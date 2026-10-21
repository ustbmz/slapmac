// spankbar.swift — a macOS menu bar app that manages the spank daemon.
//
// Architecture:
//   - spank runs as root (accelerometer access), so a normal user cannot
//     signal it. We solve this with a one-time admin authorization that
//     installs the binary to /usr/local/bin and adds a sudoers.d entry
//     allowing passwordless `sudo spank` for this user only.
//   - Afterwards the app talks to spank over stdio in --stdio JSON mode:
//     it receives {"slapNumber":N,...} events and sends pause/resume/set/quit.
//
// Build: see build-app.sh in this directory.

import AppKit

let installPath = "/usr/local/bin/spank"
let logPath = "/tmp/spankbar-spank.log"

struct SpankEvent: Decodable {
    let status: String?
    let slapNumber: Int?
    let amplitude: Double?
    let file: String?
    let error: String?
}

final class Controller: NSObject, NSApplicationDelegate {
    var statusItem: NSStatusItem!
    var process: Process?
    var stdinHandle: FileHandle?
    var lineBuffer = Data()

    var isRunning = false
    var restarting = false
    var lastError: String?

    // Settings (persisted to UserDefaults)
    var fastMode: Bool { didSet { UserDefaults.standard.set(fastMode, forKey: "fast") } }

    override init() {
        fastMode = UserDefaults.standard.bool(forKey: "fast")
        super.init()
    }

    // MARK: - App lifecycle

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        refreshMenuBarIcon()
        rebuildMenu()
        // Auto-start spank on launch (first run may prompt for the
        // one-time admin authorization to install the binary).
        startSpank()
    }

    // MARK: - Status bar icon

    var menubarImage: NSImage?

    /// Loads the black template icon (adapts to menu bar light/dark mode).
    func loadMenubarImage() -> NSImage? {
        if let img = menubarImage { return img }
        guard let url = Bundle.main.url(forResource: "menubar-icon", withExtension: "png"),
              let img = NSImage(contentsOf: url) else { return nil }
        img.size = NSSize(width: 20, height: 20)
        img.isTemplate = true
        menubarImage = img
        return img
    }

    /// Shows the template icon; appends the slap count while running,
    /// or an error emoji when spank failed to start.
    func refreshMenuBarIcon(error: Bool = false) {
        guard let button = statusItem?.button else { return }
        if error {
            button.image = nil
            button.title = "⚠️"
            return
        }
        button.image = loadMenubarImage()
        button.title = ""
    }

    // MARK: - Menu

    func rebuildMenu() {
        let menu = NSMenu()

        let startStop = NSMenuItem(
            title: isRunning ? "⏹ 停止 spank" : "▶️ 启动 spank",
            action: #selector(toggleRun), keyEquivalent: "")
        startStop.target = self
        menu.addItem(startStop)

        menu.addItem(.separator())

        let helpItem = NSMenuItem(
            title: "回应音效按力度自动分级：轻拍 → weak，越狠越惨",
            action: nil, keyEquivalent: "")
        helpItem.isEnabled = false
        menu.addItem(helpItem)

        let fastItem = NSMenuItem(
            title: "快速模式（更灵敏/更短冷却）",
            action: #selector(toggleFast), keyEquivalent: "")
        fastItem.target = self
        fastItem.state = fastMode ? .on : .off
        menu.addItem(fastItem)

        menu.addItem(.separator())

        let status = NSMenuItem(title: statusLine(), action: nil, keyEquivalent: "")
        status.isEnabled = false
        menu.addItem(status)

        if let err = lastError {
            let errItem = NSMenuItem(title: "⚠️ \(err)", action: nil, keyEquivalent: "")
            errItem.isEnabled = false
            menu.addItem(errItem)
        }

        let reinstall = NSMenuItem(
            title: "重新安装 spank 二进制（更新后使用）",
            action: #selector(reinstallBinary), keyEquivalent: "")
        reinstall.target = self
        menu.addItem(reinstall)

        let openLog = NSMenuItem(title: "打开 spank 日志", action: #selector(openLog), keyEquivalent: "")
        openLog.target = self
        menu.addItem(openLog)

        menu.addItem(.separator())

        let quit = NSMenuItem(title: "退出 spankbar", action: #selector(quitApp), keyEquivalent: "q")
        quit.target = self
        menu.addItem(quit)

        statusItem.menu = menu
    }

    func statusLine() -> String {
        return isRunning ? "运行中" : "已停止"
    }

    // MARK: - Actions

    @objc func toggleRun() {
        if isRunning { stopSpank() } else { startSpank() }
    }

    @objc func toggleFast() {
        fastMode.toggle()
        if isRunning { restartSpank() } else { rebuildMenu() }
    }

    @objc func reinstallBinary() {
        authorizeAndInstall()
    }

    @objc func openLog() {
        if let url = URL(string: "file://" + logPath.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed)!) {
            NSWorkspace.shared.open(url)
        }
    }

    @objc func quitApp() {
        if isRunning { stopSpank() }
        NSApp.terminate(nil)
    }

    // MARK: - Installation / authorization (one-time)

    func bundledBinaryPath() -> String? {
        Bundle.main.path(forResource: "spank", ofType: nil, inDirectory: nil)
            ?? Bundle.main.resourceURL?.appendingPathComponent("spank").path
    }

    func sudoOk() -> Bool {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/sudo")
        p.arguments = ["-n", "-l", installPath]
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice
        do { try p.run() } catch { return false }
        p.waitUntilExit()
        return p.terminationStatus == 0
    }

    /// One-time setup: copy the bundled binary into /usr/local/bin and add a
    /// sudoers.d entry so this user can run `sudo spank` without a password.
    func ensureInstalled() -> Bool {
        if sudoOk() { return true }
        return authorizeAndInstall()
    }

    @discardableResult
    func authorizeAndInstall() -> Bool {
        guard let src = bundledBinaryPath() else {
            lastError = "应用包内未找到 spank 二进制"
            rebuildMenu()
            return false
        }
        let user = NSUserName()
        let shell = [
            "mkdir -p /usr/local/bin",
            "cp '\(src)' \(installPath)",
            "chmod 755 \(installPath)",
            "printf '%s\\n' '\(user) ALL=(root) NOPASSWD: \(installPath)' > /etc/sudoers.d/spank",
            "chmod 440 /etc/sudoers.d/spank",
        ].joined(separator: " && ")

        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        p.arguments = ["-e", "do shell script \"\(replacingOccurrencesOfQuote(shell))\" with administrator privileges"]
        p.standardOutput = FileHandle.nullDevice
        let errPipe = Pipe()
        p.standardError = errPipe
        do { try p.run() } catch {
            lastError = "无法启动授权流程"
            rebuildMenu()
            return false
        }
        p.waitUntilExit()
        if p.terminationStatus != 0 {
            let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
            lastError = "授权被取消或失败"
            _ = errData
            rebuildMenu()
            return false
        }
        lastError = nil
        rebuildMenu()
        return true
    }

    func replacingOccurrencesOfQuote(_ s: String) -> String {
        s.replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
    }

    // MARK: - Process management

    func startSpank() {
        guard !isRunning else { return }
        guard ensureInstalled() else { return }

        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/sudo")
        var args = ["-n", installPath, "--stdio"]
        if fastMode { args.append("--fast") }
        p.arguments = args

        let inPipe = Pipe()
        let outPipe = Pipe()
        p.standardInput = inPipe
        p.standardOutput = outPipe
        if let errFile = FileHandle(forWritingAtPath: logPath) {
            errFile.seekToEndOfFile()
            p.standardError = errFile
        }

        do { try p.run() } catch {
            lastError = "启动失败: \(error.localizedDescription)"
            rebuildMenu()
            return
        }

        process = p
        stdinHandle = inPipe.fileHandleForWriting
        isRunning = true
        lastError = nil

        outPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            DispatchQueue.main.async {
                if data.isEmpty {
                    handle.readabilityHandler = nil
                    self?.spankExited()
                } else {
                    self?.consume(data: data)
                }
            }
        }

        // Wait for {"status":"ready"}
        let deadline = Date().addingTimeInterval(5)
        while isRunning && Date() < deadline && !sensorReady {
            RunLoop.current.run(until: Date().addingTimeInterval(0.05))
        }
        if !sensorReady && isRunning {
            // Don't hard-fail; some builds may not print ready. Keep running.
        }
        refreshMenuBarIcon(error: !isRunning)
        rebuildMenu()
    }

    var sensorReady = false

    func stopSpank() {
        guard let p = process else { return }
        sendQuit()
        let deadline = Date().addingTimeInterval(2)
        while p.isRunning && Date() < deadline {
            RunLoop.current.run(until: Date().addingTimeInterval(0.05))
        }
        if p.isRunning {
            // sudo relays signals to its child command.
            p.terminate()
            let hardDeadline = Date().addingTimeInterval(2)
            while p.isRunning && Date() < hardDeadline {
                RunLoop.current.run(until: Date().addingTimeInterval(0.05))
            }
            if p.isRunning { p.interrupt() }
        }
        stdinHandle?.closeFile()
        stdinHandle = nil
        process = nil
        isRunning = false
        refreshMenuBarIcon()
        rebuildMenu()
    }

    func restartSpank() {
        restarting = true
        stopSpank()
        startSpank()
        restarting = false
    }

    func spankExited() {
        guard !restarting else { return }
        if isRunning {
            isRunning = false
            lastError = "spank 意外退出（详见日志）"
        }
        process = nil
        stdinHandle = nil
        refreshMenuBarIcon()
        rebuildMenu()
    }

    // MARK: - stdio protocol

    func consume(data: Data) {
        lineBuffer.append(data)
        while let nl = lineBuffer.firstIndex(of: 0x0A) {
            let lineData = lineBuffer[lineBuffer.startIndex..<nl]
            lineBuffer.removeSubrange(lineBuffer.startIndex...nl)
            guard let line = String(data: lineData, encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines), !line.isEmpty
            else { continue }
            guard let jsonData = line.data(using: .utf8),
                  let ev = try? JSONDecoder().decode(SpankEvent.self, from: jsonData)
            else { continue }

            if ev.status == "ready" {
                sensorReady = true
                refreshMenuBarIcon()
                rebuildMenu()
            } else if ev.status == "quitting" {
                // ignore; exit handler will fire
            } else if let err = ev.error {
                lastError = "spank 错误: \(err)"
                rebuildMenu()
            }
            // Slap events carry no state we track; ignore them.
        }
    }

    func sendCommand(payload: [String: Any]) {
        guard let handle = stdinHandle,
              let line = try? JSONSerialization.data(withJSONObject: payload)
        else { return }
        handle.write(line + Data("\n".utf8))
    }

    func sendQuit() {
        sendCommand(payload: ["cmd": "quit"])
    }
}

let app = NSApplication.shared
let controller = Controller()
app.delegate = controller
app.setActivationPolicy(.accessory) // menu bar only, no Dock icon
app.run()
