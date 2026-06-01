//go:build windows

package core

import (
	"fmt"
	"os/exec"
)

// Notify sends a Windows toast notification via PowerShell.
// Uses the built-in BurntToast-style notification (available on Windows 10+).
func Notify(title, message string) {
	// PowerShell one-liner that creates a Windows toast notification using
	// the built-in .NET API. No external dependencies required.
	script := fmt.Sprintf(`
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom, ContentType = WindowsRuntime] | Out-Null
$template = @"
<toast>
  <visual>
    <binding template="ToastGeneric">
      <text>%s</text>
      <text>%s</text>
    </binding>
  </visual>
  <audio src="ms-winsoundevent:Notification.Default"/>
</toast>
"@
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml($template)
$toast = [Windows.UI.Notifications.ToastNotification]::new($xml)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier("SwiftDrop").Show($toast)
`, title, message)
	go exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Start()
}
