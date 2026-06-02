@echo off
:: Run this as Administrator to add Windows Firewall rules for SwiftDrop.
:: This allows mDNS device discovery and file transfers on your LAN.

echo Adding SwiftDrop firewall rules...
echo.

netsh advfirewall firewall delete rule name="SwiftDrop" >nul 2>&1
netsh advfirewall firewall add rule name="SwiftDrop" dir=in action=allow program="%~dp0SwiftDrop.exe" enable=yes profile=private,public

if %errorlevel% equ 0 (
    echo.
    echo SUCCESS: Firewall rule added for SwiftDrop.
    echo Your Mac, Android, and other SwiftDrop devices can now discover this PC.
) else (
    echo.
    echo FAILED: Could not add firewall rule.
    echo Please right-click this file and select "Run as administrator".
)

echo.
pause
