param([int]$ProcessId,[string]$Version,[string]$OutputPath)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
$deadline = [DateTime]::UtcNow.AddSeconds(90)
$button = $null
while ([DateTime]::UtcNow -lt $deadline) {
  $process = Get-Process -Id $ProcessId -ErrorAction Stop
  $window = $process.MainWindowHandle
  if ($window -ne [IntPtr]::Zero) {
    $root = [System.Windows.Automation.AutomationElement]::FromHandle($window)
    $condition = New-Object System.Windows.Automation.PropertyCondition ([System.Windows.Automation.AutomationElement]::NameProperty), 'About'
    $button = $root.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition)
    if ($button) { break }
  }
  Start-Sleep -Milliseconds 500
}
if (!$button) { throw 'Production webview did not expose its visible About button' }
$invoke = $button.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern)
$invoke.Invoke()
$deadline = [DateTime]::UtcNow.AddSeconds(20)
$verified = $false
while ([DateTime]::UtcNow -lt $deadline) {
  $elements=$root.FindAll([System.Windows.Automation.TreeScope]::Descendants,[System.Windows.Automation.Condition]::TrueCondition)
  $names=@($elements | ForEach-Object { $_.Current.Name })
  if (($names -contains $Version) -and ($names -contains 'Apache License 2.0')) { $verified=$true; break }
  Start-Sleep -Milliseconds 300
}
if (!$verified) { throw "Production About did not report version $Version and Apache 2.0" }
& pwsh.exe -NoProfile -NonInteractive -File "$PSScriptRoot/screenshot.ps1" -ProcessId $ProcessId -OutputPath $OutputPath
if ($LASTEXITCODE -ne 0) { throw 'Owned window screenshot failed' }
Write-Host 'PASS actual production About, build version, license and owned screenshot'
