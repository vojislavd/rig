import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import qs.Commons
import qs.Commons as Commons
import qs.Ui
import "Model.js" as Model

// Bar pill plus popup for rig. The pill is dim when nginx is down and
// red when something needs attention; the popup switches mail and s3,
// toggles site workers, and opens sites. Destructive or root-level
// commands (rm, db drop, install) are left to the terminal on purpose.
Panel {
  id: root
  moduleName: "vojislavd.rig"
  ipcTarget: "vojislavd.rig"

  readonly property string glyph: "󰒋"
  readonly property color foreground: bar ? bar.foreground : Commons.Color.foreground
  readonly property color urgent: bar ? bar.urgent : Commons.Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family
  readonly property var sites: rig.report ? rig.report.sites : []

  readonly property string summary: {
    if (!rig.report) return rig.statusError !== "" ? "Can't read status" : "Checking…"
    if (rig.health === "off") return "Stack stopped"
    if (rig.problems.length === 1) return "1 problem"
    if (rig.problems.length > 1) return rig.problems.length + " problems"
    return sites.length === 1 ? "1 site, all good" : sites.length + " sites, all good"
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: if (opened) {
    rig.refresh()
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }

  Service {
    id: rig
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: root.glyph
    active: rig.health === "warn"
    dimmed: rig.health === "off"
    tooltipText: root.opened ? "" : "rig: " + root.summary
    onPressed: function(b) {
      if (b === Qt.RightButton) rig.openDashboard()
      else if (b === Qt.MiddleButton) rig.refresh()
      else root.toggle()
    }
  }

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(380))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(560))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (t === "r" || t === "R") rig.refresh()
        else if (t === "d" || t === "D") rig.openDashboard()
      }

      Flickable {
        id: flick
        anchors.fill: parent
        contentWidth: width
        contentHeight: column.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        flickableDirection: Flickable.VerticalFlick
        interactive: contentHeight > height
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Column {
          id: column
          width: flick.width
          spacing: Style.space(12)

          PanelHero {
            width: parent.width
            title: "rig"
            meta: rig.pending !== "" ? "rig " + rig.pending + "…" : root.summary
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconOpacity: rig.health === "off" ? 0.5 : 1.0
            iconComponent: Component {
              Text {
                textFormat: Text.PlainText
                text: root.glyph
                color: rig.health === "warn" ? root.urgent : root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.display
              }
            }
            trailingControl: Component {
              PanelActionButton {
                iconText: "󰕮"
                tooltipText: "Open the dashboard (d)"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: { rig.openDashboard(); root.close() }
              }
            }
          }

          Text {
            textFormat: Text.PlainText
            visible: rig.lastError !== ""
            width: parent.width
            text: rig.lastError
            color: root.urgent
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
            wrapMode: Text.WordWrap
          }

          Column {
            visible: rig.problems.length > 0
            width: parent.width
            spacing: Style.spacing.labelGap
            Repeater {
              model: rig.problems
              Text {
                required property string modelData
                textFormat: Text.PlainText
                width: parent.width
                text: "• " + modelData
                color: root.urgent
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                wrapMode: Text.WordWrap
              }
            }
          }

          PanelSectionHeader {
            text: "SERVICES"
            foreground: root.foreground
            fontFamily: root.fontFamily
          }

          Column {
            width: parent.width
            spacing: Style.space(6)
            Repeater {
              model: rig.report ? rig.report.services : []
              ServiceRow {
                required property var modelData
                width: parent.width
                service: modelData
              }
            }
          }

          PanelSeparator {
            foreground: root.foreground
          }

          PanelSectionHeader {
            text: "SITES"
            foreground: root.foreground
            fontFamily: root.fontFamily
          }

          Text {
            visible: rig.report !== null && root.sites.length === 0
            width: parent.width
            text: "No sites yet. Run rig link in a project."
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.body
            horizontalAlignment: Text.AlignHCenter
          }

          Column {
            width: parent.width
            spacing: Style.space(6)
            Repeater {
              model: root.sites
              SiteRow {
                required property var modelData
                width: parent.width
                site: modelData
              }
            }
          }
        }
      }
    }
  }

  // One service: state, plus a switch for the on-demand ones (mail, s3).
  component ServiceRow: Item {
    id: serviceRow
    property var service: null
    readonly property bool running: service && service.state === "active"
    readonly property string key: service ? service.name : ""

    implicitHeight: Math.max(Style.spacing.controlHeight * 0.75, serviceLayout.implicitHeight)

    RowLayout {
      id: serviceLayout
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      spacing: Style.space(8)

      Text {
        textFormat: Text.PlainText
        text: serviceRow.running ? "●" : "○"
        color: serviceRow.service && serviceRow.service.state === "failed" ? root.urgent : (serviceRow.running ? root.foreground : root.dim)
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
      }

      Text {
        textFormat: Text.PlainText
        Layout.fillWidth: true
        text: serviceRow.key
        color: root.foreground
        font.family: root.fontFamily
        font.pixelSize: Style.font.body
        elide: Text.ElideRight
      }

      Text {
        textFormat: Text.PlainText
        text: serviceRow.service ? serviceRow.service.state : ""
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
      }

      PanelActionButton {
        visible: serviceRow.key === "mail" && serviceRow.running
        iconText: "󰇰"
        tooltipText: "Open the inbox"
        foreground: root.foreground
        fontFamily: root.fontFamily
        onClicked: rig.openInbox()
      }

      ToggleSwitch {
        id: serviceSwitch
        visible: serviceRow.service && serviceRow.service.on_demand === true
        checked: serviceRow.running
        busy: rig.pending === serviceRow.key + " start" || rig.pending === serviceRow.key + " stop"
        enabled: !rig.busy
        foreground: root.foreground
        onToggled: rig.setService(serviceRow.key, !serviceRow.running)

        PanelToolTip {
          visible: serviceSwitch.containsMouse
          text: (serviceRow.running ? "Stop " : "Start ") + serviceRow.key
          fontFamily: root.fontFamily
        }
      }
    }
  }

  // One site: click the name to open it; Q S R toggle its workers.
  component SiteRow: CursorSurface {
    id: siteRow
    property var site: null

    foreground: root.foreground
    hasCursor: siteMouse.containsMouse
    implicitHeight: siteLayout.implicitHeight + Style.spacing.rowPaddingX

    MouseArea {
      id: siteMouse
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onClicked: { rig.openSite(siteRow.site.name); root.close() }
    }

    RowLayout {
      id: siteLayout
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      spacing: Style.space(6)

      ColumnLayout {
        Layout.fillWidth: true
        spacing: Style.space(1)

        Text {
          textFormat: Text.PlainText
          Layout.fillWidth: true
          text: siteRow.site ? siteRow.site.name : ""
          color: siteRow.site && siteRow.site.cert_issue ? root.urgent : root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.body
          elide: Text.ElideRight
        }

        Text {
          textFormat: Text.PlainText
          Layout.fillWidth: true
          text: siteRow.site ? siteRow.site.url.replace("https://", "") : ""
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          elide: Text.ElideRight
        }
      }

      Repeater {
        model: Model.workerKinds
        PanelActionButton {
          required property string modelData
          readonly property bool running: rig.workerRunning(siteRow.site.name, modelData)
          iconText: modelData.charAt(0).toUpperCase()
          bordered: running
          opacity: running ? 1.0 : 0.5
          enabled: !rig.busy
          tooltipText: (running ? "Stop " : "Start ") + modelData
          foreground: root.foreground
          fontFamily: root.fontFamily
          fontSize: Style.font.bodySmall
          onClicked: rig.toggleWorker(siteRow.site.name, modelData)
        }
      }
    }
  }
}
