.pragma library

// Pure helpers for the rig widget: no QML, no processes.

// The worker kinds shown per site, in dashboard key order (1 2 3).
var workerKinds = ["queue", "schedule", "reverb"]

// parse turns "rig status --json" output into a report, or null.
function parse(text) {
  try {
    var r = JSON.parse(String(text || ""))
    if (!r || typeof r !== "object" || !Array.isArray(r.services)) return null
    if (!Array.isArray(r.sites)) r.sites = []
    return r
  } catch (e) {
    return null
  }
}

function service(report, name) {
  if (!report) return null
  for (var i = 0; i < report.services.length; i++) {
    if (report.services[i].name === name) return report.services[i]
  }
  return null
}

// problems lists what needs attention, in plain words. On-demand
// services (mail, s3) being stopped is normal, not a problem.
function problems(report) {
  if (!report) return []
  var out = []
  if (report.install && report.install.port_redirect !== "active") out.push("port redirect is " + report.install.port_redirect)
  if (report.install && report.install.ca_trusted !== true) out.push("CA is not trusted")
  for (var i = 0; i < report.services.length; i++) {
    var s = report.services[i]
    if (s.state === "failed" || (!s.on_demand && s.state !== "active")) out.push(s.name + " is " + s.state)
  }
  for (var j = 0; j < report.sites.length; j++) {
    if (report.sites[j].cert_issue) out.push(report.sites[j].name + " certificate: " + report.sites[j].cert_issue)
  }
  return out
}

// health is "ok", "warn" or "off" (nginx not running: nothing is served).
function health(report) {
  if (!report) return "warn"
  var nginx = service(report, "nginx")
  if (!nginx || nginx.state !== "active") return "off"
  return problems(report).length > 0 ? "warn" : "ok"
}

// workerRunning looks the site up in a parsed report. Callers pass a name,
// not a site from a Repeater: there "workers" is a Qt list, not a JS array.
function workerRunning(report, name, kind) {
  if (!report) return false
  for (var i = 0; i < report.sites.length; i++) {
    var s = report.sites[i]
    if (s.name === name) return Array.isArray(s.workers) && s.workers.indexOf(kind) !== -1
  }
  return false
}
