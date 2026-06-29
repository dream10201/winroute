# winroute installer — registers a Scheduled Task that runs winroute.exe at boot
# as SYSTEM (SYSTEM has the privileges needed to edit the routing table).
#
# Usage (run from an elevated PowerShell, in the folder containing winroute.exe):
#   .\install.ps1            # install + start
#   .\install.ps1 -Uninstall # remove
#
param([switch]$Uninstall)

$ErrorActionPreference = "Stop"
$TaskName = "winroute"
$Dir      = $PSScriptRoot
$Exe      = Join-Path $Dir "winroute.exe"
$Log      = Join-Path $Dir "winroute.log"

if (-not ([Security.Principal.WindowsPrincipal] `
      [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
      [Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw "Please run this script from an elevated (Administrator) PowerShell."
}

if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
  Stop-ScheduledTask  -TaskName $TaskName -ErrorAction SilentlyContinue
  Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
  Write-Host "Removed existing task '$TaskName'."
}

if ($Uninstall) {
  Write-Host "Uninstalled."
  return
}

if (-not (Test-Path $Exe)) { throw "winroute.exe not found next to this script ($Exe)." }

$action  = New-ScheduledTaskAction  -Execute $Exe -Argument "-log `"$Log`"" -WorkingDirectory $Dir
$trigger = New-ScheduledTaskTrigger -AtStartup
$set     = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
             -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)
$principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
  -Settings $set -Principal $principal -Description "Policy routing: 10.x via company net, rest via router" | Out-Null

Start-ScheduledTask -TaskName $TaskName
Write-Host "Installed and started '$TaskName'. Logging to $Log"
