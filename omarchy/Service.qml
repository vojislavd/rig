import QtQuick
import Quickshell
import Quickshell.Io
import "Model.js" as Model

// Runs rig and holds its last status. Every command is an argv list
// passed straight to rig (no shell), so a site name can never become
// shell code. Nothing here uses sudo or deletes anything.
Item {
  id: root

  // An absolute path, so a same-named binary earlier on the shell's
  // PATH (mise shims, for one) is never run instead.
  readonly property string rig: Quickshell.env("HOME") + "/.local/bin/rig"

  property var report: null
  // A failed action's message stays until the next action; a failed
  // status read clears on the next good one.
  property string actionError: ""
  property string statusError: ""
  readonly property string lastError: actionError || statusError
  // The action in flight, e.g. "mail start"; "" when idle.
  property string pending: ""

  readonly property string health: Model.health(report)
  readonly property var problems: Model.problems(report)
  readonly property bool busy: pending !== ""

  function refresh() {
    if (statusProcess.running) return
    statusProcess.running = true
  }

  // run starts one rig command, e.g. run(["mail", "start"]), then
  // refreshes. One at a time, so clicks can't race each other.
  function run(args) {
    if (pending !== "") return
    pending = args.join(" ")
    actionProcess.exited = false
    actionProcess.stderrDone = false
    actionProcess.command = [rig].concat(args)
    actionProcess.running = true
    actionTimeout.restart()
  }

  function setService(name, on) {
    run([name, on ? "start" : "stop"])
  }

  function workerRunning(name, kind) {
    return Model.workerRunning(report, name, kind)
  }

  function toggleWorker(name, kind) {
    run(["worker", workerRunning(name, kind) ? "stop" : "start", name, kind])
  }

  // Fire-and-forget commands that only open something.
  function openSite(name) { Quickshell.execDetached([rig, "open", name]) }
  function openInbox() { Quickshell.execDetached([rig, "mail", "open"]) }
  function openDashboard() { Quickshell.execDetached(["xdg-terminal-exec", rig]) }

  // rig's errors are one line; keep them short in the popup.
  function firstLine(text) {
    var line = String(text || "").trim().split("\n")[0]
    return line.length > 160 ? line.substring(0, 157) + "…" : line
  }

  Timer {
    interval: 10000
    repeat: true
    running: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }

  // A process's exit and the end of its output can arrive in either
  // order, so parse when stdout is complete; a failed rig prints nothing there.
  Process {
    id: statusProcess
    running: false
    // --no-db: no MariaDB login on every poll; the widget doesn't show databases.
    command: [root.rig, "status", "--json", "--no-db"]
    stderr: StdioCollector { id: statusErr; waitForEnd: true }
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        var r = Model.parse(text)
        root.report = r
        root.statusError = r ? "" : root.firstLine(statusErr.text) || "rig status failed (is rig in ~/.local/bin?)"
      }
    }
  }

  Process {
    id: actionProcess
    property int exitCode: -1
    property bool exited: false
    property bool stderrDone: false
    running: false
    command: []
    stderr: StdioCollector {
      id: actionErr
      waitForEnd: true
      onStreamFinished: { actionProcess.stderrDone = true; root.actionDone() }
    }
    onExited: function(code) {
      exitCode = code
      exited = true
      root.actionDone()
    }
  }

  function actionDone() {
    if (!actionProcess.exited || !actionProcess.stderrDone || pending === "") return
    actionError = actionProcess.exitCode === 0 ? "" : firstLine(actionErr.text) || ("rig " + pending + " failed")
    pending = ""
    actionTimeout.stop()
    refresh()
  }

  // If rig never starts (missing binary), don't leave the buttons locked.
  Timer {
    id: actionTimeout
    interval: 60000
    onTriggered: {
      if (root.pending === "") return
      root.actionError = "rig " + root.pending + " did not finish"
      root.pending = ""
      root.refresh()
    }
  }
}
